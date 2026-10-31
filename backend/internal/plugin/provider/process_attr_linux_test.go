package provider

import (
	"bufio"
	"os"
	"os/exec"
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
	host := exec.Command(fixtureBinaries["hostdeath"], fixtureBinaries["crashy"], t.TempDir())
	stdout, err := host.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Process.Kill(); _ = host.Wait() })
	pidResult := make(chan int, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil {
			pidResult <- 0
			return
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(line))
		pidResult <- pid
	}()
	var pid int
	select {
	case pid = <-pidResult:
	case <-time.After(12 * time.Second):
		t.Fatal("test host did not publish child PID")
	}
	if pid <= 0 {
		t.Fatal("test host failed before child initialization")
	}
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
