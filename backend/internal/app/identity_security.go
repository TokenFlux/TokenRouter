// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	"time"
)

// provideTotp 直接使用身份存储，通知与设置只作为窄接口注入。
func provideTotp(users *identitypostgres.UserStore, encryptor identity.SecretEncryptor, cache identity.TotpCache, settings *service.SettingService, email *service.EmailService, queue *service.EmailQueueService) *identity.TotpService {
	return identity.NewTotpService(users, encryptor, cache, settings, email, queue, time.Now)
}

// providePasskey 在装配阶段创建 SDK 验证器，旧门面只适配 request.Body。
func providePasskey(cfg *config.Config, repo identity.PasskeyRepository, sessions identity.PasskeySessionStore, users *identitypostgres.UserStore) (*service.PasskeyService, error) {
	options := provider.PasskeyOptions{Enabled: cfg.WebAuthn.Enabled, RPID: cfg.WebAuthn.RPID, RPDisplayName: cfg.WebAuthn.RPDisplayName, RPOrigins: cfg.WebAuthn.RPOrigins}
	verifier, e := provider.NewPasskeyVerifier(options)
	if e != nil {
		return nil, e
	}
	return &service.PasskeyService{PasskeyService: identity.NewPasskeyService(options.Enabled, verifier, repo, sessions, users, time.Now)}, nil
}
func provideTurnstile(settings *service.SettingService, verifier identity.TurnstileVerifier) *identity.TurnstileService {
	s := identity.NewTurnstileService(settings, verifier)
	s.SetObserver(logging.LegacyPrintf)
	return s
}
func provideTencentCaptcha(settings *service.SettingService, verifier identity.TencentCaptchaVerifier) *identity.TencentCaptchaService {
	s := identity.NewTencentCaptchaService(settings, verifier)
	s.SetObserver(logging.LegacyPrintf)
	return s
}
func provideAliyunCaptcha(settings *service.SettingService, verifier identity.AliyunCaptchaVerifier) *identity.AliyunCaptchaService {
	s := identity.NewAliyunCaptchaService(settings, verifier)
	s.SetObserver(logging.LegacyPrintf)
	return s
}
