//go:build !windows

package sdk

import "os"

// parent_other.go observes Unix orphaning through kernel parent reassignment.
// A changed parent PID ends the plugin's original ownership relationship, including
// reparenting to an init process. This implementation needs no retained operating
// system handle and shares the SDK's ordinary polling and cancellation policy.

// unixParentMonitor identifies the parent that owned this plugin when serving began.
type unixParentMonitor struct {
	pid int // original startup parent identifier
}

// newParentMonitor captures Unix ownership without opening a resource requiring cleanup.
func newParentMonitor(parent int) (parentMonitor, error) {
	return unixParentMonitor{pid: parent}, nil
}

// alive compares kernel ownership with the captured original parent process identifier.
func (m unixParentMonitor) alive() bool { return os.Getppid() == m.pid }

// close completes observation without work because Unix monitoring holds no descriptor.
func (unixParentMonitor) close() {}
