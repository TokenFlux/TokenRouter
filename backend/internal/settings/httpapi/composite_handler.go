package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
)

// CompositeSettings 仅描述综合读取与已注册更新流程；不暴露具体旧服务、配置或存储。
type CompositeSettings interface {
	GetAllSettings(context.Context) (*composite.Snapshot, error)
	GetAuthSourceDefaultSettings(context.Context) (*identity.AuthSourceDefaultSettings, error)
	GetDefaultPlatformQuotas(context.Context) (map[string]*billing.DefaultPlatformQuotaSetting, error)
	GetOpenAIFastPolicySettings(context.Context) (*tierpolicy.OpenAIFastPolicySettings, error)
	IsTotpEncryptionKeyConfigured() bool
	OIDCSecurityWriteDefaults(context.Context) (bool, bool, error)
	BeginSettingsUpdate(context.Context) (*settings.UpdateSession, error)
	PrepareSettingsWithAuthSourceDefaults(context.Context, *composite.Snapshot, *identity.AuthSourceDefaultSettings, settings.OmittedKeys) (map[string]string, error)
	ApplyPersistedSettings(context.Context) error
}
type MonitoringSettings interface {
	IsMonitoringEnabled(context.Context) bool
	SetMonitoringEnabled(bool)
}
type PaymentSettings interface {
	GetPaymentConfig(context.Context) (*payment.PaymentConfig, error)
}
type CaptchaSecretValidator interface {
	ValidateSecretKey(context.Context, string) error
}
type CaptchaCredentialsValidator interface {
	ValidateCredentials(context.Context, string, string, string, string) error
}
type CreativeModelSettings interface {
	ListCreativeModelCandidates(context.Context) ([]creative.CreativeModelCandidate, error)
}

// Handler 仅拥有综合 HTTP 接口依赖，所有缓存与业务实例由 app 唯一装配。
type Handler struct {
	settingService       CompositeSettings
	settingsParticipants *settings.Registry
	participantError     error
	opsService           MonitoringSettings
	paymentConfigService PaymentSettings
	turnstileService     CaptchaSecretValidator
	aliyunCaptchaService CaptchaCredentialsValidator
	userAttributeService *identity.UserAttributeService
	totpService          identityhttp.StepUpGrantChecker
	userService          identityhttp.UserReader
	creativeModelReader  CreativeModelSettings
}
type HandlerOptions struct {
	Settings         CompositeSettings
	Participants     *settings.Registry
	ParticipantError error
	Monitoring       MonitoringSettings
	Payment          PaymentSettings
	Turnstile        CaptchaSecretValidator
	Aliyun           CaptchaCredentialsValidator
	Attributes       *identity.UserAttributeService
	Totp             identityhttp.StepUpGrantChecker
	User             identityhttp.UserReader
	Creative         CreativeModelSettings
}

// NewHandler 构造不回源、不启动任务；参与者必须在路由开放前固定。
func NewHandler(o HandlerOptions) *Handler {
	return &Handler{settingService: o.Settings, settingsParticipants: o.Participants, participantError: o.ParticipantError, opsService: o.Monitoring, paymentConfigService: o.Payment, turnstileService: o.Turnstile, aliyunCaptchaService: o.Aliyun, userAttributeService: o.Attributes, totpService: o.Totp, userService: o.User, creativeModelReader: o.Creative}
}
func (h *Handler) preparedParticipants(ctx context.Context, input settings.Fields, values map[string]string) ([]settings.PreparedChange, error) {
	if h.participantError != nil {
		return nil, h.participantError
	}
	prepared, err := h.settingsParticipants.Prepare(ctx, input, values)
	if err != nil {
		return nil, err
	}
	for _, key := range h.settingsParticipants.OwnedKeys() {
		delete(values, key)
	}
	return prepared, nil
}
