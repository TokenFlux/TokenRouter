package provider

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestLinuxParentDeathTerminatesPlugin kills an isolated test host with SIGKILL.
// Its initialized child must stop running within three seconds without a normal
// shutdown RPC or cooperation from the terminated host's cleanup code.
func TestLinuxParentDeathTerminatesPlugin(t *testing.T) {
	host, pid := startParentDeathHost(t)
	if err := host.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = host.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		// Container init processes may delay reaping an already killed orphan; a zombie has exited and cannot retain executable plugin work.
		if state, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil && strings.Contains(string(state), ") Z ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatal("plugin survived host SIGKILL")
}
