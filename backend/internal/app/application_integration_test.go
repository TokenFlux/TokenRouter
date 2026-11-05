//go:build integration

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/testutil/rediscontainer"
)

// application_integration_test.go verifies configured startup and resource
// acquisition before HTTP readiness. The process tests wait for a successful HTTP response
// before sending SIGTERM, then compare shutdown log order and query PostgreSQL
// for leftover child connections. Version output, setup, and maintenance
// commands have separate lifecycle startup expectations.
//
// Output polling overlaps the child's stdout and stderr writes, so one named
// buffer is accessed through synchronized methods. A single Wait goroutine
// publishes the exit error by closing done after output copying finishes.
// Cleanup reaps each child before closing its stdin, including when an assertion
// fails. CLI responses are sent after individual prompts because setup uses
// separate buffered readers for ordinary text and piped passwords.

// processOutput captures concurrent child output through its own synchronized writer.
type processOutput struct {
	// mu protects appends and string snapshots of buffer during concurrent
	// process output and test polling. Keep buffer named so promoted bytes.Buffer
	// methods cannot let io.Copy bypass the locked Write method.
	mu     sync.Mutex   // capture synchronization lock
	buffer bytes.Buffer // combined process output
}

// testProcess holds a child process, prompt input, and published exit result.
type testProcess struct {
	// cmd is started once; callers signal its Process, while startTestProcess
	// cleanup owns forced termination and the single Wait call.
	cmd *exec.Cmd
	// done publishes err after Cmd.Wait completes, including its output-copying
	// goroutines. Readers receive done before inspecting err; cleanup also waits
	// for that signal before closing stdin.
	done chan struct{} // published exit notification
	err  error         // saved Wait result
	// output is shared by stdout, stderr, and polling helpers; its methods
	// synchronize access while the child runs and after it exits.
	output *processOutput
	// stdin is the command's input pipe; prompt writes sequentially while the
	// child runs, and Wait or final test cleanup closes the pipe.
	stdin io.WriteCloser
}

// Write appends child output and returns the number of bytes accepted.
// os/exec sends stdout and stderr through this method; appends share text's
// mutex so polling snapshots cannot overlap an unsynchronized buffer write.
func (o *processOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buffer.Write(p)
}

// text returns the combined child output as a string captured under the mutex.
// Prompt and readiness polling may call it while os/exec copies stdout or
// stderr because Write uses the same mutex.
func (o *processOutput) text() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buffer.String()
}

// startTestProcess starts binary with args in dir and returns captured output
// and stdin. The child receives a small environment plus env, which can
// override defaults; dir also supplies HOME and DATA_DIR. Stdout and stderr
// feed one synchronized buffer for polling during execution.
//
// One goroutine calls Wait, stores the exit error, and closes done to publish
// completion. Test cleanup kills a child whose completion signal has not arrived
// and waits for that signal before closing stdin. This final reap has no
// separate deadline, even when an earlier wait or prompt assertion times out.
func startTestProcess(t *testing.T, binary, dir string, env []string, args ...string) *testProcess {
	t.Helper()
	p := &testProcess{cmd: exec.Command(binary, args...), done: make(chan struct{}), output: &processOutput{}}
	p.cmd.Dir = dir
	// Limit runtime configuration to these variables and env, so inherited
	// application credentials or AUTO_SETUP settings cannot choose another startup path.
	// External HTTPS uses a closed loopback proxy, preventing default-enabled
	// metrics from reporting synthetic process fixtures even when a test runs slowly.
	p.cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"TMPDIR=" + os.TempDir(),
		"TZ=UTC",
		"DATA_DIR=" + dir,
		"HTTPS_PROXY=http://127.0.0.1:1",
	}, env...)
	p.cmd.Stdout = p.output
	p.cmd.Stderr = p.output
	var err error
	p.stdin, err = p.cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, p.cmd.Start())
	// Store the exit result before closing done; readers synchronize on that
	// close before accessing err from this process's single Wait goroutine.
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			// Request forced exit and then reap the child; a natural exit
			// racing with this cleanup can make Kill return an ignored error.
			_ = p.cmd.Process.Kill()
			<-p.done
		}
		// Cmd.Wait already closes StdinPipe after process exit; this final
		// best-effort close can therefore report an already-closed pipe.
		_ = p.stdin.Close()
	})
	return p
}

// wait returns the child's saved exit error when it selects the done signal.
// Selecting the timeout fails the test and includes captured output; registered
// cleanup handles a still-running child. Receiving done publishes err before
// this helper reads it. The timeout bounds this helper's wait, while cleanup's
// final process reap has its own unbounded wait. Callers inspect the returned
// error to distinguish expected successful exits from startup or command failures.
func (p *testProcess) wait(t *testing.T, timeout time.Duration) error {
	t.Helper()
	// Wait for the owner's completion signal; startTestProcess already owns
	// the single Cmd.Wait call that reaps the child and publishes its error.
	select {
	case <-p.done:
		return p.err
	case <-time.After(timeout):
		t.Fatalf("进程未在预算内退出：\n%s", p.output.text())
		return nil
	}
}

// prompt waits for prompt text, then writes value and a newline to stdin.
// Call it sequentially with text identifying the intended step because matching
// searches all accumulated output. Child completion while awaiting text or a
// forty-five-second deadline fails the test with output. Waiting for the prompt
// keeps setup's ordinary buffered reader from consuming later password input
// before the separate piped-password reader is used.
func (p *testProcess) prompt(t *testing.T, prompt, value string) {
	t.Helper()
	// The per-prompt budget also covers setup connection checks between questions.
	deadline := time.Now().Add(45 * time.Second)
	for !strings.Contains(p.output.text(), prompt) {
		select {
		case <-p.done:
			t.Fatalf("等待 %q 时进程退出：%v\n%s", prompt, p.err, p.output.text())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("没有出现提示 %q：\n%s", prompt, p.output.text())
		}
		// Prompts can arrive without a newline, so poll the captured bytes;
		// waiting for a complete output line would leave such prompts unread.
		time.Sleep(10 * time.Millisecond)
	}
	// The newline terminates this step for ReadString; withholding later
	// answers keeps the current buffered reader from prefetching future input.
	_, err := io.WriteString(p.stdin, value+"\n")
	require.NoError(t, err)
}

// freeServerPort returns an ephemeral loopback TCP port after closing its listener.
// The result is unreserved; another listener can claim it before the child
// process binds, so child startup still needs readiness or exit checks.
func freeServerPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok)
	port := address.Port
	require.NoError(t, ln.Close())
	return port
}

// waitProcessHTTP returns the first fully read HTTP 200 body from port and path
// on loopback. It uses a ninety-second polling deadline and a half-second budget
// per request, and fails when it observes child completion while awaiting
// readiness. Connection errors, body-read failures, and other statuses trigger
// another attempt. Obtained response bodies close before returning or retrying,
// so successive probes do not retain earlier response bodies.
func waitProcessHTTP(t *testing.T, p *testProcess, port int, path string) string {
	t.Helper()
	// Keep each readiness request short so an unresponsive listener leaves
	// time for further attempts before the overall polling deadline.
	client := &http.Client{Timeout: 500 * time.Millisecond}
	// Database bootstrap and lifecycle startup share this readiness polling budget.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			t.Fatalf("监听前进程退出：%v\n%s", p.err, p.output.text())
		default:
		}
		response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			// Close before evaluating readiness or retrying; any close error
			// is ignored because the body-read result and status decide readiness.
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK {
				return string(body)
			}
		}
		// Refused connections and incomplete startup responses get another
		// attempt after a short pause; readiness requires a readable HTTP 200 body.
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("HTTP 未就绪：\n%s", p.output.text())
	return ""
}

// TestProcessModes exercises server startup, setup, and maintenance commands
// against PostgreSQL and Redis fixtures. It checks exits and lifecycle log
// order together with connection cleanup, administrator concurrency, JWT
// claims, persisted deletion, and installation files.
func TestProcessModes(t *testing.T) {
	backendRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	binary := filepath.Join(t.TempDir(), "server")
	// The build inherits parent toolchain settings and the Go version in
	// go.mod; linker identifiers give the version case a specific expected output.
	build := exec.Command("go", "build", "-ldflags=-X main.Version=test-contract -X main.Commit=test-head -X main.Date=test-date -X main.BuildType=test", "-o", binary, "./cmd/server")
	build.Dir = backendRoot
	buildOutput, err := build.CombinedOutput()
	require.NoError(t, err, string(buildOutput))
	t.Run("version", func(t *testing.T) {
		p := startTestProcess(t, binary, t.TempDir(), nil, "-version")
		// Await process and output-copy completion before checking version text,
		// so the assertions inspect the final captured output from this command.
		require.NoError(t, p.wait(t, 30*time.Second))
		require.Contains(t, p.output.text(), "TokenRouter test-contract (commit: test-head, built: test-date)")
		require.NotContains(t, p.output.text(), "[Lifecycle] started")
	})
	fixture := newDatabaseFixture(t)
	ctx := context.Background()
	rdb, err := rediscontainer.Run(ctx, "redis:8.4-alpine")
	// Register Redis even when startup fails. Child processes finish subtest
	// cleanup before this parent fixture is removed, keeping their cache available.
	if rdb != nil {
		t.Cleanup(func() {
			// Setup or test cancellation must not cancel Docker removal. Start the
			// bounded cleanup context when this callback runs.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := rdb.Terminate(cleanupCtx); err != nil {
				t.Errorf("terminate Redis container: %v", err)
			}
		})
	}
	require.NoError(t, err)
	redisHost, err := rdb.Host(ctx)
	require.NoError(t, err)
	redisPort, err := rdb.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)

	configFor := func(t *testing.T, mode, dbname string, port int) (string, []string) {
		t.Helper()
		dir := t.TempDir()
		priceFile := filepath.Join(dir, "prices.json")
		require.NoError(t, os.WriteFile(priceFile, []byte(`{"test-model":{"input_cost_per_token":0.000001}}`), 0o600))
		cfg := map[string]any{
			"config_version": 2, "timezone": "UTC",
			"server":        map[string]any{"host": "127.0.0.1", "port": port, "mode": "release"},
			"database":      map[string]any{"host": fixture.host, "port": fixture.port, "user": "postgres", "password": "postgres", "dbname": dbname, "sslmode": "disable", "max_open_conns": 16, "max_idle_conns": 2},
			"redis":         map[string]any{"host": redisHost, "port": redisPort.Int(), "pool_size": 16, "min_idle_conns": 0},
			"model_catalog": map[string]any{"catalog_url": "", "supplement_file": priceFile},
			"storage":       map[string]any{"dir": dir},
			"log":           map[string]any{"level": "info", "output": map[string]any{"to_stdout": true, "to_file": false}},
		}
		data, err := yaml.Marshal(cfg)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o600))
		return dir, []string{"CONFIG_FILE=" + filepath.Join(dir, "config.yaml"), "PGAPPNAME=test-" + mode}
	}
	// Seed administrator concurrency values 5, 12, and 30 before each start;
	// both standard and simple modes must leave those persisted values unchanged.
	for _, mode := range []string{"standard", "simple"} {
		t.Run(mode+"-sigterm", func(t *testing.T) {
			administrators := make(map[int64]int)
			for _, concurrency := range []int{5, 12, 30} {
				row, createErr := fixture.client.User.Create().SetEmail(fmt.Sprintf("upgrade-%s-%d@example.test", mode, concurrency)).SetPasswordHash("hash").SetRole("admin").SetConcurrency(concurrency).Save(ctx)
				require.NoError(t, createErr)
				administrators[row.ID] = concurrency
				t.Cleanup(func() { require.NoError(t, fixture.client.User.DeleteOneID(row.ID).Exec(context.Background())) })
			}
			port := freeServerPort(t)
			dir, env := configFor(t, mode, "test_contracts", port)
			p := startTestProcess(t, binary, dir, env)
			waitProcessHTTP(t, p, port, "/health")
			for id, concurrency := range administrators {
				current, readErr := fixture.client.User.Get(ctx, id)
				require.NoError(t, readErr)
				require.Equal(t, concurrency, current.Concurrency)
			}
			// Send SIGTERM after readiness, then wait for process exit before
			// comparing lifecycle log positions or querying child connection cleanup.
			require.NoError(t, p.cmd.Process.Signal(syscall.SIGTERM))
			require.NoError(t, p.wait(t, 40*time.Second), p.output.text())
			logs := p.output.text()
			// Catalog initialization logs once before catalog service startup,
			// and the service's single stop log precedes Redis closure.
			require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started ModelCatalogInitialization"))
			require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started ModelCatalogService"))
			require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped ModelCatalogService"))
			require.Less(t, strings.Index(logs, "started ModelCatalogInitialization"), strings.Index(logs, "started ModelCatalogService"))
			require.Less(t, strings.Index(logs, "stopped ModelCatalogService"), strings.Index(logs, "stopped Redis"))
			for _, name := range []string{"HTTPRequests", "DeferredService", "TimingWheelService", "UsageLogBatchers", "Redis", "Ent"} {
				require.Contains(t, logs, "[Lifecycle] stopped "+name)
			}
			// Task activity's stop log follows HTTP draining and precedes each
			// worker or cleanup runtime's stop log, which precedes Redis closure.
			require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped TaskRequestsAndDownloads"))
			require.Less(t, strings.Index(logs, "stopped HTTPRequests"), strings.Index(logs, "stopped TaskRequestsAndDownloads"))
			for _, name := range []string{"CreativeWorkerRuntime", "BatchImageWorkerRuntime", "BatchImageCleanupService"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name), name)
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped TaskRequestsAndDownloads"), strings.Index(logs, "stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
			}
			// Billing and optional metrics hooks each start and stop once. Their
			// stop barriers complete before shared storage is released by Redis closure.
			for _, name := range []string{"BillingCacheService", "SubscriptionExpiryService", "BStats"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name))
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name))
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"))
			}
			// Authentication hooks log one start and stop; comparisons place
			// API-key shutdown after request draining and invalidation-worker shutdown.
			for _, name := range []string{"APIKeyService", "AuthCacheInvalidationWorker"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name))
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name))
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"))
			}
			require.Less(t, strings.Index(logs, "stopped HTTPRequests"), strings.Index(logs, "stopped APIKeyService"))
			require.Less(t, strings.Index(logs, "stopped AuthCacheInvalidationWorker"), strings.Index(logs, "stopped APIKeyService"))
			require.Less(t, strings.Index(logs, "stopped TimingWheelService"), strings.Index(logs, "stopped Redis"))
			require.Less(t, strings.Index(logs, "stopped Redis"), strings.Index(logs, "stopped Ent"))
			// Maintenance hooks report one start and stop, and their stops precede
			// Redis. Further comparisons place token refresh before its coordinator
			// and deferred jobs before the timing wheel.
			for _, name := range []string{"TokenRefreshService", "ProviderExpiryService", "ProxyExpiryService", "ScheduledTestRunnerService", "GroupAvailabilityProbeRunnerService", "UsageMonitor", "OllamaCloudUsageService", "DeferredService", "TLSFingerprintProfileService", "TLSFingerprintRouterService"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name), name)
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
			}
			for _, name := range []string{"ProviderRefreshCoordinator", "ProviderOAuthUsage", "ProviderUpstreamUsage", "ProviderImportProbes", "ProviderPrivacy", "ProviderTier", "ProviderModelList", "GrokQuotaProbes", "TLSFingerprintCollectorService"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
			}
			require.Less(t, strings.Index(logs, "stopped HTTPRequests"), strings.Index(logs, "stopped TokenRefreshService"))
			require.Less(t, strings.Index(logs, "stopped TokenRefreshService"), strings.Index(logs, "stopped ProviderRefreshCoordinator"))
			require.Less(t, strings.Index(logs, "stopped DeferredService"), strings.Index(logs, "stopped TimingWheelService"))

			// Snapshot, concurrency, and serial-queue stop logs follow HTTP draining
			// and precede Redis; each hook has one recorded start and stop.
			for _, name := range []string{"SchedulerSnapshotService", "ConcurrencyService", "UserMessageQueueService"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name), name)
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped HTTPRequests"), strings.Index(logs, "stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
			}

			// Ops error workers stop before the system-log sink, and usage cleanup
			// stops before dashboard aggregation; every listed stop follows HTTP
			// and precedes Redis.
			for _, name := range []string{"OpsMetricsCollector", "OpsAggregationService", "OpsAlertEvaluatorService", "OpsCleanupService", "OpsScheduledReportService", "OpsService", "OpsIngressRejectAggregator", "DashboardAggregationService", "UsageCleanupService", "AuditLogService", "OpsSystemLogSink"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name), name)
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped HTTPRequests"), strings.Index(logs, "stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
			}
			for _, name := range []string{"OpsWSRuntime", "OpsErrorLogWorkers", "UsageLogBatchers"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped HTTPRequests"), strings.Index(logs, "stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
			}
			require.Less(t, strings.Index(logs, "stopped UsageCleanupService"), strings.Index(logs, "stopped DashboardAggregationService"))
			require.Less(t, strings.Index(logs, "stopped OpsErrorLogWorkers"), strings.Index(logs, "stopped OpsSystemLogSink"))

			// Backup and system-maintenance admission stop logs precede HTTP;
			// their work-finish stops follow HTTP draining and precede Ent closure.
			for _, name := range []string{"BackupAdmission", "SystemMaintenanceAdmission"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped HTTPRequests"), name)
			}
			for _, name := range []string{"BackupService", "SystemMaintenanceOperations"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped HTTPRequests"), strings.Index(logs, "stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Ent"), name)
			}

			// Search, moderation, and queued identity mail log shutdown after
			// HTTP draining and before both Redis and Ent; moderation precedes mail.
			for _, name := range []string{"WebSearchRuntime", "ContentModerationService", "EmailQueueService"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name), name)
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped HTTPRequests"), strings.Index(logs, "stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Ent"), name)
			}
			require.Less(t, strings.Index(logs, "stopped ContentModerationService"), strings.Index(logs, "stopped EmailQueueService"))

			// Payment expiry's stop log precedes ApplicationBackgroundTasks and
			// EmailQueueService; their barriers appear before Redis in the asserted order.
			require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started PaymentOrderExpiryService"))
			require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped PaymentOrderExpiryService"))
			require.Contains(t, logs, "[Lifecycle] stopped ApplicationBackgroundTasks")
			for _, pair := range [][2]string{{"HTTPRequests", "PaymentOrderExpiryService"}, {"PaymentOrderExpiryService", "ApplicationBackgroundTasks"}, {"ApplicationBackgroundTasks", "EmailQueueService"}, {"EmailQueueService", "Redis"}, {"PaymentOrderExpiryService", "Ent"}} {
				require.Less(t, strings.Index(logs, "stopped "+pair[0]), strings.Index(logs, "stopped "+pair[1]), pair)
			}

			// Gateway and Qoder request, credential, and quota stops precede both
			// storage resources; OAuth stop logs follow gateway request completion.
			for _, name := range []string{"GatewayRequestsAndAttempts", "QoderRequestsAndAttempts", "QoderCredentialSessions", "OpenAIQuotaActions", "OpenAIQuotaService"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Ent"), name)
			}
			for _, name := range []string{"OAuthService", "OpenAIOAuthService", "GeminiOAuthService", "AntigravityOAuthService", "QoderOAuthService", "GrokOAuthService"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name), name)
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped GatewayRequestsAndAttempts"), strings.Index(logs, "stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
			}
			// Request stop logs precede the usage-worker stop. The following
			// comparisons place quota-action stop after the hijack log position
			// and before the HTTP request stop log.
			for _, name := range []string{"HTTPRequests", "GatewayRequestsAndAttempts", "QoderRequestsAndAttempts"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped UsageRecordWorkerPool"), name)
			}
			require.Less(t, strings.Index(logs, "stopped HTTPHijackedConnections"), strings.Index(logs, "stopped OpenAIQuotaActions"))
			require.Less(t, strings.Index(logs, "stopped OpenAIQuotaActions"), strings.Index(logs, "stopped HTTPRequests"))
			require.Less(t, strings.Index(logs, "stopped OpenAIQuotaActions"), strings.Index(logs, "stopped OpenAIQuotaService"))
			require.Less(t, strings.Index(logs, "stopped QoderRequestsAndAttempts"), strings.Index(logs, "stopped QoderCredentialSessions"))
			require.NotContains(t, logs, "[Lifecycle] started OpenAILiveObservers")
			require.NotContains(t, logs, "[Lifecycle] started TLSFingerprintCollectorService")
			// Count sessions by the child's PGAPPNAME while polling cleanup,
			// leaving fixture connections out; read errors keep the poll unsatisfied.
			require.Eventually(t, func() bool {
				var n int
				err := fixture.db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE application_name=$1`, "test-"+mode).Scan(&n)
				return err == nil && n == 0
			}, 3*time.Second, 25*time.Millisecond)
		})
	}

	// Validate the tool's JWT with the persisted signing secret and check
	// administrator claims, alongside output and absent lifecycle-start logs.
	t.Run("jwtgen-minimal", func(t *testing.T) {
		tool := filepath.Join(t.TempDir(), "jwtgen")
		build := exec.Command("go", "build", "-o", tool, "./cmd/jwtgen")
		build.Dir = backendRoot
		data, e := build.CombinedOutput()
		require.NoError(t, e, string(data))
		var id int64
		email := "test-jwtgen@example.com"
		require.NoError(t, fixture.db.QueryRow("INSERT INTO users(email,password_hash,role,status) VALUES($1,'fixture','admin','active') RETURNING id", email).Scan(&id))
		// Remove the temporary administrator on return so later process cases
		// cannot select this fixture user or inherit its identity state.
		defer func() { _, e := fixture.db.Exec("DELETE FROM users WHERE id=$1", id); require.NoError(t, e) }()
		var secret string
		require.NoError(t, fixture.db.QueryRow("SELECT value FROM security_secrets WHERE key='jwt_secret'").Scan(&secret))
		for _, args := range [][]string{nil, {"-email", email}} {
			dir, env := configFor(t, "standard", "test_contracts", freeServerPort(t))
			p := startTestProcess(t, tool, dir, env, args...)
			// Command completion includes identity lookup, token signing,
			// and connection cleanup before the output is checked.
			require.NoError(t, p.wait(t, 30*time.Second))
			output := p.output.text()
			require.Contains(t, output, "ADMIN_EMAIL="+email)
			require.Contains(t, output, fmt.Sprintf("ADMIN_USER_ID=%d", id))
			require.NotContains(t, output, "[Lifecycle] started")
			token := ""
			for line := range strings.SplitSeq(output, "\n") {
				if after, ok := strings.CutPrefix(line, "JWT="); ok {
					token = after
				}
			}
			verifier := identity.NewSessionService(identity.SessionOptions{Secret: secret}, nil, nil, nil, nil)
			claims, e := verifier.ValidateToken(token)
			require.NoError(t, e)
			require.Equal(t, id, claims.UserID)
			require.Equal(t, "admin", claims.Role)
		}
	})

	t.Run("cleanup-minimal", func(t *testing.T) {
		tool := filepath.Join(t.TempDir(), "cleanup-ingress-reject-logs")
		build := exec.Command("go", "build", "-o", tool, "./cmd/cleanup-ingress-reject-logs")
		build.Dir = backendRoot
		output, e := build.CombinedOutput()
		require.NoError(t, e, string(output))
		var id int64
		require.NoError(t, fixture.db.QueryRow(`INSERT INTO ops_error_logs(error_phase,error_type,status_code,error_body,created_at) VALUES('auth','auth',401,'{"code":"API_KEY_REQUIRED"}',NOW()-INTERVAL '1 hour') RETURNING id`).Scan(&id))
		// Delete the seeded rejection if dry-run or an earlier failure leaves
		// it present, so subsequent cases share a clean log fixture.
		defer func() { _, e := fixture.db.Exec("DELETE FROM ops_error_logs WHERE id=$1", id); require.NoError(t, e) }()
		// Place the cutoff ahead of the seeded one-hour-old rejection.
		before := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		for _, execute := range []bool{false, true} {
			dir, env := configFor(t, "standard", "test_contracts", freeServerPort(t))
			args := []string{"--before", before}
			if execute {
				args = append(args, "--execute")
			}
			process := startTestProcess(t, tool, dir, env, args...)
			// Await process and output-copy completion before inspecting command
			// diagnostics, then read the seeded row to distinguish dry-run from execution.
			require.NoError(t, process.wait(t, 30*time.Second), process.output.text())
			text := process.output.text()
			require.NotContains(t, text, "[Lifecycle] started")
			require.Contains(t, text, "reason=missing_key")
			var count int
			require.NoError(t, fixture.db.QueryRow("SELECT COUNT(*) FROM ops_error_logs WHERE id=$1", id).Scan(&count))
			if execute {
				require.Contains(t, text, "mode=execute")
				require.Zero(t, count)
			} else {
				require.Contains(t, text, "mode=dry-run")
				require.Contains(t, text, "deleted=0")
				require.Equal(t, 1, count)
			}
		}
		dir, env := configFor(t, "standard", "test_contracts", freeServerPort(t))
		invalid := startTestProcess(t, tool, dir, env)
		// Missing cutoff arguments fail before normal application lifecycle startup.
		require.Error(t, invalid.wait(t, 30*time.Second))
		require.Contains(t, invalid.output.text(), "--before is required")
		require.NotContains(t, invalid.output.text(), "[Lifecycle] started")
	})

	t.Run("bootstrap-failure-closes-connection", func(t *testing.T) {
		// Replace the persisted JWT secret with a deliberately short value,
		// then restore it on return so later starts use the previous secret.
		var original string
		require.NoError(t, fixture.db.QueryRow(`SELECT value FROM security_secrets WHERE key='jwt_secret'`).Scan(&original))
		_, err := fixture.db.Exec(`UPDATE security_secrets SET value='short' WHERE key='jwt_secret'`)
		require.NoError(t, err)
		defer func() {
			_, err := fixture.db.Exec(`UPDATE security_secrets SET value=$1 WHERE key='jwt_secret'`, original)
			require.NoError(t, err)
		}()
		dir, env := configFor(t, "standard", "test_contracts", freeServerPort(t))
		p := startTestProcess(t, binary, dir, env)
		// Await process and output-copy completion before inspecting the secret
		// failure, then poll PostgreSQL to observe whether the child's connections disappeared.
		require.Error(t, p.wait(t, 30*time.Second))
		require.Contains(t, p.output.text(), "must be at least 32 bytes")
		require.NotContains(t, p.output.text(), "[Lifecycle] started")
		// Bootstrap fails before lifecycle starts, so verify pool cleanup
		// by the child's application name; fixture sessions remain usable for polling.
		require.Eventually(t, func() bool {
			var n int
			err := fixture.db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE application_name='test-standard'`).Scan(&n)
			return err == nil && n == 0
		}, 3*time.Second, 25*time.Millisecond)
	})
	t.Run("listen-failure-cleans-resources", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		// Hold this listener through child startup to force a bind conflict;
		// final close is best-effort after the expected failing process has exited.
		defer func() { _ = ln.Close() }()
		address, ok := ln.Addr().(*net.TCPAddr)
		require.True(t, ok)
		dir, env := configFor(t, "standard", "test_contracts", address.Port)
		p := startTestProcess(t, binary, dir, env)
		// Await process and output-copy completion before checking the bind error
		// and the Redis and Ent stop messages produced during cleanup.
		require.Error(t, p.wait(t, 60*time.Second))
		require.Contains(t, p.output.text(), "address already in use")
		require.Contains(t, p.output.text(), "[Lifecycle] stopped Redis")
		require.Contains(t, p.output.text(), "[Lifecycle] stopped Ent")
	})
	t.Run("setup-server-only", func(t *testing.T) {
		port := freeServerPort(t)
		p := startTestProcess(t, binary, t.TempDir(), []string{"SERVER_HOST=127.0.0.1", "SERVER_PORT=" + strconv.Itoa(port)})
		require.Contains(t, waitProcessHTTP(t, p, port, "/setup/status"), `"needs_setup":true`)
		// Stop the independent setup server after its status responds; its
		// clean exit and absent lifecycle-start logs distinguish this installation path.
		require.NoError(t, p.cmd.Process.Signal(syscall.SIGTERM))
		require.NoError(t, p.wait(t, 10*time.Second))
		require.NotContains(t, p.output.text(), "[Lifecycle] started")
	})
	t.Run("cli-setup", func(t *testing.T) {
		_, err := fixture.db.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier("test_cli"))
		require.NoError(t, err)
		dir := t.TempDir()
		p := startTestProcess(t, binary, dir, nil, "-setup")
		// The shared text reader precedes separate piped-password readers in
		// this wizard; send each answer after its prompt so the text reader
		// cannot prefetch a future password line.
		for _, step := range [][2]string{
			{"PostgreSQL Host", fixture.host},
			{"PostgreSQL Port", strconv.Itoa(fixture.port)},
			{"PostgreSQL User", "postgres"},
			{"PostgreSQL Password", "postgres"},
			{"Database Name", "test_cli"},
			{"SSL Mode", "disable"},
			{"Redis Host", redisHost},
			{"Redis Port", strconv.Itoa(redisPort.Int())},
			{"Redis Password", ""},
			{"Redis DB", "0"},
			{"Enable Redis TLS?", "n"},
			{"Admin Email", "test-cli@example.test"},
			{"Admin Password", "test-test-password"},
			{"Confirm Password", "test-test-password"},
			{"Server Port", strconv.Itoa(freeServerPort(t))},
			{"Proceed with installation?", "y"},
		} {
			p.prompt(t, step[0], step[1])
		}
		// Prompts have finished; wait for final configuration and
		// installation-marker writes before inspecting their filesystem results.
		require.NoError(t, p.wait(t, 90*time.Second), p.output.text())
		require.Contains(t, p.output.text(), "Installation Complete!")
		info, err := os.Stat(filepath.Join(dir, "config.yaml"))
		require.NoError(t, err)
		// Require owner-only permissions on the generated credential configuration.
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		_, err = os.Stat(filepath.Join(dir, ".installed"))
		require.NoError(t, err)
		require.NotContains(t, p.output.text(), "[Lifecycle] started")
	})
	t.Run("auto-setup", func(t *testing.T) {
		_, err := fixture.db.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier("test_auto"))
		require.NoError(t, err)
		dir := t.TempDir()
		port := freeServerPort(t)
		p := startTestProcess(t, binary, dir, []string{
			"AUTO_SETUP=true", "DATABASE_HOST=" + fixture.host, "DATABASE_PORT=" + strconv.Itoa(fixture.port), "DATABASE_USER=postgres", "DATABASE_PASSWORD=postgres", "DATABASE_DBNAME=test_auto", "DATABASE_SSLMODE=disable",
			"REDIS_HOST=" + redisHost, "REDIS_PORT=" + strconv.Itoa(redisPort.Int()), "ADMIN_EMAIL=test-auto@example.test", "ADMIN_PASSWORD=test-test-password", "SERVER_HOST=127.0.0.1", "SERVER_PORT=" + strconv.Itoa(port),
		})
		waitProcessHTTP(t, p, port, "/health")
		// Health readiness follows automatic installation and normal startup;
		// finish shutdown before checking the installation marker and captured setup output.
		require.NoError(t, p.cmd.Process.Signal(syscall.SIGTERM))
		require.NoError(t, p.wait(t, 40*time.Second), p.output.text())
		_, err = os.Stat(filepath.Join(dir, ".installed"))
		require.NoError(t, err)
		require.Contains(t, p.output.text(), "Auto setup mode enabled")
	})
}
