package provider

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

// process_runner.go owns command execution and the host ends of plugin output pipes.
// go-plugin waits for stdout and stderr readers before calling its runner's Wait
// method. A descendant can retain either pipe after the direct plugin exits, so
// waiting for command completion alone cannot establish that transport cleanup
// will finish. This adapter keeps those read handles reachable by cancellation.
//
// Start closes the host's copies of child write handles after descriptor transfer.
// Cancellation terminates the command and its supported process group, then closes
// both host readers even when descendant termination is unavailable. Wait reaps
// the direct child once, while repeated cleanup shares idempotent output closure.
// Handshake metadata, certificates, and broker connections remain go-plugin's responsibility.

// processRunner gives go-plugin bounded output cleanup around one owned native command.
type processRunner struct {
	command *exec.Cmd // context-owned native process command
	stdout  *os.File  // host stdout pipe reader
	stderr  *os.File  // host stderr pipe reader
	// stdoutWriter and stderrWriter remain owned until Start transfers descriptors;
	// Start closes both host copies on success or failure so they cannot prevent EOF.
	stdoutWriter *os.File  // child stdout pipe writer
	stderrWriter *os.File  // child stderr pipe writer
	outputOnce   sync.Once // idempotent host output closure
	// waitOnce publishes waitErr after the command is reaped; repeated Wait callers
	// share that result without asking the operating system to reap the child twice.
	waitOnce sync.Once // direct-child wait ownership
	waitErr  error     // completed command wait result
}

// newProcessRunner acquires output pipes for an unstarted context-owned command.
// Callers transfer command ownership and invoke Start once. Pipe allocation
// failure closes resources already acquired, and cancellation owns later output closure.
func newProcessRunner(command *exec.Cmd) (*processRunner, error) {
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		// The second allocation failed before ownership could transfer; close both
		// halves of the first pipe so failed startup cannot accumulate file descriptors.
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		return nil, err
	}
	r := &processRunner{command: command, stdout: stdout, stderr: stderr, stdoutWriter: stdoutWriter, stderrWriter: stderrWriter}
	command.Stdout = stdoutWriter
	command.Stderr = stderrWriter
	command.Cancel = func() error { return r.Kill(context.Background()) }
	return r, nil
}

// Start launches the command and transfers child output descriptors once.
// A canceled startup or failed exec closes every owned pipe. Successful startup
// leaves only the host readers open, allowing natural EOF after child exit.
func (r *processRunner) Start(ctx context.Context) (resultErr error) {
	// The child inherits duplicates during Start; retaining host writer copies
	// afterward would prevent EOF even when the plugin and every descendant exit.
	defer func() {
		_ = r.stdoutWriter.Close()
		_ = r.stderrWriter.Close()
		if resultErr != nil {
			r.closeOutput()
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.command.Start()
}

// Kill terminates the direct child and closes host readers after signaling.
// Unix also terminates descendants remaining in the command's process group.
// Closing readers is required on every platform, including when a descendant
// outlives the direct child or has moved outside its original process group.
//
// go-plugin cannot reach Wait until those readers finish, so output closure
// belongs to cancellation itself. Repeated kills and command-context cancellation
// share the same idempotent closure, while process termination errors remain
// visible to whichever owner requested cancellation.
func (r *processRunner) Kill(context.Context) error {
	defer r.closeOutput()
	if r.command.Process == nil {
		return nil
	}
	return terminateProcess(r.command.Process)
}

// terminateProcess cancels a command and descendants remaining in its Unix group.
// Callers invoke it only after Start publishes a valid process handle. Group
// termination runs first because the direct child can exit while its descendants
// retain stdout or stderr, keeping transport readers open without explicit closure.
//
// Windows keeps direct-child termination without Unix signals. Both termination
// attempts run even when one fails, and their errors remain joined for the
// command's cancellation result rather than hiding a failed direct-child kill.
func terminateProcess(process *os.Process) error {
	groupErr := killProcessGroup(process.Pid)
	childErr := process.Kill()
	return errors.Join(groupErr, childErr)
}

// closeOutput releases host readers once after exit or forced cancellation.
// Concurrent closure interrupts blocked reads. Close errors cannot restore
// stream ownership, and no cleanup path attempts to reuse those handles afterward.
func (r *processRunner) closeOutput() {
	// Cancellation and completed waiting may race; one owner closes both readers
	// while all later cleanup calls observe that their ownership has already ended.
	r.outputOnce.Do(func() {
		_ = r.stdout.Close()
		_ = r.stderr.Close()
	})
}

// Wait reaps the direct command once and returns its shared completion result.
// go-plugin calls it after output readers finish. Cancellation can close those
// readers first, so Wait still waits for the killed direct child to actually exit.
func (r *processRunner) Wait(context.Context) error {
	r.waitOnce.Do(func() {
		r.waitErr = r.command.Wait()
		r.closeOutput()
	})
	return r.waitErr
}

// Stdout returns the owned host reader used for protocol negotiation.
func (r *processRunner) Stdout() io.ReadCloser { return r.stdout }

// Stderr returns the owned host reader used for plugin diagnostics.
func (r *processRunner) Stderr() io.ReadCloser { return r.stderr }

// Name returns the executable path used in go-plugin diagnostics.
func (r *processRunner) Name() string { return r.command.Path }

// ID returns the started process identifier, or empty before successful exec.
func (r *processRunner) ID() string {
	if r.command.Process == nil {
		return ""
	}
	return strconv.Itoa(r.command.Process.Pid)
}

// Diagnose leaves startup failure details with the original command error.
func (*processRunner) Diagnose(context.Context) string { return "" }

// PluginToHost preserves transport addresses for execution in the host namespace.
func (*processRunner) PluginToHost(network, address string) (string, string, error) {
	return network, address, nil
}

// HostToPlugin preserves broker addresses for execution in the host namespace.
func (*processRunner) HostToPlugin(network, address string) (string, string, error) {
	return network, address, nil
}
