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

// identity_security.go constructs encryption, TOTP, passkey, and CAPTCHA
// services for identity challenges. TOTP receives identity email challenges and
// their queue, passkeys receive the WebAuthn settings, and CAPTCHA services
// receive runtime settings with the application's logging observer.

// provideSecretEncryptor constructs identity secret encryption from cfg's
// hexadecimal TOTP key. Bootstrap validates the decoding and required key
// length, returning configuration errors before an encryptor is available.
func provideSecretEncryptor(cfg *config.Config) (identity.SecretEncryptor, error) {
	return bootstrap.NewAESEncryptor(cfg)
}

// provideTotp binds identity storage, notification delivery, and settings.
func provideTotp(users *identitypostgres.UserStore, encryptor identity.SecretEncryptor, cache identity.TotpCache, settings *identityAuthSettings, email *identity.EmailChallenges, queue *notification.EmailQueueService) *identity.TotpService {
	return identity.NewTotpService(users, encryptor, cache, settings, email, queue, time.Now)
}

// providePasskey constructs the WebAuthn verifier and passkey service from
// startup settings. Disabled WebAuthn yields an unset verifier and disabled
// service; enabled verifier-construction errors are returned before creating
// the service and its storage bindings.
// The enabled verifier retains cfg.WebAuthn.RPOrigins, so callers keep that
// shared slice unchanged during service use.
func providePasskey(cfg *config.Config, repo identity.PasskeyRepository, sessions identity.PasskeySessionStore, users *identitypostgres.UserStore) (*identity.PasskeyService, error) {
	options := identityadapter.PasskeyOptions{Enabled: cfg.Auth.WebAuthn.Enabled, RPID: cfg.Auth.WebAuthn.RPID, RPDisplayName: cfg.Auth.WebAuthn.RPDisplayName, RPOrigins: cfg.Auth.WebAuthn.RPOrigins}
	verifier, e := identityadapter.NewPasskeyVerifier(options)
	if e != nil {
		return nil, e
	}
	return identity.NewPasskeyService(options.Enabled, verifier, repo, sessions, users, time.Now), nil
}

// providePasskeyHTTP constructs passkey handlers with session authentication and backend-mode admission.
func providePasskeyHTTP(passkeys *identity.PasskeyService, auth *identityAuthGraph, backend *admission.BackendMode) *identityhttp.PasskeyHandler {
	return identityhttp.NewPasskeyHandler(passkeys, auth.Core, backend)
}

// provideTurnstile constructs Turnstile verification with runtime settings and application logging.
func provideTurnstile(settings *identity.RuntimeSettings, verifier identity.TurnstileVerifier) *identity.TurnstileService {
	s := identity.NewTurnstileService(settings, verifier)
	s.SetObserver(logging.LegacyPrintf)
	return s
}

// provideTencentCaptcha constructs Tencent verification with runtime settings and application logging.
func provideTencentCaptcha(settings *identity.RuntimeSettings, verifier identity.TencentCaptchaVerifier) *identity.TencentCaptchaService {
	s := identity.NewTencentCaptchaService(settings, verifier)
	s.SetObserver(logging.LegacyPrintf)
	return s
}

// provideAliyunCaptcha constructs Aliyun verification with runtime settings and application logging.
func provideAliyunCaptcha(settings *identity.RuntimeSettings, verifier identity.AliyunCaptchaVerifier) *identity.AliyunCaptchaService {
	s := identity.NewAliyunCaptchaService(settings, verifier)
	s.SetObserver(logging.LegacyPrintf)
	return s
}
