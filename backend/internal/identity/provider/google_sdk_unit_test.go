//go:build unit

package provider

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/idtoken"
	"google.golang.org/api/option"
)

type googleFixtureTransport struct{ target *url.URL }

// RoundTrip 只把官方验证器的公钥读取导向本地 JWKS，保留真实 HTTP 和密码学校验。
func (t googleFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.URL = t.target
	return http.DefaultTransport.RoundTrip(copy)
}

// TestGoogleOfficialValidatorWithLocalJWKS 使用原官方验证库和本地 RSA 签名，不访问 Google 或生产凭据。
func TestGoogleOfficialValidatorWithLocalJWKS(t *testing.T) {
	ctx := context.Background()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, e)
	encode := base64.RawURLEncoding.EncodeToString
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "s05", "n": encode(key.N.Bytes()), "e": encode(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	defer endpoint.Close()
	target, e := url.Parse(endpoint.URL)
	require.NoError(t, e)
	validator, e := idtoken.NewValidator(ctx, option.WithHTTPClient(&http.Client{Transport: googleFixtureTransport{target}}))
	require.NoError(t, e)
	sign := func(claims map[string]any) string {
		header, e := json.Marshal(map[string]any{"alg": "RS256", "kid": "s05", "typ": "JWT"})
		require.NoError(t, e)
		payload, e := json.Marshal(claims)
		require.NoError(t, e)
		signed := encode(header) + "." + encode(payload)
		digest := sha256.Sum256([]byte(signed))
		signature, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		require.NoError(t, e)
		return signed + "." + encode(signature)
	}
	for _, sample := range []struct {
		name            string
		change          func(map[string]any)
		tamper, success bool
	}{
		{name: "valid", success: true},
		{name: "audience", change: func(c map[string]any) { c["aud"] = "another-client" }},
		{name: "expired", change: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }},
		{name: "issuer", change: func(c map[string]any) { c["iss"] = "https://wrong.example" }},
		{name: "verified-type", change: func(c map[string]any) { c["email_verified"] = "true" }},
		{name: "subject", change: func(c map[string]any) { c["sub"] = "" }},
		{name: "signature", tamper: true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			claims := map[string]any{"iss": "https://accounts.google.com", "aud": "s05-client", "sub": "local-subject", "email": "local@example.com", "email_verified": true, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
			if sample.change != nil {
				sample.change(claims)
			}
			token := sign(claims)
			if sample.tamper {
				parts := strings.Split(token, ".")
				signature, err := base64.RawURLEncoding.DecodeString(parts[2])
				require.NoError(t, err)
				signature[len(signature)-1] ^= 1
				parts[2] = encode(signature)
				token = strings.Join(parts, ".")
			} // 保持合法 JWT 结构，仅篡改签名。
			payload, e := validator.Validate(ctx, token, "s05-client")
			if e == nil {
				_, e = ValidateGoogleIDTokenPayload(payload, "s05-client", time.Now())
			}
			if sample.success {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
		})
	}
}
