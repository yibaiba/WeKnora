package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

type stubOIDCCallbackUserService struct {
	interfaces.UserService
	login func(ctx context.Context, in types.OIDCLoginInput, provisioning types.TenantProvisioningMode) (*types.OIDCCallbackResponse, error)
}

func (s *stubOIDCCallbackUserService) LoginWithOIDC(ctx context.Context, in types.OIDCLoginInput, provisioning types.TenantProvisioningMode) (*types.OIDCCallbackResponse, error) {
	return s.login(ctx, in, provisioning)
}

func oidcCallbackRequest(t *testing.T) *http.Request {
	t.Helper()
	state, err := secutils.SignOIDCState(&secutils.OIDCStatePayload{
		Nonce:       "nonce-callback",
		RedirectURI: "http://weknora.example/api/v1/auth/oidc/callback",
		IssuedAt:    time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("sign state: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=auth-code&state="+url.QueryEscape(state), nil)
	req.AddCookie(&http.Cookie{Name: oidcNonceCookieName, Value: "nonce-callback"})
	req.AddCookie(&http.Cookie{Name: oidcPKCECookieName, Value: "pkce-verifier"})
	return req
}

func TestOIDCRedirectCallbackDoesNotLeakErrorString(t *testing.T) {
	const secret = "sql: password=super-secret"
	us := &stubOIDCCallbackUserService{
		login: func(context.Context, types.OIDCLoginInput, types.TenantProvisioningMode) (*types.OIDCCallbackResponse, error) {
			return nil, errors.New(secret)
		},
	}
	h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/auth/oidc/callback", h.OIDCRedirectCallback)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, oidcCallbackRequest(t))
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "#oidc_error=login_failed") {
		t.Fatalf("Location = %q, want login_failed hash", loc)
	}
	if strings.Contains(loc, secret) || strings.Contains(loc, "oidc_error_description") {
		t.Fatalf("Location leaked backend error: %q", loc)
	}
}

func TestOIDCRedirectCallbackMapsDisabledAccount(t *testing.T) {
	us := &stubOIDCCallbackUserService{
		login: func(context.Context, types.OIDCLoginInput, types.TenantProvisioningMode) (*types.OIDCCallbackResponse, error) {
			return &types.OIDCCallbackResponse{Success: false, Message: "Account is disabled"}, nil
		},
	}
	h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/auth/oidc/callback", h.OIDCRedirectCallback)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, oidcCallbackRequest(t))
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "#oidc_error=account_disabled") {
		t.Fatalf("Location = %q, want account_disabled", loc)
	}
	if strings.Contains(loc, "Account is disabled") {
		t.Fatalf("Location leaked account message: %q", loc)
	}
}
