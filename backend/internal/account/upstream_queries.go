// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	errors "errors"
	fmt "fmt"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	errgroup "golang.org/x/sync/errgroup"
	singleflight "golang.org/x/sync/singleflight"
	strings "strings"
	sync "sync"
	time "time"
)

// UpstreamUsageReader 只读当前身份，不提供配置、健康或消费写入。
type UpstreamUsageReader interface {
	GetByID(context.Context, int64) (*Record, error)
}

// UpstreamUsageExecution 由具体平台 Adapter 实现固定协议；不得在查询中写健康或资金。
type UpstreamUsageExecution interface {
	Available() bool
	Supports(string) bool
	BaseURL(*Record) string
	Query(context.Context, *Record, UpstreamUsageQueryConfig) (*UpstreamUsageInfo, error)
}
type UpstreamUsageOptions struct{ Now func() time.Time }

// UpstreamUsageService 唯一拥有合并查询、并发槽、指标与停止等待。
type UpstreamUsageService struct {
	accountRepo UpstreamUsageReader
	execution   UpstreamUsageExecution
	queryFlight singleflight.Group
	querySlots  chan struct{}
	slotMu      sync.Mutex
	now         func() time.Time
	metricsMu   sync.Mutex
	metrics     map[string]int64
	activity    operationActivity
}

const (
	upstreamUsageTimeout     = 60 * time.Second
	upstreamUsageBatchLimit  = 100
	upstreamUsageConcurrency = 4
)

var ErrUpstreamUsageStopped = errors.New("upstream usage queries are stopped")

func NewUpstreamUsageService(reader UpstreamUsageReader, execution UpstreamUsageExecution, options UpstreamUsageOptions) *UpstreamUsageService {
	return &UpstreamUsageService{accountRepo: reader, execution: execution, now: options.Now, querySlots: make(chan struct{}, upstreamUsageConcurrency), metrics: make(map[string]int64)}
}

// StopContext 阻止新认领，取消共享查询并等待实际完成；HTTP 等待方取消不终止其他等待方。
func (s *UpstreamUsageService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.activity.stop(ctx, "upstream usage")
}

func (s *UpstreamUsageService) recordMetric(adapter, outcome string) {
	if s == nil {
		return
	}
	adapter = strings.TrimSpace(adapter)
	if adapter == "" {
		adapter = "unknown"
	}
	outcome = strings.TrimSpace(outcome)
	if outcome == "" {
		outcome = "unknown"
	}
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if s.metrics == nil {
		s.metrics = make(map[string]int64)
	}
	s.metrics[adapter+":"+outcome]++
}

// SnapshotMetrics 返回脱敏的适配器/结果分类计数，供监控或测试读取。
func (s *UpstreamUsageService) SnapshotMetrics() UpstreamUsageMetrics {
	result := UpstreamUsageMetrics{Counts: make(map[string]int64)}
	if s == nil {
		return result
	}
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	for key, value := range s.metrics {
		result.Counts[key] = value
	}
	return result
}

// QueryAccount 查询单个账号的实时上游用量。
func (s *UpstreamUsageService) QueryAccount(ctx context.Context, accountID int64) (*UpstreamUsageQueryResult, error) {
	if s == nil || s.accountRepo == nil || s.execution == nil || !s.execution.Available() {
		return nil, ErrUpstreamUsageUnavailable
	}
	if accountID <= 0 {
		return nil, ErrUpstreamUsageAccountInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, finish, err := s.activity.begin(ctx, ErrUpstreamUsageStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	queryDeadline := time.Now().Add(upstreamUsageTimeout)
	preflightCtx, cancelPreflight := context.WithDeadline(ctx, queryDeadline)
	defer cancelPreflight()
	// 先读取一次身份快照，用它生成 singleflight 指纹。这样凭据、代理或
	// 查询配置发生变化时，不会把新请求错误地合并到旧请求中。
	account, err := s.loadQueryAccount(preflightCtx, accountID)
	if err != nil {
		return nil, err
	}
	queryConfig, err := EffectiveUpstreamUsageConfig(account)
	if err != nil {
		return nil, err
	}
	if !queryConfig.Enabled {
		return nil, ErrUpstreamUsageDisabled
	}
	// 国产供应商不允许管理员把协议适配器误选成通用站点适配器；按平台和
	// account_mode 自动选择只读适配器，保留现有查询开关与身份指纹语义。
	if account.IsCNProvider() {
		queryConfig.Adapter = CNUpstreamUsageAdapterName(account)
		if queryConfig.Adapter == "" {
			return nil, ErrUpstreamUsageUnsupported
		}
	}
	if !s.execution.Supports(queryConfig.Adapter) {
		return nil, ErrUpstreamUsageUnsupported
	}
	fingerprint := UpstreamUsageContextFingerprint(account, queryConfig, s.execution.BaseURL(account))
	key := fmt.Sprintf("%d:%s", accountID, fingerprint)
	resultCh := s.queryFlight.DoChan(key, func() (any, error) {
		// 共享操作保留首个调用方的值，但不继承其取消信号；固定截止时间
		// 则把首次身份读取也计入约 60 秒的总预算。
		workCtx, finish, err := s.activity.begin(context.WithoutCancel(ctx), ErrUpstreamUsageStopped)
		if err != nil {
			return nil, err
		}
		defer finish()
		opCtx, cancel := context.WithDeadline(workCtx, queryDeadline)
		defer cancel()
		return s.queryAccount(opCtx, accountID, account, queryConfig)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		queryResult, ok := result.Val.(*UpstreamUsageQueryResult)
		if !ok || queryResult == nil {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		return CloneUpstreamUsageResult(queryResult), nil
	}
}

// QueryBatch 查询多个账号；单账号错误通过 errors 返回，不中断其它账号。
func (s *UpstreamUsageService) QueryBatch(ctx context.Context, accountIDs []int64) (map[int64]*UpstreamUsageQueryResult, map[int64]error, error) {
	if s == nil || s.accountRepo == nil || s.execution == nil || !s.execution.Available() {
		return nil, nil, ErrUpstreamUsageUnavailable
	}
	if len(accountIDs) == 0 {
		return nil, nil, ErrUpstreamUsageBatchInvalid
	}
	if len(accountIDs) > upstreamUsageBatchLimit {
		return nil, nil, ErrUpstreamUsageBatchTooLarge
	}
	unique := make([]int64, 0, len(accountIDs))
	seen := make(map[int64]struct{}, len(accountIDs))
	errorsByID := make(map[int64]error)
	for _, id := range accountIDs {
		if id <= 0 {
			errorsByID[id] = ErrUpstreamUsageAccountInvalid
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	results := make(map[int64]*UpstreamUsageQueryResult, len(unique))
	if len(unique) == 0 {
		return results, errorsByID, nil
	}
	var mu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(upstreamUsageConcurrency)
	for _, id := range unique {
		id := id
		group.Go(func() error {
			result, err := s.QueryAccount(groupCtx, id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errorsByID[id] = err
			} else {
				results[id] = result
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, nil, err
	}
	return results, errorsByID, nil
}

func (s *UpstreamUsageService) queryAccount(ctx context.Context, accountID int64, expected *Record, expectedConfig UpstreamUsageQueryConfig) (result *UpstreamUsageQueryResult, err error) {
	defer func() {
		outcome := "success"
		if err != nil {
			outcome = infraerrors.Reason(err)
			if outcome == "" {
				outcome = "error"
			}
		}
		s.recordMetric(expectedConfig.Adapter, outcome)
	}()
	release, err := s.acquireQuerySlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	account, err := s.loadQueryAccount(ctx, accountID)
	if err != nil {
		if errors.Is(err, ErrUpstreamUsageAccountInvalid) || errors.Is(err, ErrUpstreamUsageAccountDisabled) {
			// 预读后账号类型、状态或记录本身发生变化，应报告身份冲突，
			// 而不是把一次进行中的查询误报为普通账号参数错误。
			return nil, ErrUpstreamUsageIdentityChanged
		}
		return nil, err
	}
	if !SameUpstreamUsageIdentity(expected, account, expectedConfig) {
		return nil, ErrUpstreamUsageIdentityChanged
	}
	if !s.execution.Supports(expectedConfig.Adapter) {
		return nil, ErrUpstreamUsageUnsupported
	}
	usage, err := s.execution.Query(ctx, account, expectedConfig)
	if err != nil {
		return nil, err
	}
	if err := ValidateNormalizedUsage(usage); err != nil {
		return nil, ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	current, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			// 查询开始后账号被删除也属于身份快照变化，不能把刚取得的结果
			// 归到一个已经不存在的账号上。
			return nil, ErrUpstreamUsageIdentityChanged
		}
		return nil, upstreamUsageRepositoryError(ctx, err)
	}
	if !SameUpstreamUsageIdentity(account, current, expectedConfig) {
		return nil, ErrUpstreamUsageIdentityChanged
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	return &UpstreamUsageQueryResult{
		AccountID:    accountID,
		Adapter:      expectedConfig.Adapter,
		ObservedAt:   now,
		Provider:     usage.Provider,
		Mode:         usage.Mode,
		Unit:         usage.Unit,
		Balance:      usage.Balance,
		Balances:     usage.Balances,
		Available:    usage.Available,
		Limits:       usage.Limits,
		Subscription: usage.Subscription,
		ExpiresAt:    usage.ExpiresAt,
		Usage:        usage,
	}, nil
}

func (s *UpstreamUsageService) loadQueryAccount(ctx context.Context, accountID int64) (*Record, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, upstreamUsageRepositoryError(ctx, err)
	}
	if account == nil || account.Type != AccountTypeAPIKey {
		return nil, ErrUpstreamUsageAccountInvalid
	}
	if account.Status != "" && account.Status != StatusActive {
		return nil, ErrUpstreamUsageAccountDisabled
	}
	return account, nil
}

func upstreamUsageRepositoryError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return ErrUpstreamUsageTimeout
	}
	if errors.Is(err, ErrAccountNotFound) {
		return ErrUpstreamUsageAccountInvalid
	}
	return ErrUpstreamUsageRequestFailed
}

func (s *UpstreamUsageService) acquireQuerySlot(ctx context.Context) (func(), error) {
	s.slotMu.Lock()
	if s.querySlots == nil {
		s.querySlots = make(chan struct{}, upstreamUsageConcurrency)
	}
	slots := s.querySlots
	s.slotMu.Unlock()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, UpstreamUsageContextError(ctx)
	}
}
