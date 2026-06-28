package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	ErrWeComBindingInvalid       = errors.New("invalid wecom binding")
	ErrWeComIdentityInactive     = errors.New("wecom identity is not active")
	ErrWeComIdentityUnknown      = errors.New("wecom identity is unknown")
	ErrWeComBindingAlreadyExists = errors.New("wecom binding already exists")
)

type wecomContactClientFactory func(input interfaces.WeComIdentitySyncInput) *wecomContactClient

type wecomIdentityService struct {
	repo          interfaces.WeComIdentityRepository
	userRepo      interfaces.UserRepository
	audit         interfaces.AuditLogService
	clientFactory wecomContactClientFactory
}

func NewWeComIdentityService(
	repo interfaces.WeComIdentityRepository,
	userRepo interfaces.UserRepository,
	audit interfaces.AuditLogService,
) interfaces.WeComIdentityService {
	return newWeComIdentityService(repo, userRepo, audit, func(input interfaces.WeComIdentitySyncInput) *wecomContactClient {
		return newWeComContactClient(input.CorpID, input.Secret)
	})
}

func newWeComIdentityService(
	repo interfaces.WeComIdentityRepository,
	userRepo interfaces.UserRepository,
	audit interfaces.AuditLogService,
	clientFactory wecomContactClientFactory,
) interfaces.WeComIdentityService {
	return &wecomIdentityService{
		repo:          repo,
		userRepo:      userRepo,
		audit:         audit,
		clientFactory: clientFactory,
	}
}

func (s *wecomIdentityService) Sync(
	ctx context.Context,
	tenantID uint64,
	input interfaces.WeComIdentitySyncInput,
) (*interfaces.WeComIdentitySyncResult, error) {
	if tenantID == 0 || strings.TrimSpace(input.CorpID) == "" || strings.TrimSpace(input.Secret) == "" {
		return nil, ErrWeComBindingInvalid
	}
	client := s.clientFactory(input)
	departments, err := client.departments(ctx)
	if err != nil {
		return nil, err
	}
	syncedAt := time.Now().UTC()
	deptRows := toWeComDepartments(tenantID, departments, syncedAt)
	if err := s.repo.UpsertDepartments(ctx, deptRows); err != nil {
		return nil, err
	}

	deptIDs := normalizedDepartmentIDs(input.DepartmentIDs)
	if len(deptIDs) == 0 {
		deptIDs = []string{defaultWeComRootDept}
	}
	usersByID := map[string]wecomUserPayload{}
	for _, departmentID := range deptIDs {
		users, err := client.users(ctx, departmentID, input.FetchChild)
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			userID := strings.TrimSpace(user.UserID)
			if userID != "" {
				usersByID[userID] = user
			}
		}
	}
	identityRows := toWeComIdentities(tenantID, usersByID, syncedAt)
	if err := s.repo.UpsertIdentities(ctx, identityRows); err != nil {
		return nil, err
	}
	for _, user := range usersByID {
		if err := s.repo.ReplaceIdentityDepartments(
			ctx, tenantID, user.UserID, departmentIDsFromAny(user.Department), syncedAt,
		); err != nil {
			return nil, err
		}
	}
	s.emitAudit(ctx, tenantID, types.AuditAction("wecom.identity_synced"), "wecom_identity", "", "")
	return &interfaces.WeComIdentitySyncResult{
		Departments: len(deptRows),
		Users:       len(identityRows),
		SyncedAt:    syncedAt,
	}, nil
}

func (s *wecomIdentityService) CreateOrUpdateBinding(
	ctx context.Context,
	tenantID uint64,
	input interfaces.WeComBindingInput,
) (*types.WeComUserBinding, error) {
	weknoraUserID := strings.TrimSpace(input.WeKnoraUserID)
	wecomUserID := strings.TrimSpace(input.WeComUserID)
	if tenantID == 0 || weknoraUserID == "" || wecomUserID == "" {
		return nil, ErrWeComBindingInvalid
	}
	if _, err := s.userRepo.GetUserByID(ctx, weknoraUserID); err != nil {
		return nil, err
	}
	identity, err := s.repo.FindIdentity(ctx, tenantID, wecomUserID)
	if errors.Is(err, apprepo.ErrWeComIdentityNotFound) {
		return nil, ErrWeComIdentityUnknown
	}
	if err != nil {
		return nil, err
	}
	if identity == nil || !types.IsWeComIdentityUsable(identity.Status) {
		return nil, ErrWeComIdentityInactive
	}
	binding, err := s.repo.UpsertBinding(ctx, &types.WeComUserBinding{
		TenantID:      tenantID,
		WeKnoraUserID: weknoraUserID,
		WeComUserID:   wecomUserID,
		Provider:      types.WeComProvider,
		Source:        defaultBindingSource(input.Source),
		Status:        types.WeComBindingStatusActive,
	})
	if err != nil {
		if errors.Is(err, apprepo.ErrWeComBindingAlreadyExists) || isDuplicateMembership(err) {
			return nil, ErrWeComBindingAlreadyExists
		}
		return nil, err
	}
	s.emitAudit(ctx, tenantID, types.AuditAction("wecom.binding_upserted"), "wecom_binding", wecomUserID, weknoraUserID)
	return binding, nil
}

func (s *wecomIdentityService) DeleteBinding(ctx context.Context, tenantID uint64, weknoraUserID string) error {
	err := s.repo.DeleteBinding(ctx, tenantID, strings.TrimSpace(weknoraUserID))
	if err != nil {
		return err
	}
	s.emitAudit(ctx, tenantID, types.AuditAction("wecom.binding_deleted"), "wecom_binding", "", weknoraUserID)
	return nil
}

func (s *wecomIdentityService) ListBindings(
	ctx context.Context,
	tenantID uint64,
	q interfaces.WeComBindingListQuery,
) ([]*types.WeComUserBinding, int64, error) {
	return s.repo.ListBindings(ctx, tenantID, q)
}

func (s *wecomIdentityService) ImportBindings(
	ctx context.Context,
	tenantID uint64,
	rows []interfaces.WeComBindingImportRow,
) []interfaces.WeComBindingImportResult {
	results := make([]interfaces.WeComBindingImportResult, 0, len(rows))
	for i, row := range rows {
		if row.RowNumber == 0 {
			row.RowNumber = i + 1
		}
		result := interfaces.WeComBindingImportResult{
			RowNumber:     row.RowNumber,
			WeKnoraUserID: row.WeKnoraUserID,
			Email:         row.Email,
			WeComUserID:   row.WeComUserID,
		}
		userID, err := s.resolveImportUserID(ctx, row)
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		result.WeKnoraUserID = userID
		_, err = s.CreateOrUpdateBinding(ctx, tenantID, interfaces.WeComBindingInput{
			WeKnoraUserID: userID,
			WeComUserID:   row.WeComUserID,
			Source:        types.WeComBindingSourceAdmin,
		})
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Success = true
		}
		results = append(results, result)
	}
	return results
}

func (s *wecomIdentityService) ResolveSubject(
	ctx context.Context,
	tenantID uint64,
	weknoraUserID string,
) (*types.WeComACLSubject, error) {
	return s.repo.ResolveSubject(ctx, tenantID, strings.TrimSpace(weknoraUserID))
}

func (s *wecomIdentityService) resolveImportUserID(
	ctx context.Context,
	row interfaces.WeComBindingImportRow,
) (string, error) {
	userID := strings.TrimSpace(row.WeKnoraUserID)
	if userID != "" {
		if _, err := s.userRepo.GetUserByID(ctx, userID); err != nil {
			return "", err
		}
		return userID, nil
	}
	email := strings.TrimSpace(row.Email)
	if email == "" {
		return "", fmt.Errorf("weknora_user_id or email is required")
	}
	user, err := s.userRepo.GetUserByEmail(ctx, email)
	if err != nil {
		return "", err
	}
	return user.ID, nil
}

func (s *wecomIdentityService) emitAudit(
	ctx context.Context,
	tenantID uint64,
	action types.AuditAction,
	targetType string,
	targetID string,
	targetUserID string,
) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Log(ctx, &types.AuditLog{
		TenantID:     tenantID,
		ActorUserID:  auditActor(ctx),
		ActorRole:    auditActorRole(ctx),
		Action:       action,
		TargetType:   targetType,
		TargetID:     targetID,
		TargetUserID: targetUserID,
		Outcome:      types.AuditOutcomeSuccess,
	})
}

func toWeComDepartments(
	tenantID uint64,
	departments []wecomDepartmentPayload,
	syncedAt time.Time,
) []*types.WeComDepartment {
	rows := make([]*types.WeComDepartment, 0, len(departments))
	for _, department := range departments {
		deptID := anyToString(department.ID)
		if deptID == "" {
			continue
		}
		rows = append(rows, &types.WeComDepartment{
			TenantID:     tenantID,
			Provider:     types.WeComProvider,
			DepartmentID: deptID,
			ParentID:     anyToString(department.ParentID),
			Name:         strings.TrimSpace(department.Name),
			Order:        department.Order,
			Status:       types.WeComIdentityStatusActive,
			SyncedAt:     syncedAt,
		})
	}
	return rows
}

func toWeComIdentities(
	tenantID uint64,
	usersByID map[string]wecomUserPayload,
	syncedAt time.Time,
) []*types.WeComIdentity {
	rows := make([]*types.WeComIdentity, 0, len(usersByID))
	for _, user := range usersByID {
		userID := strings.TrimSpace(user.UserID)
		if userID == "" {
			continue
		}
		meta, _ := json.Marshal(map[string]any{"source_status": user.Status})
		rows = append(rows, &types.WeComIdentity{
			TenantID: tenantID,
			Provider: types.WeComProvider,
			UserID:   userID,
			Name:     strings.TrimSpace(user.Name),
			Email:    strings.TrimSpace(user.Email),
			Mobile:   strings.TrimSpace(user.Mobile),
			Avatar:   strings.TrimSpace(user.Avatar),
			Status:   wecomStatus(user.Status),
			SyncedAt: syncedAt,
			Metadata: types.JSON(meta),
		})
	}
	return rows
}

func normalizedDepartmentIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func departmentIDsFromAny(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		id := anyToString(value)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func anyToString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return v.String()
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func wecomStatus(raw int) string {
	switch raw {
	case 1:
		return types.WeComIdentityStatusActive
	case 2:
		return types.WeComIdentityStatusSuspended
	case 4:
		return types.WeComIdentityStatusDeleted
	default:
		return types.WeComIdentityStatusUnavailable
	}
}

func defaultBindingSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return types.WeComBindingSourceAdmin
	}
	return source
}
