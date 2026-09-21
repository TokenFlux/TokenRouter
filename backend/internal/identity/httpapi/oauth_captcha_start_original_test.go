//go:build unit

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type oauthCaptchaSettingRepo struct {
	values map[string]string
}

func (r *oauthCaptchaSettingRepo) Get(context.Context, string) (*settingscore.Setting, error) {
	return nil, settingscore.ErrSettingNotFound
}
func (r *oauthCaptchaSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", settingscore.ErrSettingNotFound
	}
	return value, nil
}
func (r *oauthCaptchaSettingRepo) Set(context.Context, string, string) error { return nil }
func (r *oauthCaptchaSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}
func (r *oauthCaptchaSettingRepo) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (r *oauthCaptchaSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *oauthCaptchaSettingRepo) Delete(context.Context, string) error { return nil }

type oauthCaptchaVerifier struct {
	calls int
	proof identity.TencentCaptchaProof
}

func (v *oauthCaptchaVerifier) VerifyTicket(_ context.Context, _ identity.TencentCaptchaCredentials, proof identity.TencentCaptchaProof, _ string) (*identity.TencentCaptchaVerifyResponse, error) {
	v.calls++
	v.proof = proof
	return &identity.TencentCaptchaVerifyResponse{CaptchaCode: 1}, nil
}

func newOAuthCaptchaTestHandler(enabled bool) (*AuthenticationHandler, *oauthCaptchaVerifier) {
	values := map[string]string{}
	if enabled {
		values = map[string]string{
			identity.SettingKeyTencentCaptchaEnabled:        "true",
			identity.SettingKeyTencentCaptchaAppID:          "123456789",
			identity.SettingKeyTencentCaptchaAppSecretKey:   "app-secret",
			identity.SettingKeyTencentCaptchaCloudSecretID:  "cloud-secret-id",
			identity.SettingKeyTencentCaptchaCloudSecretKey: "cloud-secret-key",
		}
	}
	runtime := identity.NewRuntimeSettings(&oauthCaptchaSettingRepo{values: values}, settingscore.ErrSettingNotFound)
	settings := captchaRuntimeFixture{runtime: runtime}
	verifier := &oauthCaptchaVerifier{}
	auth := identity.NewAuthService(&identity.AuthDependencies{Settings: settings, Tencent: identity.NewTencentCaptchaService(runtime, verifier)}, nil)
	session := NewSessionHandler(auth, nil, nil, nil, nil, nil, SessionHTTPOptions{})
	pending := NewPendingHandler(session, nil, PendingHTTPOptions{})
	// GET 的凭据门禁必须早于任何提供方配置或 cookie 写入；后续端口故意不安装。
	return &AuthenticationHandler{Session: session, Pending: pending,
		Email:    NewEmailOAuthHandler(pending, nil, nil),
		LinuxDo:  NewLinuxDoHandler(pending, nil, nil, nil),
		OIDC:     NewOIDCHandler(pending, nil, nil, nil),
		WeChat:   NewWeChatHandler(pending, nil, nil, WeChatHTTPOptions{}),
		DingTalk: NewDingTalkHandler(pending, nil, nil, DingTalkHTTPOptions{}),
	}, verifier
}

func oauthStartHandlers() map[string]func(*AuthenticationHandler, *gin.Context) {
	return map[string]func(*AuthenticationHandler, *gin.Context){
		"github":   func(h *AuthenticationHandler, c *gin.Context) { h.GitHubOAuthStart(c) },
		"google":   func(h *AuthenticationHandler, c *gin.Context) { h.GoogleOAuthStart(c) },
		"linuxdo":  func(h *AuthenticationHandler, c *gin.Context) { h.LinuxDoOAuthStart(c) },
		"dingtalk": func(h *AuthenticationHandler, c *gin.Context) { h.DingTalkOAuthStart(c) },
		"wechat":   func(h *AuthenticationHandler, c *gin.Context) { h.WeChatOAuthStart(c) },
		"oidc":     func(h *AuthenticationHandler, c *gin.Context) { h.OIDCOAuthStart(c) },
	}
}

func TestOAuthStartGetRejectsAnonymousLoginWhenTencentEnabledWithoutSideEffects(t *testing.T) {

	for provider, start := range oauthStartHandlers() {
		t.Run(provider, func(t *testing.T) {
			handler, verifier := newOAuthCaptchaTestHandler(true)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/"+provider+"/start?intent=bind_current_user", nil)

			start(handler, c)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "TENCENT_CAPTCHA_VERIFICATION_FAILED")
			require.Empty(t, recorder.Header().Get("Location"))
			require.Empty(t, recorder.Header().Values("Set-Cookie"))
			require.Zero(t, verifier.calls)
		})
	}
}

func TestOAuthStartPostReturnsAuthorizeURLAfterTencentVerification(t *testing.T) {

	for provider := range oauthStartHandlers() {
		t.Run(provider, func(t *testing.T) {
			handler, verifier := newOAuthCaptchaTestHandler(true)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(
				http.MethodPost,
				"/api/v1/auth/oauth/"+provider+"/start",
				strings.NewReader(`{"tencent_captcha_ticket":"ticket-value","tencent_captcha_randstr":"@rand-value"}`),
			)
			c.Request.Header.Set("Content-Type", "application/json")

			require.True(t, handler.Session.RequireActionCaptchaForOAuthLoginStart(c))
			RespondOAuthStart(c, "https://provider.example/authorize")

			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"authorize_url":"https://provider.example/authorize"`)
			require.Equal(t, 1, verifier.calls)
			require.Equal(t, identity.TencentCaptchaProof{Ticket: "ticket-value", Randstr: "@rand-value"}, verifier.proof)
		})
	}
}

func TestOAuthStartPostRequiresTencentProofWhenEnabled(t *testing.T) {

	for provider := range oauthStartHandlers() {
		t.Run(provider, func(t *testing.T) {
			handler, verifier := newOAuthCaptchaTestHandler(true)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/"+provider+"/start", strings.NewReader(`{}`))
			c.Request.Header.Set("Content-Type", "application/json")

			require.False(t, handler.Session.RequireActionCaptchaForOAuthLoginStart(c))
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "TENCENT_CAPTCHA_VERIFICATION_FAILED")
			require.Zero(t, verifier.calls)
		})
	}
}

func TestOAuthBindingPathRemainsOutsideTencentGate(t *testing.T) {

	handler := &AuthenticationHandler{Session: &SessionHandler{}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/bind/start", nil)

	require.True(t, handler.Session.RequireActionCaptchaForOAuthLoginStart(c))
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestOAuthStartGetRemainsCompatibleWhenTencentDisabled(t *testing.T) {

	handler, verifier := newOAuthCaptchaTestHandler(false)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/start", nil)

	require.True(t, handler.Session.RequireActionCaptchaForOAuthLoginStart(c))
	RespondOAuthStart(c, "https://provider.example/authorize")

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "https://provider.example/authorize", recorder.Header().Get("Location"))
	require.Zero(t, verifier.calls)
}

// 只实现本组验证码场景读取的设置端口，调用无关策略会使测试失败。
type captchaRuntimeFixture struct {
	identity.AuthSettings
	runtime *identity.RuntimeSettings
}

func (s captchaRuntimeFixture) GetCaptchaProviderConfig(ctx context.Context) (identity.CaptchaProviderConfig, error) {
	return s.runtime.GetCaptchaProviderConfig(ctx)
}
