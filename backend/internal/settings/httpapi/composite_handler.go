package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
)

// composite_handler.go assembles settings handler dependencies and prepares
// registered participant changes for combined administrator updates.
// Combined administrator updates normalize Snapshot values, validate credentials
// and step-up preconditions, and delegate persistence encoding to each module's
// registered Participant in the shared settings Registry so every stored key has
// one authoritative owner.
//
// Registered preparation failure stops the update before storage. System reads,
// authentication grants, credential probes, and HTTP presence handling precede
// participant preparation; the session commits module changes and runs ordered
// post-commit runtime callbacks before response readback.

// CompositeSettings supplies module readback and update coordination for administrator HTTP requests.
type CompositeSettings interface {
	// GetAllSettings returns parsed system settings and their stored-value map.
	// Its read can invoke model publication through the configured module reader;
	// errors stop HTTP response construction or update preparation.
	GetAllSettings(context.Context) (*composite.Snapshot, error)
	// GetAuthSourceDefaultSettings reads provider-specific registration and binding
	// grants. The update caller uses that result to retain omitted values, then
	// supplies merged fields to the identity preparer before commit.
	GetAuthSourceDefaultSettings(context.Context) (*identity.AuthSourceDefaultSettings, error)
	// GetOpenAIFastPolicySettings reads the dedicated service-tier policy. HTTP
	// response callers log a returned failure and leave the optional policy absent;
	// the error does not undo a completed settings update.
	GetOpenAIFastPolicySettings(context.Context) (*tierpolicy.OpenAIFastPolicySettings, error)
	// IsTotpEncryptionKeyConfigured reports whether startup supplied a configured TOTP encryption key.
	IsTotpEncryptionKeyConfigured() bool
	// OIDCSecurityWriteDefaults returns presence-aware PKCE and ID-token validation
	// write defaults. UpdateSettings calls it on every combined save, and a read
	// error stops that save before persistence, including when OIDC is disabled.
	OIDCSecurityWriteDefaults(context.Context) (bool, bool, error)
	// BeginSettingsUpdate admits one instance update before its caller reads current
	// values. The returned session context follows cancellation and shutdown, and
	// the caller Close releases admission after all update phases finish.
	BeginSettingsUpdate(context.Context) (*settings.UpdateSession, error)
	// NormalizeSettingsSnapshot normalizes and validates the supplied Snapshot in
	// place before participant preparation. Normalization can mutate that Snapshot
	// before failure and performs no persistence, leaving Commit to the session.
	NormalizeSettingsSnapshot(*composite.Snapshot) error
	// ApplyPersistedSettings loads committed settings and applies modules in the
	// implementation's declared order. The HTTP compatibility path calls it after
	// persistence and treats returned errors as application failures for that saved batch.
	ApplyPersistedSettings(context.Context) error
}

// MonitoringSettings reads effective Ops availability and publishes its runtime monitoring switch.
type MonitoringSettings interface {
	// IsMonitoringEnabled returns the effective monitoring decision for the current
	// context, including deployment capability. GetSettings intersects that decision
	// with the saved runtime switch before including the flag in its response.
	IsMonitoringEnabled(context.Context) bool
	// SetMonitoringEnabled publishes the supplied monitoring runtime switch. The
	// compatibility application caller invokes it after committed settings have been
	// applied, while the monitoring service retains its own deployment restrictions.
	SetMonitoringEnabled(bool)
}

// PaymentSettings supplies the separately persisted payment configuration for
// combined administrator responses.
type PaymentSettings interface {
	// GetPaymentConfig returns the current payment configuration or a read error.
	// GET callers can continue with an available or empty result; PUT response
	// readback returns a payment-read failure after settings persistence.
	GetPaymentConfig(context.Context) (*payment.PaymentConfig, error)
}

// CaptchaSecretValidator checks a changed challenge secret before administrator settings persistence.
type CaptchaSecretValidator interface {
	// ValidateSecretKey probes the supplied secret before changed Turnstile settings
	// are saved. A returned error stops preparation, keeping an unverified challenge
	// credential from entering the prepared settings batch.
	ValidateSecretKey(context.Context, string) error
}

// CaptchaCredentialsValidator validates current provider credentials before an
// enabled challenge configuration is saved.
type CaptchaCredentialsValidator interface {
	// ValidateCredentials checks access-key ID/secret, scene, and region before
	// changed Aliyun settings are saved. A returned failure stops preparation while
	// the current persisted challenge configuration remains selected.
	ValidateCredentials(context.Context, string, string, string, string) error
}

// CreativeModelSettings supplies requestable model candidates for Creative Studio
// administrator selection.
type CreativeModelSettings interface {
	// ListCreativeModelCandidates returns the currently requestable Creative group,
	// model, and capability choices. An implementation can additionally expose the
	// normalization method consulted by UpdateSettings before its batch preparation.
	ListCreativeModelCandidates(context.Context) ([]creative.CreativeModelCandidate, error)
}

// Handler holds shared module services and static preparation registration for
// the combined administrator settings endpoints.
type Handler struct {
	settingService CompositeSettings // shared combined settings runtime
	// settingsParticipants and participantError are installed at assembly. The
	// registry is required by update preparation, and a saved construction error
	// is returned before participants can prepare or the session can commit.
	settingsParticipants *settings.Registry // static preparation ownership registry
	participantError     error              // stored registry construction error
	opsService           MonitoringSettings // optional monitoring availability reader
	paymentConfigService PaymentSettings    // optional payment configuration reader
	// turnstileService is required when submitted enabled settings change a key.
	// The probe runs before persistence; otherwise that validation branch is skipped.
	turnstileService CaptchaSecretValidator // changed Turnstile secret verifier
	// aliyunCaptchaService is required when effective enabled Aliyun settings
	// change access credentials, scene, or region before the handler prepares its batch.
	aliyunCaptchaService CaptchaCredentialsValidator     // changed Aliyun credential verifier
	totpService          identityhttp.StepUpGrantChecker // sensitive-operation step-up grant checker
	userService          identityhttp.UserReader         // current actor account state reader
	creativeModelReader  CreativeModelSettings           // optional Creative model selection reader
}

// HandlerOptions supplies module services and static registration to the HTTP handler.
type HandlerOptions struct {
	Settings CompositeSettings // shared combined settings runtime
	// Participants and ParticipantError describe static preparation assembly.
	// NewHandler retains both; callers install a valid registry before updates
	// reach preparedParticipants, which reports a nonnil construction error first.
	Participants     *settings.Registry              // static preparation ownership registry
	ParticipantError error                           // stored registry construction error
	Monitoring       MonitoringSettings              // optional monitoring availability reader
	Payment          PaymentSettings                 // optional payment configuration reader
	Turnstile        CaptchaSecretValidator          // changed Turnstile secret verifier
	Aliyun           CaptchaCredentialsValidator     // changed Aliyun credential verifier
	Totp             identityhttp.StepUpGrantChecker // sensitive-operation step-up grant checker
	User             identityhttp.UserReader         // current actor account state reader
	Creative         CreativeModelSettings           // optional Creative model selection reader
}

// NewHandler retains module services and the static preparation registry for
// combined HTTP endpoints. Assembly supplies the settings runtime and update
// registry before route use, and installs optional verifiers when their credential
// validation or security branches can be reached.
func NewHandler(o HandlerOptions) *Handler {
	return &Handler{settingService: o.Settings, settingsParticipants: o.Participants, participantError: o.ParticipantError, opsService: o.Monitoring, paymentConfigService: o.Payment, turnstileService: o.Turnstile, aliyunCaptchaService: o.Aliyun, totpService: o.Totp, userService: o.User, creativeModelReader: o.Creative}
}

// preparedParticipants prepares registered owner changes against current raw
// values. It requires the registry installed at assembly; a retained assembly
// error or preparation failure returns before Commit runs.
func (h *Handler) preparedParticipants(ctx context.Context, input settings.Fields, current map[string]string) ([]settings.PreparedChange, error) {
	if h.participantError != nil {
		return nil, h.participantError
	}
	return h.settingsParticipants.Prepare(ctx, input, current)
}
