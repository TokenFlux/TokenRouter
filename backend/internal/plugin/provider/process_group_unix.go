//go:build !windows

package provider

import (
	"os/exec"
	"syscall"
)

// process_group_unix.go owns plugin descendant cleanup on Unix platforms.
// Each command starts its own group so cancellation can terminate descendants
// that retain inherited output pipes before go-plugin waits for those readers.

// configureProcessGroup assigns the child a process group distinct from the host.
func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup terminates descendants before transport waits for inherited pipes.
// Missing groups are harmless. Other errors remain available for host diagnostics
// while the cancellation path still attempts to kill the direct child.
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
