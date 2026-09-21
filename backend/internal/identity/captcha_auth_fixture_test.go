//go:build unit

package identity_test

import (
	"context"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// captchaSettingsStore 保留读取次数与失败断言；未使用的写入端口保持未安装。
type captchaSettingsStore struct {
	identity.RuntimeSettingsStore
	mu               sync.Mutex
	values           map[string]string
	err              error
	getValueCalls    int
	getMultipleCalls int
}

func (s *captchaSettingsStore) GetValue(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getValueCalls++
	if s.err != nil {
		return "", s.err
	}
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", settings.ErrSettingNotFound
}

func (s *captchaSettingsStore) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getMultipleCalls++
	if s.err != nil {
		return nil, s.err
	}
	result := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			result[k] = v
		}
	}
	return result, nil
}

// captchaAuthSettings 只接入当前测试实际消费的端口；其它认证能力不可调用。
type captchaAuthSettings struct {
	identity.AuthSettings
	runtime *identity.RuntimeSettings
}

func (s captchaAuthSettings) GetCaptchaProviderConfig(ctx context.Context) (identity.CaptchaProviderConfig, error) {
	return s.runtime.GetCaptchaProviderConfig(ctx)
}
func (s captchaAuthSettings) IsEmailVerifyEnabled(ctx context.Context) bool {
	return s.runtime.IsEmailVerifyEnabled(ctx)
}

func captchaAuthOptions(required bool) *identity.AuthOptions {
	options := &identity.AuthOptions{}
	options.Server.Mode = "release"
	options.Turnstile.Required = required
	return options
}

func newAuthServiceForCaptchaRepoTest(repo *captchaSettingsStore, required bool, turnstileVerifier identity.TurnstileVerifier, tencentVerifier identity.TencentCaptchaVerifier) *identity.AuthService {
	runtime := identity.NewRuntimeSettings(repo, settings.ErrSettingNotFound)
	turnstile := identity.NewTurnstileService(runtime, turnstileVerifier)
	turnstile.SetObserver(logging.LegacyPrintf)
	tencent := identity.NewTencentCaptchaService(runtime, tencentVerifier)
	tencent.SetObserver(logging.LegacyPrintf)
	return identity.NewAuthService(&identity.AuthDependencies{Options: captchaAuthOptions(required), Settings: captchaAuthSettings{runtime: runtime}, Turnstile: turnstile, Tencent: tencent, Observer: identity.Observer{Log: logging.LegacyPrintf}}, nil)
}

func newAuthServiceForCaptchaTest(values map[string]string, required bool, turnstileVerifier identity.TurnstileVerifier, tencentVerifier identity.TencentCaptchaVerifier) *identity.AuthService {
	svc := newAuthServiceForCaptchaRepoTest(&captchaSettingsStore{values: values}, required, turnstileVerifier, tencentVerifier)
	if turnstileVerifier == nil {
		svc.Turnstile = nil
	}
	if tencentVerifier == nil {
		svc.Tencent = nil
	}
	return svc
}
func newAuthServiceForRegisterTurnstileTest(values map[string]string, verifier identity.TurnstileVerifier) *identity.AuthService {
	return newAuthServiceForCaptchaTest(values, true, verifier, nil)
}
func newAliyunAuthServiceForTest(options *identity.AuthOptions, values map[string]string, verifier *aliyunVerifierSpy) *identity.AuthService {
	runtime := identity.NewRuntimeSettings(&captchaSettingsStore{values: values}, settings.ErrSettingNotFound)
	turnstile := identity.NewTurnstileService(runtime, &turnstileVerifierSpy{})
	turnstile.SetObserver(logging.LegacyPrintf)
	aliyun := identity.NewAliyunCaptchaService(runtime, verifier)
	aliyun.SetObserver(logging.LegacyPrintf)
	return identity.NewAuthService(&identity.AuthDependencies{Options: options, Settings: captchaAuthSettings{runtime: runtime}, Turnstile: turnstile, Aliyun: aliyun, Observer: identity.Observer{Log: logging.LegacyPrintf}}, nil)
}
