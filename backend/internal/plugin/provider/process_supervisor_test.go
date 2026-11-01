package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// process_supervisor_test.go exercises native child lifecycle and request admission.
// Real SDK executables reproduce crashes, stalls, cancellation, and environment
// isolation. Handshake setup uses the production startup budget so concurrent
// package verification does not turn ordinary process scheduling into a simulated fault.

// TestProcessesDrainAndRejectNewLeases holds an admitted generation during stop.
// Draining must reject new requests, preserve the admitted lease until release,
// and terminate the child only after that lease returns.
func TestProcessesDrainAndRejectNewLeases(t *testing.T) {
	processes, spec := runtimeFixture(t, "crashy")
	waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	lease, err := processes.Acquire(context.Background(), spec.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- processes.Stop(context.Background(), spec.PluginID) }()
	waitRuntimeState(t, processes, spec.PluginID, plugin.StateDraining)
	if _, err := processes.Acquire(context.Background(), spec.PluginID); !errors.Is(err, plugin.ErrPluginRestarting) {
		t.Fatalf("drain admission: %v", err)
	}
	select {
	case err := <-stopped:
		t.Fatalf("stopped before lease release: %v", err)
	default:
	}
	lease.Release()
	lease.Release()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not finish")
	}
	if state := processes.Status(spec.PluginID); state.State != plugin.StateStopped || state.PID != 0 {
		t.Fatalf("remaining process: %+v", state)
	}
}

// TestProcessesRestartAndEnterCrashLoop repeatedly exits real child processes.
// Four abnormal exits must recover, while the fifth inside the configured
// window stops automatic restart and leaves an actionable crashloop status.
// Reconciliation with unchanged configuration must preserve that intervention state.
func TestProcessesRestartAndEnterCrashLoop(t *testing.T) {
	processes, spec := runtimeFixture(t, "crashy")
	for attempt := 1; attempt <= 5; attempt++ {
		before := waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
		lease, err := processes.Acquire(context.Background(), spec.PluginID)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _ = lease.Conn.HTTP.Exchange(ctx, &plugin.HTTPRequest{Method: "GET", Path: "/", RawQuery: "mode=exit"})
		cancel()
		lease.Release()
		if attempt == 5 {
			break
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			state := processes.Status(spec.PluginID)
			if state.State == plugin.StateRunning && state.PID != before.PID {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("restart %d failed: %+v", attempt, state)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	state := waitRuntimeState(t, processes, spec.PluginID, plugin.StateCrashLoop)
	if state.Restarts != 5 || state.PID != 0 {
		t.Fatalf("crashloop observation: %+v", state)
	}
	require.NoError(t, processes.ApplyConfig(t.Context(), spec))
	require.Equal(t, plugin.StateCrashLoop, processes.Status(spec.PluginID).State)
}

// TestProcessesRejectHostEnvironmentInheritance launches an environment-reporting
// child after adding a synthetic database secret to the host. Only the explicit
// process environment and go-plugin handshake variables may reach that child.
func TestProcessesRejectHostEnvironmentInheritance(t *testing.T) {
	t.Setenv("DATABASE_PASSWORD", "fixture-secret")
	processes, spec := runtimeFixture(t, "envdump")
	waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	lease, err := processes.Acquire(context.Background(), spec.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	response, err := lease.Conn.HTTP.Exchange(context.Background(), &plugin.HTTPRequest{Method: "GET", Path: "/"})
	if err != nil {
		t.Fatal(err)
	}
	var variables []string
	if err := json.Unmarshal(response.Body, &variables); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(variables, "\n"), "GOMEMLIMIT=128MiB") {
		t.Fatal("declared Go memory budget did not reach the child")
	}
	allowed := map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true, "LANG": true, "TZ": true, "GOMEMLIMIT": true, "TR_PLUGIN_ID": true, "TR_PLUGIN_STATE_DIR": true, "SystemRoot": true, "TEMP": true, "TMP": true, "TOKENROUTER_PLUGIN": true, "PLUGIN_MIN_PORT": true, "PLUGIN_MAX_PORT": true, "PLUGIN_PROTOCOL_VERSIONS": true, "PLUGIN_CLIENT_CERT": true, "PLUGIN_GRPC_BROKER_MULTIPLEX": true, "PLUGIN_UNIX_SOCKET_DIR": true}
	for _, variable := range variables {
		name, _, _ := strings.Cut(variable, "=")
		if !allowed[name] {
			t.Errorf("unexpected child variable name %q", name)
		}
	}
}

// TestProcessesKillStalledHandlersAfterHealthFailures starts a callback that never
// returns and cancels its RPC deadline. SDK readiness must detect unfinished
// canceled work so the host kills and replaces that process generation.
func TestProcessesKillStalledHandlersAfterHealthFailures(t *testing.T) {
	processes, spec := runtimeFixture(t, "crashy")
	before := waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	lease, err := processes.Acquire(context.Background(), spec.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	_, err = lease.Conn.HTTP.Exchange(ctx, &plugin.HTTPRequest{Method: "GET", Path: "/", RawQuery: "mode=loop"})
	cancel()
	lease.Release()
	if err == nil {
		t.Fatal("stalled handler returned success")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		state := processes.Status(spec.PluginID)
		if state.State == plugin.StateRunning && state.PID != before.PID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stalled generation survived: %+v", state)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestProcessesRejectChangedExecutable verifies SecureConfig before child execution.
// A valid binary with an intentionally incorrect digest must never reach running
// and must report a startup failure through its supervisor observation.
func TestProcessesRejectChangedExecutable(t *testing.T) {
	processes, spec := runtimeFixture(t, "crashy")
	if err := processes.Stop(context.Background(), spec.PluginID); err != nil {
		t.Fatal(err)
	}
	spec.BinarySHA256 = strings.Repeat("0", 64)
	if err := processes.Replace(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	state := waitRuntimeState(t, processes, spec.PluginID, plugin.StateCrashLoop)
	if state.LastError == "" {
		t.Fatal("checksum failure was not recorded")
	}
}

// TestProcessesRemoveBrokerDirectoriesAfterStartupFailure rejects a checksum and then attempts a missing executable through the custom runner.
// Every broker directory and its host-owned parent must be removed even when go-plugin never obtains a running child identity.
func TestProcessesRemoveBrokerDirectoriesAfterStartupFailure(t *testing.T) {
	for _, failure := range []string{"checksum", "exec"} {
		t.Run(failure, func(t *testing.T) {
			spec := plugin.ProcessSpec{PluginID: "test.cleanup", Version: "1.0.0", Executable: fixtureBinaries["crashy"], StateDir: t.TempDir(), MemoryMiB: 128, ConfigJSON: []byte(`{}`)}
			if failure == "checksum" {
				spec.VerifyBinary = true
				spec.BinarySHA256 = strings.Repeat("0", 64)
			} else {
				spec.Executable = filepath.Join(spec.StateDir, "missing-plugin")
			}
			owner := newSupervisor(spec, plugin.RuntimeOptions{}.WithDefaults(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
			defer owner.cancel()
			_, err := owner.start()
			require.Error(t, err)
			require.Nil(t, owner.current)
			found := false
			for _, line := range owner.logs.snapshot() {
				// go-plugin records its allocated broker path before invoking RunnerFunc;
				// observing that real allocation verifies cleanup after both pre-exec failures.
				if path, matched := strings.CutPrefix(line.Message, "created temporary directory for unix sockets dir="); matched {
					found = true
					require.NoDirExists(t, path)
					require.NoDirExists(t, filepath.Dir(path))
				}
			}
			require.True(t, found, "startup did not reach broker directory allocation")
		})
	}
}

// TestProcessesEnforceLeaseCapacity holds every production-default request slot.
// The next admission must fail immediately, and releasing one original lease
// must make exactly that generation available for another request.
func TestProcessesEnforceLeaseCapacity(t *testing.T) {
	processes, spec := runtimeFixture(t, "crashy")
	waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	var leases []*plugin.Lease
	defer func() {
		for _, lease := range leases {
			lease.Release()
		}
	}()
	for range 32 {
		lease, err := processes.Acquire(context.Background(), spec.PluginID)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	if _, err := processes.Acquire(context.Background(), spec.PluginID); !errors.Is(err, plugin.ErrPluginBusy) {
		t.Fatalf("capacity admission: %v", err)
	}
	leases[0].Release()
	lease, err := processes.Acquire(context.Background(), spec.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

// TestProcessesBoundInitializationFailures exercises an Init callback that only
// returns after cancellation. Repeated initialization deadlines must terminate
// their children and eventually enter crashloop without publishing a running lease.
func TestProcessesBoundInitializationFailures(t *testing.T) {
	processes, spec := runtimeFixture(t, "slowstart")
	state := waitRuntimeState(t, processes, spec.PluginID, plugin.StateCrashLoop)
	if state.Restarts != 5 || state.PID != 0 {
		t.Fatalf("initialization recovery: %+v", state)
	}
	if _, err := processes.Acquire(context.Background(), spec.PluginID); !errors.Is(err, plugin.ErrPluginUnavailable) {
		t.Fatal(err)
	}
}

// TestProcessesApplyConfigurationAtCapacity occupies every ordinary request lease during a configuration update.
// The watcher still receives the new value, and repeating a successfully applied configuration does not invoke it again.
func TestProcessesApplyConfigurationAtCapacity(t *testing.T) {
	processes, spec := runtimeFixture(t, "configuration")
	waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	leases := holdRequestCapacity(t, processes, spec.PluginID)
	spec.ConfigJSON = []byte(`{"amount":2}`)
	require.NoError(t, processes.ApplyConfig(t.Context(), spec))
	for _, lease := range leases {
		lease.Release()
	}
	require.Equal(t, map[string]int64{"amount": 2, "notifications": 1}, configurationObservation(t, processes, spec.PluginID))
	require.NoError(t, processes.ApplyConfig(t.Context(), spec))
	require.Equal(t, map[string]int64{"amount": 2, "notifications": 1}, configurationObservation(t, processes, spec.PluginID))
}

// TestProcessesDrainConfigurationNotifications holds an author configuration callback while the host begins stopping its generation.
// The management lease must delay Shutdown beyond its shorter callback budget until the configuration caller explicitly cancels and releases that lease.
func TestProcessesDrainConfigurationNotifications(t *testing.T) {
	processes, spec := runtimeFixture(t, "configuration")
	waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	processes.options.InitTimeout = 5 * time.Second
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	spec.ConfigJSON = []byte(`{"amount":2,"blockChange":true}`)
	applied := make(chan error, 1)
	go func() { applied <- processes.ApplyConfig(ctx, spec) }()
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(spec.StateDir, "changing"))
		return err == nil
	}, time.Second, 5*time.Millisecond)
	stopped := make(chan error, 1)
	go func() { stopped <- processes.Stop(context.Background(), spec.PluginID) }()
	waitRuntimeState(t, processes, spec.PluginID, plugin.StateDraining)
	// Waiting past the Shutdown RPC budget distinguishes lease draining from an
	// already dispatched Shutdown merely blocked behind the SDK's configuration mutex.
	select {
	case err := <-stopped:
		t.Fatalf("stop ended before configuration cancellation: %v", err)
	case <-time.After(2 * processes.options.ShutdownTimeout):
	}
	cancel()
	require.NoError(t, <-applied)
	require.NoError(t, <-stopped)
	require.Equal(t, plugin.StateStopped, processes.Status(spec.PluginID).State)
}

// TestProcessesRetryUnappliedConfiguration makes the author reject its first notification after the SDK receives the new values.
// Repeating identical desired configuration retries that failed callback and stops retrying only after author state is successfully updated.
func TestProcessesRetryUnappliedConfiguration(t *testing.T) {
	processes, spec := runtimeFixture(t, "configuration")
	waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	spec.ConfigJSON = []byte(`{"amount":2,"failOnce":true}`)
	require.NoError(t, processes.ApplyConfig(t.Context(), spec))
	require.Equal(t, map[string]int64{"amount": 0, "notifications": 1}, configurationObservation(t, processes, spec.PluginID))
	require.NoError(t, processes.ApplyConfig(t.Context(), spec))
	require.Equal(t, map[string]int64{"amount": 2, "notifications": 2}, configurationObservation(t, processes, spec.PluginID))
	require.NoError(t, processes.ApplyConfig(t.Context(), spec))
	require.Equal(t, map[string]int64{"amount": 2, "notifications": 2}, configurationObservation(t, processes, spec.PluginID))
}

// TestProcessesReplaceConfigurationDuringInitialization pauses an actual Init callback after its original values have been captured.
// Updating configuration must finish the old supervisor and publish a replacement that initializes with the new value instead.
func TestProcessesReplaceConfigurationDuringInitialization(t *testing.T) {
	processes, spec := runtimeFixture(t, "configuration")
	require.NoError(t, processes.Stop(t.Context(), spec.PluginID))
	processes.options.InitTimeout = 5 * time.Second
	spec.ConfigJSON = []byte(`{"amount":1,"blockInit":true}`)
	require.NoError(t, processes.Replace(t.Context(), spec))
	old := processes.entries[spec.PluginID]
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(spec.StateDir, "initializing"))
		return err == nil
	}, 20*time.Second, 5*time.Millisecond)
	spec.ConfigJSON = []byte(`{"amount":2}`)
	require.NoError(t, processes.ApplyConfig(t.Context(), spec))
	select {
	case <-old.done:
	default:
		t.Fatal("replacement retained the original initialization owner")
	}
	waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	require.Equal(t, map[string]int64{"amount": 2, "notifications": 0}, configurationObservation(t, processes, spec.PluginID))
}

// TestProcessesRestartUnwatchedConfigurationAtCapacity fills request capacity for an implementation without ConfigChanged support.
// Configuration delivery must still initiate a restart, retain old requests while draining, and initialize the replacement with new values.
func TestProcessesRestartUnwatchedConfigurationAtCapacity(t *testing.T) {
	processes, spec := runtimeFixture(t, "nowatcher")
	before := waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	leases := holdRequestCapacity(t, processes, spec.PluginID)
	spec.ConfigJSON = []byte(`{"amount":2}`)
	result := make(chan error, 1)
	// Keep all ordinary leases until restart closes admission, proving that the
	// lifecycle notification bypasses saturation while its replacement still drains requests.
	go func() { result <- processes.ApplyConfig(t.Context(), spec) }()
	waitRuntimeState(t, processes, spec.PluginID, plugin.StateDraining)
	for _, lease := range leases {
		lease.Release()
	}
	require.NoError(t, <-result)
	after := waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	require.NotEqual(t, before.PID, after.PID)
	require.Equal(t, map[string]int64{"amount": 2, "notifications": 0}, configurationObservation(t, processes, spec.PluginID))
}

// holdRequestCapacity retains every ordinary lease until the caller releases it.
// Test cleanup also releases the captured leases before runtime cleanup executes,
// ensuring failed assertions cannot keep their fixture process artificially busy.
func holdRequestCapacity(t *testing.T, processes *Processes, id string) []*plugin.Lease {
	t.Helper()
	leases := make([]*plugin.Lease, 0, processes.options.MaxLeases)
	t.Cleanup(func() {
		for _, lease := range leases {
			lease.Release()
		}
	})
	for range processes.options.MaxLeases {
		lease, err := processes.Acquire(t.Context(), id)
		require.NoError(t, err)
		leases = append(leases, lease)
	}
	return leases
}

// configurationObservation reads applied author state through the real plugin transport.
// The helper retains and returns one ordinary lease around its bounded HTTP
// exchange, so observations exercise the same process connection as production requests.
func configurationObservation(t *testing.T, processes *Processes, id string) map[string]int64 {
	t.Helper()
	lease, err := processes.Acquire(t.Context(), id)
	require.NoError(t, err)
	defer lease.Release()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	response, err := lease.Conn.HTTP.Exchange(ctx, &plugin.HTTPRequest{Method: "GET", Path: "/"})
	require.NoError(t, err)
	var observation map[string]int64
	require.NoError(t, json.Unmarshal(response.Body, &observation))
	return observation
}

// TestProcessesStopDescendantsHoldingOutputPipes starts a helper that retains the plugin's original output descriptors.
// Unix cancellation must terminate both processes and finish the supervisor without waiting for the helper's independent thirty-second lifetime.
func TestProcessesStopDescendantsHoldingOutputPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process groups are unavailable on Windows")
	}
	processes, spec := runtimeFixture(t, "crashy")
	require.NoError(t, processes.Stop(t.Context(), spec.PluginID))
	executable := filepath.Join(spec.StateDir, "launcher")
	pidPath := filepath.Join(spec.StateDir, "descendant.pid")
	escapedBinary := strings.ReplaceAll(fixtureBinaries["crashy"], "'", "'\"'\"'")
	script := fmt.Sprintf("#!/bin/sh\nsleep 30 &\necho $! > \"$TR_PLUGIN_STATE_DIR/descendant.pid\"\nexec '%s'\n", escapedBinary)
	require.NoError(t, os.WriteFile(executable, []byte(script), 0o755))
	digest := sha256.Sum256([]byte(script))
	spec.Executable = executable
	spec.BinarySHA256 = hex.EncodeToString(digest[:])
	require.NoError(t, processes.Replace(t.Context(), spec))
	before := waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	pidData, err := os.ReadFile(pidPath)
	require.NoError(t, err)
	descendantPID, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	require.NoError(t, err)
	descendant, err := os.FindProcess(descendantPID)
	require.NoError(t, err)
	// Cleanup owns only the helper PID created by this launcher; it also covers
	// assertion failures before the runtime has had an opportunity to cancel its group.
	t.Cleanup(func() {
		_ = descendant.Kill()
		_ = descendant.Release()
	})
	require.NoError(t, processes.Stop(t.Context(), spec.PluginID))
	owner := processes.entries[spec.PluginID]
	select {
	case <-owner.done:
	default:
		t.Fatal("successful stop retained supervisor cleanup")
	}
	require.Equal(t, plugin.StateStopped, processes.Status(spec.PluginID).State)
	for _, pid := range []int{before.PID, descendantPID} {
		require.Eventually(t, func() bool {
			observed, err := process.NewProcess(int32(pid))
			if err != nil {
				return errors.Is(err, process.ErrorProcessNotRunning)
			}
			states, err := observed.Status()
			// Container init may leave an exited orphan unreaped; a zombie cannot
			// retain an output pipe or perform further plugin or helper work.
			return errors.Is(err, process.ErrorProcessNotRunning) || err == nil && slices.Contains(states, "zombie")
		}, time.Second, 5*time.Millisecond)
	}
}

// TestProcessesKeepCleanupOpenUntilHostCallbacksFinish reproduces a callback
// completing persistent work after cancellation. The first bounded stop fails,
// and a retry must wait for actual completion before reporting safe cleanup.
func TestProcessesKeepCleanupOpenUntilHostCallbacksFinish(t *testing.T) {
	processes, spec := runtimeFixture(t, "crashy")
	waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	owner := processes.entries[spec.PluginID]
	owner.mu.Lock()
	calls := owner.current.callbacks
	owner.mu.Unlock()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	go func() {
		_, _ = calls.intercept(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "fixture"}, func(ctx context.Context, _ any) (any, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return nil, nil
		})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	err := processes.Stop(ctx, spec.PluginID)
	cancel()
	if err == nil {
		t.Fatal("stop claimed unfinished host work was drained")
	}
	<-canceled
	select {
	case <-owner.done:
		t.Fatal("owner completed before host callback returned")
	default:
	}
	retry := make(chan error, 1)
	go func() { retry <- processes.Stop(context.Background(), spec.PluginID) }()
	select {
	case err := <-retry:
		t.Fatalf("retry completed before callback: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-retry:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not observe completed host work")
	}
}

// TestProcessesRecoverFromProcessPanic distinguishes handler recovery from a
// panic in an author-owned goroutine. The handler panic keeps its PID, while
// the unhandled process panic must produce a different healthy generation.
func TestProcessesRecoverFromProcessPanic(t *testing.T) {
	processes, spec := runtimeFixture(t, "crashy")
	before := waitRuntimeState(t, processes, spec.PluginID, plugin.StateRunning)
	lease, err := processes.Acquire(context.Background(), spec.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	response, err := lease.Conn.HTTP.Exchange(context.Background(), &plugin.HTTPRequest{Method: "GET", Path: "/", RawQuery: "mode=panic"})
	if err != nil || response.Status != 500 {
		t.Fatalf("handler panic result: %v %v", response, err)
	}
	if processes.Status(spec.PluginID).PID != before.PID {
		t.Fatal("handler panic replaced process")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, _ = lease.Conn.HTTP.Exchange(ctx, &plugin.HTTPRequest{Method: "GET", Path: "/", RawQuery: "mode=panic-process"})
	cancel()
	lease.Release()
	deadline := time.Now().Add(3 * time.Second)
	for {
		state := processes.Status(spec.PluginID)
		if state.State == plugin.StateRunning && state.PID != before.PID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process panic was not recovered: %+v", state)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// runtimeFixture starts a real fixture with short observation and recovery intervals.
// Cleanup uses its own bounded context because test request contexts may already
// be canceled. The production defaults remain separately owned by RuntimeOptions.
func runtimeFixture(t *testing.T, name string) (*Processes, plugin.ProcessSpec) {
	t.Helper()
	data, err := os.ReadFile(fixtureBinaries[name])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	spec := plugin.ProcessSpec{PluginID: "test." + name, Version: "1.0.0", Executable: fixtureBinaries[name], BinarySHA256: hex.EncodeToString(digest[:]), StateDir: t.TempDir(), MemoryMiB: 128, ConfigJSON: []byte(`{}`), VerifyBinary: true}
	// Keep the production handshake budget under full-suite process contention;
	// explicit short Init, health, and request deadlines still exercise their failure policies.
	options := plugin.RuntimeOptions{HostVersion: "0.6.0", Timezone: "UTC", InitTimeout: 100 * time.Millisecond, HealthInterval: 30 * time.Millisecond, HealthTimeout: 50 * time.Millisecond, ExitInterval: 10 * time.Millisecond, ResourceInterval: time.Hour, DrainTimeout: time.Second, ShutdownTimeout: 200 * time.Millisecond, Backoffs: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond}}
	processes := NewProcesses(options, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := processes.StopAll(ctx); err != nil {
			t.Errorf("process cleanup: %v", err)
		}
	})
	if err := processes.Replace(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	return processes, spec
}

// waitRuntimeState waits for one observable supervisor state within a bounded budget.
// Unexpected delay reports the current observation for diagnosis. It polls real
// subprocess state rather than replacing process behavior with a timing mock.
func waitRuntimeState(t *testing.T, processes *Processes, id string, wanted plugin.State) plugin.Status {
	t.Helper()
	// Observation allows a complete production handshake before declaring fixture
	// startup stuck; behavior-specific crash and request deadlines remain independently asserted.
	deadline := time.Now().Add(20 * time.Second)
	for {
		state := processes.Status(id)
		if state.State == wanted {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("want %s, got %+v; recent logs: %+v", wanted, state, processes.Logs(id))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestProcessResourceSamplingWarnsWithoutKilling observes this test process above a deliberately tiny advisory allowance.
// Sampling records resident memory and emits its warning while preserving running state; no process termination or unbounded allocation is involved.
func TestProcessResourceSamplingWarnsWithoutKilling(t *testing.T) {
	var output bytes.Buffer
	owner := &supervisor{
		ctx: t.Context(), options: plugin.RuntimeOptions{}.WithDefaults(),
		logger: slog.New(slog.NewTextHandler(&output, nil)), spec: plugin.ProcessSpec{MemoryMiB: 1}, observation: plugin.Status{State: plugin.StateRunning},
	}
	owner.sample(&generation{pid: os.Getpid()})
	if owner.observation.RSSBytes == 0 {
		t.Fatal("self process memory was not sampled")
	}
	if !strings.Contains(output.String(), "Plugin memory exceeds advisory threshold") {
		t.Fatalf("missing resource warning: %s", output.String())
	}
	if owner.observation.State != plugin.StateRunning {
		t.Fatal("advisory sampling changed process availability")
	}
}
