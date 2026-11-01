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
	configureProcessGroup(command)
	command.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
