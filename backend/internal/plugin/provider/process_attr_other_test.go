//go:build !linux

package provider

import (
	"os"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// process_attr_other_test.go checks orphan cleanup without Linux parent-death signals.
// An isolated host initializes a real SDK plugin before abrupt termination.
// The SDK watchdog must end the remaining child independently of graceful host
// cleanup, using the platform's native parent-lifetime observation.

// TestParentWatchdogTerminatesOrphan kills an initialized test host without running any shutdown callbacks.
// Its child must exit within the two-second polling interval plus scheduling allowance, including platforms where parent identifiers survive process exit.
func TestParentWatchdogTerminatesOrphan(t *testing.T) {
	host, pid := startParentDeathHost(t)
	if err := host.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = host.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		exists, err := process.PidExistsWithContext(t.Context(), int32(pid))
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Cleanup owns only the child PID published by this isolated fixture, preventing
	// an assertion failure from leaving intentionally orphaned plugin work behind.
	child, err := os.FindProcess(pid)
	if err == nil {
		_ = child.Kill()
		_ = child.Release()
	}
	t.Fatal("plugin survived parent termination")
}
