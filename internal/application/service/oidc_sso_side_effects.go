package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func oidcIntrospectionURL(cfg *config.OIDCAuthConfig) string {
	if cfg == nil {
		return ""
	}
	if endpoint := strings.TrimSpace(cfg.IntrospectionEndpoint); endpoint != "" {
		return endpoint
	}
	if issuer := strings.TrimRight(strings.TrimSpace(cfg.IssuerURL), "/"); issuer != "" {
		return issuer + "/oauth/introspect"
	}
	return ""
}

func (s *userService) introspectSSOResourceToken(ctx context.Context, cfg *config.OIDCAuthConfig, accessToken string) error {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return errors.New("missing access token")
	}
	endpoint := oidcIntrospectionURL(cfg)
	if endpoint == "" {
		return errors.New("OIDC introspection_endpoint is required")
	}
	if err := validateOIDCEndpoint("introspection", endpoint, true); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("token", accessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)

	resp, err := newOIDCHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("introspect: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return errors.New("introspect rate limited")
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errors.New("token is not active")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("introspect failed: status=%d", resp.StatusCode)
	}
	var payload struct {
		Active bool `json:"active"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return errors.New("invalid introspect response")
	}
	if !payload.Active {
		return errors.New("token is not active")
	}
	return nil
}

func (s *userService) queryEmployeeDirectory(ctx context.Context, cfg *config.OIDCAuthConfig, accessToken string) (*types.SSODirectorySnapshot, error) {
	issuer := strings.TrimRight(strings.TrimSpace(cfg.IssuerURL), "/")
	if issuer == "" {
		return nil, errors.New("OIDC issuer_url is required for QueryEmployeeDirectory")
	}
	if strings.TrimSpace(accessToken) == "" {
		return nil, errors.New("access token is required for QueryEmployeeDirectory")
	}
	endpoint := issuer + "/QueryEmployeeDirectory"
	if err := validateOIDCEndpoint("directory", endpoint, true); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := newOIDCHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("employee directory request failed: status=%d", resp.StatusCode)
	}
	var snapshot types.SSODirectorySnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return nil, fmt.Errorf("decode employee directory: %w", err)
	}
	if snapshot.Departments == nil {
		snapshot.Departments = []types.SSODepartment{}
	}
	return &snapshot, nil
}

// applySSOAccessSideEffects uses QueryEmployeeDirectory for the current
// employee only (not the org tree): upserts their departments locally,
// binds WeCom identity for Source ACL, and adds viewer memberships on
// workspaces that already have those department records.
func (s *userService) applySSOAccessSideEffects(
	ctx context.Context,
	cfg *config.OIDCAuthConfig,
	user *types.User,
	directoryAccess string,
	wecomUserID string,
) {
	if user == nil {
		return
	}
	if cfg == nil || strings.TrimSpace(directoryAccess) == "" {
		s.bindWeComFromSSO(ctx, user, wecomUserID, user.TenantID)
		return
	}
	dir, err := s.queryEmployeeDirectory(ctx, cfg, directoryAccess)
	if err != nil {
		logger.Warnf(ctx, "SSO directory lookup failed for user %s: %v", user.ID, err)
		s.bindWeComFromSSO(ctx, user, wecomUserID, user.TenantID)
		return
	}
	user.Preferences.SSODirectory = dir
	user.UpdatedAt = time.Now()
	if uerr := s.userRepo.UpdateUser(ctx, user); uerr != nil {
		logger.Warnf(ctx, "SSO directory snapshot persist failed for user %s: %v", user.ID, uerr)
	}

	s.ensureSSOEmployeeInTenant(ctx, user.TenantID, wecomUserID, user.Username, dir.Departments)
	s.bindWeComFromSSO(ctx, user, wecomUserID, user.TenantID)

	deptIDs := make([]string, 0, len(dir.Departments))
	for _, dept := range dir.Departments {
		if dept.WeComDepartmentID == 0 {
			continue
		}
		deptIDs = append(deptIDs, strconv.FormatInt(dept.WeComDepartmentID, 10))
	}
	s.autoJoinTenantsByDepartments(ctx, user, deptIDs, wecomUserID, dir.Departments)
}

func (s *userService) bindWeComFromSSO(ctx context.Context, user *types.User, wecomUserID string, tenantID uint64) {
	if s.wecomIdentity == nil || user == nil {
		return
	}
	wecomUserID = strings.TrimSpace(wecomUserID)
	if wecomUserID == "" || tenantID == 0 {
		return
	}
	subject, err := s.wecomIdentity.ResolveSubject(ctx, tenantID, user.ID)
	if err != nil {
		logger.Warnf(ctx, "SSO WeCom resolve failed tenant=%d user=%s: %v", tenantID, user.ID, err)
		return
	}
	if subject != nil && subject.Bound {
		if subject.WeComUserID != "" && subject.WeComUserID != wecomUserID {
			logger.Warnf(ctx, "SSO WeCom bind skipped: user %s already bound to %s in tenant %d", user.ID, subject.WeComUserID, tenantID)
		}
		return
	}
	_, err = s.wecomIdentity.CreateOrUpdateBinding(ctx, tenantID, interfaces.WeComBindingInput{
		WeKnoraUserID: user.ID,
		WeComUserID:   wecomUserID,
		Source:        types.WeComBindingSourceSSO,
	})
	if err != nil {
		logger.Warnf(ctx, "SSO WeCom bind failed tenant=%d user=%s: %v", tenantID, user.ID, err)
	}
}

func (s *userService) ensureSSOEmployeeInTenant(
	ctx context.Context,
	tenantID uint64,
	wecomUserID, name string,
	departments []types.SSODepartment,
) {
	if s.wecomIdentity == nil || tenantID == 0 {
		return
	}
	if err := s.wecomIdentity.EnsureSSOEmployee(ctx, tenantID, wecomUserID, name, departments); err != nil {
		logger.Warnf(ctx, "SSO WeCom employee upsert failed tenant=%d: %v", tenantID, err)
	}
}

func (s *userService) autoJoinTenantsByDepartments(
	ctx context.Context,
	user *types.User,
	departmentIDs []string,
	wecomUserID string,
	departments []types.SSODepartment,
) {
	if s.wecomIdentity == nil || s.memberService == nil || user == nil || len(departmentIDs) == 0 {
		return
	}
	tenantIDs, err := s.wecomIdentity.ListTenantIDsByDepartmentIDs(ctx, departmentIDs)
	if err != nil {
		logger.Warnf(ctx, "SSO department tenant lookup failed for user %s: %v", user.ID, err)
		return
	}
	for _, tenantID := range tenantIDs {
		if tenantID == 0 || tenantID == user.TenantID {
			continue
		}
		if s.tenantService != nil {
			tenant, terr := s.tenantService.GetTenantByID(ctx, tenantID)
			if terr != nil || tenant == nil || !strings.EqualFold(tenant.Status, "active") {
				continue
			}
		}
		s.ensureSSOEmployeeInTenant(ctx, tenantID, wecomUserID, user.Username, departments)
		_, err := s.memberService.AddMember(ctx, user.ID, tenantID, types.TenantRoleViewer, nil)
		if err != nil && !errors.Is(err, ErrMembershipAlreadyExists) {
			logger.Warnf(ctx, "SSO auto-join failed user=%s tenant=%d: %v", user.ID, tenantID, err)
			continue
		}
		s.bindWeComFromSSO(ctx, user, wecomUserID, tenantID)
	}
}
