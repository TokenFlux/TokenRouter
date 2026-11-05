package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
)

// Routing debug settings support TOKENROUTER and compatibility environment
// names, with any nonempty current value taking precedence. The accompanying
// compatibility snapshot scenario checks that request-local thinking state does
// not populate the snapshot's metadata count when shared counters are absent.

// TestSnapshotOpenAICompatibilityFallbackMetrics reads empty compatibility
// snapshots around request-local thinking access. Both metadata totals must
// remain zero, protecting the absence of metadata accounting in the snapshot
// helper.
func TestSnapshotOpenAICompatibilityFallbackMetrics(t *testing.T) {
	before := gatewayCompatibilitySnapshot(nil)
	ctx := requeststate.WithThinkingEnabled(context.Background(), true)
	_, _ = requeststate.ThinkingEnabledFromContext(ctx)

	after := gatewayCompatibilitySnapshot(nil)
	// Request-local thinking state does not add entries to the compatibility metadata count.
	require.Zero(t, after.MetadataTotal)
	require.Zero(t, before.MetadataTotal)
}

// TestRoutingDebugConfiguration supplies the process routing diagnostic switch.
// A missing configuration disables debugging and an explicit value controls it
// independently of any process environment left by an older deployment.
func TestRoutingDebugConfiguration(t *testing.T) {
	require.False(t, selectionOptions(nil).DebugRouting)
	cfg := &config.Config{Debug: config.DebugConfig{ModelRoutingLog: true}}
	require.True(t, selectionOptions(cfg).DebugRouting)
	cfg.Debug.ModelRoutingLog = false
	require.False(t, selectionOptions(cfg).DebugRouting)
}
