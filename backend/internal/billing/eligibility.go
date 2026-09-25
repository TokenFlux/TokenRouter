package billing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"golang.org/x/sync/singleflight"
)

// 错误定义
// 注：ErrInsufficientBalance在redeem_service.go中定义
// 注：ErrDailyLimitExceeded/ErrWeeklyLimitExceeded/ErrMonthlyLimitExceeded在subscription_service.go中定义
// errBillingCacheUnavailable 内部哨兵：用于 quota 校验路径在 cache==nil 时
// 与"Redis 故障"走同一条 fail-open + DB 一次性检查的分支。
var errBillingCacheUnavailable = fmt.Errorf("billing cache unavailable")

var (
	ErrBillingServiceUnavailable = apperror.ServiceUnavailable("BILLING_SERVICE_ERROR", "Billing service temporarily unavailable. Please retry later.")
	// RPM 超限错误。gateway_handler 负责映射为 HTTP 429。

	// user × platform quota（HTTP 429 Too Many Requests + Retry-After header）。
	// 选用 429 而非 403：限额耗尽属于"暂时性资源用尽，重试可恢复"的场景（RFC 6585），
	// 大量 SDK（如 OpenAI 兼容客户端）只对 429 触发自动退避并读取 Retry-After，
	// 用 403 会被视为"权限不足，重试无意义"导致客户端直接报错且不退避。
	ErrUserPlatformDailyQuotaExhausted   = apperror.TooManyRequests("USER_PLATFORM_DAILY_QUOTA_EXHAUSTED", "Daily usage quota exhausted for this platform.")
	ErrUserPlatformWeeklyQuotaExhausted  = apperror.TooManyRequests("USER_PLATFORM_WEEKLY_QUOTA_EXHAUSTED", "Weekly usage quota exhausted for this platform.")
	ErrUserPlatformMonthlyQuotaExhausted = apperror.TooManyRequests("USER_PLATFORM_MONTHLY_QUOTA_EXHAUSTED", "Monthly usage quota exhausted for this platform.")
)

// 缓存写入任务类型
type cacheWriteKind int

const (
	cacheWriteSetBalance cacheWriteKind = iota
	cacheWriteDeductBalance
	cacheWriteUpdateRateLimitUsage
)

type cacheWriteEnqueueResult int

const (
	cacheWriteEnqueued cacheWriteEnqueueResult = iota
	cacheWriteQueueFull
	cacheWriteQueueClosed
)

// 异步缓存写入工作池配置
//
// 固定大小的工作池限制并发写入：
// 1. 预创建 10 个 worker goroutine，避免频繁创建销毁
// 2. 使用带缓冲的 channel（1000）作为任务队列，平滑写入峰值
// 3. 非阻塞写入，队列满时关键任务同步回退，非关键任务丢弃并告警
// 4. 统一超时控制，避免慢操作阻塞工作池
const (
	cacheWriteWorkerCount     = 10              // 工作协程数量
	cacheWriteBufferSize      = 1000            // 任务队列缓冲大小
	cacheWriteTimeout         = 2 * time.Second // 单个写入操作超时
	cacheWriteDropLogInterval = 5 * time.Second // 丢弃日志节流间隔
	balanceLoadTimeout        = 3 * time.Second
)

// cacheWriteTask 缓存写入任务
type cacheWriteTask struct {
	kind     cacheWriteKind
	userID   int64
	apiKeyID int64
	balance  float64
	amount   float64
}

// Eligibility 计费缓存服务
// 负责余额与 API Key 限速缓存管理，提供高性能的计费资格检查
type Eligibility struct {
	background            func(string, func())
	cache                 BillingCache
	userRepo              BalanceReader
	apiKeyRateLimitLoader APIKeyRateLimitLoader
	options               func() EligibilityOptions
	observer              Observe
	coordinator           *QuotaCoordinator
	circuitBreaker        *billingCircuitBreaker
	userPlatformQuotaRepo UserPlatformQuotaRepository

	cacheWriteChan      chan cacheWriteTask
	cacheWriteStartOnce sync.Once
	cacheWriteWg        sync.WaitGroup
	cacheWriteStopOnce  sync.Once
	cacheWriteMu        sync.RWMutex
	stopped             atomic.Bool
	balanceLoadSF       singleflight.Group
	quotaLoadSF         singleflight.Group
	// 丢弃日志节流计数器（减少高负载下日志噪音）
	cacheWriteDropFullCount     uint64
	cacheWriteDropFullLastLog   int64
	cacheWriteDropClosedCount   uint64
	cacheWriteDropClosedLastLog int64
}

// Start 在应用完成绑定后启动缓存写入 worker。
func (s *Eligibility) Start() {
	s.cacheWriteStartOnce.Do(func() {
		s.cacheWriteMu.Lock()
		defer s.cacheWriteMu.Unlock()
		if !s.stopped.Load() {
			s.startCacheWriteWorkers()
		}
	})
}

// Stop 关闭缓存写入工作池
func (s *Eligibility) Stop() {
	s.cacheWriteStopOnce.Do(func() {
		s.stopped.Store(true)

		s.cacheWriteMu.Lock()
		ch := s.cacheWriteChan
		if ch != nil {
			close(ch)
		}
		s.cacheWriteMu.Unlock()

		if ch == nil {
			return
		}
		s.cacheWriteWg.Wait()

		s.cacheWriteMu.Lock()
		if s.cacheWriteChan == ch {
			s.cacheWriteChan = nil
		}
		s.cacheWriteMu.Unlock()
	})
}

func (s *Eligibility) startCacheWriteWorkers() {
	ch := make(chan cacheWriteTask, cacheWriteBufferSize)
	s.cacheWriteChan = ch
	for i := 0; i < cacheWriteWorkerCount; i++ {
		s.cacheWriteWg.Add(1)
		go s.cacheWriteWorker(ch)
	}
}

func (s *Eligibility) tryEnqueueCacheWrite(task cacheWriteTask) cacheWriteEnqueueResult {
	if s.stopped.Load() {
		return cacheWriteQueueClosed
	}

	s.cacheWriteMu.RLock()
	defer s.cacheWriteMu.RUnlock()

	if s.cacheWriteChan == nil {
		return cacheWriteQueueClosed
	}

	select {
	case s.cacheWriteChan <- task:
		return cacheWriteEnqueued
	default:
		// 队列满时不阻塞主流程，交由调用方决定是否同步回退。
		return cacheWriteQueueFull
	}
}

// enqueueCacheWrite 尝试将任务入队，队列满时返回 false（并记录实际丢弃告警）。
func (s *Eligibility) enqueueCacheWrite(task cacheWriteTask) (enqueued bool) {
	switch s.tryEnqueueCacheWrite(task) {
	case cacheWriteEnqueued:
		return true
	case cacheWriteQueueFull:
		s.logCacheWriteDrop(task, "full")
	case cacheWriteQueueClosed:
		s.logCacheWriteDrop(task, "closed")
	}
	return false
}

func (s *Eligibility) cacheWriteWorker(ch <-chan cacheWriteTask) {
	defer s.cacheWriteWg.Done()
	for task := range ch {
		ctx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
		switch task.kind {
		case cacheWriteSetBalance:
			s.setBalanceCache(ctx, task.userID, task.balance)
		case cacheWriteDeductBalance:
			if s.cache != nil {
				if err := s.cache.DeductUserBalance(ctx, task.userID, task.amount); err != nil {
					s.observer.Printf("service.billing_cache", "Warning: deduct balance cache failed for user %d: %v", task.userID, err)
				}
			}
		case cacheWriteUpdateRateLimitUsage:
			if s.cache != nil {
				if err := s.cache.UpdateAPIKeyRateLimitUsage(ctx, task.apiKeyID, task.amount); err != nil {
					s.observer.Printf("service.billing_cache", "Warning: update rate limit usage cache failed for api key %d: %v", task.apiKeyID, err)
				}
			}
		}
		cancel()
	}
}

// cacheWriteKindName 用于日志中的任务类型标识，便于排查丢弃原因。
func cacheWriteKindName(kind cacheWriteKind) string {
	switch kind {
	case cacheWriteSetBalance:
		return "set_balance"
	case cacheWriteDeductBalance:
		return "deduct_balance"
	case cacheWriteUpdateRateLimitUsage:
		return "update_rate_limit_usage"
	default:
		return "unknown"
	}
}

// logCacheWriteDrop 使用节流方式记录丢弃情况，并汇总丢弃数量。
func (s *Eligibility) logCacheWriteDrop(task cacheWriteTask, reason string) {
	var (
		countPtr *uint64
		lastPtr  *int64
	)
	switch reason {
	case "full":
		countPtr = &s.cacheWriteDropFullCount
		lastPtr = &s.cacheWriteDropFullLastLog
	case "closed":
		countPtr = &s.cacheWriteDropClosedCount
		lastPtr = &s.cacheWriteDropClosedLastLog
	default:
		return
	}

	atomic.AddUint64(countPtr, 1)
	now := s.dateRuntime().now().UnixNano()
	last := atomic.LoadInt64(lastPtr)
	if now-last < int64(cacheWriteDropLogInterval) {
		return
	}
	if !atomic.CompareAndSwapInt64(lastPtr, last, now) {
		return
	}
	dropped := atomic.SwapUint64(countPtr, 0)
	if dropped == 0 {
		return
	}
	s.observer.Printf("service.billing_cache", "Warning: cache write queue %s, dropped %d tasks in last %s (latest kind=%s user %d)",
		reason,
		dropped,
		cacheWriteDropLogInterval,
		cacheWriteKindName(task.kind),
		task.userID,
	)
}

// GetUserBalance 获取用户余额（优先从缓存读取）
func (s *Eligibility) GetUserBalance(ctx context.Context, userID int64) (float64, error) {
	if s == nil || s.cache == nil {
		// Redis不可用，直接查询数据库
		return s.getUserBalanceFromDB(ctx, userID)
	}

	// 尝试从缓存读取
	balance, err := s.cache.GetUserBalance(ctx, userID)
	if err == nil {
		return balance, nil
	}

	// 缓存未命中：singleflight 合并同一 userID 的并发回源请求。
	value, err, _ := s.balanceLoadSF.Do(strconv.FormatInt(userID, 10), func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.Background(), balanceLoadTimeout)
		defer cancel()

		balance, err := s.getUserBalanceFromDB(loadCtx, userID)
		if err != nil {
			return nil, err
		}

		// 异步建立缓存
		_ = s.enqueueCacheWrite(cacheWriteTask{
			kind:    cacheWriteSetBalance,
			userID:  userID,
			balance: balance,
		})
		return balance, nil
	})
	if err != nil {
		return 0, err
	}
	balance, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("unexpected balance type: %T", value)
	}
	return balance, nil
}

// getUserBalanceFromDB 从数据库获取用户余额
func (s *Eligibility) getUserBalanceFromDB(ctx context.Context, userID int64) (float64, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("get user balance: %w", err)
	}
	return user.Balance, nil
}

// setBalanceCache 设置余额缓存
func (s *Eligibility) setBalanceCache(ctx context.Context, userID int64, balance float64) {
	if s == nil || s.cache == nil {
		return
	}
	if err := s.cache.SetUserBalance(ctx, userID, balance); err != nil {
		s.observer.Printf("service.billing_cache", "Warning: set balance cache failed for user %d: %v", userID, err)
	}
}

// DeductBalanceCache 扣减余额缓存（同步调用）
func (s *Eligibility) DeductBalanceCache(ctx context.Context, userID int64, amount float64) error {
	if s == nil || s.cache == nil {
		return nil
	}
	return s.cache.DeductUserBalance(ctx, userID, amount)
}

// QueueDeductBalance 异步扣减余额缓存
func (s *Eligibility) QueueDeductBalance(userID int64, amount float64) {
	if s == nil || s.cache == nil {
		return
	}
	task := cacheWriteTask{
		kind:   cacheWriteDeductBalance,
		userID: userID,
		amount: amount,
	}
	switch s.tryEnqueueCacheWrite(task) {
	case cacheWriteEnqueued:
		return
	case cacheWriteQueueClosed:
		s.logCacheWriteDrop(task, "closed")
		return
	}
	// 队列满时同步回退，避免关键扣减被静默丢弃。
	ctx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
	defer cancel()
	if err := s.DeductBalanceCache(ctx, userID, amount); err != nil {
		s.observer.Printf("service.billing_cache", "Warning: deduct balance cache fallback failed for user %d: %v", userID, err)
	}
}

// InvalidateUserBalance 失效用户余额缓存
func (s *Eligibility) InvalidateUserBalance(ctx context.Context, userID int64) error {
	if s == nil || s.cache == nil {
		return nil
	}
	if err := s.cache.InvalidateUserBalance(ctx, userID); err != nil {
		s.observer.Printf("service.billing_cache", "Warning: invalidate balance cache failed for user %d: %v", userID, err)
		return err
	}
	return nil
}

// InvalidateAPIKeyRateLimit invalidates the Redis rate-limit usage cache for an API key.
func (s *Eligibility) InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error {
	if s == nil || s.cache == nil {
		return nil
	}
	if err := s.cache.InvalidateAPIKeyRateLimit(ctx, keyID); err != nil {
		s.observer.Printf("service.billing_cache", "Warning: invalidate api key rate limit cache failed for key %d: %v", keyID, err)
		return err
	}
	return nil
}

// checkAPIKeyRateLimits checks rate limit windows for an API key.
// It loads usage from Redis cache (falling back to DB on cache miss),
// resets expired windows in-memory and triggers async DB reset,
// and returns an error if any window limit is exceeded.
func (s *Eligibility) checkAPIKeyRateLimits(ctx context.Context, apiKey *KeySnapshot) error {
	if s == nil || s.cache == nil {
		// No cache: fall back to reading from DB directly
		if s.apiKeyRateLimitLoader == nil {
			return nil
		}
		data, err := s.apiKeyRateLimitLoader.GetRateLimitData(ctx, apiKey.ID)
		if err != nil {
			return nil // Don't block requests on DB errors
		}
		return s.evaluateRateLimits(ctx, apiKey, data.Usage5h, data.Usage1d, data.Usage7d,
			data.Window5hStart, data.Window1dStart, data.Window7dStart)
	}

	cacheData, err := s.cache.GetAPIKeyRateLimit(ctx, apiKey.ID)
	if err != nil {
		// Cache miss: load from DB and populate cache
		if s.apiKeyRateLimitLoader == nil {
			return nil
		}
		dbData, dbErr := s.apiKeyRateLimitLoader.GetRateLimitData(ctx, apiKey.ID)
		if dbErr != nil {
			return nil // Don't block requests on DB errors
		}
		// Build cache entry from DB data
		cacheEntry := &APIKeyRateLimitCacheData{
			Usage5h: dbData.Usage5h,
			Usage1d: dbData.Usage1d,
			Usage7d: dbData.Usage7d,
		}
		if dbData.Window5hStart != nil {
			cacheEntry.Window5h = dbData.Window5hStart.Unix()
		}
		if dbData.Window1dStart != nil {
			cacheEntry.Window1d = dbData.Window1dStart.Unix()
		}
		if dbData.Window7dStart != nil {
			cacheEntry.Window7d = dbData.Window7dStart.Unix()
		}
		_ = s.cache.SetAPIKeyRateLimit(ctx, apiKey.ID, cacheEntry)
		cacheData = cacheEntry
	}

	var w5h, w1d, w7d *time.Time
	if cacheData.Window5h > 0 {
		t := time.Unix(cacheData.Window5h, 0)
		w5h = &t
	}
	if cacheData.Window1d > 0 {
		t := time.Unix(cacheData.Window1d, 0)
		w1d = &t
	}
	if cacheData.Window7d > 0 {
		t := time.Unix(cacheData.Window7d, 0)
		w7d = &t
	}
	return s.evaluateRateLimits(ctx, apiKey, cacheData.Usage5h, cacheData.Usage1d, cacheData.Usage7d, w5h, w1d, w7d)
}

// evaluateRateLimits checks usage against limits, triggering async resets for expired windows.
func (s *Eligibility) evaluateRateLimits(ctx context.Context, apiKey *KeySnapshot, usage5h, usage1d, usage7d float64, w5h, w1d, w7d *time.Time) error {
	needsReset := false

	// Reset expired windows in-memory for check purposes
	if IsWindowExpired(w5h, RateLimitWindow5h) {
		usage5h = 0
		needsReset = true
	}
	if IsWindowExpired(w1d, RateLimitWindow1d) {
		usage1d = 0
		needsReset = true
	}
	if IsWindowExpired(w7d, RateLimitWindow7d) {
		usage7d = 0
		needsReset = true
	}

	// Trigger async DB reset if any window expired
	if needsReset {
		keyID := apiKey.ID
		s.runBackground("service/billing_cache_service.go:evaluateRateLimits", func() {
			resetCtx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
			defer cancel()
			if s.apiKeyRateLimitLoader != nil {
				// Use the repo directly - reset then reload cache
				if loader, ok := s.apiKeyRateLimitLoader.(interface {
					ResetRateLimitWindows(ctx context.Context, id int64) error
				}); ok {
					if err := loader.ResetRateLimitWindows(resetCtx, keyID); err != nil {
						s.observer.Printf("service.billing_cache", "Warning: reset rate limit windows failed for api key %d: %v", keyID, err)
					}
				}
			}
			// Invalidate cache so next request loads fresh data
			if s.cache != nil {
				if err := s.cache.InvalidateAPIKeyRateLimit(resetCtx, keyID); err != nil {
					s.observer.Printf("service.billing_cache", "Warning: invalidate rate limit cache failed for api key %d: %v", keyID, err)
				}
			}
		})
	}

	// Check limits
	if apiKey.RateLimit5h > 0 && usage5h >= apiKey.RateLimit5h {
		return ErrAPIKeyRateLimit5hExceeded
	}
	if apiKey.RateLimit1d > 0 && usage1d >= apiKey.RateLimit1d {
		return ErrAPIKeyRateLimit1dExceeded
	}
	if apiKey.RateLimit7d > 0 && usage7d >= apiKey.RateLimit7d {
		return ErrAPIKeyRateLimit7dExceeded
	}
	return nil
}

// QueueUpdateAPIKeyRateLimitUsage asynchronously updates rate limit usage in the cache.
func (s *Eligibility) QueueUpdateAPIKeyRateLimitUsage(apiKeyID int64, cost float64) {
	if s == nil || s.cache == nil {
		return
	}
	task := cacheWriteTask{
		kind:     cacheWriteUpdateRateLimitUsage,
		apiKeyID: apiKeyID,
		amount:   cost,
	}
	switch s.tryEnqueueCacheWrite(task) {
	case cacheWriteEnqueued:
		return
	case cacheWriteQueueClosed:
		s.logCacheWriteDrop(task, "closed")
		return
	}
	// 队列满时同步回退，避免限速使用量统计被静默丢弃。
	ctx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
	defer cancel()
	if err := s.cache.UpdateAPIKeyRateLimitUsage(ctx, apiKeyID, cost); err != nil {
		s.observer.Printf("service.billing_cache", "Warning: update rate limit usage cache fallback failed for api key %d: %v", apiKeyID, err)
	}
}

// IncrementUserPlatformQuotaUsage 同步累加 user × platform usage 到 Redis 缓存。
//
// 设计：同步写入而非异步入队。同步写确保下次 preflight 立即看到最新 usage，
// 把 TOCTOU 超支窗口限制在并发 in-flight 请求数量内（而非随时间无限累积）。
// 写延迟通常 < 1ms（本地 Redis），换取 quota 视图实时性的取舍合理。
//
// Redis 写失败用 ALERT 级 log；DB 持久化由 caller 单独 goroutine 兜底（gateway_service.go）。
func (s *Eligibility) IncrementUserPlatformQuotaUsage(userID int64, platform string, cost float64) {
	if s == nil || s.cache == nil {
		return
	}
	if platform == "" || cost <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
	defer cancel()
	unlock, lockErr := s.coordinator.Acquire(ctx, userID)
	if lockErr != nil {
		s.observer.Printf("service.billing_cache", "ALERT: incr user platform quota lock failed user=%d platform=%s cost=%f: %v", userID, platform, cost, lockErr)
		return
	}
	defer unlock()
	if !s.prepareQuotaIncrement(ctx, s.userPlatformQuotaRepo, userID, platform) {
		return
	}
	ttl := time.Duration(s.options().Billing.UserPlatformQuotaCacheTTLSeconds) * time.Second
	markDirty := s.options().Database.UserPlatformQuotaFlusherEnabled
	if err := s.cache.IncrUserPlatformQuotaUsageCache(ctx, userID, platform, cost, ttl, markDirty); err != nil {
		s.observer.Printf("service.billing_cache",
			"ALERT: incr user platform quota cache failed user=%d platform=%s cost=%f: %v",
			userID, platform, cost, err)
	}
}

// CheckBillingEligibility 检查用户是否有资格发起请求。
// auto 模式保留订阅额度耗尽后回退余额的历史行为；指定订阅和仅余额模式严格遵循 Key 配置。
// platform 为请求的目标平台（如 "anthropic"），传空串 "" 时跳过 user × platform quota 检查。
func (s *Eligibility) CheckBillingEligibility(ctx context.Context, user *UserSummary, apiKey *KeySnapshot, group *GroupSnapshot, subscription *UserSubscription, platform string) error {
	// 简易模式：跳过所有计费检查
	if s.options().RunMode == RunModeSimple {
		return nil
	}
	if s.circuitBreaker != nil && !s.circuitBreaker.Allow() {
		return ErrBillingServiceUnavailable
	}

	billingMode := effectiveKeyBillingMode(apiKey)
	if billingMode == APIKeyBillingModeSubscription {
		// 请求期二次校验防止分组回退、套餐变更或异步路径绕过指定套餐的范围。
		if subscription == nil {
			return ErrPreferredSubscriptionInvalid
		}
		if group != nil && !SubscriptionAllowsGroup(subscription, group.ID) {
			return ErrPreferredSubscriptionGroup
		}
		if err := CheckEffectiveSubscriptionEligibility(subscription, s.dateRuntime().calendar()); err != nil {
			if IsSubscriptionQuotaExceeded(err) {
				return ErrPreferredSubscriptionInsufficient
			}
			return ErrPreferredSubscriptionInvalid
		}
	}
	if billingMode == APIKeyBillingModeBalance {
		// 即使上游误传了订阅快照，余额模式也不能因此使用套餐额度。
		subscription = nil
	}

	isSubscriptionMode := subscription != nil
	if subscription != nil {
		if err := CheckEffectiveSubscriptionEligibility(subscription, s.dateRuntime().calendar()); err != nil {
			if !IsSubscriptionQuotaExceeded(err) {
				return err
			}
			isSubscriptionMode = false
			if err := s.checkBalanceEligibility(ctx, user.ID); err != nil {
				return err
			}
		}
	} else {
		if err := s.checkBalanceEligibility(ctx, user.ID); err != nil {
			return err
		}
	}

	// user × platform quota 仅在 standard（余额）模式生效；订阅模式豁免。
	// 不在 allowlist 的平台不参与本功能的 per-user USD quota。
	if !isSubscriptionMode && IsAllowedQuotaPlatform(platform) {
		if err := s.CheckUserPlatformQuotaEligibility(ctx, user.ID, platform); err != nil {
			return err
		}
	}

	// Check API Key rate limits (applies to both billing modes)
	if apiKey != nil && apiKey.HasRateLimits() {
		if err := s.checkAPIKeyRateLimits(ctx, apiKey); err != nil {
			return err
		}
	}

	return nil
}

func CheckEffectiveSubscriptionEligibility(subscription *UserSubscription, calendar timezone.Calendar) error {
	if subscription == nil {
		return ErrSubscriptionInvalid
	}
	switch subscription.EffectiveStatus(time.Now()) {
	case SubscriptionStatusExpired:
		return ErrSubscriptionInvalid
	case SubscriptionStatusSuspended:
		return ErrSubscriptionInvalid
	case SubscriptionStatusPending:
		return ErrSubscriptionInvalid
	case SubscriptionStatusRevoked:
		return ErrSubscriptionInvalid
	}

	effective := *subscription
	if effective.NeedsDailyReset(calendar) {
		effective.DailyUsageUSD = 0
	}
	if effective.NeedsWeeklyReset() {
		effective.WeeklyUsageUSD = 0
	}
	if effective.NeedsMonthlyReset() {
		effective.MonthlyUsageUSD = 0
	}

	return CheckSubscriptionUsageLimits(&effective, 0)
}

func IsSubscriptionQuotaExceeded(err error) bool {
	return errors.Is(err, ErrDailyLimitExceeded) ||
		errors.Is(err, ErrWeeklyLimitExceeded) ||
		errors.Is(err, ErrMonthlyLimitExceeded)
}

func (s *Eligibility) MinimumBalanceReserve() float64 {
	if s == nil || s.options == nil || s.options().Billing.MinimumBalanceReserve <= 0 {
		return 0
	}
	return s.options().Billing.MinimumBalanceReserve
}

func (s *Eligibility) BalanceBelowEligibilityThreshold(balance float64) bool {
	if balance <= 0 {
		return true
	}
	minimumReserve := s.MinimumBalanceReserve()
	return minimumReserve > 0 && balance < minimumReserve
}

// checkBalanceEligibility 检查余额模式资格
func (s *Eligibility) checkBalanceEligibility(ctx context.Context, userID int64) error {
	balance, err := s.GetUserBalance(ctx, userID)
	if err != nil {
		if s.circuitBreaker != nil {
			s.circuitBreaker.OnFailure(err)
		}
		s.observer.Printf("service.billing_cache", "ALERT: billing balance check failed for user %d: %v", userID, err)
		return ErrBillingServiceUnavailable.WithCause(err)
	}
	if s.circuitBreaker != nil {
		s.circuitBreaker.OnSuccess()
	}

	if s.BalanceBelowEligibilityThreshold(balance) {
		return ErrInsufficientBalance
	}

	return nil
}

type billingCircuitBreakerState int

const (
	billingCircuitClosed billingCircuitBreakerState = iota
	billingCircuitOpen
	billingCircuitHalfOpen
)

type billingCircuitBreaker struct {
	observer          Observe
	mu                sync.Mutex
	state             billingCircuitBreakerState
	failures          int
	openedAt          time.Time
	failureThreshold  int
	resetTimeout      time.Duration
	halfOpenRequests  int
	halfOpenRemaining int
}

func newBillingCircuitBreaker(cfg CircuitBreakerOptions) *billingCircuitBreaker {
	if !cfg.Enabled {
		return nil
	}
	resetTimeout := time.Duration(cfg.ResetTimeoutSeconds) * time.Second
	if resetTimeout <= 0 {
		resetTimeout = 30 * time.Second
	}
	halfOpen := cfg.HalfOpenRequests
	if halfOpen <= 0 {
		halfOpen = 1
	}
	threshold := cfg.FailureThreshold
	if threshold <= 0 {
		threshold = 5
	}
	return &billingCircuitBreaker{
		state:            billingCircuitClosed,
		failureThreshold: threshold,
		resetTimeout:     resetTimeout,
		halfOpenRequests: halfOpen,
	}
}

func (b *billingCircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case billingCircuitClosed:
		return true
	case billingCircuitOpen:
		if time.Since(b.openedAt) < b.resetTimeout {
			return false
		}
		b.state = billingCircuitHalfOpen
		b.halfOpenRemaining = b.halfOpenRequests
		b.observer.Printf("service.billing_cache", "ALERT: billing circuit breaker entering half-open state")
		fallthrough
	case billingCircuitHalfOpen:
		if b.halfOpenRemaining <= 0 {
			return false
		}
		b.halfOpenRemaining--
		return true
	default:
		return false
	}
}

func (b *billingCircuitBreaker) OnFailure(err error) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case billingCircuitOpen:
		return
	case billingCircuitHalfOpen:
		b.state = billingCircuitOpen
		b.openedAt = time.Now()
		b.halfOpenRemaining = 0
		b.observer.Printf("service.billing_cache", "ALERT: billing circuit breaker opened after half-open failure: %v", err)
		return
	default:
		b.failures++
		if b.failures >= b.failureThreshold {
			b.state = billingCircuitOpen
			b.openedAt = time.Now()
			b.halfOpenRemaining = 0
			b.observer.Printf("service.billing_cache", "ALERT: billing circuit breaker opened after %d failures: %v", b.failures, err)
		}
	}
}

func (b *billingCircuitBreaker) OnSuccess() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	previousState := b.state
	previousFailures := b.failures

	b.state = billingCircuitClosed
	b.failures = 0
	b.halfOpenRemaining = 0

	// 只有状态真正发生变化时才记录日志
	if previousState != billingCircuitClosed {
		b.observer.Printf("service.billing_cache", "ALERT: billing circuit breaker closed (was %s)", circuitStateString(previousState))
	} else if previousFailures > 0 {
		b.observer.Printf("service.billing_cache", "INFO: billing circuit breaker failures reset from %d", previousFailures)
	}
}

func circuitStateString(state billingCircuitBreakerState) string {
	switch state {
	case billingCircuitClosed:
		return "closed"
	case billingCircuitOpen:
		return "open"
	case billingCircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CheckUserPlatformQuotaEligibility 在 standard 模式下检查 user × platform 日/周/月 quota。
// 返回 nil = 允许；返回 ErrUserPlatform{Daily/Weekly/Monthly}QuotaExhausted = 拒绝（带 window_resets_at metadata）。
// CheckUserPlatformQuotaEligibility 检查用户在指定平台的 USD 配额。
//
// 流程（Redis-first / DB-fallback）：
//  1. 先读 Redis cache；若命中且 SchemaVersion==1，直接用 entry 中的 limits 和 window_start 做校验，
//     免除 DB 查询。
//  2. cache MISS 或旧版 entry（SchemaVersion==0）→ 查 DB 回填完整 entry（含 limits/window_start）。
//  3. Redis 故障（err != nil）→ fail-open，查 DB 做一次性检查，不回填。
//
// CheckUserPlatformQuotaEligibility 保留 Redis-first 和取消独立的 singleflight 回源。
// 只读命中不写缓存；需要刷新或回源时在共享用户锁内重新读取并完成写回。
func (s *Eligibility) CheckUserPlatformQuotaEligibility(ctx context.Context, userID int64, platform string) error {
	if platform == "" || s.userPlatformQuotaRepo == nil {
		return nil
	}
	loadBudget := 3 * time.Second
	entry, hit, cacheErr := s.readQuotaCache(ctx, userID, platform)
	if cacheErr == nil && hit && entry != nil && entry.SchemaVersion == UserPlatformQuotaCacheSchemaV1 {
		now := s.dateRuntime().now()
		limited := entry.DailyLimitUSD != nil || entry.WeeklyLimitUSD != nil || entry.MonthlyLimitUSD != nil
		expired := QuotaWindowExpired(entry.DailyWindowStart, s.dateRuntime().calendar().StartOfDay(now)) || QuotaWindowExpired(entry.WeeklyWindowStart, s.dateRuntime().calendar().StartOfWeek(now)) || MonthlyQuotaWindowExpired(entry.MonthlyWindowStart, now)
		if limited && expired {
			loadBudget = 50 * time.Millisecond
		}
		if !limited || !expired {
			return s.checkCachedQuota(ctx, entry, userID, platform, false)
		}
	}
	key := strconv.FormatInt(userID, 10) + ":" + platform
	result := s.quotaLoadSF.DoChan(key, func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.Background(), loadBudget)
		defer cancel()
		unlock, err := s.coordinator.Acquire(loadCtx, userID)
		if err != nil {
			return nil, err
		}
		defer unlock()
		// 等待期间管理操作或另一请求可能已经更新缓存，不能使用加锁前的快照。
		if cacheErr == nil {
			current, ok, readErr := s.readQuotaCache(loadCtx, userID, platform)
			if readErr == nil && ok && current != nil && current.SchemaVersion == UserPlatformQuotaCacheSchemaV1 {
				return quotaCheckDecision{s.checkCachedQuota(loadCtx, current, userID, platform, true)}, nil
			}
			cacheErr = readErr
		}
		rec, err := s.userPlatformQuotaRepo.GetByUserPlatform(loadCtx, userID, platform)
		if err != nil {
			return nil, err
		}
		return quotaCheckDecision{s.checkDatabaseQuota(loadCtx, rec, cacheErr, userID, platform)}, nil
	})
	select {
	case value := <-result:
		if value.Err != nil {
			s.observer.Printf("service.billing_cache", "Warning: load user platform quota failed user=%d platform=%s: %v (fail-open)", userID, platform, value.Err)
			return nil
		}
		decision, ok := value.Val.(quotaCheckDecision)
		if !ok {
			s.observer.Printf("service.billing_cache", "Warning: invalid user platform quota load result user=%d platform=%s (fail-open)", userID, platform)
			return nil
		}
		return decision.err
	case <-ctx.Done():
		s.observer.Printf("service.billing_cache", "Warning: user platform quota check ctx cancelled user=%d platform=%s: %v (fail-open)", userID, platform, ctx.Err())
		return nil
	}
}

type quotaCheckDecision struct{ err error }

func (s *Eligibility) readQuotaCache(ctx context.Context, userID int64, platform string) (*UserPlatformQuotaCacheEntry, bool, error) {
	if s == nil || s.cache == nil {
		return nil, false, errBillingCacheUnavailable
	}
	return s.cache.GetUserPlatformQuotaCache(ctx, userID, platform)
}

// checkCachedQuota 只在调用方持有用户锁时允许刷新，避免覆盖并发累计。
func (s *Eligibility) checkCachedQuota(ctx context.Context, entry *UserPlatformQuotaCacheEntry, userID int64, platform string, refresh bool) error {
	now := s.dateRuntime().now()
	dailyUsage := entry.DailyUsageUSD
	weeklyUsage := entry.WeeklyUsageUSD
	monthlyUsage := entry.MonthlyUsageUSD
	// 若窗口已更新（DB 已重置但 cache 尚未失效）,将对应 usage 清零再做比较,
	// 同时记录新窗口起点用于后续刷新 cache entry。
	// 本次请求用本地清零值继续判断;DB 层 IncrementUsageWithReset 已有窗口自愈能力,
	// 持久化数据始终正确。
	windowExpired := false
	newDailyStart := entry.DailyWindowStart
	newWeeklyStart := entry.WeeklyWindowStart
	newMonthlyStart := entry.MonthlyWindowStart
	if QuotaWindowExpired(entry.DailyWindowStart, s.dateRuntime().calendar().StartOfDay(now)) {
		dailyUsage = 0
		windowExpired = true
		dayStart := s.dateRuntime().calendar().StartOfDay(now)
		newDailyStart = &dayStart
	}
	if QuotaWindowExpired(entry.WeeklyWindowStart, s.dateRuntime().calendar().StartOfWeek(now)) {
		weeklyUsage = 0
		windowExpired = true
		weekStart := s.dateRuntime().calendar().StartOfWeek(now)
		newWeeklyStart = &weekStart
	}
	if MonthlyQuotaWindowExpired(entry.MonthlyWindowStart, now) {
		monthlyUsage = 0
		windowExpired = true
		monthStart := now
		newMonthlyStart = &monthStart
	}
	// 检测到任意窗口过期：用 reset 后的 entry 覆盖 Redis（而非 Delete）。
	// 旧实现 Delete 后,期间到达的 IncrUserPlatformQuotaUsage 调用让 Lua 看到
	// EXISTS=0 直接 return 0,并发请求的 cost 永久丢失,直到下次 cache MISS 回填。
	// 改为 SetCache 原子覆盖:key 不断链,Lua INCR 可在新窗口 entry 上正确累加。
	// 超时 50ms:覆盖正常路径与可接受抖动;Redis 异常时 hot path 不阻塞超过此值。
	// 用 context.Background()+短超时,避免请求 ctx 取消导致刷新丢失。
	// 显式 setCancel()(而非 defer):缩短 context 生命周期,避免 defer 延迟到函数返回。
	// isSentinel 判定「该 entry 无任何 limit」,涵盖两类,跨窗口命中时都跳过 refresh:
	//   1) A3 回填的 sentinel(DB 无行,短 TTL):refresh 会把短 TTL 误升级为 86400s,有害;
	//   2) DB 有行但三 limit 全未配置的用户(TTL 86400s):refresh 纯属无意义(TTL 升级本身无害)。
	// 两类的 enforcement(下方 limit!=nil 比较)都因 limit 全 nil 永远放行,跳过 refresh 均正确。
	isSentinel := entry.DailyLimitUSD == nil && entry.WeeklyLimitUSD == nil && entry.MonthlyLimitUSD == nil
	if refresh && windowExpired && s.cache != nil && !isSentinel {
		refreshed := &UserPlatformQuotaCacheEntry{
			DailyUsageUSD:      dailyUsage,
			WeeklyUsageUSD:     weeklyUsage,
			MonthlyUsageUSD:    monthlyUsage,
			SchemaVersion:      UserPlatformQuotaCacheSchemaV1,
			DailyLimitUSD:      entry.DailyLimitUSD,
			WeeklyLimitUSD:     entry.WeeklyLimitUSD,
			MonthlyLimitUSD:    entry.MonthlyLimitUSD,
			DailyWindowStart:   newDailyStart,
			WeeklyWindowStart:  newWeeklyStart,
			MonthlyWindowStart: newMonthlyStart,
		}
		ttl := time.Duration(s.options().Billing.UserPlatformQuotaCacheTTLSeconds) * time.Second
		setCtx, setCancel := context.WithTimeout(ctx, 50*time.Millisecond)
		if setErr := s.cache.SetUserPlatformQuotaCache(setCtx, userID, platform, refreshed, ttl); setErr != nil {
			s.observer.Printf("service.billing_cache",
				"Warning: refresh expired user platform quota cache failed user=%d platform=%s: %v",
				userID, platform, setErr)
		}
		setCancel()
	}
	if entry.DailyLimitUSD != nil && dailyUsage >= *entry.DailyLimitUSD {
		return WithWindowResetsMetadata(ErrUserPlatformDailyQuotaExhausted, s.dateRuntime().calendar().StartOfDay(now).AddDate(0, 0, 1))
	}
	if entry.WeeklyLimitUSD != nil && weeklyUsage >= *entry.WeeklyLimitUSD {
		return WithWindowResetsMetadata(ErrUserPlatformWeeklyQuotaExhausted, s.dateRuntime().calendar().StartOfWeek(now).AddDate(0, 0, 7))
	}
	if entry.MonthlyLimitUSD != nil && monthlyUsage >= *entry.MonthlyLimitUSD {
		return WithWindowResetsMetadata(ErrUserPlatformMonthlyQuotaExhausted, NextMonthlyResetFrom(entry.MonthlyWindowStart, now))
	}
	return nil
}

// checkDatabaseQuota 由 singleflight 的锁内回源调用，调用者不在锁外重复回填。
func (s *Eligibility) checkDatabaseQuota(ctx context.Context, rec *UserPlatformQuotaRecord, cacheErr error, userID int64, platform string) error {
	if rec == nil {
		// 仅在 cache 可用且本次 GET 未出错时回填 sentinel:Redis GET 故障(cacheErr!=nil)
		// 时不回填,与下方 line ~1201 "Redis 故障时 fail-open:不回填" 保持一致,
		// 避免在 Redis 异常期做一次注定失败的 SET。
		if s.cache != nil && cacheErr == nil {
			now := s.dateRuntime().now()
			startOfDay := s.dateRuntime().calendar().StartOfDay(now)
			startOfWeek := s.dateRuntime().calendar().StartOfWeek(now)
			sentinel := &UserPlatformQuotaCacheEntry{
				SchemaVersion:      UserPlatformQuotaCacheSchemaV1,
				DailyWindowStart:   &startOfDay,
				WeeklyWindowStart:  &startOfWeek,
				MonthlyWindowStart: &now,
				// limits 全 nil, usage 全 0(零值)
			}
			sentinelTTL := time.Duration(s.options().Billing.UserPlatformQuotaSentinelTTLSeconds) * time.Second
			if sentinelTTL <= 0 {
				// 防御:TTL<=0 时 Redis EXPIRE 会立即删除整个 key(见 billing_cache.go 的 pipe.Expire),
				// sentinel 不持久化 → 每请求击穿 DB。配置缺失/误配为 0 时 fallback 到 1h。
				sentinelTTL = time.Hour
			}
			setCtx, setCancel := context.WithTimeout(ctx, 50*time.Millisecond)
			if setErr := s.cache.SetUserPlatformQuotaCache(setCtx, userID, platform, sentinel, sentinelTTL); setErr != nil {
				userPlatformQuotaSentinelSetCacheErrorTotal.Add(1)
				s.observer.Printf("service.billing_cache", "Warning: set sentinel quota cache failed user=%d platform=%s: %v", userID, platform, setErr)
			}
			setCancel()
		}
		return nil
	}

	now := s.dateRuntime().now()
	dailyUsage := rec.DailyUsageUSD
	weeklyUsage := rec.WeeklyUsageUSD
	monthlyUsage := rec.MonthlyUsageUSD
	if QuotaWindowExpired(rec.DailyWindowStart, s.dateRuntime().calendar().StartOfDay(now)) {
		dailyUsage = 0
	}
	if QuotaWindowExpired(rec.WeeklyWindowStart, s.dateRuntime().calendar().StartOfWeek(now)) {
		weeklyUsage = 0
	}
	if MonthlyQuotaWindowExpired(rec.MonthlyWindowStart, now) {
		monthlyUsage = 0
	}

	// Redis 故障时 fail-open：不回填，直接用 DB 数据做一次性检查
	if cacheErr != nil {
		if rec.DailyLimitUSD != nil && dailyUsage >= *rec.DailyLimitUSD {
			return WithWindowResetsMetadata(ErrUserPlatformDailyQuotaExhausted, s.dateRuntime().calendar().StartOfDay(now).AddDate(0, 0, 1))
		}
		if rec.WeeklyLimitUSD != nil && weeklyUsage >= *rec.WeeklyLimitUSD {
			return WithWindowResetsMetadata(ErrUserPlatformWeeklyQuotaExhausted, s.dateRuntime().calendar().StartOfWeek(now).AddDate(0, 0, 7))
		}
		if rec.MonthlyLimitUSD != nil && monthlyUsage >= *rec.MonthlyLimitUSD {
			return WithWindowResetsMetadata(ErrUserPlatformMonthlyQuotaExhausted, NextMonthlyResetFrom(rec.MonthlyWindowStart, now))
		}
		return nil
	}

	// cache MISS 或旧版 entry → 回填完整 entry（含 limits 和 window_start）
	newEntry := &UserPlatformQuotaCacheEntry{
		DailyUsageUSD:      dailyUsage,
		WeeklyUsageUSD:     weeklyUsage,
		MonthlyUsageUSD:    monthlyUsage,
		SchemaVersion:      UserPlatformQuotaCacheSchemaV1,
		DailyLimitUSD:      rec.DailyLimitUSD,
		WeeklyLimitUSD:     rec.WeeklyLimitUSD,
		MonthlyLimitUSD:    rec.MonthlyLimitUSD,
		DailyWindowStart:   rec.DailyWindowStart,
		WeeklyWindowStart:  rec.WeeklyWindowStart,
		MonthlyWindowStart: rec.MonthlyWindowStart,
	}
	if s.cache != nil {
		ttl := time.Duration(s.options().Billing.UserPlatformQuotaCacheTTLSeconds) * time.Second
		// 与 HIT 过期回填路径（上文 SetCache 调用）保持一致：用 context.Background()+50ms,
		// 避免请求 ctx 提前取消（客户端断连/上游超时）导致 cache 回填失败,
		// 让下一次 preflight 仍然 MISS 并击穿到 DB（高并发下增大 DB 压力）。
		setCtx, setCancel := context.WithTimeout(ctx, 50*time.Millisecond)
		if setErr := s.cache.SetUserPlatformQuotaCache(setCtx, userID, platform, newEntry, ttl); setErr != nil {
			s.observer.Printf("service.billing_cache", "Warning: set user platform quota cache failed user=%d platform=%s: %v", userID, platform, setErr)
		}
		setCancel()
	}

	if rec.DailyLimitUSD != nil && dailyUsage >= *rec.DailyLimitUSD {
		return WithWindowResetsMetadata(ErrUserPlatformDailyQuotaExhausted, s.dateRuntime().calendar().StartOfDay(now).AddDate(0, 0, 1))
	}
	if rec.WeeklyLimitUSD != nil && weeklyUsage >= *rec.WeeklyLimitUSD {
		return WithWindowResetsMetadata(ErrUserPlatformWeeklyQuotaExhausted, s.dateRuntime().calendar().StartOfWeek(now).AddDate(0, 0, 7))
	}
	if rec.MonthlyLimitUSD != nil && monthlyUsage >= *rec.MonthlyLimitUSD {
		return WithWindowResetsMetadata(ErrUserPlatformMonthlyQuotaExhausted, NextMonthlyResetFrom(rec.MonthlyWindowStart, now))
	}
	return nil
}

// WithWindowResetsMetadata 给 quota error 附加 window_resets_at metadata（RFC3339）。
func WithWindowResetsMetadata(err error, resetAt time.Time) error {
	appErr, ok := err.(*apperror.ApplicationError)
	if !ok || appErr == nil {
		return err
	}
	return appErr.WithMetadata(map[string]string{
		"window_resets_at": resetAt.Format(time.RFC3339),
	})
}

// NextMonthlyResetFrom 返回 30 天滚动窗口的下次重置时间（start + 30d）。
// start 为 nil（未初始化）或已过期（now-start >= 30d，与 MonthlyQuotaWindowExpired 同口径）时
// 退化为 now+30d：过期窗口会在下次 increment 时重置为 now，下次重置即 now+30d；
// 否则按 start 计算会得到一个过去的时间，使 Retry-After 落回 fallback 并触发客户端紧凑重试。
func NextMonthlyResetFrom(start *time.Time, now time.Time) time.Time {
	if start == nil || now.Sub(*start) >= 30*24*time.Hour {
		return now.Add(30 * 24 * time.Hour)
	}
	return start.Add(30 * 24 * time.Hour)
}

// QuotaWindowExpired 判断窗口是否已过期：start 为 nil（未初始化）或在 currWindowStart 之前视为已过期。
func QuotaWindowExpired(start *time.Time, currWindowStart time.Time) bool {
	if start == nil {
		return true
	}
	return start.Before(currWindowStart)
}

// MonthlyQuotaWindowExpired 判断 30 天滚动月度窗口是否已过期。
// 过期条件：now - start >= 30×24h（与订阅模式 NeedsMonthlyReset 语义一致）。
// start 为 nil 时视为已过期（未初始化窗口）。
func MonthlyQuotaWindowExpired(start *time.Time, now time.Time) bool {
	if start == nil {
		return true
	}
	return now.Sub(*start) >= 30*24*time.Hour
}

// HasUserPlatformQuotaLimit 判断该 user×platform 是否设了任一非 nil limit。
// 写入点守卫:无 limit 直接跳过 Redis 写 + 脏集标记,消除无谓写入。
// fail-safe:任何不确定(simple 模式除外)都返回 true 维持写入。
func (s *Eligibility) HasUserPlatformQuotaLimit(ctx context.Context, userID int64, platform string) bool {
	if s != nil && s.options != nil && s.options().RunMode == RunModeSimple {
		return false
	}
	if s == nil || s.cache == nil {
		return true
	}
	entry, ok, err := s.cache.GetUserPlatformQuotaCache(ctx, userID, platform)
	if err != nil || !ok || entry == nil {
		return true
	}
	return entry.DailyLimitUSD != nil || entry.WeeklyLimitUSD != nil || entry.MonthlyLimitUSD != nil
}
func NewEligibility(cache BillingCache, users BalanceReader, keys APIKeyRateLimitLoader, quotas UserPlatformQuotaRepository, options func() EligibilityOptions, observe Observe, coordinator *QuotaCoordinator, background ...func(string, func())) *Eligibility {
	if coordinator == nil {
		coordinator = NewQuotaCoordinator()
	}
	s := &Eligibility{cache: cache, userRepo: users, apiKeyRateLimitLoader: keys, userPlatformQuotaRepo: quotas, options: options, observer: observe, coordinator: coordinator}
	if len(background) > 0 {
		s.background = background[0]
	}
	s.circuitBreaker = newBillingCircuitBreaker(options().Billing.CircuitBreaker)
	if s.circuitBreaker != nil {
		s.circuitBreaker.observer = observe
	}
	return s
}

// Check 只执行资金准入；RPM 仍由旧请求编排在资金检查后决定。
func (s *Eligibility) Check(ctx context.Context, input CheckInput) error {
	return s.CheckBillingEligibility(ctx, input.Payer, input.Key, input.Group, input.Subscription, input.Platform)
}

// runBackground 通过装配注入的任务拥有者执行；独立测试构造时同步完成。
func (s *Eligibility) runBackground(name string, fn func()) {
	if s.background == nil {
		fn()
		return
	}
	s.background(name, fn)
}

// SentinelCacheWriteErrors 返回唯一 sentinel 回填失败计数。
var userPlatformQuotaSentinelSetCacheErrorTotal atomic.Int64

func SentinelCacheWriteErrors() int64 { return userPlatformQuotaSentinelSetCacheErrorTotal.Load() }

// QuotaCoordinator 返回本运行时的协调器，供 app 显式共享给管理用例和 flusher。
func (s *Eligibility) QuotaCoordinator() *QuotaCoordinator { return s.coordinator }

func (s *Eligibility) dateRuntime() DateRuntime {
	if s != nil && s.options != nil {
		return s.options().Dates
	}
	return DateRuntime{}
}

// prepareQuotaIncrement 在已持有用户锁时核对最新配置并修复失效缓存。
// 这不是再次准入：已放行请求即使额度耗尽也继续累计；只有配置删除才跳过。
func (s *Eligibility) prepareQuotaIncrement(ctx context.Context, repo UserPlatformQuotaRepository, userID int64, platform string) bool {
	if repo == nil || s.cache == nil {
		return true
	}
	entry, hit, err := s.cache.GetUserPlatformQuotaCache(ctx, userID, platform)
	if err != nil {
		return true
	} // 沿用 Redis 故障时尝试累计及数据库降级的语义。
	if hit && entry != nil {
		if entry.DailyLimitUSD == nil && entry.WeeklyLimitUSD == nil && entry.MonthlyLimitUSD == nil {
			return false
		}
		_ = s.checkCachedQuota(ctx, entry, userID, platform, true)
		return true
	}
	record, err := repo.GetByUserPlatform(ctx, userID, platform)
	if err != nil {
		s.observer.Printf("service.billing_cache", "ALERT: load user platform quota before increment failed user=%d platform=%s: %v", userID, platform, err)
		return true
	}
	_ = s.checkDatabaseQuota(ctx, record, nil, userID, platform)
	if record == nil || record.DailyLimitUSD == nil && record.WeeklyLimitUSD == nil && record.MonthlyLimitUSD == nil {
		return false
	}
	// 数据库旧窗口回填后，先设置当前窗口起点，再执行本次累计，避免下次刷新将其清零。
	if refreshed, ok, readErr := s.cache.GetUserPlatformQuotaCache(ctx, userID, platform); readErr == nil && ok && refreshed != nil {
		_ = s.checkCachedQuota(ctx, refreshed, userID, platform, true)
	}
	return true
}
