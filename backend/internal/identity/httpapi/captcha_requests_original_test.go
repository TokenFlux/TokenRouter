//go:build unit

package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestAuthRequestsBindTencentCaptchaProof(t *testing.T) {
	const payload = `{"email":"user@example.com","password":"secret-123","tencent_captcha_ticket":"ticket-value","tencent_captcha_randstr":"@rand-value"}`

	tests := []struct {
		name   string
		decode func([]byte) identity.CaptchaProof
	}{
		{
			name: "登录",
			decode: func(raw []byte) identity.CaptchaProof {
				var req LoginRequest
				require.NoError(t, json.Unmarshal(raw, &req))
				return identity.CaptchaProof{TurnstileToken: req.TurnstileToken, TencentTicket: req.TencentCaptchaTicket, TencentRandstr: req.TencentCaptchaRandstr}
			},
		},
		{
			name: "注册",
			decode: func(raw []byte) identity.CaptchaProof {
				var req RegisterRequest
				require.NoError(t, json.Unmarshal(raw, &req))
				return identity.CaptchaProof{TurnstileToken: req.TurnstileToken, TencentTicket: req.TencentCaptchaTicket, TencentRandstr: req.TencentCaptchaRandstr}
			},
		},
		{
			name: "发送邮箱验证码",
			decode: func(raw []byte) identity.CaptchaProof {
				var req SendVerifyCodeRequest
				require.NoError(t, json.Unmarshal(raw, &req))
				return identity.CaptchaProof{TurnstileToken: req.TurnstileToken, TencentTicket: req.TencentCaptchaTicket, TencentRandstr: req.TencentCaptchaRandstr}
			},
		},
		{
			name: "忘记密码",
			decode: func(raw []byte) identity.CaptchaProof {
				var req ForgotPasswordRequest
				require.NoError(t, json.Unmarshal(raw, &req))
				return identity.CaptchaProof{TurnstileToken: req.TurnstileToken, TencentTicket: req.TencentCaptchaTicket, TencentRandstr: req.TencentCaptchaRandstr}
			},
		},
		{
			name: "OAuth启动",
			decode: func(raw []byte) identity.CaptchaProof {
				var req OAuthStartCaptchaRequest
				require.NoError(t, json.Unmarshal(raw, &req))
				return identity.CaptchaProof{TurnstileToken: req.TurnstileToken, TencentTicket: req.TencentCaptchaTicket, TencentRandstr: req.TencentCaptchaRandstr}
			},
		},
		{
			name: "Passkey登录",
			decode: func(raw []byte) identity.CaptchaProof {
				var req PasskeyBeginLoginRequest
				require.NoError(t, json.Unmarshal(raw, &req))
				return identity.CaptchaProof{TurnstileToken: req.TurnstileToken, TencentTicket: req.TencentCaptchaTicket, TencentRandstr: req.TencentCaptchaRandstr}
			},
		},
		{
			name: "OAuth待处理账号发送邮箱验证码",
			decode: func(raw []byte) identity.CaptchaProof {
				var req SendPendingOAuthVerifyCodeRequest
				require.NoError(t, json.Unmarshal(raw, &req))
				return identity.CaptchaProof{TurnstileToken: req.TurnstileToken, TencentTicket: req.TencentCaptchaTicket, TencentRandstr: req.TencentCaptchaRandstr}
			},
		},
		{
			name: "OAuth待处理账号创建",
			decode: func(raw []byte) identity.CaptchaProof {
				var req CreatePendingOAuthAccountRequest
				require.NoError(t, json.Unmarshal(raw, &req))
				return identity.CaptchaProof{TurnstileToken: req.TurnstileToken, TencentTicket: req.TencentCaptchaTicket, TencentRandstr: req.TencentCaptchaRandstr}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proof := test.decode([]byte(payload))
			require.Equal(t, "ticket-value", proof.TencentTicket)
			require.Equal(t, "@rand-value", proof.TencentRandstr)
		})
	}
}
