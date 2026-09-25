// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/golang-jwt/jwt/v5"
	"github.com/imroc/req/v3"
	"github.com/tidwall/gjson"
)

type OidcTokenResponse = identity.OIDCTokenResponse
type OidcTokenExchangeError = identity.OIDCTokenExchangeError

type OidcIDTokenClaims struct {
	Email             string `json:"email,omitempty"`
	EmailVerified     *bool  `json:"email_verified,omitempty"`
	PreferredUsername string `json:"preferred_username,omitempty"`
	Name              string `json:"name,omitempty"`
	Nonce             string `json:"nonce,omitempty"`
	Azp               string `json:"azp,omitempty"`
	jwt.RegisteredClaims
}

type OidcUserInfoClaims = identity.OIDCUserInfoClaims

type OidcJWKSet struct {
	Keys []OidcJWK `json:"keys"`
}

type OidcJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`

	N string `json:"n"`
	E string `json:"e"`

	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func OidcExchangeCode(
	ctx context.Context,
	cfg OIDCOptions,
	code string,
	redirectURI string,
	codeVerifier string,
) (*OidcTokenResponse, error) {
	client := req.C().SetTimeout(30 * time.Second)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", cfg.ClientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	if strings.TrimSpace(codeVerifier) != "" {
		form.Set("code_verifier", codeVerifier)
	}

	r := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json")

	switch strings.ToLower(strings.TrimSpace(cfg.TokenAuthMethod)) {
	case "", "client_secret_post":
		form.Set("client_secret", cfg.ClientSecret)
	case "client_secret_basic":
		r.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	case "none":
	default:
		return nil, fmt.Errorf("unsupported token_auth_method: %s", cfg.TokenAuthMethod)
	}

	resp, err := r.SetFormDataFromValues(form).Post(cfg.TokenURL)
	if err != nil {
		return nil, fmt.Errorf("request token: %w", err)
	}
	body := strings.TrimSpace(resp.String())
	if !resp.IsSuccessState() {
		providerErr, providerDesc := ParseOAuthProviderError(body)
		return nil, &OidcTokenExchangeError{
			StatusCode:          resp.StatusCode,
			ProviderError:       providerErr,
			ProviderDescription: providerDesc,
			Body:                body,
		}
	}

	tokenResp, ok := OidcParseTokenResponse(body)
	if !ok {
		return nil, &OidcTokenExchangeError{StatusCode: resp.StatusCode, Body: body}
	}
	if strings.TrimSpace(tokenResp.TokenType) == "" {
		tokenResp.TokenType = "Bearer"
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" && strings.TrimSpace(tokenResp.IDToken) == "" {
		return nil, &OidcTokenExchangeError{StatusCode: resp.StatusCode, Body: body}
	}
	return tokenResp, nil
}

func OidcParseTokenResponse(body string) (*OidcTokenResponse, bool) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, false
	}

	accessToken := strings.TrimSpace(GetGJSON(body, "access_token"))
	idToken := strings.TrimSpace(GetGJSON(body, "id_token"))
	if accessToken != "" || idToken != "" {
		tokenType := strings.TrimSpace(GetGJSON(body, "token_type"))
		refreshToken := strings.TrimSpace(GetGJSON(body, "refresh_token"))
		scope := strings.TrimSpace(GetGJSON(body, "scope"))
		expiresIn := gjson.Get(body, "expires_in").Int()
		return &OidcTokenResponse{
			AccessToken:  accessToken,
			TokenType:    tokenType,
			ExpiresIn:    expiresIn,
			RefreshToken: refreshToken,
			Scope:        scope,
			IDToken:      idToken,
		}, true
	}

	values, err := url.ParseQuery(body)
	if err != nil {
		return nil, false
	}
	accessToken = strings.TrimSpace(values.Get("access_token"))
	idToken = strings.TrimSpace(values.Get("id_token"))
	if accessToken == "" && idToken == "" {
		return nil, false
	}
	expiresIn := int64(0)
	if raw := strings.TrimSpace(values.Get("expires_in")); raw != "" {
		if v, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil {
			expiresIn = v
		}
	}
	return &OidcTokenResponse{
		AccessToken:  accessToken,
		TokenType:    strings.TrimSpace(values.Get("token_type")),
		ExpiresIn:    expiresIn,
		RefreshToken: strings.TrimSpace(values.Get("refresh_token")),
		Scope:        strings.TrimSpace(values.Get("scope")),
		IDToken:      idToken,
	}, true
}

func OidcFetchUserInfo(
	ctx context.Context,
	cfg OIDCOptions,
	token *OidcTokenResponse,
) (*OidcUserInfoClaims, error) {
	if strings.TrimSpace(cfg.UserInfoURL) == "" {
		return &OidcUserInfoClaims{}, nil
	}
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return nil, errors.New("missing access_token for userinfo request")
	}

	client := req.C().SetTimeout(30 * time.Second)
	authorization, err := BuildBearerAuthorization(token.TokenType, token.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("invalid token for userinfo request: %w", err)
	}

	resp, err := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetHeader("Authorization", authorization).
		Get(cfg.UserInfoURL)
	if err != nil {
		return nil, fmt.Errorf("request userinfo: %w", err)
	}
	if !resp.IsSuccessState() {
		return nil, fmt.Errorf("userinfo status=%d", resp.StatusCode)
	}

	return OidcParseUserInfo(resp.String(), cfg), nil
}

func OidcParseUserInfo(body string, cfg OIDCOptions) *OidcUserInfoClaims {
	claims := &OidcUserInfoClaims{}
	claims.Email = identity.OAuthFirstNonEmpty(
		GetGJSON(body, cfg.UserInfoEmailPath),
		GetGJSON(body, "email"),
		GetGJSON(body, "user.email"),
		GetGJSON(body, "data.email"),
		GetGJSON(body, "attributes.email"),
	)
	claims.Username = identity.OAuthFirstNonEmpty(
		GetGJSON(body, cfg.UserInfoUsernamePath),
		GetGJSON(body, "preferred_username"),
		GetGJSON(body, "username"),
		GetGJSON(body, "name"),
		GetGJSON(body, "user.username"),
		GetGJSON(body, "user.name"),
	)
	claims.Subject = identity.OAuthFirstNonEmpty(
		GetGJSON(body, cfg.UserInfoIDPath),
		GetGJSON(body, "sub"),
		GetGJSON(body, "id"),
		GetGJSON(body, "user_id"),
		GetGJSON(body, "uid"),
		GetGJSON(body, "user.id"),
	)
	if verified, ok := GetGJSONBool(body, "email_verified"); ok {
		claims.EmailVerified = &verified
	}
	claims.DisplayName = identity.OAuthFirstNonEmpty(
		GetGJSON(body, "name"),
		GetGJSON(body, "nickname"),
		GetGJSON(body, "display_name"),
		GetGJSON(body, "preferred_username"),
		GetGJSON(body, "username"),
	)
	claims.AvatarURL = identity.OAuthFirstNonEmpty(
		GetGJSON(body, "picture"),
		GetGJSON(body, "avatar_url"),
		GetGJSON(body, "avatar"),
		GetGJSON(body, "profile_image_url"),
		GetGJSON(body, "user.avatar"),
		GetGJSON(body, "user.avatar_url"),
	)
	claims.Email = strings.TrimSpace(claims.Email)
	claims.Username = strings.TrimSpace(claims.Username)
	claims.Subject = strings.TrimSpace(claims.Subject)
	claims.DisplayName = strings.TrimSpace(claims.DisplayName)
	claims.AvatarURL = strings.TrimSpace(claims.AvatarURL)
	return claims
}

func GetGJSONBool(body string, path string) (bool, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, false
	}
	res := gjson.Get(body, path)
	if !res.Exists() {
		return false, false
	}
	return res.Bool(), true
}

func OidcParseAndValidateIDToken(ctx context.Context, cfg OIDCOptions, idToken string, expectedNonce string) (*OidcIDTokenClaims, error) {
	idToken = strings.TrimSpace(idToken)
	if idToken == "" {
		return nil, errors.New("missing id_token")
	}
	allowed := OidcAllowedSigningAlgs(cfg.AllowedSigningAlgs)
	if len(allowed) == 0 {
		return nil, errors.New("empty allowed signing algorithms")
	}

	jwks, err := OidcFetchJWKSet(ctx, cfg.JWKSURL)
	if err != nil {
		return nil, err
	}
	leeway := time.Duration(cfg.ClockSkewSeconds) * time.Second
	claims := &OidcIDTokenClaims{}

	parsed, err := jwt.ParseWithClaims(
		idToken,
		claims,
		func(token *jwt.Token) (any, error) {
			alg := strings.TrimSpace(token.Method.Alg())
			if !ContainsString(allowed, alg) {
				return nil, fmt.Errorf("unexpected signing algorithm: %s", alg)
			}
			kid, _ := token.Header["kid"].(string)
			return OidcFindPublicKey(jwks, strings.TrimSpace(kid), alg)
		},
		jwt.WithValidMethods(allowed),
		jwt.WithAudience(cfg.ClientID),
		jwt.WithIssuer(cfg.IssuerURL),
		jwt.WithLeeway(leeway),
	)
	if err != nil {
		return nil, err
	}
	if !parsed.Valid {
		return nil, errors.New("id_token invalid")
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return nil, errors.New("id_token missing sub")
	}
	if expectedNonce != "" && strings.TrimSpace(claims.Nonce) != strings.TrimSpace(expectedNonce) {
		return nil, errors.New("id_token nonce mismatch")
	}
	if len(claims.Audience) > 1 {
		if strings.TrimSpace(claims.Azp) == "" || strings.TrimSpace(claims.Azp) != strings.TrimSpace(cfg.ClientID) {
			return nil, errors.New("id_token azp mismatch")
		}
	}
	return claims, nil
}

func OidcAllowedSigningAlgs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{"RS256", "ES256", "PS256"}
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		alg := strings.ToUpper(strings.TrimSpace(part))
		if alg == "" {
			continue
		}
		if _, ok := seen[alg]; ok {
			continue
		}
		seen[alg] = struct{}{}
		out = append(out, alg)
	}
	return out
}

func OidcFetchJWKSet(ctx context.Context, jwksURL string) (*OidcJWKSet, error) {
	jwksURL = strings.TrimSpace(jwksURL)
	if jwksURL == "" {
		return nil, errors.New("missing jwks_url")
	}
	resp, err := req.C().
		SetTimeout(30*time.Second).
		R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		Get(jwksURL)
	if err != nil {
		return nil, fmt.Errorf("request jwks: %w", err)
	}
	if !resp.IsSuccessState() {
		return nil, fmt.Errorf("jwks status=%d", resp.StatusCode)
	}
	set := &OidcJWKSet{}
	if err := json.Unmarshal(resp.Bytes(), set); err != nil {
		return nil, fmt.Errorf("parse jwks: %w", err)
	}
	if len(set.Keys) == 0 {
		return nil, errors.New("jwks empty keys")
	}
	return set, nil
}

func OidcFindPublicKey(set *OidcJWKSet, kid, alg string) (any, error) {
	if set == nil {
		return nil, errors.New("jwks not loaded")
	}
	alg = strings.ToUpper(strings.TrimSpace(alg))
	kid = strings.TrimSpace(kid)

	var lastErr error
	for i := range set.Keys {
		k := set.Keys[i]
		if strings.TrimSpace(k.Use) != "" && !strings.EqualFold(strings.TrimSpace(k.Use), "sig") {
			continue
		}
		if kid != "" && strings.TrimSpace(k.Kid) != kid {
			continue
		}
		if strings.TrimSpace(k.Alg) != "" && !strings.EqualFold(strings.TrimSpace(k.Alg), alg) {
			continue
		}
		pk, err := k.PublicKey()
		if err != nil {
			lastErr = err
			continue
		}
		if pk != nil {
			return pk, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	if kid != "" {
		return nil, fmt.Errorf("jwk not found for kid=%s", kid)
	}
	return nil, errors.New("jwk not found")
}

func (k OidcJWK) PublicKey() (any, error) {
	switch strings.ToUpper(strings.TrimSpace(k.Kty)) {
	case "RSA":
		n, err := DecodeBase64URLBigInt(k.N)
		if err != nil {
			return nil, fmt.Errorf("decode rsa n: %w", err)
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(k.E))
		if err != nil {
			return nil, fmt.Errorf("decode rsa e: %w", err)
		}
		if len(eBytes) == 0 {
			return nil, errors.New("empty rsa e")
		}
		e := 0
		for _, b := range eBytes {
			e = (e << 8) | int(b)
		}
		if e <= 0 {
			return nil, errors.New("invalid rsa exponent")
		}
		if n.Sign() <= 0 {
			return nil, errors.New("invalid rsa modulus")
		}
		return &rsa.PublicKey{N: n, E: e}, nil
	case "EC":
		var curve elliptic.Curve
		switch strings.TrimSpace(k.Crv) {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported ec curve: %s", k.Crv)
		}
		x, err := DecodeBase64URLBigInt(k.X)
		if err != nil {
			return nil, fmt.Errorf("decode ec x: %w", err)
		}
		y, err := DecodeBase64URLBigInt(k.Y)
		if err != nil {
			return nil, fmt.Errorf("decode ec y: %w", err)
		}
		//nolint:staticcheck // 这里需要生成 ecdsa.PublicKey，保留 elliptic 曲线检查兼容 JWT。
		if !curve.IsOnCurve(x, y) {
			return nil, errors.New("ec point is not on curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil //nolint:staticcheck // JWK 使用裸坐标；迁移到 ecdsa.ParseUncompressedPublicKey 需改变点编码
	default:
		return nil, fmt.Errorf("unsupported jwk kty: %s", k.Kty)
	}
}

func DecodeBase64URLBigInt(raw string) (*big.Int, error) {
	buf, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, errors.New("empty value")
	}
	return new(big.Int).SetBytes(buf), nil
}

func ContainsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), target) {
			return true
		}
	}
	return false
}

// OIDCClient 复用原 HTTP、JWK 与声明校验，不改变验证顺序或算法白名单。
type OIDCClient struct{}

func (OIDCClient) ExchangeCode(ctx context.Context, c OIDCOptions, code, redirect, verifier string) (*OidcTokenResponse, error) {
	return OidcExchangeCode(ctx, c, code, redirect, verifier)
}
func (OIDCClient) FetchUserInfo(ctx context.Context, c OIDCOptions, t *OidcTokenResponse) (*OidcUserInfoClaims, error) {
	return OidcFetchUserInfo(ctx, c, t)
}
func (OIDCClient) ValidateIDToken(ctx context.Context, c OIDCOptions, token, nonce string) (*identity.OIDCVerifiedClaims, error) {
	v, e := OidcParseAndValidateIDToken(ctx, c, token, nonce)
	if v == nil {
		return nil, e
	}
	return &identity.OIDCVerifiedClaims{Issuer: v.Issuer, Subject: v.Subject, Email: v.Email, PreferredUsername: v.PreferredUsername, Name: v.Name, EmailVerified: v.EmailVerified}, e
}
