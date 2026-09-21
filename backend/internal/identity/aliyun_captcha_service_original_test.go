//go:build unit

package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/stretchr/testify/require"
)

type aliyunVerifierSpy struct {
	called    int
	lastCred  identity.AliyunCaptchaCredentials
	lastParam string
	result    *identity.AliyunCaptchaVerifyResult
	err       error
}

func (s *aliyunVerifierSpy) VerifyCaptcha(_ context.Context, cred identity.AliyunCaptchaCredentials, param string) (*identity.AliyunCaptchaVerifyResult, error) {
	s.called++
	s.lastCred = cred
	s.lastParam = param
	if s.err != nil {
		return nil, s.err
	}
	if s.result != nil {
		return s.result, nil
	}
	return &identity.AliyunCaptchaVerifyResult{VerifyResult: true}, nil
}

func aliyunEnabledSettings() map[string]string {
	return map[string]string{
		identity.SettingKeyAliyunCaptchaEnabled:         "true",
		identity.SettingKeyAliyunCaptchaAccessKeyID:     "ak-id",
		identity.SettingKeyAliyunCaptchaAccessKeySecret: "ak-secret",
		identity.SettingKeyAliyunCaptchaSceneID:         "scene-1",
		identity.SettingKeyAliyunCaptchaPrefix:          "prefix-1",
	}
}

func aliyunTestConfig() identity.AliyunCaptchaConfig {
	return identity.AliyunCaptchaConfig{
		Enabled:         true,
		AccessKeyID:     "ak-id",
		AccessKeySecret: "ak-secret",
		SceneID:         "scene-1",
		Region:          identity.AliyunCaptchaRegionCN,
	}
}

func TestAliyunCaptchaServiceVerifyParamDispatch(t *testing.T) {
	spy := &aliyunVerifierSpy{}
	svc := identity.NewAliyunCaptchaService(nil, spy)

	err := svc.VerifyParamWithConfig(context.Background(), aliyunTestConfig(), "captcha-verify-param")

	require.NoError(t, err)
	require.Equal(t, 1, spy.called)
	require.Equal(t, "captcha-verify-param", spy.lastParam)
	require.Equal(t, "ak-id", spy.lastCred.AccessKeyID)
	require.Equal(t, "scene-1", spy.lastCred.SceneID)
	require.Equal(t, "captcha.cn-shanghai.aliyuncs.com", spy.lastCred.Endpoint)
}

func TestAliyunCaptchaServiceSgpEndpoint(t *testing.T) {
	spy := &aliyunVerifierSpy{}
	svc := identity.NewAliyunCaptchaService(nil, spy)
	cfg := aliyunTestConfig()
	cfg.Region = identity.AliyunCaptchaRegionSGP

	err := svc.VerifyParamWithConfig(context.Background(), cfg, "captcha-verify-param")

	require.NoError(t, err)
	require.Equal(t, "captcha.ap-southeast-1.aliyuncs.com", spy.lastCred.Endpoint)
}

func TestAliyunCaptchaServiceFailsClosedOnVerifierError(t *testing.T) {
	spy := &aliyunVerifierSpy{err: errors.New("network down")}
	svc := identity.NewAliyunCaptchaService(nil, spy)

	err := svc.VerifyParamWithConfig(context.Background(), aliyunTestConfig(), "captcha-verify-param")

	require.ErrorIs(t, err, identity.ErrAliyunCaptchaVerificationFailed)
}

func TestAliyunCaptchaServiceRejectsVerifyResultFalse(t *testing.T) {
	spy := &aliyunVerifierSpy{result: &identity.AliyunCaptchaVerifyResult{VerifyResult: false, VerifyCode: "F001"}}
	svc := identity.NewAliyunCaptchaService(nil, spy)

	err := svc.VerifyParamWithConfig(context.Background(), aliyunTestConfig(), "captcha-verify-param")

	require.ErrorIs(t, err, identity.ErrAliyunCaptchaVerificationFailed)
}

func TestAliyunCaptchaServiceRejectsIncompleteCredentials(t *testing.T) {
	spy := &aliyunVerifierSpy{}
	svc := identity.NewAliyunCaptchaService(nil, spy)
	cfg := aliyunTestConfig()
	cfg.AccessKeySecret = ""

	err := svc.VerifyParamWithConfig(context.Background(), cfg, "captcha-verify-param")

	require.ErrorIs(t, err, identity.ErrAliyunCaptchaNotConfigured)
	require.Zero(t, spy.called)
}

func TestAliyunCaptchaServiceRejectsEmptyParam(t *testing.T) {
	spy := &aliyunVerifierSpy{}
	svc := identity.NewAliyunCaptchaService(nil, spy)

	err := svc.VerifyParamWithConfig(context.Background(), aliyunTestConfig(), "")

	require.ErrorIs(t, err, identity.ErrAliyunCaptchaVerificationFailed)
	require.Zero(t, spy.called)
}

func TestAliyunCaptchaServiceValidateCredentials(t *testing.T) {
	t.Run("invalid credential code", func(t *testing.T) {
		spy := &aliyunVerifierSpy{err: &identity.AliyunCaptchaAPIError{Code: "SignatureDoesNotMatch", Message: "bad sk"}}
		svc := identity.NewAliyunCaptchaService(nil, spy)

		err := svc.ValidateCredentials(context.Background(), "id", "sk", "scene", "cn")
		require.ErrorIs(t, err, identity.ErrCaptchaInvalidCredentials)
	})

	t.Run("network error surfaces", func(t *testing.T) {
		spy := &aliyunVerifierSpy{err: errors.New("timeout")}
		svc := identity.NewAliyunCaptchaService(nil, spy)

		err := svc.ValidateCredentials(context.Background(), "id", "sk", "scene", "cn")
		require.Error(t, err)
		require.NotErrorIs(t, err, identity.ErrCaptchaInvalidCredentials)
	})

	t.Run("verify result false means credentials valid", func(t *testing.T) {
		spy := &aliyunVerifierSpy{result: &identity.AliyunCaptchaVerifyResult{VerifyResult: false}}
		svc := identity.NewAliyunCaptchaService(nil, spy)

		err := svc.ValidateCredentials(context.Background(), "id", "sk", "scene", "sgp")
		require.NoError(t, err)
		require.Equal(t, "captcha.ap-southeast-1.aliyuncs.com", spy.lastCred.Endpoint)
	})
}

func TestAuthServiceVerifyCaptchaDispatchesAliyun(t *testing.T) {
	spy := &aliyunVerifierSpy{}
	authService := newAliyunAuthServiceForTest(&identity.AuthOptions{}, aliyunEnabledSettings(), spy)

	// 阿里云 captchaVerifyParam 复用 turnstile_token 请求字段
	err := authService.VerifyCaptcha(context.Background(), identity.CaptchaProof{TurnstileToken: "captcha-verify-param"}, "127.0.0.1")

	require.NoError(t, err)
	require.Equal(t, 1, spy.called)
	require.Equal(t, "captcha-verify-param", spy.lastParam)
}

func TestAuthServiceVerifyCaptchaRejectsProviderConflict(t *testing.T) {
	settings := aliyunEnabledSettings()
	settings[identity.SettingKeyTurnstileEnabled] = "true"
	settings[identity.SettingKeyTurnstileSecretKey] = "secret"
	spy := &aliyunVerifierSpy{}
	authService := newAliyunAuthServiceForTest(&identity.AuthOptions{}, settings, spy)

	err := authService.VerifyCaptcha(context.Background(), identity.CaptchaProof{TurnstileToken: "param"}, "127.0.0.1")

	require.ErrorIs(t, err, identity.ErrCaptchaProviderConflict)
	require.Zero(t, spy.called)
}

func TestAuthServiceVerifyCaptchaRequiredModeWithAliyun(t *testing.T) {
	cfg := captchaAuthOptions(true)
	spy := &aliyunVerifierSpy{}
	authService := newAliyunAuthServiceForTest(cfg, aliyunEnabledSettings(), spy)

	// required 模式 + 阿里云启用且凭证齐全：不误报 NOT_CONFIGURED，正常走阿里云校验
	err := authService.VerifyCaptcha(context.Background(), identity.CaptchaProof{TurnstileToken: "captcha-verify-param"}, "127.0.0.1")

	require.NoError(t, err)
	require.Equal(t, 1, spy.called)
}

func TestAuthServiceVerifyActionCaptchaIfEnabledDispatchesAliyun(t *testing.T) {
	spy := &aliyunVerifierSpy{}
	authService := newAliyunAuthServiceForTest(&identity.AuthOptions{}, aliyunEnabledSettings(), spy)

	err := authService.VerifyActionCaptchaIfEnabled(context.Background(), identity.CaptchaProof{TurnstileToken: "captcha-verify-param"}, "127.0.0.1")

	require.NoError(t, err)
	require.Equal(t, 1, spy.called)
	require.Equal(t, "captcha-verify-param", spy.lastParam)
}

func TestAuthServiceVerifyActionCaptchaIfEnabledSkipsWhenOnlyTurnstile(t *testing.T) {
	spy := &aliyunVerifierSpy{}
	authService := newAliyunAuthServiceForTest(&identity.AuthOptions{}, map[string]string{
		identity.SettingKeyTurnstileEnabled:   "true",
		identity.SettingKeyTurnstileSecretKey: "secret",
	}, spy)

	// Turnstile 不扩大既有覆盖：扩展入口不拦截
	err := authService.VerifyActionCaptchaIfEnabled(context.Background(), identity.CaptchaProof{}, "127.0.0.1")

	require.NoError(t, err)
	require.Zero(t, spy.called)
}
