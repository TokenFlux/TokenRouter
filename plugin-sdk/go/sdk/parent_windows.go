package sdk

import "golang.org/x/sys/windows"

// parent_windows.go binds orphan detection to a retained Windows process object.
// Windows parent identifiers do not change when the parent exits, so comparing
// Getppid values would leave an orphan alive. A synchronization handle identifies
// the original process and becomes signaled when that process terminates, even
// if the numeric PID is later assigned to another process.
//
// Observation requests only SYNCHRONIZE access and performs nonblocking waits.
// Failure to open the parent or inspect its state prevents a claim that the
// original host remains alive. The common watchdog owns cancellation and releases
// the handle when normal observation ends; no parent process is modified.

// windowsParentMonitor retains the original parent process object for exit observation.
type windowsParentMonitor struct {
	// handle remains owned until close; keeping it binds every observation to the
	// original process object even after Windows reuses the numeric parent identifier.
	handle windows.Handle
}

// newParentMonitor opens the parent for read-only synchronization until the watchdog closes it.
// Callers release the returned monitor on normal cancellation. A missing or
// inaccessible parent fails setup rather than substituting PID-only observation.
func newParentMonitor(parent int) (parentMonitor, error) {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(parent))
	if err != nil {
		return nil, err
	}
	return windowsParentMonitor{handle: handle}, nil
}

// alive checks the original parent's exit state without blocking the watchdog loop.
// Only a successful timeout means the process has not exited. Signaled objects
// and observation errors both end the ownership claim used by the common watchdog.
func (m windowsParentMonitor) alive() bool {
	state, err := windows.WaitForSingleObject(m.handle, 0)
	return err == nil && state == uint32(windows.WAIT_TIMEOUT)
}

// close releases the synchronization handle without signaling or terminating the parent.
func (m windowsParentMonitor) close() {
	// Observation has already ended and no operation can reuse this handle;
	// closing failure cannot restore parent ownership or require another polling cycle.
	_ = windows.CloseHandle(m.handle)
}
