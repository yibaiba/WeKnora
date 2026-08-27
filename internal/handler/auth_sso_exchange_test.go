package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type stubSSOExchangeUserService struct {
	interfaces.UserService
	exchange func(ctx context.Context, token string, provisioning types.TenantProvisioningMode) (*types.LoginResponse, error)
}

func (s *stubSSOExchangeUserService) ExchangeSSOAccess(ctx context.Context, token string, provisioning types.TenantProvisioningMode) (*types.LoginResponse, error) {
	return s.exchange(ctx, token, provisioning)
}

func TestExchangeSSOAccessReturnsLoginShape(t *testing.T) {
	us := &stubSSOExchangeUserService{
		exchange: func(_ context.Context, token string, _ types.TenantProvisioningMode) (*types.LoginResponse, error) {
			if token != "sso-resource" {
				t.Fatalf("token = %q", token)
			}
			return &types.LoginResponse{
				Success:      true,
				User:         &types.User{ID: "u1", SSOSubject: "emp_1"},
				Memberships:  []types.Membership{},
				Token:        "weknora-jwt",
				RefreshToken: "weknora-refresh",
			}, nil
		},
	}
	h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/auth/sso/exchange", h.ExchangeSSOAccess)

	req := httptest.NewRequest(http.MethodPost, "/auth/sso/exchange", nil)
	req.Header.Set("Authorization", "Bearer sso-resource")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["token"] != "weknora-jwt" {
		t.Fatalf("token = %v", body["token"])
	}
}

func TestExchangeSSOAccessRequiresBearer(t *testing.T) {
	h := NewAuthHandler(&config.Config{}, &stubSSOExchangeUserService{
		exchange: func(context.Context, string, types.TenantProvisioningMode) (*types.LoginResponse, error) {
			t.Fatal("should not exchange")
			return nil, nil
		},
	}, nil, nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture())
	r.POST("/auth/sso/exchange", h.ExchangeSSOAccess)

	req := httptest.NewRequest(http.MethodPost, "/auth/sso/exchange", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatal("expected failure without Authorization")
	}
}

func TestExchangeSSOAccessMapsVerifyErrorTo401(t *testing.T) {
	us := &stubSSOExchangeUserService{
		exchange: func(context.Context, string, types.TenantProvisioningMode) (*types.LoginResponse, error) {
			return nil, errors.New("token audience mismatch")
		},
	}
	h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture())
	r.POST("/auth/sso/exchange", h.ExchangeSSOAccess)

	req := httptest.NewRequest(http.MethodPost, "/auth/sso/exchange", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}
