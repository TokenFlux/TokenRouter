//go:build !linux

package provider

import "os/exec"

// process_attr_other.go keeps command setup portable outside Linux.
// The SDK parent watchdog supplies orphan detection on these platforms,
// while cancellation terminates the Unix process group before transport cleanup.
// Windows retains direct-child termination, and Linux parent-death signals remain
// confined to the Linux-specific setup implementation.

// configureProcess applies available group ownership without Linux parent-death signals.
func configureProcess(command *exec.Cmd) { configureProcessGroup(command) }
