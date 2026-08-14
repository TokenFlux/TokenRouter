//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 腾讯验证码校验必须先于 WebAuthn ceremony，缺少票据时不能创建 Passkey 会话。
func TestPasskeyBeginLoginRequiresTencentCaptchaBeforeCeremony(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authHandler, verifier := newOAuthCaptchaTestHandler(true)
	passkeys, err := service.NewPasskeyService(&config.Config{}, nil, nil, nil)
	require.NoError(t, err)
	handler := NewPasskeyHandler(passkeys, authHandler.authService, authHandler.settingSvc)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/begin", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.BeginLogin(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "TENCENT_CAPTCHA_VERIFICATION_FAILED")
	require.Zero(t, verifier.calls)
}

// 票据通过后才进入 Passkey 服务；测试中的禁用实例会返回明确的功能关闭错误。
func TestPasskeyBeginLoginVerifiesTencentCaptchaBeforePasskeyService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authHandler, verifier := newOAuthCaptchaTestHandler(true)
	passkeys, err := service.NewPasskeyService(&config.Config{}, nil, nil, nil)
	require.NoError(t, err)
	handler := NewPasskeyHandler(passkeys, authHandler.authService, authHandler.settingSvc)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/passkey/login/begin",
		strings.NewReader(`{"tencent_captcha_ticket":"ticket-value","tencent_captcha_randstr":"@rand-value"}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")

	handler.BeginLogin(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "PASSKEY_DISABLED")
	require.Equal(t, 1, verifier.calls)
	require.Equal(t, service.TencentCaptchaProof{Ticket: "ticket-value", Randstr: "@rand-value"}, verifier.proof)
}
