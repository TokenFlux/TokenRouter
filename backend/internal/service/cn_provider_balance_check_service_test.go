package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"

	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

type cnUsageMonitorRepo struct {
	gatewayprovider.ExecutionAccountStore

	mu               sync.Mutex
	accounts         map[int64]*gatewayprovider.ExecutionAccount
	byPlatform       map[string][]int64
	writes           []*acctcore.CNUsageMonitorSnapshot
	casResult        bool
	pauseReason      string
	pauseUntil       time.Time
	clearCalls       int
	updateExtraCalls int
}

func (r *cnUsageMonitorRepo) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.accounts[id]
	if account == nil {
		return nil, acctcore.ErrAccountNotFound
	}
	copy := *account
	return &copy, nil
}

func (r *cnUsageMonitorRepo) ListByPlatform(_ context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := r.byPlatform[platform]
	result := make([]gatewayprovider.ExecutionAccount, 0, len(ids))
	for _, id := range ids {
		if account := r.accounts[id]; account != nil {
			result = append(result, *account)
		}
	}
	return result, nil
}

func (r *cnUsageMonitorRepo) UpdateCNUsageMonitorSnapshotCAS(
	_ context.Context,
	accountID int64,
	expectedUpdatedAt time.Time,
	snapshot *acctcore.CNUsageMonitorSnapshot,
	_ string,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.accounts[accountID]
	if account == nil || !account.Record.UpdatedAt.Equal(expectedUpdatedAt) || !r.casResult {
		return false, nil
	}
	copy := *snapshot
	r.writes = append(r.writes, &copy)
	return true, nil
}

func (r *cnUsageMonitorRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pauseReason = reason
	r.pauseUntil = until
	if account := r.accounts[id]; account != nil {
		account.Record.TempUnschedulableUntil = &until
		account.Record.TempUnschedulableReason = reason
	}
	return nil
}

func (r *cnUsageMonitorRepo) ClearTempUnschedulable(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearCalls++
	if account := r.accounts[id]; account != nil {
		account.Record.TempUnschedulableUntil = nil
		account.Record.TempUnschedulableReason = ""
	}
	return nil
}

func (r *cnUsageMonitorRepo) UpdateExtra(_ context.Context, _ int64, _ map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateExtraCalls++
	return nil
}

type cnUsageMonitorHTTP struct {
	mu       sync.Mutex
	calls    int
	requests []*http.Request
	status   int
	body     string
	started  chan struct{}
	block    bool
}

func (h *cnUsageMonitorHTTP) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	return h.DoWithTLS(req, proxyURL, accountID, concurrency, nil)
}

func (h *cnUsageMonitorHTTP) DoWithTLS(
	req *http.Request,
	_ string,
	_ int64,
	_ int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	h.mu.Lock()
	h.calls++
	h.requests = append(h.requests, req.Clone(req.Context()))
	started := h.started
	block := h.block
	status := h.status
	body := h.body
	h.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if block {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
}

type cnUsageMonitorLeaderLock struct {
	acquired bool
	calls    int
}

func (l *cnUsageMonitorLeaderLock) TryAcquireLeaderLock(context.Context, string, string, time.Duration) (bool, error) {
	l.calls++
	return l.acquired, nil
}

func (*cnUsageMonitorLeaderLock) ReleaseLeaderLock(context.Context, string, string) error { return nil }

func newCNUsageMonitorAccount(id int64, platform, mode string) *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: acctcore.Record{LoadLocation: time.LoadLocation, ID: id,
		Platform:    platform,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		UpdatedAt:   time.Date(2026, 8, 23, 1, 0, 0, 0, time.UTC),
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"account_mode": mode,
		},
		Extra: map[string]any{}},
	}
}

func newCNUsageMonitorForTest(repo *cnUsageMonitorRepo, upstream httpclient.UpstreamTransport, cfg *config.Config, configure ...func(*acctcore.CNMonitorOptions)) *acctcore.CNUsageMonitor {
	usage := NewUpstreamUsageService(repo, upstream, cfg, nil)
	return newCNMonitorLegacyFixture(repo, usage, cfg, configure...)
}

func TestCNUsageMonitorRunOncePersistsUnifiedSnapshotWithoutLegacyWrites(t *testing.T) {
	account := newCNUsageMonitorAccount(1, capability.PlatformKimi, acctcore.AccountModePayG)
	repo := &cnUsageMonitorRepo{
		accounts:   map[int64]*gatewayprovider.ExecutionAccount{1: account},
		byPlatform: map[string][]int64{capability.PlatformKimi: {1}},
		casResult:  true,
	}
	upstream := &cnUsageMonitorHTTP{body: `{"code":0,"data":{"available_balance":12.5}}`}
	service := newCNUsageMonitorForTest(repo, upstream, testUpstreamUsageConfig())
	service.RunOnce(context.Background())

	require.Len(t, repo.writes, 1)
	snapshot := repo.writes[0]
	require.Equal(t, acctcore.CNUsageMonitorSnapshotVersion, snapshot.Version)
	require.Equal(t, acctcore.UpstreamUsageAdapterKimiBalance, snapshot.Adapter)
	require.Equal(t, capability.PlatformKimi, snapshot.Provider)
	require.Equal(t, "balance", snapshot.Mode)
	require.NotNil(t, snapshot.Balance)
	require.InDelta(t, 12.5, *snapshot.Balance.Remaining, 1e-9)
	require.Empty(t, snapshot.LastError)
	require.Zero(t, repo.updateExtraCalls, "纯适配器和协调器不得调用通用 UpdateExtra")
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "api.moonshot.cn", upstream.requests[0].URL.Hostname())
	require.True(t, upstreamcore.HTTPUpstreamRedirectsDisabled(upstream.requests[0].Context()))
}

func TestCNUsageMonitorFailurePreservesLastSuccess(t *testing.T) {
	account := newCNUsageMonitorAccount(2, capability.PlatformDeepseek, acctcore.AccountModePayG)
	queryConfig, err := EffectiveUpstreamUsageConfig(account)
	require.NoError(t, err)
	queryConfig.Adapter = cnUpstreamUsageAdapterName(account)
	observed := time.Date(2026, 8, 22, 3, 0, 0, 0, time.UTC)
	remaining := 8.0
	account.Record.Extra[acctcore.CNUsageMonitorSnapshotExtraKey] = &acctcore.CNUsageMonitorSnapshot{
		Version:       acctcore.CNUsageMonitorSnapshotVersion,
		Adapter:       queryConfig.Adapter,
		IdentityHash:  upstreamUsageContextFingerprint(account, queryConfig),
		Provider:      capability.PlatformDeepseek,
		Mode:          "balance",
		Unit:          "CNY",
		Balance:       &acctcore.UpstreamUsageAmount{Remaining: &remaining},
		ObservedAt:    &observed,
		LastAttemptAt: observed,
	}
	repo := &cnUsageMonitorRepo{
		accounts:   map[int64]*gatewayprovider.ExecutionAccount{2: account},
		byPlatform: map[string][]int64{capability.PlatformDeepseek: {2}},
		casResult:  true,
	}
	upstream := &cnUsageMonitorHTTP{status: http.StatusBadGateway, body: `{}`}
	service := newCNUsageMonitorForTest(repo, upstream, testUpstreamUsageConfig())
	service.RunOnce(context.Background())

	require.Len(t, repo.writes, 1)
	snapshot := repo.writes[0]
	require.NotNil(t, snapshot.LastError)
	require.Equal(t, observed, *snapshot.ObservedAt)
	require.InDelta(t, remaining, *snapshot.Balance.Remaining, 1e-9)
}

func TestCNUsageMonitorCustomHostRequiresExplicitAllowlist(t *testing.T) {
	account := newCNUsageMonitorAccount(3, capability.PlatformDeepseek, acctcore.AccountModePayG)
	account.Record.Credentials["base_url"] = "https://relay.example/v1"
	repo := &cnUsageMonitorRepo{
		accounts:   map[int64]*gatewayprovider.ExecutionAccount{3: account},
		byPlatform: map[string][]int64{capability.PlatformDeepseek: {3}},
		casResult:  true,
	}
	upstream := &cnUsageMonitorHTTP{body: `{}`}
	cfg := testUpstreamUsageConfig()
	service := newCNUsageMonitorForTest(repo, upstream, cfg)
	service.RunOnce(context.Background())
	require.Zero(t, upstream.calls)
	require.Len(t, repo.writes, 1)
	require.NotNil(t, repo.writes[0].LastError)

	cfg.Security.URLAllowlist.Enabled = true
	cfg.Security.URLAllowlist.UpstreamHosts = []string{"relay.example"}
	repo.writes = nil
	upstream.body = `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"3"}]}`
	service = newCNUsageMonitorForTest(repo, upstream, cfg)
	service.RunOnce(context.Background())
	require.Equal(t, 1, upstream.calls)
	require.Len(t, repo.writes, 1)
	require.Equal(t, "relay.example", upstream.requests[0].URL.Hostname())
}

func TestCNUsageMonitorSkipsCycleWhenNotLeader(t *testing.T) {
	account := newCNUsageMonitorAccount(4, capability.PlatformKimi, acctcore.AccountModePayG)
	repo := &cnUsageMonitorRepo{
		accounts:   map[int64]*gatewayprovider.ExecutionAccount{4: account},
		byPlatform: map[string][]int64{capability.PlatformKimi: {4}},
		casResult:  true,
	}
	upstream := &cnUsageMonitorHTTP{body: `{}`}
	lock := &cnUsageMonitorLeaderLock{acquired: false}
	service := newCNUsageMonitorForTest(repo, upstream, testUpstreamUsageConfig(), func(o *acctcore.CNMonitorOptions) { o.Leader = lock })
	service.RunOnce(context.Background())
	require.Equal(t, 1, lock.calls)
	require.Zero(t, upstream.calls)
	require.Empty(t, repo.writes)
}

func TestCNUsageMonitorDefaultOffAndStopCancelsProbe(t *testing.T) {
	repo := &cnUsageMonitorRepo{accounts: map[int64]*gatewayprovider.ExecutionAccount{}, byPlatform: map[string][]int64{}, casResult: true}
	service := newCNUsageMonitorForTest(repo, &cnUsageMonitorHTTP{}, testUpstreamUsageConfig())
	service.Start()
	// 同一构造无上下文断言迁至 account 的生命周期测试；这里保留外层无探测副作用。
	require.Empty(t, repo.writes)

	account := newCNUsageMonitorAccount(5, capability.PlatformKimi, acctcore.AccountModePayG)
	repo.accounts[5] = account
	repo.byPlatform[capability.PlatformKimi] = []int64{5}
	started := make(chan struct{}, 1)
	upstream := &cnUsageMonitorHTTP{started: started, block: true}
	cfg := testUpstreamUsageConfig()
	cfg.Gateway.CNProviders.MonitorEnabled = true
	service = newCNUsageMonitorForTest(repo, upstream, cfg, func(o *acctcore.CNMonitorOptions) { o.Interval = time.Millisecond })
	service.Start()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("监控探测未启动")
	}
	done := make(chan struct{})
	go func() {
		service.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop 未取消进行中的探测")
	}
}

func TestCNUsageBalanceThresholdUsesAllCurrenciesAndIdentityReason(t *testing.T) {
	available := true
	result := &acctcore.UpstreamUsageQueryResult{
		Mode:      "balance",
		Available: &available,
		Balances: []acctcore.UpstreamUsageBalanceEntry{
			{Currency: "CNY", Remaining: 0.1},
			{Currency: "USD", Remaining: 2},
		},
	}
	low, known := acctcore.CNUsageBalanceBelowThreshold(result, 0.5)
	require.True(t, known)
	require.False(t, low)
	result.Balances[1].Remaining = 0.2
	low, known = acctcore.CNUsageBalanceBelowThreshold(result, 0.5)
	require.True(t, known)
	require.True(t, low)
	require.True(t, strings.HasPrefix(acctcore.CNUsageMonitorReason("abc"), "cn_usage_monitor:abc:"))
}
