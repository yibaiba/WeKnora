package service

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

const jwksDefaultTTL = 5 * time.Minute

type oidcJWKSet struct {
	Keys []oidcCachedJWK `json:"keys"`
}

type oidcCachedJWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksCacheEntry struct {
	uri       string
	keys      map[string]*rsa.PublicKey
	expiresAt time.Time
}

type oidcJWTVerifier struct {
	mu    sync.Mutex
	cache jwksCacheEntry
}

func newOIDCJWTVerifier() *oidcJWTVerifier {
	return &oidcJWTVerifier{}
}

func oidcResourceAudience(cfg *config.OIDCAuthConfig) string {
	if cfg != nil {
		if aud := strings.TrimSpace(cfg.ResourceAudience); aud != "" {
			return aud
		}
	}
	return "weknora"
}

func rsaPublicKeyFromJWK(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decode JWK n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decode JWK e: %w", err)
	}
	if len(nBytes) == 0 || len(eBytes) == 0 {
		return nil, errors.New("empty JWK RSA parameters")
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e <= 0 {
		return nil, errors.New("invalid JWK RSA exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

func fetchOIDCJWKS(ctx context.Context, jwksURI string) (map[string]*rsa.PublicKey, time.Duration, error) {
	if err := validateOIDCEndpoint("jwks", jwksURI, true); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := newOIDCHTTPClient().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		return nil, 0, fmt.Errorf("JWKS request failed: status=%d", resp.StatusCode)
	}
	var set oidcJWKSet
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, 0, fmt.Errorf("decode JWKS: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for i := range set.Keys {
		key := &set.Keys[i]
		if key.Kty != "" && !strings.EqualFold(key.Kty, "RSA") {
			continue
		}
		if key.Use != "" && !strings.EqualFold(key.Use, "sig") {
			continue
		}
		kid := strings.TrimSpace(key.Kid)
		if kid == "" {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(key.N, key.E)
		if err != nil {
			continue
		}
		keys[kid] = pub
	}
	if len(keys) == 0 {
		return nil, 0, errors.New("JWKS contains no RSA signing keys")
	}
	ttl := jwksCacheTTL(resp.Header.Get("Cache-Control"))
	return keys, ttl, nil
}

func jwksCacheTTL(cacheControl string) time.Duration {
	ttl := jwksDefaultTTL
	for _, part := range strings.Split(cacheControl, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if strings.HasPrefix(part, "max-age=") {
			var seconds int
			if _, err := fmt.Sscanf(part, "max-age=%d", &seconds); err == nil && seconds > 0 {
				ttl = time.Duration(seconds) * time.Second
			}
		}
	}
	if ttl > 5*time.Minute {
		ttl = 5 * time.Minute
	}
	if ttl < time.Second {
		ttl = time.Second
	}
	return ttl
}

func decodeJWTClaims(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, errors.New("invalid JWT format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func jwtHeaderString(raw, field string) string {
	parts := strings.Split(raw, ".")
	if len(parts) < 1 {
		return ""
	}
	buf, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ""
	}
	var header map[string]interface{}
	if err := json.Unmarshal(buf, &header); err != nil {
		return ""
	}
	s, _ := header[field].(string)
	return strings.TrimSpace(s)
}

func (v *oidcJWTVerifier) loadKey(ctx context.Context, jwksURI, kid string) (*rsa.PublicKey, error) {
	jwksURI = strings.TrimSpace(jwksURI)
	kid = strings.TrimSpace(kid)
	if kid == "" {
		return nil, errors.New("token kid is required")
	}
	now := time.Now()

	v.mu.Lock()
	if v.cache.uri == jwksURI && now.Before(v.cache.expiresAt) {
		if key, ok := v.cache.keys[kid]; ok {
			v.mu.Unlock()
			return key, nil
		}
	}
	v.mu.Unlock()

	keys, ttl, err := fetchOIDCJWKS(ctx, jwksURI)
	if err != nil {
		return nil, err
	}

	v.mu.Lock()
	v.cache = jwksCacheEntry{
		uri:       jwksURI,
		keys:      keys,
		expiresAt: time.Now().Add(ttl),
	}
	v.mu.Unlock()

	if key, ok := keys[kid]; ok {
		return key, nil
	}
	return nil, errors.New("no matching JWKS key")
}

func (v *oidcJWTVerifier) verifyRS256(ctx context.Context, cfg *config.OIDCAuthConfig, raw, expectedAud string) (jwt.MapClaims, error) {
	if v == nil {
		return nil, errors.New("OIDC verifier is required")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("missing access token")
	}
	if cfg == nil {
		return nil, errors.New("OIDC config is required")
	}
	expectedIss := strings.TrimRight(strings.TrimSpace(cfg.IssuerURL), "/")
	if expectedIss == "" {
		return nil, errors.New("OIDC issuer_url is required")
	}
	jwksURI := strings.TrimSpace(cfg.JwksURI)
	if jwksURI == "" {
		return nil, errors.New("OIDC jwks_uri is required")
	}
	expectedAud = strings.TrimSpace(expectedAud)
	if expectedAud == "" {
		return nil, errors.New("token audience is required")
	}

	unverified, err := decodeJWTClaims(raw)
	if err != nil {
		return nil, errors.New("invalid token")
	}
	if _, isString := unverified["aud"].(string); !isString {
		return nil, errors.New("token audience must be a single string")
	}
	if claimAsString(unverified, "aud") != expectedAud {
		return nil, errors.New("token audience mismatch")
	}
	if strings.TrimSpace(claimAsString(unverified, "sub")) == "" {
		return nil, errors.New("token subject is required")
	}
	if jwtHeaderString(raw, "kid") == "" {
		return nil, errors.New("token kid is required")
	}

	parser := jwt.NewParser(jwt.WithValidMethods([]string{"RS256"}))
	token, err := parser.Parse(raw, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		return v.loadKey(ctx, jwksURI, strings.TrimSpace(kid))
	})
	if err != nil {
		return nil, errors.New("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	iss := strings.TrimRight(claimAsString(map[string]interface{}(claims), "iss"), "/")
	if iss == "" || iss != expectedIss {
		return nil, errors.New("token issuer mismatch")
	}
	return claims, nil
}

func (v *oidcJWTVerifier) verifyResourceToken(ctx context.Context, cfg *config.OIDCAuthConfig, raw string) (jwt.MapClaims, error) {
	return v.verifyRS256(ctx, cfg, raw, oidcResourceAudience(cfg))
}

func (v *oidcJWTVerifier) verifyIDToken(ctx context.Context, cfg *config.OIDCAuthConfig, raw, nonce string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("OIDC id_token is required")
	}
	expectedAud := ""
	if cfg != nil {
		expectedAud = strings.TrimSpace(cfg.ClientID)
	}
	if expectedAud == "" {
		return errors.New("OIDC client_id is required")
	}
	claims, err := v.verifyRS256(ctx, cfg, raw, expectedAud)
	if err != nil {
		return err
	}
	asMap := map[string]interface{}(claims)
	nonce = strings.TrimSpace(nonce)
	if nonce != "" && claimAsString(asMap, "nonce") != nonce {
		return errors.New("OIDC id_token nonce mismatch")
	}
	if claimAsString(asMap, "acr") == oidcBreakglassACR {
		return errors.New("breakglass session rejected")
	}
	return nil
}

func verifySSOResourceToken(ctx context.Context, cfg *config.OIDCAuthConfig, raw string) (jwt.MapClaims, error) {
	return newOIDCJWTVerifier().verifyResourceToken(ctx, cfg, raw)
}

func verifyOIDCIDToken(ctx context.Context, cfg *config.OIDCAuthConfig, raw, nonce string) error {
	return newOIDCJWTVerifier().verifyIDToken(ctx, cfg, raw, nonce)
}
