//go:build integration

package app_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"gopkg.in/yaml.v3"
)

type processOutput struct {
	sync.Mutex
	bytes.Buffer
}

func (o *processOutput) Write(p []byte) (int, error) {
	o.Lock()
	defer o.Unlock()
	return o.Buffer.Write(p)
}
func (o *processOutput) text() string { o.Lock(); defer o.Unlock(); return o.String() }

type testProcess struct {
	cmd    *exec.Cmd
	done   chan struct{}
	err    error
	output *processOutput
	stdin  io.WriteCloser
}

func startTestProcess(t *testing.T, binary, dir string, env []string, args ...string) *testProcess {
	t.Helper()
	p := &testProcess{cmd: exec.Command(binary, args...), done: make(chan struct{}), output: &processOutput{}}
	p.cmd.Dir = dir
	// 子进程只接收测试配置，不能继承开发机的数据库或供应商密钥。
	p.cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + os.TempDir(), "TZ=UTC", "DATA_DIR=" + dir}, env...)
	p.cmd.Stdout = p.output
	p.cmd.Stderr = p.output
	var err error
	p.stdin, err = p.cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, p.cmd.Start())
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Kill()
			<-p.done
		}
		_ = p.stdin.Close()
	})
	return p
}

func (p *testProcess) wait(t *testing.T, timeout time.Duration) error {
	t.Helper()
	select {
	case <-p.done:
		return p.err
	case <-time.After(timeout):
		t.Fatalf("进程未在预算内退出：\n%s", p.output.text())
		return nil
	}
}

func (p *testProcess) prompt(t *testing.T, prompt, value string) {
	t.Helper()
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
		time.Sleep(10 * time.Millisecond)
	}
	_, err := io.WriteString(p.stdin, value+"\n")
	require.NoError(t, err)
}

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

func waitProcessHTTP(t *testing.T, p *testProcess, port int, path string) string {
	t.Helper()
	client := &http.Client{Timeout: 500 * time.Millisecond}
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
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK {
				return string(body)
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("HTTP 未就绪：\n%s", p.output.text())
	return ""
}

func TestS02ProcessModes(t *testing.T) {
	backendRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	binary := filepath.Join(t.TempDir(), "server")
	build := exec.Command("go", "build", "-ldflags=-X main.Version=s02-contract -X main.Commit=s02-head -X main.Date=s02-date -X main.BuildType=test", "-o", binary, "./cmd/server")
	build.Dir = backendRoot
	build.Env = append(os.Environ(), "GOTOOLCHAIN=go1.27.0")
	buildOutput, err := build.CombinedOutput()
	require.NoError(t, err, string(buildOutput))
	t.Run("version", func(t *testing.T) {
		p := startTestProcess(t, binary, t.TempDir(), nil, "-version")
		require.NoError(t, p.wait(t, 30*time.Second))
		require.Contains(t, p.output.text(), "Sub2API s02-contract (commit: s02-head, built: s02-date)")
		require.NotContains(t, p.output.text(), "[Lifecycle] started")
	})
	fixture := newDatabaseFixture(t)
	ctx := context.Background()
	rdb, err := tcredis.Run(ctx, "redis:8.4-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rdb.Terminate(context.Background())) })
	redisHost, err := rdb.Host(ctx)
	require.NoError(t, err)
	redisPort, err := rdb.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)
	pricing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"s02-model":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002}}`)
	}))
	t.Cleanup(pricing.Close)
	configFor := func(t *testing.T, mode, dbname string, port int) (string, []string) {
		t.Helper()
		dir := t.TempDir()
		priceFile := filepath.Join(dir, "prices.json")
		require.NoError(t, os.WriteFile(priceFile, []byte(`{"s02-model":{"input_cost_per_token":0.000001}}`), 0600))
		cfg := map[string]any{
			"run_mode": mode, "timezone": "UTC",
			"server":   map[string]any{"host": "127.0.0.1", "port": port, "mode": "release"},
			"database": map[string]any{"host": fixture.host, "port": fixture.port, "user": "postgres", "password": "postgres", "dbname": dbname, "sslmode": "disable", "max_open_conns": 16, "max_idle_conns": 2},
			"redis":    map[string]any{"host": redisHost, "port": redisPort.Int(), "pool_size": 16, "min_idle_conns": 0},
			"pricing":  map[string]any{"remote_url": "", "hash_url": "", "data_dir": dir, "fallback_file": priceFile},
			"log":      map[string]any{"level": "info", "output": map[string]any{"to_stdout": true, "to_file": false}},
		}
		data, err := yaml.Marshal(cfg)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0600))
		return dir, []string{"PGAPPNAME=s02-" + mode}
	}
	for _, mode := range []string{"standard", "simple"} {
		t.Run(mode+"-sigterm", func(t *testing.T) {
			port := freeServerPort(t)
			dir, env := configFor(t, mode, "s02_contracts", port)
			p := startTestProcess(t, binary, dir, env)
			waitProcessHTTP(t, p, port, "/health")
			require.NoError(t, p.cmd.Process.Signal(syscall.SIGTERM))
			require.NoError(t, p.wait(t, 40*time.Second), p.output.text())
			logs := p.output.text()
			// 定价迁移后仍只有一个运行实例，初始化先于调度，信号退出等待其停止。
			require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started PricingInitialization"))
			require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started PricingService"))
			require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped PricingService"))
			require.Less(t, strings.Index(logs, "started PricingInitialization"), strings.Index(logs, "started PricingService"))
			require.Less(t, strings.Index(logs, "stopped PricingService"), strings.Index(logs, "stopped Redis"))
			for _, name := range []string{"HTTPRequests", "DeferredService", "TimingWheelService", "UsageLogBatchers", "Redis", "Ent"} {
				require.Contains(t, logs, "[Lifecycle] stopped "+name)
			}
			// S04 的资金运行组件各启动一次，所有资金队列完成后才关闭 Redis。
			for _, name := range []string{"BillingCacheService", "UserPlatformQuotaUsageFlusher", "SubscriptionExpiryService"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name))
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name))
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"))
			}
			// S05 认证资源在完整请求结束后退出；持久化延迟 outbox 不等同于全部排空。
			for _, name := range []string{"APIKeyService", "AuthCacheInvalidationWorker"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name))
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name))
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"))
			}
			require.Less(t, strings.Index(logs, "stopped HTTPRequests"), strings.Index(logs, "stopped APIKeyService"))
			require.Less(t, strings.Index(logs, "stopped AuthCacheInvalidationWorker"), strings.Index(logs, "stopped APIKeyService"))
			require.Less(t, strings.Index(logs, "stopped BillingCacheService"), strings.Index(logs, "stopped UserPlatformQuotaUsageFlusher"))
			require.Less(t, strings.Index(logs, "stopped TimingWheelService"), strings.Index(logs, "stopped Redis"))
			require.Less(t, strings.Index(logs, "stopped Redis"), strings.Index(logs, "stopped Ent"))
			// S06 的周期维护只启动一次；生产者停止后才结束共享刷新、查询与技术依赖。
			for _, name := range []string{"TokenRefreshService", "AccountExpiryService", "ProxyExpiryService", "ScheduledTestRunnerService", "GroupAvailabilityProbeRunnerService", "CNProviderBalanceCheckService", "OllamaCloudUsageService", "DeferredService", "TLSFingerprintProfileService", "TLSFingerprintRouterService"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name), name)
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
			}
			for _, name := range []string{"AccountRefreshCoordinator", "AccountOAuthUsage", "AccountUpstreamUsage", "AccountImportProbes", "AccountPrivacy", "AccountTier", "AccountModelList", "GrokQuotaProbes", "TLSFingerprintCollectorService"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
			}
			require.Less(t, strings.Index(logs, "stopped HTTPRequests"), strings.Index(logs, "stopped TokenRefreshService"))
			require.Less(t, strings.Index(logs, "stopped TokenRefreshService"), strings.Index(logs, "stopped AccountRefreshCoordinator"))
			require.Less(t, strings.Index(logs, "stopped DeferredService"), strings.Index(logs, "stopped TimingWheelService"))

			// S07 的快照、并发和串行队列各启动一次；实际请求结束后才停止，随后关闭存储。
			for _, name := range []string{"SchedulerSnapshotService", "ConcurrencyService", "UserMessageQueueService"} {
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] started "+name), name)
				require.Equal(t, 1, strings.Count(logs, "[Lifecycle] stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped HTTPRequests"), strings.Index(logs, "stopped "+name), name)
				require.Less(t, strings.Index(logs, "stopped "+name), strings.Index(logs, "stopped Redis"), name)
			}
			require.NotContains(t, logs, "[Lifecycle] started TLSFingerprintCollectorService")
			require.Eventually(t, func() bool {
				var n int
				err := fixture.db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE application_name=$1`, "s02-"+mode).Scan(&n)
				return err == nil && n == 0
			}, 3*time.Second, 25*time.Millisecond)
		})
	}

	// JWT 维护命令只初始化用户读取和签发能力，参数/输出与真实签名保持兼容。
	t.Run("jwtgen-minimal", func(t *testing.T) {
		tool := filepath.Join(t.TempDir(), "jwtgen")
		build := exec.Command("go", "build", "-o", tool, "./cmd/jwtgen")
		build.Dir = backendRoot
		build.Env = append(os.Environ(), "GOTOOLCHAIN=go1.27.0")
		data, e := build.CombinedOutput()
		require.NoError(t, e, string(data))
		var id int64
		email := "s05-jwtgen@example.com"
		require.NoError(t, fixture.db.QueryRow("INSERT INTO users(email,password_hash,role,status) VALUES($1,'fixture','admin','active') RETURNING id", email).Scan(&id))
		defer func() { _, e := fixture.db.Exec("DELETE FROM users WHERE id=$1", id); require.NoError(t, e) }()
		var secret string
		require.NoError(t, fixture.db.QueryRow("SELECT value FROM security_secrets WHERE key='jwt_secret'").Scan(&secret))
		for _, args := range [][]string{nil, {"-email", email}} {
			dir, env := configFor(t, "standard", "s02_contracts", freeServerPort(t))
			p := startTestProcess(t, tool, dir, env, args...)
			require.NoError(t, p.wait(t, 30*time.Second))
			output := p.output.text()
			require.Contains(t, output, "ADMIN_EMAIL="+email)
			require.Contains(t, output, fmt.Sprintf("ADMIN_USER_ID=%d", id))
			require.NotContains(t, output, "[Lifecycle] started")
			token := ""
			for _, line := range strings.Split(output, "\n") {
				if strings.HasPrefix(line, "JWT=") {
					token = strings.TrimPrefix(line, "JWT=")
				}
			}
			verifier := identity.NewSessionService(identity.SessionOptions{Secret: secret}, nil, nil, nil, nil)
			claims, e := verifier.ValidateToken(token)
			require.NoError(t, e)
			require.Equal(t, id, claims.UserID)
			require.Equal(t, "admin", claims.Role)
		}
	})

	t.Run("bootstrap-failure-closes-connection", func(t *testing.T) {
		var original string
		require.NoError(t, fixture.db.QueryRow(`SELECT value FROM security_secrets WHERE key='jwt_secret'`).Scan(&original))
		_, err := fixture.db.Exec(`UPDATE security_secrets SET value='short' WHERE key='jwt_secret'`)
		require.NoError(t, err)
		defer func() {
			_, err := fixture.db.Exec(`UPDATE security_secrets SET value=$1 WHERE key='jwt_secret'`, original)
			require.NoError(t, err)
		}()
		dir, env := configFor(t, "standard", "s02_contracts", freeServerPort(t))
		p := startTestProcess(t, binary, dir, env)
		require.Error(t, p.wait(t, 30*time.Second))
		require.Contains(t, p.output.text(), "must be at least 32 bytes")
		require.NotContains(t, p.output.text(), "[Lifecycle] started")
		require.Eventually(t, func() bool {
			var n int
			err := fixture.db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE application_name='s02-standard'`).Scan(&n)
			return err == nil && n == 0
		}, 3*time.Second, 25*time.Millisecond)
	})
	t.Run("listen-failure-cleans-resources", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = ln.Close() }()
		address, ok := ln.Addr().(*net.TCPAddr)
		require.True(t, ok)
		dir, env := configFor(t, "standard", "s02_contracts", address.Port)
		p := startTestProcess(t, binary, dir, env)
		require.Error(t, p.wait(t, 60*time.Second))
		require.Contains(t, p.output.text(), "address already in use")
		require.Contains(t, p.output.text(), "[Lifecycle] stopped Redis")
		require.Contains(t, p.output.text(), "[Lifecycle] stopped Ent")
	})
	t.Run("setup-server-only", func(t *testing.T) {
		port := freeServerPort(t)
		p := startTestProcess(t, binary, t.TempDir(), []string{"SERVER_HOST=127.0.0.1", "SERVER_PORT=" + strconv.Itoa(port)})
		require.Contains(t, waitProcessHTTP(t, p, port, "/setup/status"), `"needs_setup":true`)
		require.NoError(t, p.cmd.Process.Signal(syscall.SIGTERM))
		require.NoError(t, p.wait(t, 10*time.Second))
		require.NotContains(t, p.output.text(), "[Lifecycle] started")
	})
	t.Run("cli-setup", func(t *testing.T) {
		_, err := fixture.db.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier("s02_cli"))
		require.NoError(t, err)
		dir := t.TempDir()
		p := startTestProcess(t, binary, dir, nil, "-setup")
		// 按提示逐行输入，使普通 stdin 的密码读取不受其它 reader 预读影响。
		for _, step := range [][2]string{
			{"PostgreSQL Host", fixture.host}, {"PostgreSQL Port", strconv.Itoa(fixture.port)}, {"PostgreSQL User", "postgres"}, {"PostgreSQL Password", "postgres"}, {"Database Name", "s02_cli"}, {"SSL Mode", "disable"},
			{"Redis Host", redisHost}, {"Redis Port", strconv.Itoa(redisPort.Int())}, {"Redis Password", ""}, {"Redis DB", "0"}, {"Enable Redis TLS?", "n"},
			{"Admin Email", "s02-cli@example.test"}, {"Admin Password", "s02-test-password"}, {"Confirm Password", "s02-test-password"}, {"Server Port", strconv.Itoa(freeServerPort(t))}, {"Proceed with installation?", "y"},
		} {
			p.prompt(t, step[0], step[1])
		}
		require.NoError(t, p.wait(t, 90*time.Second), p.output.text())
		require.Contains(t, p.output.text(), "Installation Complete!")
		info, err := os.Stat(filepath.Join(dir, "config.yaml"))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		_, err = os.Stat(filepath.Join(dir, ".installed"))
		require.NoError(t, err)
		require.NotContains(t, p.output.text(), "[Lifecycle] started")
	})
	t.Run("auto-setup", func(t *testing.T) {
		_, err := fixture.db.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier("s02_auto"))
		require.NoError(t, err)
		dir := t.TempDir()
		port := freeServerPort(t)
		p := startTestProcess(t, binary, dir, []string{
			"AUTO_SETUP=true", "DATABASE_HOST=" + fixture.host, "DATABASE_PORT=" + strconv.Itoa(fixture.port), "DATABASE_USER=postgres", "DATABASE_PASSWORD=postgres", "DATABASE_DBNAME=s02_auto", "DATABASE_SSLMODE=disable",
			"REDIS_HOST=" + redisHost, "REDIS_PORT=" + strconv.Itoa(redisPort.Int()), "ADMIN_EMAIL=s02-auto@example.test", "ADMIN_PASSWORD=s02-test-password", "SERVER_HOST=127.0.0.1", "SERVER_PORT=" + strconv.Itoa(port),
			"PRICING_REMOTE_URL=" + pricing.URL, "PRICING_HASH_URL=" + pricing.URL, "PRICING_DATA_DIR=" + dir, "LOG_OUTPUT_TO_FILE=false",
		})
		waitProcessHTTP(t, p, port, "/health")
		require.NoError(t, p.cmd.Process.Signal(syscall.SIGTERM))
		require.NoError(t, p.wait(t, 40*time.Second), p.output.text())
		_, err = os.Stat(filepath.Join(dir, ".installed"))
		require.NoError(t, err)
		require.Contains(t, p.output.text(), "Auto setup mode enabled")
	})
}
