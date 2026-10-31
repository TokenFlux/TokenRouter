package plugin

import (
	"slices"
	"testing"
	"time"
)

// TestRuntimeDefaultsPreserveRecoveryContract checks the production restart
// sequence, crash window, and admission limit together. Explicit test backoffs
// must be copied so later caller mutation cannot alter a running supervisor.
func TestRuntimeDefaultsPreserveRecoveryContract(t *testing.T) {
	options := (RuntimeOptions{}).WithDefaults()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second}
	if !slices.Equal(options.Backoffs, want) || options.MaxCrashes != 5 || options.CrashWindow != 10*time.Minute || options.MaxLeases != 32 {
		t.Fatalf("changed runtime policy: %+v", options)
	}
	input := []time.Duration{time.Millisecond}
	custom := (RuntimeOptions{Backoffs: input}).WithDefaults()
	input[0] = time.Hour
	if custom.Backoffs[0] != time.Millisecond {
		t.Fatal("runtime retained caller-owned backoff storage")
	}
}
