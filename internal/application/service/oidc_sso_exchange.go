package service

import (
	"context"
	"errors"
	"strings"

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

	claims, err := s.tokenVerifier().verifyResourceToken(ctx, cfg, rawToken)
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

	s.applySSOAccessSideEffects(ctx, cfg, user, rawToken, info.WeComUserID)

	return s.issueWeKnoraSession(ctx, user, "登录成功")
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
