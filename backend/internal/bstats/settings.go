package bstats

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// settings.go owns administrator controls for the bStats reporting service.
// Missing settings follow the official MetricsConfig defaults, while submitted
// fields participate in the existing atomic settings update. Reading distinguishes
// a missing enable key from a saved false or malformed value, so installations
// without this feature's settings retain the documented default without treating
// corrupted text as permission to report.
//
// Participant preparation processes only its declared public fields. Checking
// presence before decoding prevents an omitted boolean from becoming an opt-out
// through its Go zero value. The persistent server UUID belongs to the reporter
// and is excluded from administrator input, preserving identity across unrelated
// settings saves and preventing callers from replacing it through this participant.

const (
	// These keys retain the official opt-out and logging choices in shared settings storage.
	SettingKeyEnabled               = "bstats_enabled"
	SettingKeyLogErrors             = "bstats_log_errors"
	SettingKeyLogSentData           = "bstats_log_sent_data"
	SettingKeyLogResponseStatusText = "bstats_log_response_status_text"
	SettingKeyServerUUID            = "bstats_server_uuid"
)

// Config holds the public reporting controls without the persistent instance identifier.
// @project-doc docs/interfaces/configuration.md#bstats_reporting
type Config struct {
	Enabled               bool `json:"bstats_enabled"`                  // administrator reporting opt-out
	LogErrors             bool `json:"bstats_log_errors"`               // report failure logging
	LogSentData           bool `json:"bstats_log_sent_data"`            // outgoing payload logging
	LogResponseStatusText bool `json:"bstats_log_response_status_text"` // received response logging
}

// ReadSettings interprets stored controls using the Java MetricsConfig defaults.
// An absent enable key means true; present values enable a control only when
// they equal true exactly. Missing logging keys remain disabled.
func ReadSettings(values map[string]string) Config {
	enabled, exists := values[SettingKeyEnabled]
	return Config{
		Enabled:               !exists || enabled == "true",
		LogErrors:             values[SettingKeyLogErrors] == "true",
		LogSentData:           values[SettingKeyLogSentData] == "true",
		LogResponseStatusText: values[SettingKeyLogResponseStatusText] == "true",
	}
}

// Values encodes all four controls for a complete settings snapshot.
func (c Config) Values() map[string]string {
	return map[string]string{
		SettingKeyEnabled:               strconv.FormatBool(c.Enabled),
		SettingKeyLogErrors:             strconv.FormatBool(c.LogErrors),
		SettingKeyLogSentData:           strconv.FormatBool(c.LogSentData),
		SettingKeyLogResponseStatusText: strconv.FormatBool(c.LogResponseStatusText),
	}
}

// SettingsParticipant declares the four writable controls for combined updates.
// It prepares only provided fields, leaving omitted controls and the internal
// instance identifier untouched until the coordinator commits the complete batch.
// The returned field and key lists use the same names and remain owned by the
// participant; registry callers must not modify those lists.
//
// Decoding errors stop preparation before persistence. The composite HTTP layer
// merges nullable request pointers with stored values before invoking this
// participant, while direct callers supply the exact public fields to change.
func SettingsParticipant() settings.Participant {
	fields := []string{
		SettingKeyEnabled,
		SettingKeyLogErrors,
		SettingKeyLogSentData,
		SettingKeyLogResponseStatusText,
	}
	return settings.Participant{Module: "bstats", Fields: fields, Keys: fields, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		values := make(map[string]string, len(input))
		for _, key := range fields {
			raw, supplied := input[key]
			if !supplied {
				continue
			}
			var value bool
			if err := json.Unmarshal(raw, &value); err != nil {
				return settings.PreparedChange{}, err
			}
			values[key] = strconv.FormatBool(value)
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}
