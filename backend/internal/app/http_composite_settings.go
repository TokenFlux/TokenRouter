package app

import (
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
)

// provideCompositeSettingsHTTP 只在构造时绑定唯一领域实例与原生综合协调端口。
func provideCompositeSettingsHTTP(source *composite.Runtime, registry *settings.Registry, monitor *ops.OpsService, pay *payment.ConfigService, turnstile *identity.TurnstileService, aliyun *identity.AliyunCaptchaService, attributes *identity.UserAttributeService, totp *identity.TotpService, users *identity.UserService, creative *creative.Public) *settingshttp.Handler {
	return settingshttp.NewHandler(settingshttp.HandlerOptions{Settings: source, Participants: registry, Monitoring: monitor, Payment: pay, Turnstile: turnstile, Aliyun: aliyun, Attributes: attributes, Totp: totp, User: users, Creative: creative})
}
