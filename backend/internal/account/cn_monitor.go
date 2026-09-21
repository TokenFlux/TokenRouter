// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	json "encoding/json"
	errors "errors"
	strings "strings"
	sync "sync"
	time "time"

	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	errgroup "golang.org/x/sync/errgroup"
)

const (
	CNUsageMonitorSnapshotVersion = 1
	cnUsageMonitorLeaderLockKey   = "cn:usage:monitor:leader"
	cnUsageMonitorReasonPrefix    = "cn_usage_monitor:"
)

// CNMonitorStore 只提供监控所需的账号读取、快照 CAS 和身份条件健康写入。
type CNMonitorStore interface {
	GetByID(context.Context, int64) (*Record, error)
	ListByPlatform(context.Context, string) ([]Record, error)
	UpdateCNUsageMonitorSnapshotCAS(context.Context, int64, time.Time, *CNUsageMonitorSnapshot, string) (bool, error)
	SetCNUsageDecisionCAS(context.Context, int64, time.Time, time.Time, string, bool) (bool, error)
}
type CNMonitorQueries interface {
	QueryAccount(context.Context, int64) (*UpstreamUsageQueryResult, error)
}
type CNMonitorLeader interface {
	TryAcquireLeaderLock(context.Context, string, string, time.Duration) (bool, error)
	ReleaseLeaderLock(context.Context, string, string) error
}
type CNMonitorOptions struct {
	Enabled                              bool
	Interval, ProbeTimeout, RoundTimeout time.Duration
	Concurrency                          int
	BalanceThreshold                     float64
	HostPolicy                           egress.MonitorHostPolicy
	Now                                  func() time.Time
	InstanceID                           string
	Leader                               CNMonitorLeader
	Advisory                             func(context.Context, string) (func(), bool)
	Warn, Debug                          func(string, ...any)
}

// CNUsageMonitor 拥有周期、身份复核与健康决策；手动 API Key 查询独立于此写入能力。
type CNUsageMonitor struct {
	accountRepo                          CNMonitorStore
	usageService                         CNMonitorQueries
	options                              CNMonitorOptions
	interval, probeTimeout, roundTimeout time.Duration
	concurrency                          int
	mu                                   sync.Mutex
	started, stopped                     bool
	cancel                               context.CancelFunc
	activity                             operationActivity
}

func NewCNUsageMonitor(store CNMonitorStore, queries CNMonitorQueries, options CNMonitorOptions) *CNUsageMonitor {
	if options.Interval <= 0 {
		options.Interval = 10 * time.Minute
	}
	if options.ProbeTimeout <= 0 {
		options.ProbeTimeout = 20 * time.Second
	}
	if options.RoundTimeout <= 0 {
		options.RoundTimeout = 5 * time.Minute
	}
	if options.Concurrency <= 0 {
		options.Concurrency = 4
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	if options.Debug == nil {
		options.Debug = func(string, ...any) {}
	}
	options.HostPolicy = options.HostPolicy.Clone()
	return &CNUsageMonitor{accountRepo: store, usageService: queries, options: options, interval: options.Interval, probeTimeout: options.ProbeTimeout, roundTimeout: options.RoundTimeout, concurrency: options.Concurrency}
}

// Start 保留首次等待整周期；构造、禁用和停止后的再次启动均不产生后台任务。
func (s *CNUsageMonitor) Start() { _ = s.StartContext(context.Background()) }
func (s *CNUsageMonitor) StartContext(parent context.Context) error {
	if s == nil || s.accountRepo == nil || s.usageService == nil || !s.options.Enabled {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return nil
	}
	loopParent, cancel := context.WithCancel(parent)
	ctx, finish, err := s.activity.begin(loopParent, ErrCNMonitorStopped)
	if err != nil {
		cancel()
		return err
	}
	s.started = true
	s.cancel = cancel
	go func() {
		defer cancel()
		defer finish()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.RunOnce(ctx)
			}
		}
	}()
	return nil
}

var ErrCNMonitorStopped = errors.New("CN usage monitor is stopped")

func (s *CNUsageMonitor) Stop() { _ = s.StopContext(context.Background()) }
func (s *CNUsageMonitor) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	return s.activity.stop(ctx, "CN usage monitor")
}

// acquireLease 保留竞争失败跳过、Redis 故障回退 DB 以及无后端时执行的原策略。
func (s *CNUsageMonitor) acquireLease(ctx context.Context) (func(), bool) {
	return AcquireSingletonLease(ctx, s.options.Leader, s.options.Advisory, cnUsageMonitorLeaderLockKey, s.options.InstanceID, s.roundTimeout+30*time.Second)
}

func (s *CNUsageMonitor) RunOnce(parents ...context.Context) {
	parent := context.Background()
	if len(parents) > 0 && parents[0] != nil {
		parent = parents[0]
	}
	if s == nil {
		return
	}
	if s.accountRepo == nil || s.usageService == nil {
		return
	}
	parent, finish, err := s.activity.begin(parent, ErrCNMonitorStopped)
	if err != nil {
		return
	}
	defer finish()
	roundCtx, cancel := context.WithTimeout(parent, s.roundTimeout)
	defer cancel()
	release, acquired := s.acquireLease(roundCtx)
	if !acquired {
		return
	}
	defer release()

	accounts := s.monitorCandidates(roundCtx)
	group, groupCtx := errgroup.WithContext(roundCtx)
	group.SetLimit(s.concurrency)
	for i := range accounts {
		accountID := accounts[i].ID
		group.Go(func() error {
			s.probeOne(groupCtx, accountID)
			return nil
		})
	}
	_ = group.Wait()
}

func (s *CNUsageMonitor) monitorCandidates(ctx context.Context) []Record {
	result := make([]Record, 0)
	for _, platform := range []string{PlatformKimi, PlatformZhipu, PlatformDeepseek} {
		if ctx.Err() != nil {
			return result
		}
		accounts, err := s.accountRepo.ListByPlatform(ctx, platform)
		if err != nil {
			s.options.Warn("cn_usage_monitor_list_failed", "platform", platform, "error", err)
			continue
		}
		for i := range accounts {
			account := accounts[i]
			if account.Type != AccountTypeAPIKey || account.Status != StatusActive {
				continue
			}
			queryConfig, err := EffectiveUpstreamUsageConfig(&account)
			if err != nil || !queryConfig.Enabled || CNUpstreamUsageAdapterName(&account) == "" {
				continue
			}
			result = append(result, account)
		}
	}
	return result
}

func (s *CNUsageMonitor) probeOne(parent context.Context, accountID int64) {
	if parent.Err() != nil {
		return
	}
	account, err := s.accountRepo.GetByID(parent, accountID)
	if err != nil || account == nil {
		return
	}
	queryConfig, err := EffectiveUpstreamUsageConfig(account)
	if err != nil || !queryConfig.Enabled {
		return
	}
	queryConfig.Adapter = CNUpstreamUsageAdapterName(account)
	if queryConfig.Adapter == "" {
		return
	}
	identityHash := cnMonitorFingerprint(account, queryConfig)
	if err := s.validateMonitorHost(account, queryConfig); err != nil {
		s.persistAttempt(parent, account, queryConfig, identityHash, nil, err)
		return
	}

	probeCtx, cancel := context.WithTimeout(parent, s.probeTimeout)
	result, queryErr := s.usageService.QueryAccount(probeCtx, accountID)
	cancel()
	if queryErr == nil && result != nil {
		s.applyBalanceDecision(parent, accountID, identityHash, result)
	}
	current, err := s.accountRepo.GetByID(parent, accountID)
	if err != nil || current == nil {
		return
	}
	s.persistAttempt(parent, current, queryConfig, identityHash, result, queryErr)
}

func (s *CNUsageMonitor) persistAttempt(
	ctx context.Context,
	account *Record,
	queryConfig UpstreamUsageQueryConfig,
	identityHash string,
	result *UpstreamUsageQueryResult,
	queryErr error,
) {
	if account == nil || ctx.Err() != nil {
		return
	}
	currentConfig, err := EffectiveUpstreamUsageConfig(account)
	if err != nil {
		return
	}
	currentConfig.Adapter = CNUpstreamUsageAdapterName(account)
	if currentConfig != queryConfig || cnMonitorFingerprint(account, currentConfig) != identityHash {
		return
	}
	now := s.options.Now().UTC()
	snapshot := CNUsageMonitorSnapshotFromExtra(account.Extra)
	if snapshot == nil || snapshot.Version != CNUsageMonitorSnapshotVersion || snapshot.IdentityHash != identityHash {
		snapshot = &CNUsageMonitorSnapshot{
			Version:      CNUsageMonitorSnapshotVersion,
			Adapter:      queryConfig.Adapter,
			IdentityHash: identityHash,
		}
	}
	snapshot.LastAttemptAt = now
	if queryErr != nil || result == nil {
		code := infraerrors.Reason(queryErr)
		if code == "" {
			code = "CN_USAGE_MONITOR_QUERY_FAILED"
		}
		snapshot.LastError = &CNUsageMonitorError{Code: code, ObservedAt: now}
	} else {
		observedAt := result.ObservedAt.UTC()
		snapshot.Adapter = result.Adapter
		snapshot.Provider = result.Provider
		snapshot.Mode = result.Mode
		snapshot.Unit = result.Unit
		snapshot.Balance = result.Balance
		snapshot.Balances = result.Balances
		snapshot.Available = result.Available
		snapshot.Limits = result.Limits
		snapshot.Subscription = result.Subscription
		snapshot.ExpiresAt = result.ExpiresAt
		snapshot.ObservedAt = &observedAt
		snapshot.LastError = nil
	}
	written, err := s.accountRepo.UpdateCNUsageMonitorSnapshotCAS(
		ctx,
		account.ID,
		account.UpdatedAt,
		snapshot,
		"",
	)
	if err != nil {
		s.options.Warn("cn_usage_monitor_snapshot_failed", "account_id", account.ID, "error", err)
	} else if !written {
		s.options.Debug("cn_usage_monitor_snapshot_stale", "account_id", account.ID)
	}
}

func (s *CNUsageMonitor) applyBalanceDecision(
	ctx context.Context,
	accountID int64,
	identityHash string,
	result *UpstreamUsageQueryResult,
) {
	if result == nil || result.Mode != "balance" || ctx.Err() != nil {
		return
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil {
		return
	}
	queryConfig, err := EffectiveUpstreamUsageConfig(account)
	if err != nil {
		return
	}
	queryConfig.Adapter = CNUpstreamUsageAdapterName(account)
	if cnMonitorFingerprint(account, queryConfig) != identityHash {
		return
	}
	threshold := s.options.BalanceThreshold
	low, known := CNUsageBalanceBelowThreshold(result, threshold)
	if !known {
		return
	}
	reason := CNUsageMonitorReason(identityHash)
	if low {
		if !account.IsSchedulable() {
			return
		}
		until := s.options.Now().Add(2 * s.interval)
		if _, err := s.accountRepo.SetCNUsageDecisionCAS(ctx, account.ID, account.UpdatedAt, until, reason, false); err != nil {
			s.options.Warn("cn_usage_monitor_pause_failed", "account_id", account.ID, "error", err)
		}
		return
	}
	if account.TempUnschedulableUntil != nil && account.TempUnschedulableReason == reason {
		if _, err := s.accountRepo.SetCNUsageDecisionCAS(ctx, account.ID, account.UpdatedAt, time.Time{}, reason, true); err != nil {
			s.options.Warn("cn_usage_monitor_resume_failed", "account_id", account.ID, "error", err)
		}
	}
}

func CNUsageBalanceBelowThreshold(result *UpstreamUsageQueryResult, threshold float64) (bool, bool) {
	if result == nil || result.Mode != "balance" {
		return false, false
	}
	if result.Available != nil && !*result.Available {
		return true, true
	}
	if len(result.Balances) > 0 {
		for _, balance := range result.Balances {
			if balance.Remaining >= threshold {
				return false, true
			}
		}
		return true, true
	}
	if result.Balance == nil || result.Balance.Remaining == nil {
		return false, false
	}
	return *result.Balance.Remaining < threshold, true
}

func CNUsageMonitorReason(identityHash string) string {
	return cnUsageMonitorReasonPrefix + identityHash + ": 余额低于监控阈值"
}

func CNUsageMonitorSnapshotFromExtra(extra map[string]any) *CNUsageMonitorSnapshot {
	if len(extra) == 0 || extra[CNUsageMonitorSnapshotExtraKey] == nil {
		return nil
	}
	payload, err := json.Marshal(extra[CNUsageMonitorSnapshotExtraKey])
	if err != nil {
		return nil
	}
	var snapshot CNUsageMonitorSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil
	}
	return &snapshot
}

// ValidCNUsageMonitorSnapshot 只返回与账号当前完整查询身份匹配的快照。
func ValidCNUsageMonitorSnapshot(account *Record) *CNUsageMonitorSnapshot {
	if account == nil || !account.IsCNProvider() {
		return nil
	}
	queryConfig, err := EffectiveUpstreamUsageConfig(account)
	if err != nil {
		return nil
	}
	queryConfig.Adapter = CNUpstreamUsageAdapterName(account)
	if queryConfig.Adapter == "" {
		return nil
	}
	snapshot := CNUsageMonitorSnapshotFromExtra(account.Extra)
	if snapshot == nil || snapshot.Version != CNUsageMonitorSnapshotVersion ||
		snapshot.Adapter != queryConfig.Adapter ||
		snapshot.IdentityHash != cnMonitorFingerprint(account, queryConfig) {
		return nil
	}
	return snapshot
}

func CNUsageOfficialHost(platform, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	switch platform {
	case PlatformKimi:
		return host == "api.moonshot.cn" || host == "api.kimi.com"
	case PlatformZhipu:
		return host == "open.bigmodel.cn" || host == "api.z.ai"
	case PlatformDeepseek:
		return host == "api.deepseek.com"
	default:
		return false
	}
}
func cnMonitorFingerprint(value *Record, config UpstreamUsageQueryConfig) string {
	return UpstreamUsageContextFingerprint(value, config, value.OpenAIBaseURL(value.ConfiguredAPIProtocol() == APIProtocolAdaptive))
}
func (s *CNUsageMonitor) validateMonitorHost(value *Record, config UpstreamUsageQueryConfig) error {
	base := strings.TrimSpace(config.BaseURL)
	if base == "" {
		base = value.OpenAIBaseURL(value.ConfiguredAPIProtocol() == APIProtocolAdaptive)
	}
	err := s.options.HostPolicy.Validate(base, func(host string) bool { return CNUsageOfficialHost(value.Platform, host) })
	if errors.Is(err, egress.ErrMonitorURLInvalid) {
		return ErrUpstreamUsageConfigInvalid
	}
	return err
}
