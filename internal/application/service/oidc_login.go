package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
)

const (
	oidcBreakglassACR = "urn:enterprise-sso:acr:breakglass"
	oidcTokenRetryMax = 3
)

func generatePKCEVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceS256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func ssoPlaceholderEmail(sub string) string {
	local := strings.ToLower(strings.TrimSpace(sub))
	local = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, local)
	if local == "" {
		local = "unknown"
	}
	return "sso-" + local + "@users.noreply.weknora.invalid"
}

func postOIDCTokenForm(ctx context.Context, cfg *config.OIDCAuthConfig, form url.Values) (*oidcTokenResponse, error) {
	var lastErr error
	for attempt := 0; attempt < oidcTokenRetryMax; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenEndpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, fmt.Errorf("create token request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		resp, err := newOIDCHTTPClient().Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read token response: %w", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			wait := retryAfterDuration(resp)
			lastErr = errors.New("OIDC token endpoint rate limited")
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("OIDC token request failed: status=%d", resp.StatusCode)
		}
		var tokenResp oidcTokenResponse
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			return nil, fmt.Errorf("decode token response: %w", err)
		}
		return &tokenResp, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("OIDC token request failed")
}

func retryAfterDuration(resp *http.Response) time.Duration {
	if resp == nil {
		return time.Second
	}
	n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
	if err != nil || n < 1 {
		return time.Second
	}
	return time.Duration(n) * time.Second
}

func verifyOIDCIDToken(_ context.Context, cfg *config.OIDCAuthConfig, raw, nonce string) error {
	raw = strings.TrimSpace(raw)
	nonce = strings.TrimSpace(nonce)
	if raw == "" {
		if nonce != "" {
			return errors.New("OIDC id_token is required")
		}
		return nil
	}
	claims, err := decodeJWTClaims(raw)
	if err != nil {
		return fmt.Errorf("decode OIDC id_token: %w", err)
	}
	if nonce != "" && claimAsString(claims, "nonce") != nonce {
		return errors.New("OIDC id_token nonce mismatch")
	}
	if claimAsString(claims, "acr") == oidcBreakglassACR {
		return errors.New("breakglass session rejected")
	}
	if cfg != nil {
		if aud := claimAsString(claims, "aud"); aud != "" && strings.TrimSpace(cfg.ClientID) != "" && aud != cfg.ClientID {
			return errors.New("OIDC id_token audience mismatch")
		}
	}
	return nil
}

func claimAsString(claims map[string]interface{}, key string) string {
	raw, ok := claims[key]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return v
	case []interface{}:
		if len(v) == 0 {
			return ""
		}
		if s, ok := v[0].(string); ok {
			return s
		}
	}
	return fmt.Sprint(raw)
}

func (s *userService) findOrProvisionOIDCUser(
	ctx context.Context,
	_ *config.OIDCAuthConfig,
	info *types.OIDCUserInfo,
	provisioning types.TenantProvisioningMode,
) (*types.User, bool, error) {
	if info != nil && strings.TrimSpace(info.Subject) != "" {
		return s.findOrProvisionOIDCUserBySubject(ctx, info, provisioning)
	}
	if info == nil || strings.TrimSpace(info.Email) == "" {
		return nil, false, errors.New("OIDC provider did not return sub or email")
	}
	user, err := s.userRepo.GetUserByEmail(ctx, info.Email)
	if err != nil && !isUserLookupNotFound(err) {
		return nil, false, fmt.Errorf("failed to query user by email: %w", err)
	}
	if isUserLookupNotFound(err) || user == nil {
		user, err = s.provisionOIDCUser(ctx, info, provisioning)
		if err != nil {
			return nil, false, err
		}
		return user, true, nil
	}
	s.syncOIDCUserProfile(ctx, user, info)
	return user, false, nil
}

func (s *userService) findOrProvisionOIDCUserBySubject(
	ctx context.Context,
	info *types.OIDCUserInfo,
	provisioning types.TenantProvisioningMode,
) (*types.User, bool, error) {
	sub := strings.TrimSpace(info.Subject)
	user, err := s.userRepo.GetUserBySSOSubject(ctx, sub)
	if err != nil && !isUserLookupNotFound(err) {
		return nil, false, fmt.Errorf("failed to query user by SSO subject: %w", err)
	}
	if user != nil {
		s.syncOIDCUserProfile(ctx, user, info)
		return user, false, nil
	}

	if email := strings.TrimSpace(info.Email); email != "" {
		existing, eerr := s.userRepo.GetUserByEmail(ctx, email)
		if eerr != nil && !isUserLookupNotFound(eerr) {
			return nil, false, fmt.Errorf("failed to query user by email: %w", eerr)
		}
		if existing != nil {
			if existing.SSOSubject != "" && existing.SSOSubject != sub {
				return nil, false, errors.New("OIDC subject does not match bound local user")
			}
			existing.SSOSubject = sub
			s.syncOIDCUserProfile(ctx, existing, info)
			return existing, false, nil
		}
	}

	user, err = s.provisionOIDCUser(ctx, info, provisioning)
	if err != nil {
		return nil, false, err
	}
	return user, true, nil
}

func (s *userService) syncOIDCUserProfile(ctx context.Context, user *types.User, info *types.OIDCUserInfo) {
	if user == nil || info == nil {
		return
	}
	changed := false
	if sub := strings.TrimSpace(info.Subject); sub != "" && user.SSOSubject != sub {
		user.SSOSubject = sub
		changed = true
	}
	if avatar := strings.TrimSpace(info.Avatar); avatar != "" && user.Avatar != avatar {
		user.Avatar = avatar
		changed = true
	}
	if !changed {
		return
	}
	user.UpdatedAt = time.Now()
	if err := s.userRepo.UpdateUser(ctx, user); err != nil {
		logger.Warnf(ctx, "OIDC login: failed to sync local user profile: %v", err)
	}
}
