package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/bootstrap"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	identityadapter "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/notification"
)

// provideSecretEncryptor 使用启动配置创建 AES 加密器。
func provideSecretEncryptor(cfg *config.Config) (identity.SecretEncryptor, error) {
	return bootstrap.NewAESEncryptor(cfg)
}

// provideTotp 绑定身份存储、通知接口和设置接口。
func provideTotp(users *identitypostgres.UserStore, encryptor identity.SecretEncryptor, cache identity.TotpCache, settings *identityAuthSettings, email *identity.EmailChallenges, queue *notification.EmailQueueService) *identity.TotpService {
	return identity.NewTotpService(users, encryptor, cache, settings, email, queue, time.Now)
}

// providePasskey 创建 SDK 验证器，HTTP 适配器负责解析请求和转换用户数据。
func providePasskey(cfg *config.Config, repo identity.PasskeyRepository, sessions identity.PasskeySessionStore, users *identitypostgres.UserStore) (*identity.PasskeyService, error) {
	options := identityadapter.PasskeyOptions{Enabled: cfg.WebAuthn.Enabled, RPID: cfg.WebAuthn.RPID, RPDisplayName: cfg.WebAuthn.RPDisplayName, RPOrigins: cfg.WebAuthn.RPOrigins}
	verifier, e := identityadapter.NewPasskeyVerifier(options)
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
