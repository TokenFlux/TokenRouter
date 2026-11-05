package provider

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

// ollama_usage_values.go defines Ollama usage settings, fetch inputs,
// snapshots, and administrator state.
// Ollama browser usage snapshots keep the latest attempt and the last
// successful display observation separately. Failed attempts can retain older
// plan and window data while carrying their own status and retry deadline, and
// administrator state also reports managed session availability.

// Ollama Cloud usage statuses distinguish successful fetches, authentication
// rejection, and other failures. Managed Extra keys store local session/refresh
// settings, and the minimum fetch interval constrains successful periodic
// observations.
const (
	OllamaCloudUsageStatusOK           = "ok"           // successful settings-page fetch
	OllamaCloudUsageStatusUnauthorized = "unauthorized" // observed browser session rejection
	OllamaCloudUsageStatusFailed       = "failed"       // other normalized fetch failure

	OllamaCloudUsageSessionExtraKey     = "ollama_cloud_usage_session"      // encrypted local browser session
	OllamaCloudUsageAutoRefreshExtraKey = "ollama_cloud_usage_auto_refresh" // per-group refresh enablement key
	OllamaCloudUsageMinFetchInterval    = 15 * time.Minute                  // minimum successful periodic fetch spacing
)

// These Ollama usage errors report unavailable dependencies, invalid
// provider/session configuration, identity changes during refresh, and manual
// cooldown rejection to administrator callers.
var (
	ErrOllamaCloudUsageUnavailable = apperror.ServiceUnavailable(
		"OLLAMA_CLOUD_USAGE_UNAVAILABLE", "Ollama Cloud usage is unavailable",
	)
	ErrOllamaCloudUsageProviderInvalid = apperror.BadRequest(
		"OLLAMA_CLOUD_USAGE_PROVIDER_INVALID", "provider must be an OpenAI or Anthropic API key provider using https://ollama.com",
	)
	ErrOllamaCloudUsageSessionRequired = apperror.BadRequest(
		"OLLAMA_CLOUD_USAGE_SESSION_REQUIRED", "an Ollama web session must be configured first",
	)
	ErrOllamaCloudUsageEncryptionKey = apperror.BadRequest(
		"OLLAMA_CLOUD_USAGE_ENCRYPTION_KEY_NOT_CONFIGURED", "cannot store an Ollama web session without a fixed security.secret_encryption_key",
	)
	ErrOllamaCloudUsageIdentityChanged = apperror.Conflict(
		"OLLAMA_CLOUD_USAGE_IDENTITY_CHANGED", "provider identity or Ollama web session changed during refresh; retry",
	)
	ErrOllamaCloudUsageRefreshRateLimited = apperror.TooManyRequests(
		"OLLAMA_CLOUD_USAGE_REFRESH_RATE_LIMITED", "Ollama Cloud usage can be refreshed manually once every 30 seconds",
	)
)

// OllamaUsageFetchInput captures the transport and observation inputs for one
// settings-page fetch.
type OllamaUsageFetchInput struct {
	ProviderID  int64     // provider transport pool identity
	Concurrency int       // provider HTTP concurrency configuration
	ProxyURL    string    `json:"-"` // sensitive provider proxy endpoint
	Cookie      string    `json:"-"` // normalized browser session cookies
	ObservedAt  time.Time // timestamp captured before fetching
}

// OllamaUsageObservation aliases the upstream parsed display data and sanitized
// fetch failure evidence.
type OllamaUsageObservation = usageview.OllamaUsageObservation

// OllamaCloudUsageSettings configures global activity-triggered scans and their
// timing in minutes.
type OllamaCloudUsageSettings struct {
	Enabled bool `json:"enabled"` // global periodic scanning enabled
	// IntervalMinutes and DebounceMinutes jointly determine min(last
	// activity
	// plus debounce, reference attempt plus maximum wait). Normalization
	// bounds
	// each separately, while EncodeOllamaCloudUsageSettings additionally
	// requires debounce below interval.
	IntervalMinutes int `json:"interval_minutes"` // maximum activity wait in minutes
	DebounceMinutes int `json:"debounce_minutes"` // quiet period after activity in minutes
}

// OllamaCloudUsageData aliases parsed plan, window, balance, and model-count
// display observations.
type OllamaCloudUsageData = usageview.OllamaCloudUsageData

// OllamaCloudUsageSnapshot stores the latest attempt beside retained successful
// display data in provider Extra.
type OllamaCloudUsageSnapshot struct {
	Status string `json:"status"` // latest attempt status enum
	// Data and FetchedAt describe the last successful fetch and remain
	// together
	// on later failure. A failed attempt advances LastAttemptAt and retry
	// state
	// while the previous display observation remains available.
	Data          *OllamaCloudUsageData `json:"data,omitempty"`       // last successful display data
	FetchedAt     *time.Time            `json:"fetched_at,omitempty"` // last successful observation timestamp
	LastAttemptAt time.Time             `json:"last_attempt_at"`      // latest fetch attempt timestamp
	// NextRefreshAt supplies a retry floor after failure or unauthorized
	// status; successful periodic scheduling instead derives its due time
	// from
	// FetchedAt and new activity.
	NextRefreshAt time.Time `json:"next_refresh_at"`
	FailureCount  int       `json:"failure_count,omitempty"` // consecutive persisted failed attempts
	HTTPStatus    int       `json:"http_status,omitempty"`   // latest observed HTTP status
	LastError     string    `json:"last_error,omitempty"`    // latest sanitized failure label
}

// OllamaCloudUsageState reports provider eligibility and managed usage
// configuration to administrator clients.
type OllamaCloudUsageState struct {
	ProviderID int64 `json:"provider_id"` // displayed provider record ID
	// Eligible uses IsOllamaCloudUsageProvider to check platform, API Key
	// type,
	// and Ollama base URL. Usable API-key contents are checked separately
	// when
	// group identity is required.
	Eligible bool `json:"eligible"` // supported Ollama provider kind
	// Configured reports a nonblank stored session string for an eligible
	// provider; it does not establish successful decryption or upstream
	// authentication.
	Configured bool `json:"configured"`
	// AutoRefreshEnabled combines a configured session with the stored
	// per-group boolean; global settings and actual due timing are
	// evaluated
	// when scanning.
	AutoRefreshEnabled bool `json:"auto_refresh_enabled"`
	// EncryptionKeyConfigured is filled by service EnrichState from its
	// process
	// configuration; direct state construction leaves this flag false until
	// enrichment.
	EncryptionKeyConfigured bool `json:"encryption_key_configured"`
	// Snapshot holds fresh JSON-decoded display state when its stored
	// status is
	// recognized; decode failure yields nil and group resolution happens in
	// the
	// service.
	Snapshot *OllamaCloudUsageSnapshot `json:"snapshot,omitempty"`
}
