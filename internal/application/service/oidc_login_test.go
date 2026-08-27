package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/golang-jwt/jwt/v5"
)

type oidcMemoryUserRepo struct {
	interfaces.UserRepository
	bySubject  map[string]*types.User
	byEmail    map[string]*types.User
	byUsername map[string]*types.User
	created    []*types.User
}

func (r *oidcMemoryUserRepo) GetUserBySSOSubject(_ context.Context, subject string) (*types.User, error) {
	if r.bySubject == nil {
		return nil, apprepo.ErrUserNotFound
	}
	user, ok := r.bySubject[subject]
	if !ok {
		return nil, apprepo.ErrUserNotFound
	}
	cp := *user
	return &cp, nil
}

func (r *oidcMemoryUserRepo) GetUserByEmail(_ context.Context, email string) (*types.User, error) {
	if r.byEmail == nil {
		return nil, apprepo.ErrUserNotFound
	}
	user, ok := r.byEmail[email]
	if !ok {
		return nil, apprepo.ErrUserNotFound
	}
	cp := *user
	return &cp, nil
}

func (r *oidcMemoryUserRepo) GetUserByUsername(_ context.Context, username string) (*types.User, error) {
	if r.byUsername == nil {
		return nil, apprepo.ErrUserNotFound
	}
	user, ok := r.byUsername[username]
	if !ok {
		return nil, apprepo.ErrUserNotFound
	}
	cp := *user
	return &cp, nil
}

func (r *oidcMemoryUserRepo) CreateUser(_ context.Context, user *types.User) error {
	cp := *user
	r.created = append(r.created, &cp)
	if r.byEmail == nil {
		r.byEmail = map[string]*types.User{}
	}
	if r.byUsername == nil {
		r.byUsername = map[string]*types.User{}
	}
	if r.bySubject == nil {
		r.bySubject = map[string]*types.User{}
	}
	r.byEmail[user.Email] = &cp
	r.byUsername[user.Username] = &cp
	if user.SSOSubject != "" {
		r.bySubject[user.SSOSubject] = &cp
	}
	return nil
}

func (r *oidcMemoryUserRepo) UpdateUser(_ context.Context, user *types.User) error {
	cp := *user
	if r.byEmail == nil {
		r.byEmail = map[string]*types.User{}
	}
	if r.bySubject == nil {
		r.bySubject = map[string]*types.User{}
	}
	r.byEmail[user.Email] = &cp
	if user.SSOSubject != "" {
		r.bySubject[user.SSOSubject] = &cp
	}
	return nil
}

type oidcMemoryTokenRepo struct {
	interfaces.AuthTokenRepository
}

func (r *oidcMemoryTokenRepo) CreateToken(context.Context, *types.AuthToken) error { return nil }

func TestPKCES256ChallengeIsUnpaddedBase64URL(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	got := pkceS256Challenge(verifier)
	if strings.Contains(got, "=") {
		t.Fatalf("challenge has padding: %s", got)
	}
	if got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge = %s", got)
	}
}

func TestGetOIDCAuthorizationURLIncludesPKCEAndNonce(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	idp := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(idp.Close)

	svc := &userService{config: &config.Config{OIDCAuth: &config.OIDCAuthConfig{
		Enable:                true,
		ClientID:              "weknora-web",
		ClientSecret:          "secret",
		AuthorizationEndpoint: idp.URL + "/oauth/authorize",
		TokenEndpoint:         idp.URL + "/oauth/token",
		Scopes:                []string{"openid", "profile"},
		UserInfoMapping:       &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
	}}}

	resp, err := svc.GetOIDCAuthorizationURL(context.Background(), "https://app.example/api/v1/auth/oidc/callback")
	if err != nil {
		t.Fatalf("GetOIDCAuthorizationURL: %v", err)
	}
	if resp.CodeVerifier == "" || resp.Nonce == "" {
		t.Fatal("expected PKCE verifier and nonce")
	}
	u, err := url.Parse(resp.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") != pkceS256Challenge(resp.CodeVerifier) {
		t.Fatalf("code_challenge mismatch")
	}
	if q.Get("nonce") != resp.Nonce {
		t.Fatalf("nonce query = %q, want %q", q.Get("nonce"), resp.Nonce)
	}
	if q.Get("audience") != "" {
		t.Fatalf("webpage login must not send audience, got %q", q.Get("audience"))
	}
	if q.Get("client_id") != "weknora-web" {
		t.Fatalf("client_id = %q", q.Get("client_id"))
	}
}

func TestVerifyOIDCIDTokenRejectsBreakglass(t *testing.T) {
	key, server := startJWKSServer(t)
	raw := mustSignRS256(t, key, jwt.MapClaims{
		"iss":   server.URL,
		"sub":   "emp_1",
		"nonce": "n1",
		"acr":   oidcBreakglassACR,
		"aud":   "weknora-web",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	})
	err := verifyOIDCIDToken(context.Background(), &config.OIDCAuthConfig{
		IssuerURL:        server.URL,
		JWKSURI:          server.URL + "/jwks",
		ClientID:         "weknora-web",
		ResourceAudience: "weknora",
	}, raw, "n1")
	if err == nil || !strings.Contains(err.Error(), "breakglass") {
		t.Fatalf("error = %v, want breakglass rejection", err)
	}
}

func TestVerifyOIDCIDTokenNonceMismatch(t *testing.T) {
	key, server := startJWKSServer(t)
	raw := mustSignRS256(t, key, jwt.MapClaims{
		"iss":   server.URL,
		"sub":   "emp_1",
		"nonce": "other",
		"aud":   "weknora-web",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	})
	err := verifyOIDCIDToken(context.Background(), &config.OIDCAuthConfig{
		IssuerURL:        server.URL,
		JWKSURI:          server.URL + "/jwks",
		ClientID:         "weknora-web",
		ResourceAudience: "weknora",
	}, raw, "n1")
	if err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("error = %v, want nonce mismatch", err)
	}
}

func TestVerifyOIDCIDTokenRejectsUnsigned(t *testing.T) {
	_, server := startJWKSServer(t)
	raw := unsignedJWT(map[string]any{
		"iss":   server.URL,
		"sub":   "emp_1",
		"nonce": "n1",
		"aud":   "weknora-web",
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	err := verifyOIDCIDToken(context.Background(), &config.OIDCAuthConfig{
		IssuerURL: server.URL,
		JWKSURI:   server.URL + "/jwks",
		ClientID:  "weknora-web",
	}, raw, "n1")
	if err == nil {
		t.Fatal("expected unsigned id_token to be rejected")
	}
}

func TestFindOrProvisionOIDCUserKeysBySubject(t *testing.T) {
	existing := &types.User{ID: "u-sso", Email: "old@example.com", Username: "old", SSOSubject: "emp_abc"}
	repo := &oidcMemoryUserRepo{
		bySubject: map[string]*types.User{"emp_abc": existing},
		byEmail:   map[string]*types.User{"other@example.com": {ID: "u-email", Email: "other@example.com"}},
	}
	svc := &userService{userRepo: repo}
	user, isNew, err := svc.findOrProvisionOIDCUserBySubject(context.Background(), &types.OIDCUserInfo{
		Subject:  "emp_abc",
		Email:    "other@example.com",
		Username: "Alice",
	}, types.TenantProvisioningTenantless)
	if err != nil {
		t.Fatalf("findOrProvisionOIDCUserBySubject: %v", err)
	}
	if isNew || user.ID != "u-sso" {
		t.Fatalf("got user=%+v isNew=%v, want existing subject-keyed user", user, isNew)
	}
}

func TestSSOPlaceholderEmailDoesNotUseSubAsUsername(t *testing.T) {
	email := ssoPlaceholderEmail("emp_11111111-1111-1111-1111-111111111111")
	if !strings.HasPrefix(email, "sso-emp_") || !strings.HasSuffix(email, "@users.noreply.weknora.invalid") {
		t.Fatalf("placeholder email = %s", email)
	}
}

func TestLoginWithOIDCKeysBySubjectWithoutTokenExchange(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	const (
		sub      = "emp_22222222-2222-2222-2222-222222222222"
		nonce    = "nonce-login"
		verifier = "pkce-verifier-aaaaaaaaaaaaaaaaaaaaaaaa"
		loginTok = "sso-login-access"
	)
	key, jwks := mustRSAJWKS(t)
	var idToken string
	var sawVerifier, sawExchange, sawDirectory bool
	var userinfoAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.Contains(r.Form.Get("grant_type"), "token-exchange") {
			sawExchange = true
			http.Error(w, "token exchange must not be used for webpage login", http.StatusBadRequest)
			return
		}
		if r.Form.Get("code_verifier") == verifier {
			sawVerifier = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": loginTok,
			"id_token":     idToken,
			"token_type":   "Bearer",
			"expires_in":   900,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		userinfoAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sub":  sub,
			"name": "Alice",
		})
	})
	mux.HandleFunc("/QueryEmployeeDirectory", func(http.ResponseWriter, *http.Request) {
		sawDirectory = true
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	idToken = mustSignRS256(t, key, jwt.MapClaims{
		"iss":   server.URL,
		"sub":   sub,
		"nonce": nonce,
		"acr":   "urn:enterprise-sso:acr:wecom-scan",
		"aud":   "weknora-web",
		"name":  "Alice",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
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
			UserInfoEndpoint:      server.URL + "/userinfo",
			JWKSURI:               server.URL + "/jwks",
			Scopes:                []string{"openid", "profile"},
			UserInfoMapping:       &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
		}},
	}

	resp, err := svc.LoginWithOIDC(context.Background(), types.OIDCLoginInput{
		Code:         "auth-code",
		RedirectURI:  "https://app.example/api/v1/auth/oidc/callback",
		CodeVerifier: verifier,
		Nonce:        nonce,
	}, types.TenantProvisioningTenantless)
	if err != nil {
		t.Fatalf("LoginWithOIDC: %v", err)
	}
	if !resp.Success || resp.User == nil || !resp.IsNewUser {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.User.SSOSubject != sub {
		t.Fatalf("SSOSubject = %q, want %q", resp.User.SSOSubject, sub)
	}
	if resp.User.Username == sub {
		t.Fatal("username must not be the SSO sub")
	}
	if !sawVerifier {
		t.Fatal("expected authorization_code exchange to send code_verifier")
	}
	if sawExchange {
		t.Fatal("webpage login must not token-exchange")
	}
	_ = sawDirectory
	if userinfoAuth != "Bearer "+loginTok {
		t.Fatalf("userinfo used %q, want login access token", userinfoAuth)
	}
	if strings.Contains(resp.Token, loginTok) {
		t.Fatal("WeKnora JWT must not be the SSO access token")
	}
}

func unsignedJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".x"
}
