package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// quotaDirtyCache 是 flusher 依赖的窄接口（来自 BillingCache）。
type quotaDirtyCache interface {
	PopDirtyUserPlatformQuotaKeys(ctx context.Context, n int) ([]UserPlatformQuotaKey, error)
	ReaddDirtyUserPlatformQuotaKeys(ctx context.Context, keys []UserPlatformQuotaKey) error
	BatchGetUserPlatformQuotaCache(ctx context.Context, keys []UserPlatformQuotaKey) ([]*UserPlatformQuotaCacheEntry, error)
}

// quotaSnapshotWriter 是 flusher 依赖的 DB 写入窄接口。
// 使用 service 层的 UserPlatformQuotaSnapshot，避免与 repository 包形成循环依赖；
// 实际实现由 repository adapter 在 B7 注入。
type quotaSnapshotWriter interface {
	BatchSnapshotUsage(ctx context.Context, snapshots []UserPlatformQuotaSnapshot, now time.Time) error
}

// FlusherMetrics 记录 flusher 运行时指标（原子量，零值可用）。
type FlusherMetrics struct {
	FlushSuccessTotal   atomic.Int64
	FlushErrorTotal     atomic.Int64
	FlushBatchSizeTotal atomic.Int64
	FlushLatencyMsMax   atomic.Int64
	DirtyReaddTotal     atomic.Int64
	// DirtyLostTotal：Readd 失败导致脏 key 丢失——已 SPOP+主操作失败+Readd 也失败；
	// Redis 仍权威，活跃 key 下次 SADD 自愈。
	DirtyLostTotal        atomic.Int64
	FlushFKViolationTotal atomic.Int64
}

// flusherMaxBatchesPerTick 单次 tick 最多消费的批数，防止 tick 执行时间过长。
const flusherMaxBatchesPerTick = 16

// maxFlushBatchSize 限制单批行数,必须 ≤ repository.BatchSnapshotUsage 的 batchRows(6000),
// 以保证单次 flush 的 snapshots 仅生成一条 UPSERT(单事务原子)。两处需手动保持一致。
const maxFlushBatchSize = 6000

// defaultFlushBatchSize 是配置 flush_batch_size 非法(≤0)时的回退值。
const defaultFlushBatchSize = 1000

// UserPlatformQuotaUsageFlusher 将 Redis 脏集快照定期批量写入 DB。
// 不维护任何 delta/in-process 状态；每批读取 Redis 当前绝对值覆盖写入。
type UserPlatformQuotaUsageFlusher struct {
	flushMu     sync.Mutex
	stopOnce    sync.Once
	lifecycleMu sync.Mutex
	started     bool
	stopErr     error
	cache       quotaDirtyCache
	quotaRepo   quotaSnapshotWriter
	timingWheel QuotaScheduler
	coordinator *QuotaCoordinator
	observer    Observe
	// enabled 对应 flusher_enabled 配置；false 时 Start() 不注册定时器。
	enabled      bool
	interval     time.Duration
	batchSize    int
	flushTimeout time.Duration
	metrics      *FlusherMetrics
	stopped      atomic.Bool
}

// NewUserPlatformQuotaUsageFlusher 创建 UserPlatformQuotaUsageFlusher。
// cache(BillingCache) 隐式满足 quotaDirtyCache；quotaRepo(UserPlatformQuotaRepository) 隐式满足 quotaSnapshotWriter。
func NewUserPlatformQuotaUsageFlusher(options FlusherOptions, cache BillingCache, quotaRepo quotaSnapshotWriter, tw QuotaScheduler, coordinator *QuotaCoordinator, observe Observe) *UserPlatformQuotaUsageFlusher {
	batchSize := options.UserPlatformQuotaFlushBatchSize
	if batchSize <= 0 {
		batchSize = defaultFlushBatchSize
	}
	if batchSize > maxFlushBatchSize {
		observe.Printf("quota_flusher",
			"[QuotaFlusher] flush_batch_size %d 超过上限 %d,已 clamp(避免 BatchSnapshotUsage 多子批非原子)",
			options.UserPlatformQuotaFlushBatchSize, maxFlushBatchSize)
		batchSize = maxFlushBatchSize
	}
	interval := time.Duration(options.UserPlatformQuotaFlushIntervalMs) * time.Millisecond
	if interval <= 0 {
		observe.Printf("quota_flusher", "[QuotaFlusher] flush_interval_ms %d 非法,回退 2000ms", options.UserPlatformQuotaFlushIntervalMs)
		interval = 2 * time.Second
	}
	return &UserPlatformQuotaUsageFlusher{
		cache:       cache,
		coordinator: coordinator, observer: observe,
		quotaRepo:    quotaRepo,
		timingWheel:  tw,
		enabled:      options.UserPlatformQuotaFlusherEnabled,
		interval:     interval,
		batchSize:    batchSize,
		flushTimeout: 3 * time.Second,
		metrics:      &FlusherMetrics{},
	}
}

// updateLatencyMax 用 CAS 单调更新最大延迟。
func (s *UserPlatformQuotaUsageFlusher) updateLatencyMax(ms int64) {
	for {
		old := s.metrics.FlushLatencyMsMax.Load()
		if ms <= old {
			return
		}
		if s.metrics.FlushLatencyMsMax.CompareAndSwap(old, ms) {
			return
		}
	}
}

// readdOrCountLost 尝试把 keys 回填脏集：成功计 DirtyReaddTotal，失败计 DirtyLostTotal 并 ALERT。
func (s *UserPlatformQuotaUsageFlusher) readdOrCountLost(ctx context.Context, keys []UserPlatformQuotaKey, stage string) {
	// 清理预算独立于已取消的取锁/写入上下文，否则回填必定失败。
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
	}
	if err := s.cache.ReaddDirtyUserPlatformQuotaKeys(ctx, keys); err != nil {
		s.metrics.DirtyLostTotal.Add(int64(len(keys)))
		s.observer.Printf("quota_flusher", "[QuotaFlusher] ALERT: Readd after %s failed, %d keys 丢出脏集(DB 镜像缺这批,Redis 仍权威,活跃 key 下次 SADD 自愈): %v", stage, len(keys), err)
		return
	}
	s.metrics.DirtyReaddTotal.Add(int64(len(keys)))
}

// flushOneBatch 处理单批：Pop → BatchGet → 组装 snaps → BatchSnapshotUsage。
// 返回 (shouldContinue bool)：false 表示本轮循环应停止（空集/错误/最后一批）。
// 每次调用独立创建带 timeout 的 ctx 并 defer cancel，不会在循环中累积泄漏。
func (s *UserPlatformQuotaUsageFlusher) flushOneBatch(parentCtx context.Context) bool {
	ctx, cancel := context.WithTimeout(parentCtx, s.flushTimeout)
	defer cancel()

	// 1. Pop 脏集
	keys, err := s.cache.PopDirtyUserPlatformQuotaKeys(ctx, s.batchSize)
	if err != nil {
		s.metrics.FlushErrorTotal.Add(1)
		s.observer.Printf("quota_flusher", "[QuotaFlusher] PopDirty error: %v", err)
		return false
	}
	if len(keys) == 0 {
		// 脏集已空
		return false
	}

	// Pop 只取得待处理键；取得用户锁后才读取快照，持锁至数据库写回结束。
	// 取消等待必须回填 dirty key，不能带着旧快照绕过协调继续写入。
	if s.coordinator != nil {
		userIDs := make([]int64, 0, len(keys))
		for _, key := range keys {
			userIDs = append(userIDs, key.UserID)
		}
		unlock, err := s.coordinator.Acquire(ctx, userIDs...)
		if err != nil {
			s.metrics.FlushErrorTotal.Add(1)
			s.readdOrCountLost(ctx, keys, "QuotaLock")
			return false
		}
		defer unlock()
	}
	// 2. 批量读 Redis 快照
	entries, err := s.cache.BatchGetUserPlatformQuotaCache(ctx, keys)
	if err != nil {
		s.metrics.FlushErrorTotal.Add(1)
		s.readdOrCountLost(ctx, keys, "BatchGet")
		s.observer.Printf("quota_flusher", "[QuotaFlusher] BatchGet error: %v", err)
		return false
	}

	// 3. 组装 snapshots（MISS 或任一 WindowStart==nil → 跳过）
	snaps := make([]UserPlatformQuotaSnapshot, 0, len(keys))
	for i, key := range keys {
		e := entries[i]
		if e == nil {
			continue
		}
		if e.DailyWindowStart == nil || e.WeeklyWindowStart == nil || e.MonthlyWindowStart == nil {
			continue
		}
		snaps = append(snaps, UserPlatformQuotaSnapshot{
			UserID:             key.UserID,
			Platform:           key.Platform,
			DailyUsageUSD:      e.DailyUsageUSD,
			WeeklyUsageUSD:     e.WeeklyUsageUSD,
			MonthlyUsageUSD:    e.MonthlyUsageUSD,
			DailyWindowStart:   *e.DailyWindowStart,
			WeeklyWindowStart:  *e.WeeklyWindowStart,
			MonthlyWindowStart: *e.MonthlyWindowStart,
		})
	}

	// 4. 全部 MISS/异常跳过时
	if len(snaps) == 0 {
		// 若 Pop 数量已不满一批，表示脏集将空，停止
		if len(keys) < s.batchSize {
			return false
		}
		// 否则继续下一批（可能还有更多脏 key）
		return true
	}

	// 管理写入和本批快照共用单实例用户锁，管理重置不能插入读取与写回之间。
	// Redis/数据库故障沿用各自降级；这不是跨存储事务或跨进程协调。

	// 5. 写入 DB
	start := time.Now()
	writeErr := s.quotaRepo.BatchSnapshotUsage(ctx, snaps, time.Now().UTC())
	s.updateLatencyMax(time.Since(start).Milliseconds())

	if writeErr != nil {
		if errors.Is(writeErr, ErrUserPlatformQuotaFKViolation) {
			// 注意:PG FK violation 是整条 INSERT 回滚 → 整批(含同批正常用户)均未写入 DB,
			// 且这些 key 已被 SPOP 出脏集、此处不 Readd。活跃 key 会在下次请求重新 SADD,
			// flusher 读 Redis 当前累计绝对值刷库即自愈;低活跃 key 这轮 DB usage 偏低
			// (Redis 仍是 enforcement 权威,不受影响;DB 仅展示)。已删用户边角的接受取舍,不做逐行重试。
			// FK 违反：用户已被删除，直接丢弃不 Readd
			s.metrics.FlushFKViolationTotal.Add(1)
			s.metrics.FlushErrorTotal.Add(1)
			s.observer.Printf("quota_flusher", "[QuotaFlusher] FK violation (dropped %d snaps): %v", len(snaps), writeErr)
		} else {
			// 其他错误：回填脏集，保留下次重试
			s.metrics.FlushErrorTotal.Add(1)
			s.readdOrCountLost(ctx, keys, "BatchSnapshotUsage")
			s.observer.Printf("quota_flusher", "[QuotaFlusher] BatchSnapshotUsage error: %v", writeErr)
		}
		return false
	}

	// 6. 成功
	s.metrics.FlushSuccessTotal.Add(1)
	s.metrics.FlushBatchSizeTotal.Add(int64(len(snaps)))

	// 若 Pop 数量不满一批，脏集已空，停止
	if len(keys) < s.batchSize {
		return false
	}
	return true
}

// flush 执行一次完整的 flush，循环消费至脏集空或达到 maxBatchesPerTick。
func (s *UserPlatformQuotaUsageFlusher) flush() {
	if s == nil {
		return
	}
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	parentCtx := context.Background()
	for b := 0; b < flusherMaxBatchesPerTick; b++ {
		if !s.flushOneBatch(parentCtx) {
			return
		}
	}
	// 连续消费满 flusherMaxBatchesPerTick 批仍未取空脏集:本 tick 主动让出,剩余积压留待下一 tick。
	// 记一条 log 便于 oncall 发现 distinct 活跃 key 远超 maxBatchesPerTick×batchSize(DB 镜像延迟上升);
	// 可配合 Redis SCARD billing:upq:dirty 观察脏集存量。
	s.observer.Printf("quota_flusher",
		"[QuotaFlusher] 单 tick 达到 max batches 上限(%d × batchSize=%d),脏集仍非空,积压顺延至下一 tick",
		flusherMaxBatchesPerTick, s.batchSize)
}

// tick 是 TimingWheel 回调。若 flusher 已停止则直接返回。
func (s *UserPlatformQuotaUsageFlusher) tick() {
	if s == nil || s.stopped.Load() {
		return
	}
	s.flush()
}

// Start 注册定时 tick。flusher_enabled=false 时直接返回，不注册定时器。
func (s *UserPlatformQuotaUsageFlusher) Start() {
	if s == nil || !s.enabled {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started || s.stopped.Load() {
		return
	}
	if s.timingWheel == nil {
		s.observer.Printf("quota_flusher", "[QuotaFlusher] timing wheel 为空,跳过启动")
		return
	}
	s.started = true
	s.timingWheel.ScheduleRecurring("deferred:platform_quota", s.interval, s.tick)
}

// Stop 停止 flusher：标记 stopped → Cancel 定时器 → 执行最后一次 flush。
func (s *UserPlatformQuotaUsageFlusher) Stop() {
	_ = s.Shutdown(context.Background())
}

// Shutdown 保持单批写回及失败回填策略，退出时继续处理超过周期上限的积压。
// 数据库失败或预算到期必须报告未排空，不能把停止周期任务当作 drain 成功。
func (s *UserPlatformQuotaUsageFlusher) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.stopOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.stopped.Store(true)
		s.lifecycleMu.Unlock()
		if s.timingWheel != nil {
			s.timingWheel.CancelAndWait("deferred:platform_quota")
		}
		s.flushMu.Lock()
		defer s.flushMu.Unlock()
		for {
			if err := ctx.Err(); err != nil {
				s.stopErr = err
				return
			}
			errorsBefore := s.metrics.FlushErrorTotal.Load()
			more := s.flushOneBatch(ctx)
			if s.metrics.FlushErrorTotal.Load() != errorsBefore {
				s.stopErr = fmt.Errorf("platform quota final flush incomplete: inspect quota_flusher errors")
				return
			}
			if !more {
				return
			}
		}
	})
	return s.stopErr
}

// QuotaScheduler 只提供本用例需要的周期登记与取消等待。
type QuotaScheduler interface {
	ScheduleRecurring(string, time.Duration, func())
	CancelAndWait(string)
}

// FlusherOptions 不依赖完整数据库配置，app 负责参数投影。
type FlusherOptions struct {
	UserPlatformQuotaFlushBatchSize  int
	UserPlatformQuotaFlushIntervalMs int
	UserPlatformQuotaFlusherEnabled  bool
}

// Metrics 返回该唯一运行实例的指标，旧监控入口仅作委托。
func (s *UserPlatformQuotaUsageFlusher) Metrics() *FlusherMetrics {
	if s == nil {
		return nil
	}
	return s.metrics
}
