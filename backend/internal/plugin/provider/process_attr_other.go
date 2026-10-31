//go:build !linux

package provider

import "os/exec"

// process_attr_other.go keeps command setup portable outside Linux.
// The SDK parent watchdog supplies orphan detection on these platforms,
// while go-plugin owns termination of the direct child. Linux-only process-group
// and parent-death signal attributes are deliberately absent from this build.

// configureProcess leaves portable command attributes unchanged on non-Linux systems.
func configureProcess(*exec.Cmd) {}

// killProcessGroup reports no group operation where Linux process groups are unavailable.
func killProcessGroup(int) error { return nil }
