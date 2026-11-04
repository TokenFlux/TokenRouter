package runtimeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip/policy"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// forwarded.go loads and prepares forwarded client-IP settings for runtime
// publication.
// Client-IP identity must follow one selected mode and ordered header list
// across startup and administrator updates. Persisted values overlay process
// defaults; a missing header key is backfilled, while malformed stored names
// disable raw-header compatibility so security consumers use their peer policy.
// The loader prepares these related values before publishing them together.
//
// Raw-header trust defaults to off, because any client that can reach the origin
// could otherwise choose its own address. Installations that already ran with the
// earlier enabled default depend on it behind a reverse proxy, so a hidden marker
// makes one upgrade decision: an existing installation with no stored mode, no
// explicit switch, and no trusted-proxy policy keeps raw-header trust as a stored
// value. An installation without the earlier marker is treated as new.
// Migration persistence precedes
// publication, but a write failure still publishes the calculated policy and
// returns its error. Startup reports the migration failure while this process
// has the policy selected from its current inputs.

// These keys store the mode, ordered custom sources, and one-time upgrade marker
// used by startup loading and the shared administrator settings participant.
const (
	SettingKeyForwardedClientIPModeV2     = "forwarded_client_ip_mode_v2_migrated"     // installation predates the secure default
	SettingKeyForwardedClientIPDefaultOff = "forwarded_client_ip_default_off_migrated" // completed secure-default upgrade marker
	SettingKeyAPIKeyACLTrustForwardedIP   = "api_key_acl_trust_forwarded_ip"           // raw-header trust boolean text
	SettingKeyForwardedClientIPHeaders    = "forwarded_client_ip_headers"              // ordered source-name JSON array
)

// ForwardedSettingsRepository reads client IP settings and persists migration updates.
type ForwardedSettingsRepository interface {
	// GetMultiple returns stored values among the requested keys under ctx.
	// Missing keys are omitted so startup loading can distinguish persisted
	// preferences from process defaults and absent migration metadata.
	GetMultiple(context.Context, []string) (map[string]string, error)
	// SetMultiple persists the selected migration keys under the supplied context.
	// The loader returns write failures while publishing its calculated values,
	// allowing startup to report persistence failure with a selected runtime policy.
	SetMultiple(context.Context, map[string]string) error
}

// ForwardedSettingsOptions contains initial client IP settings and runtime publication callbacks.
type ForwardedSettingsOptions struct {
	// InitialTrust supplies mode when no persisted value exists. A stored value
	// takes precedence before the one-time compatibility migration is evaluated.
	InitialTrust bool
	// InitialTrustConfigured and TrustedProxiesConfigured prevent the one-time
	// upgrade from retaining raw headers when the operator supplied the switch or
	// a peer-trust policy, including an empty list.
	InitialTrustConfigured   bool // explicit startup trust switch
	TrustedProxiesConfigured bool // explicit trusted proxy policy
	// Headers supplies fallback names after a successful repository read. Return
	// validated, nonnil names so an absent header key can be persisted as a JSON array.
	Headers func() []string
	// Publish receives the chosen mode and names synchronously. It must be
	// callable, and it owns any copy needed before retaining the supplied slice.
	Publish func(bool, []string)
}

// ForwardedSettings migrates persisted client IP settings and publishes them to the runtime.
type ForwardedSettings struct {
	// settingRepo remains caller-owned. A nil repository makes startup Load a
	// no-op, so construction can represent a loader with no persistent source.
	settingRepo ForwardedSettingsRepository
	options     ForwardedSettingsOptions // captured startup defaults and callbacks
}

// ForwardedInput carries the editable raw-header mode and ordered client-IP sources.
type ForwardedInput struct {
	APIKeyACLTrustForwardedIP bool `json:"api_key_acl_trust_forwarded_ip"` // raw forwarding-header trust flag
	// ForwardedClientIPHeaders is the editable source order. PrepareForwardedSettings
	// canonicalizes and copies names before encoding a nonnull persisted JSON array.
	ForwardedClientIPHeaders []string `json:"forwarded_client_ip_headers"`
}

// NewForwardedSettings stores startup defaults and callbacks for later policy loading.
// A configured repository requires callable Headers and Publish callbacks;
// Headers returns validated, nonnil names. App calls LoadForwardedClientIPSettings
// during startup, and the caller retains repository ownership.
func NewForwardedSettings(repo ForwardedSettingsRepository, options ForwardedSettingsOptions) *ForwardedSettings {
	return &ForwardedSettings{settingRepo: repo, options: options}
}

// PrepareForwardedSettings returns validated input and encoded mode/header persistence values.
// Header names become canonical, unique, and independently allocated. Invalid
// names produce BadRequest with the supplied input and no values; callers use
// the normalized result after successful preparation.
func PrepareForwardedSettings(value ForwardedInput) (ForwardedInput, map[string]string, error) {
	headers, err := policy.NormalizeForwardedClientIPHeaders(value.ForwardedClientIPHeaders)
	if err != nil {
		return value, nil, apperror.BadRequest("INVALID_FORWARDED_CLIENT_IP_HEADERS", err.Error())
	}
	value.ForwardedClientIPHeaders = headers
	raw, err := json.Marshal(headers)
	if err != nil {
		return value, nil, fmt.Errorf("marshal forwarded client IP headers: %w", err)
	}
	return value, map[string]string{SettingKeyAPIKeyACLTrustForwardedIP: strconv.FormatBool(value.APIKeyACLTrustForwardedIP), SettingKeyForwardedClientIPHeaders: string(raw)}, nil
}

// Apply synchronously publishes the supplied mode and names through the configured callback.
// Supply prepared input and a callable Publish implementation. Header storage
// is passed directly, so the callback owns copying before it retains mutable names.
func (s *ForwardedSettings) Apply(value ForwardedInput) {
	s.options.Publish(value.APIKeyACLTrustForwardedIP, value.ForwardedClientIPHeaders)
}

// SettingsParticipant registers the client-IP mode and header list with shared
// settings preparation. Its callback accepts a sparse field map; an empty map
// produces no writes, and supplied header names are validated and normalized
// before persistence values are returned.
//
// JSON conversion initializes omitted bool and slice fields to their zero
// values. PrepareForwardedSettings encodes both fields, so the callback removes
// keys that were absent from input before returning the batch. This keeps an
// omitted mode or header list unchanged while preserving supplied false and
// empty-list updates as writes.
func SettingsParticipant() settings.Participant {
	return settings.Participant{Module: "server", Fields: []string{"api_key_acl_trust_forwarded_ip", "forwarded_client_ip_headers"}, Keys: []string{SettingKeyAPIKeyACLTrustForwardedIP, SettingKeyForwardedClientIPHeaders}, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		if len(input) == 0 {
			return settings.PreparedChange{}, nil
		}
		var value ForwardedInput
		raw, err := json.Marshal(input)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			return settings.PreparedChange{}, err
		}
		_, values, err := PrepareForwardedSettings(value)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		// Binding creates zero values for absent fields. Remove their encoded keys
		// before the shared write so partial updates retain omitted persisted settings.
		for key := range values {
			if _, ok := input[key]; !ok {
				delete(values, key)
			}
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}

// ParseForwardedHeaders returns normalized names decoded from a persisted JSON array.
// Malformed JSON, null, and invalid names return errors; an empty array is valid.
// Successful results use independently allocated storage in the supplied source order.
func ParseForwardedHeaders(value string) ([]string, error) {
	var headers []string
	if err := json.Unmarshal([]byte(value), &headers); err != nil {
		return nil, fmt.Errorf("parse forwarded_client_ip_headers: %w", err)
	}
	if headers == nil {
		return nil, fmt.Errorf("parse forwarded_client_ip_headers: value must be a JSON array")
	}
	normalized, err := policy.NormalizeForwardedClientIPHeaders(headers)
	if err != nil {
		return nil, fmt.Errorf("parse forwarded_client_ip_headers: %w", err)
	}
	return normalized, nil
}

// LoadForwardedClientIPSettings selects and publishes the startup client-IP policy.
// A nil receiver or repository makes no change. Configured loaders use ctx for
// repository calls and require the constructor's callbacks; read failure publishes
// false mode with no custom sources before returning the error.
//
// Stored mode enables compatibility for literal "true". Invalid stored headers
// clear the source list and disable the mode, while missing headers use the
// supplied nonnil fallback. Before the secure-default marker exists, an existing
// installation with no stored mode or operator policy keeps raw-header trust.
// An enabled result logs a warning, since it relies on the ingress for safety.
//
// Missing metadata is persisted before the final publication. A migration write
// failure still publishes the calculated values and joins persistence and header
// errors, so the caller can report incomplete persistence with the current policy.
func (s *ForwardedSettings) LoadForwardedClientIPSettings(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}

	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyAPIKeyACLTrustForwardedIP,
		SettingKeyForwardedClientIPHeaders,
		SettingKeyForwardedClientIPModeV2,
		SettingKeyForwardedClientIPDefaultOff,
	})
	if err != nil {
		// Stored preferences could not be inspected. Publish peer-policy mode so
		// stale raw-header trust cannot survive this failed startup settings read.
		s.options.Publish(false, nil)
		return fmt.Errorf("get forwarded client ip settings: %w", err)
	}

	enabled := s.options.InitialTrust
	headers := s.options.Headers()
	storedValue, hasStoredValue := values[SettingKeyAPIKeyACLTrustForwardedIP]
	if hasStoredValue {
		enabled = storedValue == "true"
	}

	var headersErr error
	if storedHeaders, ok := values[SettingKeyForwardedClientIPHeaders]; ok {
		headers, headersErr = ParseForwardedHeaders(storedHeaders)
		if headersErr != nil {
			// Invalid persisted names remove the raw-header mode as well as sources;
			// otherwise a malformed custom list could leave built-in raw trust enabled.
			enabled = false
			headers = []string{}
			headersErr = fmt.Errorf("load forwarded client ip headers: %w", headersErr)
		}
	}

	updates := make(map[string]string)
	if _, hasStoredHeaders := values[SettingKeyForwardedClientIPHeaders]; !hasStoredHeaders {
		headersJSON, marshalErr := json.Marshal(headers)
		if marshalErr != nil {
			// Keep migration metadata serializable with an empty array and retain
			// the preparation error for the caller to report after policy publication.
			headers = []string{}
			headersErr = errors.Join(headersErr, fmt.Errorf("marshal forwarded client ip headers: %w", marshalErr))
			headersJSON = []byte("[]")
		}
		updates[SettingKeyForwardedClientIPHeaders] = string(headersJSON)
	}
	if values[SettingKeyForwardedClientIPDefaultOff] != "true" {
		updates[SettingKeyForwardedClientIPDefaultOff] = "true"
		// The earlier marker shows this installation resolved an unset mode as enabled.
		// Storing that mode keeps its proxied clients identified after the default
		// changes; operator policies and invalid headers keep peer-policy resolution.
		existingInstallation := values[SettingKeyForwardedClientIPModeV2] == "true"
		operatorChose := hasStoredValue || s.options.InitialTrustConfigured || s.options.TrustedProxiesConfigured
		if existingInstallation && !operatorChose && headersErr == nil {
			enabled = true
			updates[SettingKeyAPIKeyACLTrustForwardedIP] = "true"
		}
	}
	if enabled {
		slog.Warn("forwarded client IP compatibility is enabled; clients can spoof their address unless the ingress blocks direct origin access and overwrites these headers. Configure server.trusted_proxies and disable it")
	}
	if len(updates) > 0 {
		// Publish after the persistence attempt, including its failure path. The
		// calculated runtime mode remains usable while startup reports unfinished migration.
		if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
			s.options.Publish(enabled, headers)
			return errors.Join(headersErr, fmt.Errorf("migrate forwarded client ip setting: %w", err))
		}
	}

	s.options.Publish(enabled, headers)
	return headersErr
}

// ReadForwardedSettings builds editable settings from prior values and persisted keys.
// Missing keys retain the corresponding prior value, including shared prior
// header storage. Stored mode enables compatibility only for literal "true";
// parsed headers receive normalized, independently allocated storage.
//
// Invalid stored header text overrides an enabled mode by clearing the sources
// and selecting false. The parsing failure is logged, and callers receive that
// usable value to show the security state implied by the stored input. This
// overlay leaves persistence and runtime publication to its consumers.
func ReadForwardedSettings(settings map[string]string, prior ForwardedInput) ForwardedInput {
	apiKeyACLTrustForwardedIP := prior.APIKeyACLTrustForwardedIP
	forwardedClientIPHeaders := prior.ForwardedClientIPHeaders
	if value, ok := settings[SettingKeyAPIKeyACLTrustForwardedIP]; ok {
		apiKeyACLTrustForwardedIP = value == "true"
	}
	if value, ok := settings[SettingKeyForwardedClientIPHeaders]; ok {
		parsed, err := ParseForwardedHeaders(value)
		if err != nil {
			// A malformed persisted source list cannot justify raw header trust.
			// Return the disabled display state and retain the parse cause in logs.
			slog.Error("invalid persisted forwarded client IP headers; forwarded trust disabled", "error", err)
			apiKeyACLTrustForwardedIP = false
			forwardedClientIPHeaders = []string{}
		} else {
			forwardedClientIPHeaders = parsed
		}
	}

	return ForwardedInput{APIKeyACLTrustForwardedIP: apiKeyACLTrustForwardedIP, ForwardedClientIPHeaders: forwardedClientIPHeaders}
}
