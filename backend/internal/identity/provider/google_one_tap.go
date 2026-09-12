// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	context "context"
	errors "errors"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	idtoken "google.golang.org/api/idtoken"
	strings "strings"
	time "time"
)

type GoogleIDTokenClaims = identity.GoogleIDTokenClaims

type GoogleIDTokenVerifier = identity.GoogleIDTokenVerifier

type GoogleAPIIDTokenVerifier struct{}

// Verify 使用 Google 官方验证器校验签名和标准声明，再收紧本站依赖的身份字段。
func (GoogleAPIIDTokenVerifier) Verify(ctx context.Context, credential string, audience string) (*GoogleIDTokenClaims, error) {
	payload, err := idtoken.Validate(ctx, credential, audience)
	if err != nil {
		return nil, err
	}
	return ValidateGoogleIDTokenPayload(payload, audience, time.Now())
}

// ValidateGoogleIDTokenPayload 对官方验证器的结果再做本站所需的严格声明检查。
func ValidateGoogleIDTokenPayload(payload *idtoken.Payload, audience string, now time.Time) (*GoogleIDTokenClaims, error) {
	if payload == nil {
		return nil, errors.New("google id token payload is missing")
	}
	if payload.Issuer != "accounts.google.com" && payload.Issuer != "https://accounts.google.com" {
		return nil, errors.New("google id token issuer is invalid")
	}
	if payload.Audience != strings.TrimSpace(audience) {
		return nil, errors.New("google id token audience is invalid")
	}
	if payload.Expires <= now.Unix() {
		return nil, errors.New("google id token is expired")
	}

	verified, ok := payload.Claims["email_verified"].(bool)
	if !ok || !verified {
		return nil, errors.New("google verified email is missing")
	}
	claims := &GoogleIDTokenClaims{
		Subject:       strings.TrimSpace(payload.Subject),
		Email:         strings.TrimSpace(GoogleIDTokenStringClaim(payload.Claims, "email")),
		EmailVerified: true,
		Name:          strings.TrimSpace(GoogleIDTokenStringClaim(payload.Claims, "name")),
		GivenName:     strings.TrimSpace(GoogleIDTokenStringClaim(payload.Claims, "given_name")),
		Picture:       strings.TrimSpace(GoogleIDTokenStringClaim(payload.Claims, "picture")),
		Locale:        strings.TrimSpace(GoogleIDTokenStringClaim(payload.Claims, "locale")),
		HostedDomain:  strings.TrimSpace(GoogleIDTokenStringClaim(payload.Claims, "hd")),
	}
	if claims.Subject == "" || claims.Email == "" {
		return nil, errors.New("google id token identity is incomplete")
	}
	return claims, nil
}

func GoogleIDTokenStringClaim(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return value
}
