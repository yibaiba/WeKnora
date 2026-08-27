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
	Keys []oidcJWK `json:"keys"`
}

type oidcJWK struct {
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

var (
	jwksCacheMu sync.Mutex
	jwksCache   jwksCacheEntry
)

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
		pub, err := rsaPublicKeyFromJWK(key.N, key.E)
		if err != nil {
			continue
		}
		kid := strings.TrimSpace(key.Kid)
		if kid == "" {
			kid = fmt.Sprintf("idx-%d", i)
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

func loadOIDCRSAKey(ctx context.Context, jwksURI, kid string) (*rsa.PublicKey, error) {
	jwksURI = strings.TrimSpace(jwksURI)
	kid = strings.TrimSpace(kid)
	now := time.Now()

	jwksCacheMu.Lock()
	if jwksCache.uri == jwksURI && now.Before(jwksCache.expiresAt) {
		if kid != "" {
			if key, ok := jwksCache.keys[kid]; ok {
				jwksCacheMu.Unlock()
				return key, nil
			}
		}
	}
	jwksCacheMu.Unlock()

	keys, ttl, err := fetchOIDCJWKS(ctx, jwksURI)
	if err != nil {
		return nil, err
	}

	jwksCacheMu.Lock()
	jwksCache = jwksCacheEntry{
		uri:       jwksURI,
		keys:      keys,
		expiresAt: time.Now().Add(ttl),
	}
	jwksCacheMu.Unlock()

	if kid != "" {
		if key, ok := keys[kid]; ok {
			return key, nil
		}
		return nil, errors.New("no matching JWKS key")
	}
	for _, key := range keys {
		return key, nil
	}
	return nil, errors.New("no matching JWKS key")
}

func resetOIDCJWKSCacheForTest() {
	jwksCacheMu.Lock()
	jwksCache = jwksCacheEntry{}
	jwksCacheMu.Unlock()
}

func verifySSOResourceToken(ctx context.Context, cfg *config.OIDCAuthConfig, raw string) (jwt.MapClaims, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("missing access token")
	}
	if cfg == nil {
		return nil, errors.New("OIDC config is required")
	}
	unverified, err := decodeJWTClaims(raw)
	if err != nil {
		return nil, errors.New("invalid token")
	}
	if _, isString := unverified["aud"].(string); !isString {
		return nil, errors.New("token audience must be a single string")
	}
	expectedAud := oidcResourceAudience(cfg)
	if claimAsString(unverified, "aud") != expectedAud {
		return nil, errors.New("token audience mismatch")
	}
	if strings.TrimSpace(claimAsString(unverified, "sub")) == "" {
		return nil, errors.New("token subject is required")
	}

	jwksURI := strings.TrimSpace(cfg.JWKSURI)
	if jwksURI == "" {
		return nil, errors.New("OIDC jwks_uri is required")
	}

	parser := jwt.NewParser(jwt.WithValidMethods([]string{"RS256"}))
	token, err := parser.Parse(raw, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		return loadOIDCRSAKey(ctx, jwksURI, kid)
	})
	if err != nil {
		return nil, errors.New("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	if issuer := strings.TrimRight(strings.TrimSpace(cfg.IssuerURL), "/"); issuer != "" {
		iss := strings.TrimRight(claimAsString(map[string]interface{}(claims), "iss"), "/")
		if iss != "" && iss != issuer {
			return nil, errors.New("token issuer mismatch")
		}
	}
	return claims, nil
}
