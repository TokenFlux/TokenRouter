//go:build linux

package provider

import (
	"os/exec"
	"syscall"
)

// process_attr_linux.go binds native plugin lifetime to the Linux host process.
// Each plugin starts a separate process group and receives SIGKILL when its
// parent exits. Group cleanup also terminates descendants created inside that
// group when normal plugin shutdown has finished or exhausted its budget.

// configureProcess sets parent-death and process-group attributes before child startup.
func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

// killProcessGroup terminates the plugin group after graceful shutdown attempts.
// Missing processes are harmless. Other errors are returned for host diagnostics
// without changing the already selected shutdown or recovery outcome.
func killProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}
