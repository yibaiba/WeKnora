package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/golang-jwt/jwt/v5"
)

func startJWKSServer(t *testing.T) (*rsa.PrivateKey, *httptest.Server) {
	t.Helper()
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	key, jwks := mustRSAJWKS(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return key, server
}

func TestExchangeSSOAccessIssuesWeKnoraJWT(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, jwks := mustRSAJWKS(t)
	var sawUserinfo bool
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(jwks)
	})
	mux.HandleFunc("/oauth/introspect", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("token") == "" {
			http.Error(w, "missing token", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "aud": "weknora"})
	})
	mux.HandleFunc("/userinfo", func(http.ResponseWriter, *http.Request) {
		sawUserinfo = true
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	const sub = "emp_33333333-3333-3333-3333-333333333333"
	raw := mustSignRS256(t, key, jwt.MapClaims{
		"iss":  server.URL,
		"sub":  sub,
		"aud":  "weknora",
		"azp":  "hermes",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iat":  time.Now().Unix(),
		"jti":  "jti-1",
		"name": "Hermes User",
	})

	repo := &oidcMemoryUserRepo{}
	svc := &userService{
		userRepo:  repo,
		tokenRepo: &oidcMemoryTokenRepo{},
		config: &config.Config{OIDCAuth: &config.OIDCAuthConfig{
			Enable:                true,
			IssuerURL:             server.URL,
			ClientID:              "weknora-web",
			ClientSecret:          "secret",
			AuthorizationEndpoint: server.URL + "/authorize",
			TokenEndpoint:         server.URL + "/token",
			JwksURI:               server.URL + "/jwks",
			Scopes:                []string{"openid", "profile"},
			UserInfoMapping:       &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
		}},
	}

	resp, err := svc.ExchangeSSOAccess(context.Background(), raw, types.TenantProvisioningTenantless)
	if err != nil {
		t.Fatalf("ExchangeSSOAccess: %v", err)
	}
	if !resp.Success || resp.User == nil || resp.Token == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.User.SSOSubject != sub {
		t.Fatalf("SSOSubject = %q, want %q", resp.User.SSOSubject, sub)
	}
	if resp.User.Username == sub {
		t.Fatal("username must not be the SSO sub")
	}
	if sawUserinfo {
		t.Fatal("resource ticket must not call UserInfo")
	}
	if resp.Token == raw || strings.Count(resp.Token, ".") != 2 {
		t.Fatal("expected a WeKnora JWT, not the SSO access token")
	}
}

func TestExchangeSSOAccessRejectsInactiveIntrospection(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, jwks := mustRSAJWKS(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	})
	mux.HandleFunc("/oauth/introspect", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	raw := mustSignRS256(t, key, jwt.MapClaims{
		"iss": server.URL,
		"sub": "emp_1",
		"aud": "weknora",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	svc := &userService{
		userRepo:  &oidcMemoryUserRepo{},
		tokenRepo: &oidcMemoryTokenRepo{},
		config: &config.Config{OIDCAuth: &config.OIDCAuthConfig{
			Enable:                true,
			IssuerURL:             server.URL,
			ClientID:              "weknora-web",
			ClientSecret:          "secret",
			AuthorizationEndpoint: server.URL + "/authorize",
			TokenEndpoint:         server.URL + "/token",
			JwksURI:               server.URL + "/jwks",
			UserInfoMapping:       &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
		}},
	}
	if _, err := svc.ExchangeSSOAccess(context.Background(), raw, types.TenantProvisioningTenantless); err == nil {
		t.Fatal("expected inactive introspect to fail exchange")
	}
}

func TestVerifySSOResourceTokenRejectsLoginAudience(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, jwks := mustRSAJWKS(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	cfg := &config.OIDCAuthConfig{
		IssuerURL:        server.URL,
		JwksURI:          server.URL + "/jwks",
		ResourceAudience: "weknora",
	}
	raw := mustSignRS256(t, key, jwt.MapClaims{
		"iss": server.URL,
		"sub": "emp_1",
		"aud": "sso",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	if _, err := verifySSOResourceToken(context.Background(), cfg, raw); err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("error = %v, want audience mismatch", err)
	}
}

func TestVerifySSOResourceTokenRejectsNoneAlg(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	_, jwks := mustRSAJWKS(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	cfg := &config.OIDCAuthConfig{
		IssuerURL: server.URL,
		JwksURI:   server.URL + "/jwks",
	}
	raw := unsignedJWT(map[string]any{
		"iss": server.URL,
		"sub": "emp_1",
		"aud": "weknora",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := verifySSOResourceToken(context.Background(), cfg, raw); err == nil {
		t.Fatal("expected unsigned token to be rejected")
	}
}

func TestVerifySSOResourceTokenRejectsExpired(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, jwks := mustRSAJWKS(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	cfg := &config.OIDCAuthConfig{
		IssuerURL: server.URL,
		JwksURI:   server.URL + "/jwks",
	}
	raw := mustSignRS256(t, key, jwt.MapClaims{
		"iss": server.URL,
		"sub": "emp_1",
		"aud": "weknora",
		"exp": time.Now().Add(-time.Hour).Unix(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	})
	if _, err := verifySSOResourceToken(context.Background(), cfg, raw); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestVerifySSOResourceTokenRejectsArrayAudience(t *testing.T) {
	cfg := &config.OIDCAuthConfig{
		IssuerURL: "http://127.0.0.1",
		JwksURI:   "http://127.0.0.1/jwks",
	}
	raw := unsignedJWT(map[string]any{
		"iss": "http://127.0.0.1",
		"sub": "emp_1",
		"aud": []string{"weknora", "owc"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := verifySSOResourceToken(context.Background(), cfg, raw); err == nil || !strings.Contains(err.Error(), "single string") {
		t.Fatalf("error = %v, want single-string audience rejection", err)
	}
}

func TestVerifySSOResourceTokenRejectsEmptyKid(t *testing.T) {
	key, server := startJWKSServer(t)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": server.URL,
		"sub": "emp_1",
		"aud": "weknora",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	raw, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	cfg := &config.OIDCAuthConfig{
		IssuerURL: server.URL,
		JwksURI:   server.URL + "/jwks",
	}
	if _, err := verifySSOResourceToken(context.Background(), cfg, raw); err == nil || !strings.Contains(err.Error(), "kid") {
		t.Fatalf("error = %v, want kid required", err)
	}
}

func TestVerifySSOResourceTokenRejectsEmptyIssuer(t *testing.T) {
	key, server := startJWKSServer(t)
	raw := mustSignRS256(t, key, jwt.MapClaims{
		"sub": "emp_1",
		"aud": "weknora",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	cfg := &config.OIDCAuthConfig{
		IssuerURL: server.URL,
		JwksURI:   server.URL + "/jwks",
	}
	if _, err := verifySSOResourceToken(context.Background(), cfg, raw); err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("error = %v, want issuer mismatch", err)
	}
}

func TestExchangeSSOAccessMembershipsIncludeAutoJoinedTenant(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	key, jwks := mustRSAJWKS(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	})
	mux.HandleFunc("/oauth/introspect", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "aud": "weknora"})
	})
	mux.HandleFunc("/QueryEmployeeDirectory", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(types.SSODirectorySnapshot{
			Sub: "emp_join",
			Departments: []types.SSODepartment{
				{WeComDepartmentID: 42, Name: "Sales", IsMain: true},
			},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	raw := mustSignRS256(t, key, jwt.MapClaims{
		"iss":          server.URL,
		"sub":          "emp_join",
		"aud":          "weknora",
		"name":         "Join User",
		"wecom_userid": "wx-join",
		"exp":          time.Now().Add(time.Hour).Unix(),
		"iat":          time.Now().Unix(),
	})

	wecom := &sideEffectWeCom{tenants: []uint64{9}}
	members := &sideEffectMembers{}
	tenants := &sideEffectTenants{}
	repo := &oidcMemoryUserRepo{}
	svc := &userService{
		userRepo:      repo,
		tokenRepo:     &oidcMemoryTokenRepo{},
		memberService: members,
		tenantService: tenants,
		ssoEffects:    newSSOAccessEffects(repo, tenants, members, wecom),
		config: &config.Config{OIDCAuth: &config.OIDCAuthConfig{
			Enable:                true,
			IssuerURL:             server.URL,
			ClientID:              "weknora-web",
			ClientSecret:          "secret",
			AuthorizationEndpoint: server.URL + "/authorize",
			TokenEndpoint:         server.URL + "/token",
			JwksURI:               server.URL + "/jwks",
			UserInfoMapping:       &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
		}},
	}

	resp, err := svc.ExchangeSSOAccess(context.Background(), raw, types.TenantProvisioningTenantless)
	if err != nil {
		t.Fatalf("ExchangeSSOAccess: %v", err)
	}
	if !resp.Success {
		t.Fatalf("unexpected response: %+v", resp)
	}
	foundJoined := false
	for _, m := range resp.Memberships {
		if m.TenantID == 9 {
			foundJoined = true
			break
		}
	}
	if !foundJoined {
		t.Fatalf("memberships = %+v, want tenant 9 from auto-join", resp.Memberships)
	}
	if len(wecom.bound) != 1 {
		t.Fatalf("expected one WeCom bind for joined tenant, got %d", len(wecom.bound))
	}
}

func mustRSAJWKS(t *testing.T) (*rsa.PrivateKey, oidcJWKSet) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1})
	return key, oidcJWKSet{Keys: []oidcCachedJWK{{
		Kid: "k1",
		Kty: "RSA",
		Alg: "RS256",
		Use: "sig",
		N:   n,
		E:   e,
	}}}
}

func mustSignRS256(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "k1"
	raw, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return raw
}
