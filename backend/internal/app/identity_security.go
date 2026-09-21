// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/notification"

	config "github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"

	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"time"
)

// provideTotp 直接使用身份存储，通知与设置只作为窄接口注入。
func provideTotp(users *identitypostgres.UserStore, encryptor identity.SecretEncryptor, cache identity.TotpCache, settings *identityAuthSettings, email *identity.EmailChallenges, queue *notification.EmailQueueService) *identity.TotpService {
	return identity.NewTotpService(users, encryptor, cache, settings, email, queue, time.Now)
}

// providePasskey 在装配阶段创建 SDK 验证器，HTTP 解析及主体投影由原生 Adapter 持有。
func providePasskey(cfg *config.Config, repo identity.PasskeyRepository, sessions identity.PasskeySessionStore, users *identitypostgres.UserStore) (*identity.PasskeyService, error) {
	options := provider.PasskeyOptions{Enabled: cfg.WebAuthn.Enabled, RPID: cfg.WebAuthn.RPID, RPDisplayName: cfg.WebAuthn.RPDisplayName, RPOrigins: cfg.WebAuthn.RPOrigins}
	verifier, e := provider.NewPasskeyVerifier(options)
	if e != nil {
		return nil, e
	}
	return identity.NewPasskeyService(options.Enabled, verifier, repo, sessions, users, time.Now), nil
}

func providePasskeyHTTP(passkeys *identity.PasskeyService, auth *identityAuthGraph, backend *admission.BackendMode) *identityhttp.PasskeyHandler {
	return identityhttp.NewPasskeyHandler(passkeys, auth.Core, backend)
}
func provideTurnstile(settings *identity.RuntimeSettings, verifier identity.TurnstileVerifier) *identity.TurnstileService {
	s := identity.NewTurnstileService(settings, verifier)
	s.SetObserver(logging.LegacyPrintf)
	return s
}
func provideTencentCaptcha(settings *identity.RuntimeSettings, verifier identity.TencentCaptchaVerifier) *identity.TencentCaptchaService {
	s := identity.NewTencentCaptchaService(settings, verifier)
	s.SetObserver(logging.LegacyPrintf)
	return s
}
func provideAliyunCaptcha(settings *identity.RuntimeSettings, verifier identity.AliyunCaptchaVerifier) *identity.AliyunCaptchaService {
	s := identity.NewAliyunCaptchaService(settings, verifier)
	s.SetObserver(logging.LegacyPrintf)
	return s
}
