package plugin

import (
	"context"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// runtime.go defines short-lived access to a running plugin generation.
// Callers acquire a lease before invoking declared services, then release it
// after the call completes. The process owner counts those leases to reject
// new work during draining and wait for admitted work before terminating the
// generation. Consumers never manage process handles or transport startup.
//
// A lease retains its generation's connection and version even if that process
// exits and a replacement starts. Release uses its captured callback once, so
// cleanup paths cannot decrement a newer generation's in-flight count or release
// the same admission twice. Process implementations own capacity, state, and
// recovery decisions behind the Runtime interface.

const (
	// Lifecycle states distinguish admission, recovery waits, operator intervention, and installations that do not require a native process.
	StateStopped   State = "stopped"   // no process is desired
	StateStarting  State = "starting"  // handshake or initialization pending
	StateRunning   State = "running"   // process accepts new leases
	StateBackoff   State = "backoff"   // automatic restart delay active
	StateDraining  State = "draining"  // existing leases are finishing
	StateCrashLoop State = "crashloop" // restart threshold requires intervention
	StateFailed    State = "failed"    // installation cannot start locally
	StateStatic    State = "static"    // installation requires no process
)

// Runtime grants bounded access to already running plugin process generations.
// @project-doc docs/architecture/plugin_system.md#leases_and_callbacks
type Runtime interface {
	// Acquire returns a lease immediately or an unavailable, restarting, or busy error.
	// It never waits for startup or capacity. Callers release successful leases
	// after completing all work that uses the retained connection.
	Acquire(context.Context, string) (*Lease, error)
}

// TaskRunner invokes a declared task without exposing transport-specific call options.
type TaskRunner interface {
	// RunTask invokes one named occurrence and waits for its host-side completion.
	// The caller owns its process lease and deadline. Cancellation ends transport
	// waiting, while SDK callback tracking protects actual author execution separately.
	RunTask(context.Context, string, time.Time) error
}

// Conn contains the typed service clients available on one plugin connection.
type Conn struct {
	Plugin pluginpb.PluginClient // required lifecycle service client
	HTTP   HTTPTransport         // optional HTTP contribution client
	Tasks  TaskRunner            // optional scheduled task client
}

// Lease retains one process generation until its caller releases admission.
type Lease struct {
	Conn    *Conn  // retained generation service clients
	Version string // retained plugin release version
	// once and release bind cleanup to the acquired generation; copied leases must not be used because sync.Once is not copyable.
	once    sync.Once // one-time release synchronization
	release func()    // generation-owned admission release callback
}

// State names the process lifecycle states exposed to installation management.
type State string

// Status reports a copied observation of one installation's process lifecycle.
type Status struct {
	State      State     `json:"state"`                // current process lifecycle state
	PID        int       `json:"pid"`                  // running operating-system process identifier
	RSSBytes   uint64    `json:"rss_bytes"`            // last resident memory sample
	CPUPercent float64   `json:"cpu_percent"`          // last process CPU percentage
	Restarts   int       `json:"restarts"`             // abnormal exits since operator reset
	LastError  string    `json:"last_error,omitempty"` // most recent lifecycle failure
	Since      time.Time `json:"since"`                // current state's start time
}

// LogLine contains one bounded diagnostic record exposed by plugin administration.
type LogLine struct {
	Time    time.Time `json:"time"`    // host observation timestamp
	Level   string    `json:"level"`   // normalized log severity
	Message string    `json:"message"` // bounded diagnostic message text
}

// LogReader returns copied diagnostic history without exposing process ownership details.
type LogReader interface {
	// Logs returns at most five hundred recent lines in publication order.
	Logs(string) []LogLine
}

// ProcessSpec supplies immutable inputs for starting one plugin generation.
type ProcessSpec struct {
	PluginID        string   // publisher-qualified installation identifier
	Version         string   // desired plugin release version
	PreviousVersion string   // previously running release version
	Executable      string   // absolute verified executable path
	BinarySHA256    string   // expected executable SHA-256 digest
	StateDir        string   // absolute disposable process state directory
	ConfigJSON      []byte   // normalized decrypted configuration object
	Permissions     []string // administrator-granted capability names
	MemoryMiB       int      // advisory process memory limit
	// VerifyBinary is false only for trusted development directories; installed release executables require a matching digest before process launch.
	VerifyBinary bool
}

// Supervisors owns process generations while the manager owns desired installation state.
type Supervisors interface {
	Runtime
	// Replace stops the old process before starting the supplied generation.
	// The process lifetime is independent of this management request's context.
	// Callers serialize changes for one plugin and observe readiness through Status.
	Replace(context.Context, ProcessSpec) error
	// Stop rejects new leases and drains the current generation within its budget.
	// Cancellation forces termination rather than leaving the child running. Missing
	// plugin IDs succeed so installation removal can be retried safely.
	Stop(context.Context, string) error
	// ApplyConfig reconciles desired values against the generation's applied configuration.
	// Identical successful values do nothing; failed notifications remain retryable.
	// Request capacity cannot suppress delivery. Changed values replace incomplete
	// startups or implementations without a watcher. Callers serialize this operation
	// with version replacement and removal, and persisted intent survives callback failure.
	ApplyConfig(context.Context, ProcessSpec) error
	// Status returns a detached process observation for administration and recovery decisions.
	Status(string) Status
	// StopAll drains all owned processes concurrently within the supplied shutdown budget.
	// No new replacements are admitted afterward. The caller retains ownership of
	// shared databases and other host services until this operation has finished.
	StopAll(context.Context) error
}

// NewLease binds service clients and release ownership to one admitted generation.
// The caller supplies a nonnil callback and returns the pointer without copying
// its synchronization state. Release may safely run from multiple cleanup paths.
func NewLease(conn *Conn, version string, release func()) *Lease {
	return &Lease{Conn: conn, Version: version, release: release}
}

// Release returns admission to the retained generation at most once.
// Nil leases are harmless. Callers must finish using Conn before release;
// the owner may terminate the process as soon as its final lease returns.
func (l *Lease) Release() {
	if l != nil {
		l.once.Do(l.release)
	}
}

// RunTask invokes the declared task service with the selected scheduled timestamp.
// A missing task client is unavailable. The caller retains its generation lease
// until this RPC returns, including cancellation or deadline completion.
func (c *Conn) RunTask(ctx context.Context, name string, scheduled time.Time) error {
	if c == nil || c.Tasks == nil {
		return ErrPluginUnavailable
	}
	err := c.Tasks.RunTask(ctx, name, scheduled)
	// Preserve the caller's cancellation identity when transport wraps it, allowing scheduler results to classify deadlines without importing gRPC status types.
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
