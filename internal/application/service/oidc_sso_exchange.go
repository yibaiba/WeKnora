package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/golang-jwt/jwt/v5"
)

func (s *userService) ExchangeSSOAccess(
	ctx context.Context,
	rawToken string,
	provisioning types.TenantProvisioningMode,
) (*types.LoginResponse, error) {
	cfg, err := s.getOIDCConfig(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.JWKSURI) == "" && strings.TrimSpace(cfg.DiscoveryURL) == "" && strings.TrimSpace(cfg.IssuerURL) == "" {
		return nil, errors.New("OIDC jwks_uri is required")
	}

	claims, err := verifySSOResourceToken(ctx, cfg, rawToken)
	if err != nil {
		return nil, err
	}
	if err := s.introspectSSOResourceToken(ctx, cfg, rawToken); err != nil {
		return nil, err
	}

	info := ssoResourceUserInfo(claims)
	user, _, err := s.findOrProvisionOIDCUserBySubject(ctx, info, provisioning)
	if err != nil {
		return nil, err
	}
	if !user.IsActive {
		return &types.LoginResponse{Success: false, Message: "Account is disabled"}, nil
	}

	resolvedTenantID := s.resolveLoginTenantID(ctx, user)
	accessToken, refreshToken, err := s.generateTokensForTenant(ctx, user, resolvedTenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate local tokens: %w", err)
	}

	var tenant *types.Tenant
	if resolvedTenantID > 0 && s.tenantService != nil {
		if t, terr := s.tenantService.GetTenantByID(ctx, resolvedTenantID); terr == nil {
			tenant = t
		} else {
			logger.Warnf(ctx, "SSO exchange: failed to load tenant %d for user %s: %v",
				resolvedTenantID, user.ID, terr)
		}
	}
	memberships := s.buildMembershipsForUser(ctx, user, tenant)

	s.applySSOAccessSideEffects(ctx, cfg, user, rawToken, info.WeComUserID)
	if resolvedTenantID > 0 {
		s.bindWeComFromSSO(ctx, user, info.WeComUserID, resolvedTenantID)
	}

	return &types.LoginResponse{
		Success:      true,
		Message:      "登录成功",
		User:         user,
		ActiveTenant: tenant,
		Memberships:  memberships,
		Token:        accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func ssoResourceUserInfo(claims jwt.MapClaims) *types.OIDCUserInfo {
	asMap := map[string]interface{}(claims)
	info := &types.OIDCUserInfo{
		Subject:     strings.TrimSpace(claimAsString(asMap, "sub")),
		Username:    strings.TrimSpace(claimAsString(asMap, "name")),
		Email:       strings.TrimSpace(claimAsString(asMap, "email")),
		WeComUserID: strings.TrimSpace(claimAsString(asMap, "wecom_userid")),
		Claims:      asMap,
	}
	if info.Username == "" {
		info.Username = strings.TrimSpace(claimAsString(asMap, "preferred_username"))
	}
	return info
}
