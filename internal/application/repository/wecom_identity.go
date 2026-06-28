package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrWeComIdentityNotFound     = errors.New("wecom identity not found")
	ErrWeComBindingAlreadyExists = errors.New("wecom binding already exists")
)

type wecomIdentityRepository struct {
	db *gorm.DB
}

func NewWeComIdentityRepository(db *gorm.DB) interfaces.WeComIdentityRepository {
	return &wecomIdentityRepository{db: db}
}

func (r *wecomIdentityRepository) UpsertIdentities(ctx context.Context, identities []*types.WeComIdentity) error {
	for _, identity := range identities {
		if identity == nil {
			continue
		}
		identity.Provider = defaultString(identity.Provider, types.WeComProvider)
		if identity.SyncedAt.IsZero() {
			identity.SyncedAt = time.Now().UTC()
		}
		if err := r.upsertIdentity(ctx, identity); err != nil {
			return err
		}
	}
	return nil
}

func (r *wecomIdentityRepository) upsertIdentity(ctx context.Context, identity *types.WeComIdentity) error {
	var existing types.WeComIdentity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND userid = ? AND deleted_at IS NULL", identity.TenantID, identity.UserID).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.WithContext(ctx).Create(identity).Error
	}
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&existing).Updates(map[string]any{
		"provider":   identity.Provider,
		"name":       identity.Name,
		"email":      identity.Email,
		"mobile":     identity.Mobile,
		"avatar":     identity.Avatar,
		"status":     identity.Status,
		"synced_at":  identity.SyncedAt,
		"metadata":   identity.Metadata,
		"updated_at": time.Now().UTC(),
	}).Error
}

func (r *wecomIdentityRepository) UpsertDepartments(ctx context.Context, departments []*types.WeComDepartment) error {
	for _, department := range departments {
		if department == nil {
			continue
		}
		department.Provider = defaultString(department.Provider, types.WeComProvider)
		if department.SyncedAt.IsZero() {
			department.SyncedAt = time.Now().UTC()
		}
		if err := r.upsertDepartment(ctx, department); err != nil {
			return err
		}
	}
	return nil
}

func (r *wecomIdentityRepository) upsertDepartment(ctx context.Context, department *types.WeComDepartment) error {
	var existing types.WeComDepartment
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND department_id = ? AND deleted_at IS NULL", department.TenantID, department.DepartmentID).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.WithContext(ctx).Create(department).Error
	}
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&existing).Updates(map[string]any{
		"provider":   department.Provider,
		"parent_id":  department.ParentID,
		"name":       department.Name,
		"dept_order": department.Order,
		"status":     department.Status,
		"synced_at":  department.SyncedAt,
		"updated_at": time.Now().UTC(),
	}).Error
}

func (r *wecomIdentityRepository) ReplaceIdentityDepartments(
	ctx context.Context,
	tenantID uint64,
	userID string,
	departmentIDs []string,
	syncedAt time.Time,
) error {
	userID = strings.TrimSpace(userID)
	if tenantID == 0 || userID == "" {
		return errors.New("tenant id and userid are required")
	}
	if syncedAt.IsZero() {
		syncedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND userid = ?", tenantID, userID).
			Delete(&types.WeComIdentityDepartment{}).Error; err != nil {
			return err
		}
		rows := make([]types.WeComIdentityDepartment, 0, len(departmentIDs))
		seen := make(map[string]struct{}, len(departmentIDs))
		for _, departmentID := range departmentIDs {
			departmentID = strings.TrimSpace(departmentID)
			if departmentID == "" {
				continue
			}
			if _, ok := seen[departmentID]; ok {
				continue
			}
			seen[departmentID] = struct{}{}
			rows = append(rows, types.WeComIdentityDepartment{
				TenantID:     tenantID,
				UserID:       userID,
				DepartmentID: departmentID,
				SyncedAt:     syncedAt,
			})
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	})
}

func (r *wecomIdentityRepository) FindIdentity(
	ctx context.Context, tenantID uint64, userID string,
) (*types.WeComIdentity, error) {
	var identity types.WeComIdentity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND userid = ? AND deleted_at IS NULL", tenantID, strings.TrimSpace(userID)).
		First(&identity).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWeComIdentityNotFound
	}
	if err != nil {
		return nil, err
	}
	return &identity, nil
}

func (r *wecomIdentityRepository) UpsertBinding(
	ctx context.Context, binding *types.WeComUserBinding,
) (*types.WeComUserBinding, error) {
	if binding == nil {
		return nil, errors.New("wecom binding is nil")
	}
	binding.Provider = defaultString(binding.Provider, types.WeComProvider)
	binding.Source = defaultString(binding.Source, types.WeComBindingSourceAdmin)
	binding.Status = defaultString(binding.Status, types.WeComBindingStatusActive)
	now := time.Now().UTC()
	return binding, r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := rejectConflictingWeComBinding(tx, binding.TenantID, binding.WeKnoraUserID, binding.WeComUserID); err != nil {
			return err
		}
		if err := softDeleteActiveBindingForUser(tx, binding.TenantID, binding.WeKnoraUserID); err != nil {
			return err
		}
		binding.ID = 0
		binding.LastVerifiedAt = &now
		return tx.Create(binding).Error
	})
}

func rejectConflictingWeComBinding(tx *gorm.DB, tenantID uint64, weknoraUserID, wecomUserID string) error {
	var existing types.WeComUserBinding
	err := tx.Where("tenant_id = ? AND wecom_userid = ? AND weknora_user_id <> ? AND deleted_at IS NULL",
		tenantID, wecomUserID, weknoraUserID).
		Where("status = ?", types.WeComBindingStatusActive).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrWeComBindingAlreadyExists
}

func softDeleteActiveBindingForUser(tx *gorm.DB, tenantID uint64, weknoraUserID string) error {
	res := tx.Model(&types.WeComUserBinding{}).
		Where("tenant_id = ?", tenantID).
		Where("deleted_at IS NULL").
		Where("status = ?", types.WeComBindingStatusActive).
		Where("weknora_user_id = ?", weknoraUserID).
		Updates(map[string]any{
			"status":     types.WeComBindingStatusDeleted,
			"deleted_at": time.Now().UTC(),
			"updated_at": time.Now().UTC(),
		})
	return res.Error
}

func (r *wecomIdentityRepository) DeleteBinding(ctx context.Context, tenantID uint64, weknoraUserID string) error {
	res := r.db.WithContext(ctx).Model(&types.WeComUserBinding{}).
		Where("tenant_id = ? AND weknora_user_id = ? AND deleted_at IS NULL", tenantID, weknoraUserID).
		Where("status = ?", types.WeComBindingStatusActive).
		Updates(map[string]any{
			"status":     types.WeComBindingStatusDeleted,
			"deleted_at": time.Now().UTC(),
			"updated_at": time.Now().UTC(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *wecomIdentityRepository) ListBindings(
	ctx context.Context,
	tenantID uint64,
	q interfaces.WeComBindingListQuery,
) ([]*types.WeComUserBinding, int64, error) {
	base := r.db.WithContext(ctx).Model(&types.WeComUserBinding{}).
		Where("tenant_id = ? AND deleted_at IS NULL", tenantID).
		Where("status = ?", types.WeComBindingStatusActive)
	search := strings.TrimSpace(q.Search)
	if search != "" {
		like := "%" + escapeLikePattern(search) + "%"
		base = base.Where(
			"(LOWER(weknora_user_id) LIKE LOWER(?) OR LOWER(wecom_userid) LIKE LOWER(?))",
			like, like,
		)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	var bindings []*types.WeComUserBinding
	err := base.Order("updated_at DESC, id DESC").Offset(offset).Limit(limit).Find(&bindings).Error
	return bindings, total, err
}

func (r *wecomIdentityRepository) ResolveSubject(
	ctx context.Context, tenantID uint64, weknoraUserID string,
) (*types.WeComACLSubject, error) {
	var binding types.WeComUserBinding
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND weknora_user_id = ? AND deleted_at IS NULL", tenantID, weknoraUserID).
		Where("status = ?", types.WeComBindingStatusActive).
		First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return unboundSubject(weknoraUserID), nil
	}
	if err != nil {
		return nil, err
	}

	identity, err := r.FindIdentity(ctx, tenantID, binding.WeComUserID)
	if errors.Is(err, ErrWeComIdentityNotFound) || (identity != nil && !types.IsWeComIdentityUsable(identity.Status)) {
		return &types.WeComACLSubject{
			Bound:         false,
			WeKnoraUserID: weknoraUserID,
			WeComUserID:   binding.WeComUserID,
			Status:        types.WeComIdentityStatusUnavailable,
			Departments:   []string{},
			Groups:        []string{},
			ResolvedAt:    time.Now().UTC(),
		}, nil
	}
	if err != nil {
		return nil, err
	}

	departments, err := r.identityDepartments(ctx, tenantID, binding.WeComUserID)
	if err != nil {
		return nil, err
	}
	return &types.WeComACLSubject{
		Bound:         true,
		WeKnoraUserID: weknoraUserID,
		WeComUserID:   binding.WeComUserID,
		Status:        identity.Status,
		Departments:   departments,
		Groups:        []string{},
		ResolvedAt:    time.Now().UTC(),
	}, nil
}

func (r *wecomIdentityRepository) identityDepartments(ctx context.Context, tenantID uint64, userID string) ([]string, error) {
	var rows []types.WeComIdentityDepartment
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND userid = ?", tenantID, userID).
		Order("department_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.DepartmentID)
	}
	return out, nil
}

func unboundSubject(weknoraUserID string) *types.WeComACLSubject {
	return &types.WeComACLSubject{
		Bound:         false,
		WeKnoraUserID: weknoraUserID,
		Departments:   []string{},
		Groups:        []string{},
		ResolvedAt:    time.Now().UTC(),
	}
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
