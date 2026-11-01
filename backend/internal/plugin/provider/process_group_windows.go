package provider

import "os/exec"

// process_group_windows.go preserves Windows direct-child process ownership.
// Unix group attributes and group signals are unavailable on this platform;
// command cancellation still kills the direct plugin, and the SDK monitors
// the original host through its retained Windows process handle.

// configureProcessGroup leaves Windows command attributes unchanged for direct-child ownership.
func configureProcessGroup(*exec.Cmd) {}

// killProcessGroup performs no Unix group operation on Windows hosts.
func killProcessGroup(int) error { return nil }
