package site

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// public_test.go verifies that public settings and HTML injection expose only
// their defined presentation fields. The source deliberately includes private
// metrics settings, so serialization must not leak controls or the deployment's
// persistent identifier even if an input source supplies a wider map.

// metricsInputSource supplies private settings to exercise the public output whitelist.
type metricsInputSource struct{}

// LoadSitePublicInputs returns reporting settings alongside their private instance identifier.
func (metricsInputSource) LoadSitePublicInputs(context.Context) (PublicInputs, error) {
	return PublicInputs{Values: map[string]string{
		"bstats_enabled":       "true",
		"bstats_log_sent_data": "true",
		"bstats_server_uuid":   "private-instance-identifier",
	}}, nil
}

// PublicVersion returns the non-sensitive build value expected by public injection.
func (metricsInputSource) PublicVersion() string { return "test-version" }

// TestPublicSettingsExcludeMetricsIdentity supplies private bStats keys through
// the public source. Both ordinary API output and embedded HTML settings must
// omit the controls and persistent instance identifier from serialized data.
func TestPublicSettingsExcludeMetricsIdentity(t *testing.T) {
	service := NewPublicService(metricsInputSource{}, timezone.NewCalendar(time.UTC), "UTC")
	public, err := service.GetPublicSettings(context.Background())
	require.NoError(t, err)
	injection, err := service.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)
	for _, output := range []any{public, injection} {
		encoded, err := json.Marshal(output)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "bstats")
		require.NotContains(t, string(encoded), "private-instance-identifier")
	}
}

// LoadSiteShareImage leaves this text-only fixture without an uploaded cover.
func (metricsInputSource) LoadSiteShareImage(context.Context) (string, error) { return "", nil }
