package provider

import (
	"context"
	"fmt"
	"maps"
	"time"
)

// configuration_write.go merges selected administrator configuration fields
// into current provider records and dispatches conditional storage updates.
// Administrator edits can begin from a provider snapshot that predates token
// refresh, billing increments, or maintenance observations. Applying that
// entire snapshot would replace data those other writers have already updated.
// ConfigurationFields instead identifies the values this edit intends to change
// against the latest locked record.
//
// The merge keeps managed Extra keys and omitted sensitive credentials from
// that current record, applies requested patches, and reconciles quota windows
// at the caller's captured time. Protocol normalization runs after secret
// inheritance, with the current fingerprint seed taking priority over the
// desired snapshot. A changed upstream usage monitoring identity discards its observation.
// Storage supplies the row lock and durable transaction; conditional and patch
// updates require a ConfigurationWriter so a plain whole-record adapter cannot
// silently weaken those requirements.

// Configuration field bits select values from the desired administrator record.
// Omitted bits keep the locked current value; runtime columns use separate
// writers.
const (
	ConfigName               ConfigurationFields = 1 << iota // administrator display name
	ConfigNotes                                              // administrator provider notes
	ConfigPlatform                                           // upstream platform identity
	ConfigType                                               // upstream authentication type
	ConfigCredentials                                        // credential replacement or patch
	ConfigExtra                                              // configuration replacement or patch
	ConfigProxyID                                            // outbound proxy selection
	ConfigConcurrency                                        // provider concurrency limit
	ConfigPriority                                           // scheduling priority value
	ConfigRateMultiplier                                     // provider billing multiplier
	ConfigLoadFactor                                         // scheduling load divisor
	ConfigStatus                                             // persisted provider status
	ConfigErrorMessage                                       // persisted status explanation
	ConfigSchedulable                                        // administrator scheduling switch
	ConfigExpiresAt                                          // provider expiration timestamp
	ConfigAutoPauseOnExpired                                 // expiration scheduling policy
	ConfigParentProviderID                                   // credential parent identity
	ConfigQuotaDimension                                     // provider usage dimension
)

// managedConfigurationExtraKeys names values owned by billing and maintenance
// writers. Configuration replacement inherits each present value from the locked
// current record and removes a desired value when the current record lacks it.
var managedConfigurationExtraKeys = []string{
	"quota_used", "quota_daily_used", "quota_daily_start", "quota_weekly_used", "quota_weekly_start",
	"quota_monthly_used", "quota_monthly_start",
	"grok_billing_snapshot", "grok_usage_snapshot", "grok_observed_models",
	"qoder_quota_snapshot", "qoder_quota_updated_at", QoderCatalogExtraKey,
	"ollama_cloud_usage_session", "ollama_cloud_usage_auto_refresh", "ollama_cloud_usage_snapshot",
	"cn_usage_monitor_snapshot", "model_rate_limits", "antigravity_quota_scopes", "antigravity_credits_overages",
}

// ConfigurationFields selects administrator-owned values during a locked
// provider configuration merge.
type ConfigurationFields uint32

// ConfigurationChange carries selected provider fields and the intent needed to
// merge them.
// The writer evaluates these inputs against its locked current record before
// persistence.
type ConfigurationChange struct {
	// ExtraPatch merges selected maintenance values into current Extra when
	// ConfigExtra is selected. Managed keys are then restored from the locked
	// record, so a caller supplies intent rather than an older complete snapshot.
	ExtraPatch map[string]any `json:"-"`
	// CredentialPatch is merged into current credentials when ConfigCredentials
	// is selected. A nonnil patch requires storage with conditional merge support.
	CredentialPatch map[string]any `json:"-"`
	// ExpectedCredentials rejects a write when the locked identity differs from
	// the observation that produced this change, before any desired fields merge.
	ExpectedCredentials *CredentialVersion `json:"-"`
	// ProtocolExtra supplies original Extra intent to ApplyLegacyProtocolPatch
	// when NormalizeProtocols is enabled, separately from already merged Extra.
	ProtocolExtra map[string]any
	// PreserveExtraKeys retains these additional current values during Extra
	// replacement, including their absence when the current record lacks them.
	PreserveExtraKeys []string
	// ComputeResetAt requests next-reset publication using this captured time
	// after the latest managed usage values have been retained in Extra.
	ComputeResetAt *time.Time
	// NormalizeWindowAt requests fixed-window reconciliation at this captured
	// time, allowing expired daily or weekly consumption to reset during the merge.
	NormalizeWindowAt *time.Time

	Fields ConfigurationFields // selected desired record fields
	// PreserveSensitive inherits current sensitive keys omitted by CredentialInput
	// when credentials are selected, then sanitizes ephemeral authentication fields.
	PreserveSensitive bool
	// CredentialInput records the submitted key presence. A supplied sensitive
	// key replaces the current value even when its desired value is empty.
	CredentialInput map[string]any
	// NormalizeProtocols applies compatibility conversion and current protocol
	// validation after merging, retaining a valid current fingerprint seed first.
	NormalizeProtocols bool
}

// ConfigurationWriter persists field intent against the current locked provider
// record.
type ConfigurationWriter interface {
	// UpdateConfiguration merges the declared values and writes their scheduling
	// event with the configuration. A transaction supplied through ctx remains
	// owned by its caller, including completion and later publication.
	UpdateConfiguration(context.Context, *Record, ConfigurationChange) error
}

// ApplyConfigurationChange merges selected desired fields into a copy of the
// latest current record. Supply nonnil stable records, and hold the storage
// lock that protects current until the eventual write completes; this helper
// performs no persistence.
//
// Credential identity is checked before merging. Sensitive-key presence comes
// from CredentialInput, while managed Extra keys and requested retained keys
// come from current. GroupIDs is taken from desired independently of Fields.
// Quota timestamps use the supplied captured times, and protocol normalization
// retains the current valid fingerprint seed before consulting desired.
//
// Changing the Antigravity overages setting clears observations tied to its
// prior state. Changing the upstream usage monitoring fingerprint removes its saved
// snapshot. The final CloneRecord isolates supported mutable data from both
// inputs before storage receives the result.
func ApplyConfigurationChange(current, desired *Record, change ConfigurationChange) (*Record, error) {
	if change.ExpectedCredentials != nil && !MatchesCredentialVersion(current, *change.ExpectedCredentials) {
		return nil, ErrRefreshProviderStateChanged
	}
	out := CloneRecord(current)
	if change.Fields&ConfigName != 0 {
		out.Name = desired.Name
	}
	if change.Fields&ConfigNotes != 0 {
		out.Notes = desired.Notes
	}
	if change.Fields&ConfigPlatform != 0 {
		out.Platform = desired.Platform
	}
	if change.Fields&ConfigType != 0 {
		out.Type = desired.Type
	}
	if change.Fields&ConfigCredentials != 0 {
		out.Credentials = desired.Credentials
		if change.CredentialPatch != nil {
			out.Credentials = MergeCredentials(current.Credentials, CloneValues(change.CredentialPatch))
		}
	}
	if change.Fields&ConfigExtra != 0 {
		out.Extra = desired.Extra
		if change.ExtraPatch != nil {
			out.Extra = CloneValues(current.Extra)
			if out.Extra == nil {
				out.Extra = map[string]any{}
			}
			maps.Copy(out.Extra, CloneValues(change.ExtraPatch))
		}
	}
	if change.Fields&ConfigProxyID != 0 {
		out.ProxyID = desired.ProxyID
	}
	if change.Fields&ConfigConcurrency != 0 {
		out.Concurrency = desired.Concurrency
	}
	if change.Fields&ConfigPriority != 0 {
		out.Priority = desired.Priority
	}
	if change.Fields&ConfigRateMultiplier != 0 {
		out.RateMultiplier = desired.RateMultiplier
	}
	if change.Fields&ConfigLoadFactor != 0 {
		out.LoadFactor = desired.LoadFactor
	}
	if change.Fields&ConfigStatus != 0 {
		out.Status = desired.Status
	}
	if change.Fields&ConfigErrorMessage != 0 {
		out.ErrorMessage = desired.ErrorMessage
	}
	if change.Fields&ConfigSchedulable != 0 {
		out.Schedulable = desired.Schedulable
	}
	if change.Fields&ConfigExpiresAt != 0 {
		out.ExpiresAt = desired.ExpiresAt
	}
	if change.Fields&ConfigAutoPauseOnExpired != 0 {
		out.AutoPauseOnExpired = desired.AutoPauseOnExpired
	}
	if change.Fields&ConfigParentProviderID != 0 {
		out.ParentProviderID = desired.ParentProviderID
	}
	if change.Fields&ConfigQuotaDimension != 0 {
		out.QuotaDimension = desired.QuotaDimension
	}
	out.GroupIDs = desired.GroupIDs
	out.Credentials = CloneValues(out.Credentials)
	if change.Fields&ConfigCredentials != 0 && change.PreserveSensitive && out.Credentials == nil {
		out.Credentials = map[string]any{}
	}
	out.Extra = CloneValues(out.Extra)
	if change.Fields&ConfigCredentials != 0 && change.PreserveSensitive {
		// Key presence comes from submitted input, not a previously validated
		// snapshot. Restore an omitted secret from the locked current record so
		// an administrator editing another field cannot reinstate an older token.
		for _, key := range SensitiveCredentialKeys {
			if _, provided := change.CredentialInput[key]; provided {
				continue
			}
			delete(out.Credentials, key)
			if v, ok := current.Credentials[key]; ok {
				out.Credentials[key] = CloneValues(map[string]any{key: v})[key]
			}
		}
		// Sanitization follows inheritance because old stored credentials can still
		// contain temporary SSO, password, or cookie fields that should not return
		// to the next persisted credential object.
		out.Credentials = SanitizeStoredCredentials(out.Platform, out.Credentials)
	}
	if change.Fields&ConfigExtra != 0 {
		if out.Extra == nil {
			out.Extra = map[string]any{}
		}
		// Consumption and maintenance observations may have advanced since desired
		// was read. Restore their current values, including absence, before quota
		// calculations interpret those retained counters and window starts.
		for _, key := range managedConfigurationExtraKeys {
			delete(out.Extra, key)
			if v, ok := current.Extra[key]; ok {
				out.Extra[key] = CloneValues(map[string]any{key: v})[key]
			}
		}
		for _, key := range change.PreserveExtraKeys {
			delete(out.Extra, key)
			if v, ok := current.Extra[key]; ok {
				out.Extra[key] = CloneValues(map[string]any{key: v})[key]
			}
		}
		if change.ComputeResetAt != nil {
			ComputeQuotaResetAt(out.Extra, *change.ComputeResetAt, current.LoadLocation)
		}
		if change.NormalizeWindowAt != nil {
			NormalizeFixedQuotaWindows(out.Extra, *change.NormalizeWindowAt, current.LoadLocation)
		}
		// The overages observation and credit limits describe the former allowance
		// setting. Clear that observation, and clear all model limits when enabling
		// overages or just the AICredits entry when disabling it.
		if current.Platform == PlatformAntigravity && current.IsOveragesEnabled() != out.IsOveragesEnabled() {
			delete(out.Extra, "antigravity_credits_overages")
			if out.IsOveragesEnabled() {
				delete(out.Extra, "model_rate_limits")
			} else if limits, ok := out.Extra["model_rate_limits"].(map[string]any); ok {
				delete(limits, "AICredits")
			}
		}
	}
	if change.NormalizeProtocols {
		// Convert submitted legacy intent before validating the resulting protocol
		// set. Keep the locked seed when present so editing routes cannot reset
		// the provider identity used by outgoing fingerprinted requests.
		ApplyLegacyProtocolPatch(out, change.CredentialInput, change.ProtocolExtra)
		if err := NormalizeTextEndpointCredentials(out, false); err != nil {
			return nil, err
		}
		if err := NormalizeOpenAIAPIKeyConfiguration(out); err != nil {
			return nil, err
		}
		if err := NormalizeProviderProtocols(out); err != nil {
			return nil, err
		}
		if seed, ok := CodexFingerprintSeed(current.Extra); ok {
			if out.Extra == nil {
				out.Extra = map[string]any{}
			}
			out.Extra[CodexFingerprintSeedExtraKey] = seed
		} else if seed, ok := CodexFingerprintSeed(desired.Extra); ok {
			if out.Extra == nil {
				out.Extra = map[string]any{}
			}
			out.Extra[CodexFingerprintSeedExtraKey] = seed
		}
	}
	// Usage observations identify credentials and transport. Once that identity
	// changes, a retained usage snapshot belongs to the former connection and
	// must be removed before the merged configuration is returned.
	if UsageMonitorIdentityFingerprint(current) != UsageMonitorIdentityFingerprint(out) {
		delete(out.Extra, UsageMonitorSnapshotExtraKey)
	}
	if _, err := BalanceAlertConfiguration(out); err != nil {
		return nil, err
	}
	return CloneRecord(out), nil
}

// WriteConfiguration dispatches the declared edit to a ConfigurationWriter when
// the store provides one. That writer owns its transaction rules, including
// participation in a transaction carried by ctx.
//
// A plain Update adapter supports whole-record writes for compatibility, but
// ExpectedCredentials, CredentialPatch, or ExtraPatch require the conditional
// writer and return ErrRefreshCredentialPersist when it is absent. Callers
// using plain Update therefore supply a complete record whose fields are safe
// to replace; field-selection and patch merging are supplied by the
// ConfigurationWriter implementation.
func WriteConfiguration(ctx context.Context, store interface {
	// Update replaces the complete provider record through the compatibility
	// adapter. Callers supply fields safe to replace because this method receives
	// no field-selection or conditional merge intent.
	Update(context.Context, *Record) error
}, value *Record, change ConfigurationChange,
) error {
	if writer, ok := store.(ConfigurationWriter); ok {
		return writer.UpdateConfiguration(ctx, value, change)
	}
	if change.ExpectedCredentials != nil || change.CredentialPatch != nil || change.ExtraPatch != nil {
		return fmt.Errorf("%w: conditional configuration writer is not configured", ErrRefreshCredentialPersist)
	}
	// Compatibility stores receive the caller's complete record. Conditional
	// and patch changes were rejected above because this path cannot compare
	// identity or merge selected values under the provider storage lock.
	return store.Update(ctx, value)
}

// CRSSyncConfiguration selects the record fields owned by a CRS import. Proxy
// selection participates only when proxyProvided is true, allowing
// synchronization without a supplied proxy to retain the destination's current
// proxy.
func CRSSyncConfiguration(proxyProvided bool) ConfigurationChange {
	change := ConfigurationChange{Fields: ConfigName | ConfigPlatform | ConfigType | ConfigCredentials | ConfigExtra | ConfigConcurrency | ConfigPriority | ConfigStatus | ConfigSchedulable}
	if proxyProvided {
		change.Fields |= ConfigProxyID
	}
	return change
}
