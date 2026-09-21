package provider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/idtoken"
)

func TestValidateGoogleIDTokenPayload(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	validPayload := func() *idtoken.Payload {
		return &idtoken.Payload{
			Issuer:   "https://accounts.google.com",
			Audience: "google-web-client",
			Expires:  now.Add(time.Minute).Unix(),
			Subject:  "google-subject",
			Claims: map[string]any{
				"email":          "user@example.com",
				"email_verified": true,
				"name":           "Example User",
			},
		}
	}

	claims, err := ValidateGoogleIDTokenPayload(validPayload(), "google-web-client", now)
	require.NoError(t, err)
	require.Equal(t, "google-subject", claims.Subject)
	require.Equal(t, "user@example.com", claims.Email)
	require.True(t, claims.EmailVerified)

	tests := []struct {
		name   string
		mutate func(*idtoken.Payload)
	}{
		{name: "错误 audience", mutate: func(payload *idtoken.Payload) { payload.Audience = "other-client" }},
		{name: "错误 issuer", mutate: func(payload *idtoken.Payload) { payload.Issuer = "https://issuer.example" }},
		{name: "token 已过期", mutate: func(payload *idtoken.Payload) { payload.Expires = now.Unix() }},
		{name: "邮箱未验证", mutate: func(payload *idtoken.Payload) { payload.Claims["email_verified"] = false }},
		{name: "缺少主体", mutate: func(payload *idtoken.Payload) { payload.Subject = "" }},
		{name: "缺少邮箱", mutate: func(payload *idtoken.Payload) { payload.Claims["email"] = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := validPayload()
			tt.mutate(payload)
			_, err := ValidateGoogleIDTokenPayload(payload, "google-web-client", now)
			require.Error(t, err)
		})
	}
}
