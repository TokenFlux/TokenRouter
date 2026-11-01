package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"github.com/hashicorp/go-plugin/runner"
	"github.com/shirou/gopsutil/v4/process"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// process_supervisor.go owns plugin process generations and bounded recovery.
// Each supervisor admits leases only while its current generation is running.
// A graceful stop closes admission before waiting for that generation's leases,
// then invokes shutdown and terminates the child. Unexpected exits discard the
// failed connection and restart with bounded backoff until the rolling crash
// threshold requires an administrator to replace the supervisor.
//
// Process cancellation is separate from loop cancellation. Stopping a running
// supervisor first cancels its observation loop, preserving the child while
// admitted requests drain; cancellation during startup terminates the incomplete
// generation immediately. Every lease captures its own generation counter, so
// a late release after a crash cannot affect the replacement's capacity. The
// runtime collection prevents new replacements once application shutdown starts.
// Configuration notifications retain a drain lease independently of request
// capacity, and each generation remembers only successfully applied values.
// Cancellation terminates Unix process groups before transport cleanup waits
// for inherited output pipes, including cancellation during the initial handshake.
// An owned command runner closes host pipe readers on every platform, so an
// independently surviving descendant cannot keep the supervisor's cleanup blocked.

// Processes implements the core process collection without owning installation persistence.
type Processes struct {
	options plugin.RuntimeOptions // copied process policy inputs
	logger  *slog.Logger          // host diagnostic logger
	factory HostFactory           // installation-bound host callback constructor
	// mu protects collection membership and terminal shutdown; per-plugin lifecycle operations are serialized by the calling installation manager.
	mu      sync.RWMutex
	entries map[string]*supervisor // process owners keyed by plugin
	stopped bool                   // terminal collection shutdown flag
}

// supervisor owns one installation's restart loop and current process generation.
type supervisor struct {
	options plugin.RuntimeOptions // immutable process policy inputs
	logger  *slog.Logger          // host diagnostic logger
	factory HostFactory           // host callback service constructor
	gate    *hostGate             // shared callback admission checks
	ctx     context.Context       // observation loop cancellation context
	cancel  context.CancelFunc    // observation loop stop signal
	done    chan struct{}         // completed supervisor cleanup signal
	// mu protects spec, observation, current, and stopping; lease counts inside a generation use this same mutex until that generation is released.
	mu            sync.Mutex
	spec          plugin.ProcessSpec // desired generation startup inputs
	observation   plugin.Status      // current copied process observation
	current       *generation        // currently owned process generation
	stopping      bool               // new lease admission closed
	stopContext   context.Context    // first graceful stop caller budget
	stopErr       error              // first stop's drain failure
	logs          *processLogs       // retained generation diagnostic buffer
	processLogger hclog.Logger       // generation-bound go-plugin log adapter
}

// generation retains process resources independently of newer supervisor generations.
type generation struct {
	client    *goplugin.Client   // owned HashiCorp process client
	cancel    context.CancelFunc // child and Unix group termination signal
	conn      *connection        // initialized service and broker connection
	callbacks *hostCalls         // admitted host callback completion barrier
	outputs   []*processOutput   // generation-owned standard stream assemblers
	pid       int                // started child process identifier
	stats     *process.Process   // generation-owned resource sampling baseline
	socketDir string             // owned broker socket metadata directory
	leases    int                // retained generation lease count
	graceful  bool               // completed graceful shutdown callback
	// configJSON records successful initialization or notification under the supervisor lock; failed notifications remain eligible for an identical later retry.
	configJSON []byte
	// drained closes when the generation reaches zero leases; the supervisor lock serializes opening and closing against admission and release.
	drained chan struct{}
}

// NewProcesses constructs a stopped-work collection without launching any subprocess.
// A nil logger selects slog.Default. The installation manager must serialize
// same-plugin mutations, and application lifecycle owns the final StopAll call.
func NewProcesses(options plugin.RuntimeOptions, logger *slog.Logger, factory HostFactory) *Processes {
	if logger == nil {
		logger = slog.Default()
	}
	return &Processes{options: options.WithDefaults(), logger: logger, factory: factory, entries: make(map[string]*supervisor)}
}

// Replace stops an existing owner before publishing a new asynchronous supervisor.
// Its process lifetime does not inherit the management request context. Once
// StopAll begins, replacement fails without launching an unowned child.
//
// The collection lock is released while the old owner drains, allowing shutdown
// to proceed independently. A second shutdown check before publication closes
// that race. The caller serializes same-plugin replacements, while this method
// coordinates replacement against the collection's terminal stop state and
// transfers ownership of the new loop before it can start acquiring resources.
func (p *Processes) Replace(ctx context.Context, spec plugin.ProcessSpec) error {
	p.mu.RLock()
	old, stopped := p.entries[spec.PluginID], p.stopped
	p.mu.RUnlock()
	if stopped {
		return plugin.ErrPluginUnavailable
	}
	if old != nil {
		if err := old.stop(ctx); err != nil {
			return err
		}
	}
	var retainedLogs *processLogs
	if old != nil {
		retainedLogs = old.logs
	}
	s := newSupervisor(spec, p.options, p.logger, p.factory, retainedLogs)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		s.cancel()
		return plugin.ErrPluginUnavailable
	}
	p.entries[spec.PluginID] = s
	go s.run()
	return nil
}

// newSupervisor copies startup data and creates an owner before its loop starts.
// A retained log buffer comes from an already stopped owner. Rebinding keeps
// previous diagnostics while new records receive this generation's version metadata.
func newSupervisor(spec plugin.ProcessSpec, options plugin.RuntimeOptions, logger *slog.Logger, factory HostFactory, retainedLogs *processLogs) *supervisor {
	spec.ConfigJSON = append([]byte(nil), spec.ConfigJSON...)
	spec.Permissions = append([]string(nil), spec.Permissions...)
	ctx, cancel := context.WithCancel(context.Background())
	logs, processLogger := newProcessLogs(logger, spec.PluginID, spec.Version, options.Now)
	if retainedLogs != nil {
		logs = retainedLogs
		processLogger = logs.bind(logger, spec.PluginID, spec.Version)
	}
	return &supervisor{spec: spec, options: options, logger: logger, factory: factory, gate: newHostGate(spec.Permissions), ctx: ctx, cancel: cancel, done: make(chan struct{}), logs: logs, processLogger: processLogger, observation: plugin.Status{State: plugin.StateStarting, Since: options.Now()}}
}

// Acquire admits a call through the selected supervisor without waiting for capacity.
func (p *Processes) Acquire(ctx context.Context, id string) (*plugin.Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	s, stopped := p.entries[id], p.stopped
	p.mu.RUnlock()
	if stopped || s == nil {
		return nil, plugin.ErrPluginUnavailable
	}
	return s.acquire()
}

// acquire atomically checks admission and retains the current running generation.
// Restarting and draining states fail immediately, and a full generation returns
// busy. Successful leases retain the original version and connection until release.
//
// The zero-to-one transition opens a new drain channel under the same mutex
// used by stop admission. Each cleanup closure captures this generation rather
// than consulting the current pointer, preserving capacity when a process
// crashes and restarts before an older RPC has returned to its caller.
func (s *supervisor) acquire() (*plugin.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping || s.observation.State == plugin.StateDraining {
		return nil, plugin.ErrPluginRestarting
	}
	if s.observation.State != plugin.StateRunning || s.current == nil || s.current.conn == nil {
		return nil, plugin.ErrPluginUnavailable
	}
	g := s.current
	if g.leases >= s.options.MaxLeases {
		return nil, plugin.ErrPluginBusy
	}
	return s.retainLocked(g), nil
}

// retainLocked retains a generation until an admitted operation releases it.
// The caller holds the supervisor mutex and has checked its own admission rule.
// Configuration notifications use this same drain ownership without competing
// for the bounded request pool, so load cannot silently discard an update.
//
// The zero-to-one transition and final release share the supervisor mutex with
// shutdown. Release captures this generation rather than consulting current,
// preventing an old operation from changing a replacement's capacity or drain signal.
func (s *supervisor) retainLocked(g *generation) *plugin.Lease {
	if g.leases == 0 {
		g.drained = make(chan struct{})
	}
	g.leases++
	return plugin.NewLease(g.conn.services, s.spec.Version, func() {
		// Return admission to the captured generation even after replacement; only its own final lease may close its drain signal.
		s.mu.Lock()
		g.leases--
		if g.leases == 0 {
			close(g.drained)
		}
		s.mu.Unlock()
	})
}

// Stop drains an existing plugin owner or succeeds when none exists.
func (p *Processes) Stop(ctx context.Context, id string) error {
	p.mu.RLock()
	s := p.entries[id]
	p.mu.RUnlock()
	if s == nil {
		return nil
	}
	return s.stop(ctx)
}

// stop closes admission and waits for the loop's resource cleanup.
// Repeated callers share cleanup completion. A caller whose budget expires
// cancels the child and its Unix group, then returns its context error.
//
// The first caller supplies the graceful drain budget. Loop cancellation is
// separate from child cancellation, preserving admitted calls during normal
// shutdown. A later caller with a shorter deadline can still force termination,
// so application shutdown cannot be held beyond its own budget by an earlier
// administrative stop request that allowed a longer drain period.
func (s *supervisor) stop(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.options.DrainTimeout+s.options.ShutdownTimeout)
	defer cancel()
	s.mu.Lock()
	first := !s.stopping
	if first {
		s.stopping = true
		s.stopContext = ctx
		s.observation.State = plugin.StateDraining
		s.observation.Since = s.options.Now()
		s.cancel()
	}
	s.mu.Unlock()
	select {
	case <-s.done:
		if first {
			s.mu.Lock()
			err := s.stopErr
			s.mu.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		g := s.current
		s.mu.Unlock()
		if g != nil {
			g.cancel()
		}
		return ctx.Err()
	}
}

// Status returns an installation's latest detached process observation.
func (p *Processes) Status(id string) plugin.Status {
	p.mu.RLock()
	s := p.entries[id]
	p.mu.RUnlock()
	if s == nil {
		return plugin.Status{State: plugin.StateStopped}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observation
}

// Logs returns a detached chronological snapshot of the retained diagnostic buffer.
func (p *Processes) Logs(id string) []plugin.LogLine {
	p.mu.RLock()
	s := p.entries[id]
	p.mu.RUnlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	logs := s.logs
	s.mu.Unlock()
	if logs == nil {
		return nil
	}
	return logs.snapshot()
}

// ApplyConfig reconciles desired values with the current generation's applied configuration.
// Callers serialize same-plugin mutations. Running generations retain a drain
// lease independently of request capacity; unchanged successfully applied values
// need no callback. Failed callbacks remain eligible for identical later retries.
//
// Starting or unavailable generations are replaced with the new startup inputs,
// preventing an Init snapshot from becoming ready with older values. Unimplemented
// watchers also request replacement. Other callback failures retain the saved
// desired configuration and log their diagnostics without claiming it was applied.
// Unchanged desired values preserve an unavailable owner's existing recovery policy.
func (p *Processes) ApplyConfig(ctx context.Context, spec plugin.ProcessSpec) error {
	p.mu.RLock()
	s := p.entries[spec.PluginID]
	p.mu.RUnlock()
	if s == nil {
		return p.Replace(ctx, spec)
	}
	s.mu.Lock()
	changed := !bytes.Equal(s.spec.ConfigJSON, spec.ConfigJSON)
	s.spec.ConfigJSON = append([]byte(nil), spec.ConfigJSON...)
	g := s.current
	if s.stopping || s.observation.State != plugin.StateRunning || g == nil || g.conn == nil {
		s.mu.Unlock()
		if !changed {
			// Ordinary reconciliation must preserve backoff and crash-loop intervention;
			// the next permitted startup already owns these unchanged desired values.
			return nil
		}
		// Replacing an incomplete generation prevents its captured Init payload from
		// winning over configuration that was persisted while startup was in progress.
		return p.Replace(ctx, spec)
	}
	if bytes.Equal(g.configJSON, spec.ConfigJSON) {
		s.mu.Unlock()
		return nil
	}
	data := s.spec.ConfigJSON
	lease := s.retainLocked(g)
	s.mu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, p.options.InitTimeout)
	_, err := lease.Conn.Plugin.ConfigChanged(callCtx, &pluginpb.ConfigChangedRequest{ConfigJson: data})
	cancel()
	if err == nil {
		// Record delivery on the retained generation only; a concurrent crash cannot
		// mark its replacement as having received a notification sent to this connection.
		s.mu.Lock()
		g.configJSON = data
		s.mu.Unlock()
	}
	lease.Release()
	if status.Code(err) == codes.Unimplemented {
		return p.Replace(ctx, spec)
	}
	if err != nil {
		p.logger.Warn("Plugin configuration callback failed", "plugin_id", spec.PluginID, "error", err)
	}
	return nil
}

// StopAll closes collection admission and drains every owner concurrently.
// Repeated calls reuse each owner's cleanup state. Errors report incomplete
// shutdown while command cancellation still runs for expired caller budgets.
func (p *Processes) StopAll(ctx context.Context) error {
	p.mu.Lock()
	p.stopped = true
	owners := make([]*supervisor, 0, len(p.entries))
	for _, s := range p.entries {
		owners = append(owners, s)
	}
	p.mu.Unlock()
	results := make(chan error, len(owners))
	for _, s := range owners {
		go func() { results <- s.stop(ctx) }()
	}
	var result error
	for range owners {
		result = errors.Join(result, <-results)
	}
	return result
}

// run starts, observes, and recovers process generations until stopped.
// Abnormal failures enter bounded backoff and count against a rolling window.
// The configured threshold closes automatic recovery until an operator replaces
// this supervisor, while its existing status remains available for diagnosis.
//
// A generation is never replaced before its process resources are terminated.
// Normal stop follows the separate drain path, preserving accepted leases.
// Startup cancellation kills its incomplete child before returning, and final
// cleanup closes done only after the loop has relinquished process ownership.
func (s *supervisor) run() {
	defer close(s.done)
	defer s.setState(plugin.StateStopped, "")
	var crashes []time.Time
	attempt := 0
	for s.ctx.Err() == nil {
		s.setState(plugin.StateStarting, "")
		g, err := s.start()
		if err == nil {
			s.mu.Lock()
			stopping := s.stopping
			if !stopping {
				s.observation.State = plugin.StateRunning
				s.observation.Since = s.options.Now()
				s.observation.PID = g.pid
			}
			s.mu.Unlock()
			if stopping {
				s.shutdown(g)
				return
			}
			err = s.observe(g)
			if s.ctx.Err() != nil {
				s.shutdown(g)
				return
			}
			s.setState(plugin.StateBackoff, err.Error())
			s.kill(g)
		}
		if s.ctx.Err() != nil {
			return
		}
		now := s.options.Now()
		kept := crashes[:0]
		for _, crash := range crashes {
			if now.Sub(crash) < s.options.CrashWindow {
				kept = append(kept, crash)
			}
		}
		crashes = append(kept, now)
		s.mu.Lock()
		s.observation.Restarts++
		s.mu.Unlock()
		if len(crashes) >= s.options.MaxCrashes {
			s.setState(plugin.StateCrashLoop, err.Error())
			s.logger.Error("Plugin entered crash loop", "plugin_id", s.spec.PluginID, "error", err)
			<-s.ctx.Done()
			return
		}
		s.setState(plugin.StateBackoff, err.Error())
		delay := s.options.Backoffs[min(attempt, len(s.options.Backoffs)-1)]
		attempt++
		// Cancellation interrupts backoff before another generation can start, keeping disable and application shutdown responsive during repeated failures.
		timer := time.NewTimer(delay)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// start establishes one verified process and authenticated callback connection.
// The startup observer cancels the child if the loop is stopped before Init
// finishes. Once startup succeeds, normal stop preserves the child for draining.
//
// Process ownership is published before handshake so forced shutdown can reach
// the command's cancellation function even while go-plugin holds its startup
// lock. Failed startup always kills and clears that generation. The host callback
// server is bound to copied installation permissions before plugin initialization
// receives its broker identifier and decrypted configuration.
func (s *supervisor) start() (_ *generation, resultErr error) {
	s.mu.Lock()
	spec := s.spec
	s.mu.Unlock()
	command, err := processCommand(spec, s.options)
	if err != nil {
		return nil, err
	}
	childCtx, cancelChild := context.WithCancel(context.Background())
	contextCommand := exec.CommandContext(childCtx, spec.Executable)
	contextCommand.Dir, contextCommand.Env, contextCommand.SysProcAttr = command.Dir, command.Env, command.SysProcAttr
	logs, logger := s.logs, s.processLogger
	stdout, stderr := &processOutput{logs: logs}, &processOutput{logs: logs}
	var secure *goplugin.SecureConfig
	if spec.VerifyBinary {
		digest, err := hex.DecodeString(spec.BinarySHA256)
		if err != nil || len(digest) != sha256.Size {
			cancelChild()
			return nil, fmt.Errorf("invalid plugin executable checksum")
		}
		secure = &goplugin.SecureConfig{Checksum: digest, Hash: sha256.New()}
	}
	config := &goplugin.ClientConfig{
		HandshakeConfig: sdk.Handshake(), Plugins: goplugin.PluginSet{sdk.PluginKey: &grpcPlugin{}},
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC}, AutoMTLS: true, SkipHostEnv: true,
		Managed: false, StartTimeout: s.options.StartTimeout, Logger: logger, SyncStdout: stdout, SyncStderr: stderr,
		GRPCDialOptions: []grpc.DialOption{grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(sdk.MaxMessageBytes), grpc.MaxCallSendMsgSize(sdk.MaxMessageBytes))},
		RunnerFunc: func(_ hclog.Logger, metadata *exec.Cmd, _ string) (runner.Runner, error) {
			// RunnerFunc receives only go-plugin's handshake environment, certificate,
			// and socket metadata. Append those to the explicit child allowlist without
			// inheriting the host environment or replacing the context-owned command.
			contextCommand.Env = append(contextCommand.Env, metadata.Env...)
			contextCommand.Stdin = metadata.Stdin
			if secure != nil {
				// The runner owns the real executable path, so preserve SecureConfig's
				// exact file check here before acquiring pipes or executing installed code.
				valid, err := secure.Check(contextCommand.Path)
				if err != nil {
					return nil, fmt.Errorf("verify plugin executable checksum: %w", err)
				}
				if !valid {
					return nil, goplugin.ErrChecksumsDoNotMatch
				}
			}
			return newProcessRunner(contextCommand)
		},
	}
	socketRoot := ""
	if runtime.GOOS != "windows" {
		// Unix socket addresses have a small platform limit; keep transport files short while preserving the plugin's configured HOME and TMPDIR.
		socketRoot = "/tmp"
	}
	// Own the broker's temporary parent on every platform, including runner setup
	// failures before go-plugin has a child whose Kill cleanup can remove metadata.
	socketDir, err := os.MkdirTemp(socketRoot, "trp-")
	if err != nil {
		cancelChild()
		return nil, err
	}
	config.UnixSocketConfig = &goplugin.UnixSocketConfig{TempDir: socketDir}
	g := &generation{client: goplugin.NewClient(config), cancel: cancelChild, drained: make(chan struct{}), socketDir: socketDir, callbacks: newHostCalls(s.logger.With("plugin_id", spec.PluginID, "plugin_version", spec.Version)), outputs: []*processOutput{stdout, stderr}}
	close(g.drained)
	s.mu.Lock()
	s.current = g
	s.mu.Unlock()
	stopStartupWatch := context.AfterFunc(s.ctx, cancelChild)
	defer stopStartupWatch()
	defer func() {
		if resultErr != nil {
			s.kill(g)
		}
	}()
	client, err := g.client.Client()
	if contextCommand.Process != nil {
		g.pid = contextCommand.Process.Pid
	}
	if err != nil {
		return nil, err
	}
	dispensed, err := client.Dispense(sdk.PluginKey)
	if err != nil {
		return nil, err
	}
	conn, ok := dispensed.(*connection)
	if !ok {
		return nil, fmt.Errorf("unexpected plugin connection type")
	}
	g.conn = conn
	brokerID := conn.broker.NextId()
	var implementation pluginpb.HostServer
	if s.factory != nil {
		implementation = s.factory(spec)
	}
	go conn.broker.AcceptAndServe(brokerID, func(options []grpc.ServerOption) *grpc.Server {
		return s.gate.server(options, implementation, g.callbacks)
	})
	ctx, cancel := context.WithTimeout(s.ctx, s.options.InitTimeout)
	defer cancel()
	_, err = conn.services.Plugin.Init(ctx, &pluginpb.InitRequest{HostVersion: s.options.HostVersion, ApiVersion: sdk.ProtocolVersion, PluginId: spec.PluginID, PluginVersion: spec.Version, PreviousVersion: spec.PreviousVersion, InstanceId: s.options.InstanceID, ConfigJson: spec.ConfigJSON, HostBrokerId: brokerID, GrantedPermissions: spec.Permissions, StateDir: spec.StateDir})
	if err != nil {
		return nil, err
	}
	// Retain the exact Init payload as applied state; later identical updates can
	// then distinguish successful initialization from a notification that never completed.
	s.mu.Lock()
	g.configJSON = spec.ConfigJSON
	s.mu.Unlock()
	if observed, err := process.NewProcess(int32(g.pid)); err == nil {
		g.stats = observed
		// Prime the CPU baseline once per generation so later samples describe the observation interval rather than lifetime average consumption.
		_, _ = observed.PercentWithContext(ctx, 0)
	}
	return g, nil
}

// observe checks process exit, readiness, and resource samples until failure or stop.
// Three consecutive readiness failures trigger recovery. Successful checks reset
// that consecutive counter but do not erase the rolling abnormal-exit history.
func (s *supervisor) observe(g *generation) error {
	exitTicker := time.NewTicker(s.options.ExitInterval)
	healthTicker := time.NewTicker(s.options.HealthInterval)
	resourceTicker := time.NewTicker(s.options.ResourceInterval)
	defer exitTicker.Stop()
	defer healthTicker.Stop()
	defer resourceTicker.Stop()
	failures := 0
	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-exitTicker.C:
			s.logs.flushDrops()
			if g.client.Exited() {
				return fmt.Errorf("plugin process exited")
			}
		case <-healthTicker.C:
			ctx, cancel := context.WithTimeout(s.ctx, s.options.HealthTimeout)
			response, err := g.conn.services.Plugin.Health(ctx, &pluginpb.HealthRequest{})
			cancel()
			if err != nil || !response.GetReady() {
				failures++
			} else {
				failures = 0
			}
			if failures >= 3 {
				return fmt.Errorf("plugin failed three consecutive health checks")
			}
		case <-resourceTicker.C:
			s.sample(g)
		}
	}
}

// shutdown drains retained leases before author cleanup and callback admission closure.
// The first stop caller's context limits graceful work. A lease timeout records
// a failure so uninstall can retain its installation rather than purge live data.
//
// Existing plugin requests may still need host callbacks, so callback admission
// remains open through request draining and the author's Shutdown method. It
// closes afterward and waits for admitted host work. Final termination cancels
// remaining callbacks but keeps cleanup ownership until their actual return.
func (s *supervisor) shutdown(g *generation) {
	defer s.kill(g)
	s.mu.Lock()
	ctx := s.stopContext
	drained := g.drained
	s.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	drainCtx, cancel := context.WithTimeout(ctx, s.options.DrainTimeout)
	select {
	case <-drained:
	case <-drainCtx.Done():
		s.mu.Lock()
		s.stopErr = plugin.ErrPluginTimeout.WithCause(drainCtx.Err())
		s.mu.Unlock()
		cancel()
		return
	}
	cancel()
	if ctx.Err() != nil || g.conn == nil {
		return
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, s.options.ShutdownTimeout)
	defer shutdownCancel()
	if _, err := g.conn.services.Plugin.Shutdown(shutdownCtx, &pluginpb.ShutdownRequest{}); err != nil {
		s.logger.Warn("Plugin shutdown callback failed", "plugin_id", s.spec.PluginID, "error", err)
	} else {
		g.graceful = true
	}
	select {
	case <-g.callbacks.closeAdmission():
	case <-ctx.Done():
		s.mu.Lock()
		s.stopErr = ctx.Err()
		s.mu.Unlock()
	}
}

// kill terminates the child and its Unix process group before releasing generation ownership.
// Only the supervisor loop invokes cleanup; management callers can independently
// force its child context when their own stop budgets expire.
//
// Cooperative shutdown receives a bounded controller-close opportunity, while
// failed generations are canceled immediately. Host callback admission closes
// before termination, and actual callback completion remains a separate barrier.
// This prevents a new generation or data purge from racing an earlier host write,
// even after the old plugin process has already exited.
func (s *supervisor) kill(g *generation) {
	callbacksDone := g.callbacks.closeAdmission()
	g.callbacks.cancelActive()
	if g.graceful {
		// Give cooperative transport cleanup a bounded opportunity; cancellation also
		// terminates Unix descendants before their inherited pipes can keep Kill waiting.
		force := time.AfterFunc(min(2*time.Second, s.options.ShutdownTimeout), g.cancel)
		g.client.Kill()
		force.Stop()
		g.cancel()
	} else {
		g.cancel()
		g.client.Kill()
	}
	for _, output := range g.outputs {
		output.flush()
	}
	if err := killProcessGroup(g.pid); err != nil {
		s.logger.Warn("Plugin process group cleanup failed", "pid", g.pid, "error", err)
	}
	if g.socketDir != "" {
		if err := os.RemoveAll(g.socketDir); err != nil {
			s.logger.Warn("Plugin socket directory cleanup failed", "error", err)
		}
	}
	s.mu.Lock()
	s.observation.PID = 0
	s.observation.RSSBytes = 0
	s.observation.CPUPercent = 0
	s.mu.Unlock()
	// Keep generation ownership until host handlers have actually returned; a killed plugin alone cannot prove that its earlier database callbacks are finished.
	<-callbacksDone
	s.mu.Lock()
	if s.current == g {
		s.current = nil
	}
	s.mu.Unlock()
}

// setState updates the visible lifecycle state and retains the last failure detail.
func (s *supervisor) setState(state plugin.State, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping && state != plugin.StateStopped {
		return
	}
	s.observation.State = state
	s.observation.Since = s.options.Now()
	if message != "" {
		s.observation.LastError = message
	}
}

// sample records process RSS and CPU without treating observation failures as crashes.
// Memory above twice the configured advisory limit emits a warning only. Hard
// process resource limits remain the deployment environment's responsibility.
func (s *supervisor) sample(g *generation) {
	if g.stats == nil {
		observed, err := process.NewProcess(int32(g.pid))
		if err != nil {
			return
		}
		g.stats = observed
	}
	ctx, cancel := context.WithTimeout(s.ctx, s.options.HealthTimeout)
	defer cancel()
	memory, err := g.stats.MemoryInfoWithContext(ctx)
	if err != nil {
		return
	}
	cpu, err := g.stats.PercentWithContext(ctx, 0)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.observation.RSSBytes, s.observation.CPUPercent = memory.RSS, cpu
	limit := uint64(s.spec.MemoryMiB) * 2 << 20
	s.mu.Unlock()
	if limit > 0 && memory.RSS > limit {
		s.logger.Warn("Plugin memory exceeds advisory threshold", "pid", g.pid, "rss_bytes", memory.RSS, "threshold_bytes", limit)
	}
}
