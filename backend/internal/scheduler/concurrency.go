package scheduler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// ConcurrencyCache 定义并发控制的缓存接口
// 使用有序集合存储槽位，按时间戳清理过期条目
type ConcurrencyCache interface {
	// 账号槽位管理
	// 键格式: concurrency:account:{accountID}（有序集合，成员为 requestID）
	AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error)
	ReleaseAccountSlot(ctx context.Context, accountID int64, requestID string) error
	GetAccountConcurrency(ctx context.Context, accountID int64) (int, error)
	GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error)

	// 账号等待队列（账号级）
	IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error)
	DecrementAccountWaitCount(ctx context.Context, accountID int64) error
	GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error)

	// 用户槽位管理
	// 键格式: concurrency:user:{userID}（有序集合，成员为 requestID）
	AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error)
	ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error
	GetUserConcurrency(ctx context.Context, userID int64) (int, error)

	// 等待队列计数（每次入队都会刷新 TTL，避免长时间排队时计数提前过期）
	IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error)
	DecrementWaitCount(ctx context.Context, userID int64) error

	// 批量负载查询（只读）
	GetAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error)
	GetUsersLoadBatch(ctx context.Context, users []UserWithConcurrency) (map[int64]*UserLoadInfo, error)

	// 清理过期槽位（后台任务）
	CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error
	CleanupExpiredAccountSlotKeys(ctx context.Context) error

	// 启动时清理旧进程遗留槽位与等待计数
	CleanupStaleProcessSlots(ctx context.Context, activeRequestPrefix string) error
}

// APIKeyConcurrencyCache 定义 API Key 实时并发统计所需的缓存能力。
type APIKeyConcurrencyCache interface {
	TrackAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error
	ReleaseAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error
	GetAPIKeyConcurrencyBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]int, error)
}

// OpenAIWSIngressLeaseCache 管理限制客户端 WebSocket 活跃会话数的短期分布式租约。
// 它与请求槽位命名空间相互独立，空闲入站连接不会占用轮次槽位。
type OpenAIWSIngressLeaseCache interface {
	AcquireOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, maxConnections int, leaseID string) (bool, error)
	RefreshOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, leaseID string) (bool, error)
	ReleaseOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, leaseID string) error
}

const (
	openAIWSIngressLeaseTTL             = 60 * time.Second
	openAIWSIngressLeaseRefreshInterval = 20 * time.Second
	openAIWSIngressLeaseOperationTO     = 2 * time.Second
)

var ErrOpenAIWSIngressLeaseLost = errors.New("openai websocket ingress lease lost")

// OpenAIWSIngressLease 维持 Redis 入站租约；若整个租约周期都无法确认所有权，则取消上下文。
// handler 的所有退出路径都必须调用 Release，立即归还容量。
type OpenAIWSIngressLease struct {
	diagnostics Diagnostics
	ctx         context.Context
	cancel      context.CancelCauseFunc
	cache       OpenAIWSIngressLeaseCache
	apiKeyID    int64
	leaseID     string

	stopOnce      sync.Once
	stopCh        chan struct{}
	refreshDone   chan struct{}
	finishRuntime func()
}

func (l *OpenAIWSIngressLease) Context() context.Context {
	if l == nil || l.ctx == nil {
		return context.Background()
	}
	return l.ctx
}

func (l *OpenAIWSIngressLease) Release() {
	if l == nil {
		return
	}
	l.stopOnce.Do(func() {
		if l.finishRuntime != nil {
			defer l.finishRuntime()
		}
		if l.stopCh != nil {
			close(l.stopCh)
		}
		if l.cancel != nil {
			l.cancel(nil)
		}
		if l.refreshDone != nil {
			<-l.refreshDone
		}
		if l.cache == nil || l.apiKeyID <= 0 || l.leaseID == "" {
			return
		}
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), openAIWSIngressLeaseOperationTO)
		defer releaseCancel()
		if err := l.cache.ReleaseOpenAIWSIngressLease(releaseCtx, l.apiKeyID, l.leaseID); err != nil {
			l.diagnostics.event("warn", "openai_ws_ingress_lease_release_failed",
				"api_key_id", l.apiKeyID,
				"error", err,
			)
		}
	})
}

func (l *OpenAIWSIngressLease) refreshLoop() {
	defer func() {
		if l != nil && l.refreshDone != nil {
			close(l.refreshDone)
		}
	}()
	if l == nil || l.cache == nil {
		return
	}
	ticker := time.NewTicker(openAIWSIngressLeaseRefreshInterval)
	defer ticker.Stop()
	lastConfirmedAt := time.Now()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-l.stopCh:
			return
		case <-ticker.C:
			var lost bool
			lastConfirmedAt, lost = l.refresh(lastConfirmedAt)
			if lost {
				l.cancel(ErrOpenAIWSIngressLeaseLost)
				return
			}
		}
	}
}

// refresh 确认租约仍归当前进程所有。成员缺失会立即判定丢失，临时 Redis 错误最多容忍一个 TTL。
func (l *OpenAIWSIngressLease) refresh(lastConfirmedAt time.Time) (time.Time, bool) {
	refreshCtx, refreshCancel := context.WithTimeout(context.Background(), openAIWSIngressLeaseOperationTO)
	owned, err := l.cache.RefreshOpenAIWSIngressLease(refreshCtx, l.apiKeyID, l.leaseID)
	refreshCancel()
	if err == nil && owned {
		return time.Now(), false
	}
	if err == nil {
		err = ErrOpenAIWSIngressLeaseLost
	}
	elapsed := time.Since(lastConfirmedAt)
	l.diagnostics.event("warn", "openai_ws_ingress_lease_refresh_failed",
		"api_key_id", l.apiKeyID,
		"unconfirmed_for", elapsed,
		"error", err,
	)
	if errors.Is(err, ErrOpenAIWSIngressLeaseLost) || elapsed >= openAIWSIngressLeaseTTL {
		l.diagnostics.event("error", "openai_ws_ingress_lease_lost",
			"api_key_id", l.apiKeyID,
			"unconfirmed_for", elapsed,
			"error", err,
		)
		return lastConfirmedAt, true
	}
	return lastConfirmedAt, false
}

var (
	requestIDPrefix  = initRequestIDPrefix()
	requestIDCounter atomic.Uint64
)

func initRequestIDPrefix() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err == nil {
		return "r" + strconv.FormatUint(binary.BigEndian.Uint64(b), 36)
	}
	fallback := uint64(time.Now().UnixNano()) ^ (uint64(os.Getpid()) << 16)
	return "r" + strconv.FormatUint(fallback, 36)
}

func RequestIDPrefix() string {
	return requestIDPrefix
}

func GenerateRequestID() string {
	seq := requestIDCounter.Add(1)
	return requestIDPrefix + "-" + strconv.FormatUint(seq, 36)
}

func (s *ConcurrencyService) CleanupStaleProcessSlots(ctx context.Context) error {
	if s == nil || s.cache == nil {
		return nil
	}
	return s.cache.CleanupStaleProcessSlots(ctx, RequestIDPrefix())
}

const (
	// 默认等待队列额外槽位
	defaultExtraWaitSlots = 20

	defaultAccountLoadBatchCacheTTL = 200 * time.Millisecond
	accountLoadBatchFetchTimeout    = 3 * time.Second
	maxAccountLoadBatchCacheEntries = 256
	apiKeyConcurrencyFetchTimeout   = 3 * time.Second
	apiKeySlotTrackTimeout          = 2 * time.Second
)

// ConcurrencyService 管理账号和用户的并发限制。
type ConcurrencyService struct {
	diagnostics Diagnostics
	runtime     WorkerRuntime

	cache ConcurrencyCache

	accountLoadCacheTTL atomic.Int64
	accountLoadCacheMu  sync.RWMutex
	accountLoadCache    map[string]cachedAccountLoadBatch
	accountLoadGroup    singleflight.Group
}

type cachedAccountLoadBatch struct {
	loadMap   map[int64]*AccountLoadInfo
	expiresAt time.Time
}

// NewConcurrencyService 创建并发控制服务。
func NewConcurrencyService(cache ConcurrencyCache, options ...Diagnostics) *ConcurrencyService {
	var diagnostics Diagnostics
	if len(options) > 0 {
		diagnostics = options[0]
	}

	svc := &ConcurrencyService{
		cache:            cache,
		diagnostics:      diagnostics,
		accountLoadCache: make(map[string]cachedAccountLoadBatch),
	}
	svc.SetAccountLoadBatchCacheTTL(defaultAccountLoadBatchCacheTTL)
	return svc
}

// AcquireOpenAIWSIngressLease 为 API Key 原子预留一个活跃入站连接；非正数上限表示关闭保护。
func (s *ConcurrencyService) AcquireOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, maxConnections int) (*OpenAIWSIngressLease, bool, error) {
	if maxConnections <= 0 {
		return nil, true, nil
	}
	if s == nil || s.cache == nil || apiKeyID <= 0 {
		return nil, false, errors.New("openai websocket ingress lease cache is unavailable")
	}
	cache, ok := s.cache.(OpenAIWSIngressLeaseCache)
	if !ok {
		return nil, false, errors.New("openai websocket ingress lease cache is unsupported")
	}
	leaseID := GenerateRequestID()
	baseCtx, done, enterErr := s.runtime.Enter(context.WithoutCancel(nonNilContext(ctx)), fmt.Sprintf("ws-ingress:%d", apiKeyID))
	if enterErr != nil {
		return nil, false, enterErr
	}
	acquireCtx, acquireCancel := context.WithTimeout(baseCtx, openAIWSIngressLeaseOperationTO)
	acquired, err := cache.AcquireOpenAIWSIngressLease(acquireCtx, apiKeyID, maxConnections, leaseID)
	acquireCancel()
	if err != nil || !acquired {
		done()
		return nil, acquired, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	leaseCtx, leaseCancel := context.WithCancelCause(ctx)
	lease := &OpenAIWSIngressLease{
		diagnostics: s.diagnostics,
		ctx:         leaseCtx,
		cancel:      leaseCancel,
		cache:       cache,
		apiKeyID:    apiKeyID,
		leaseID:     leaseID,
		stopCh:      make(chan struct{}),
		refreshDone: make(chan struct{}),
	}
	stop := context.AfterFunc(baseCtx, func() { leaseCancel(ErrRuntimeStopped) })
	lease.finishRuntime = func() { stop(); done() }
	go lease.refreshLoop()
	return lease, true, nil
}

// SetAccountLoadBatchCacheTTL 设置账号负载批量读取的极短 TTL 缓存；非正数表示禁用缓存。
func (s *ConcurrencyService) SetAccountLoadBatchCacheTTL(ttl time.Duration) {
	if s == nil {
		return
	}
	s.accountLoadCacheTTL.Store(int64(ttl))
	if ttl <= 0 {
		s.accountLoadCacheMu.Lock()
		s.accountLoadCache = make(map[string]cachedAccountLoadBatch)
		s.accountLoadCacheMu.Unlock()
	}
}

// AcquireResult represents the result of acquiring a concurrency slot
type AcquireResult struct {
	Acquired    bool
	ReleaseFunc func() // Must be called when done (typically via defer)
}

type AccountWithConcurrency struct {
	ID             int64
	MaxConcurrency int
}

type UserWithConcurrency struct {
	ID             int64
	MaxConcurrency int
}

type AccountLoadInfo struct {
	AccountID          int64
	CurrentConcurrency int
	WaitingCount       int
	LoadRate           int // 0-100+ (percent)
}

type UserLoadInfo struct {
	UserID             int64
	CurrentConcurrency int
	WaitingCount       int
	LoadRate           int // 0-100+ (percent)
}

// AcquireAccountSlot attempts to acquire a concurrency slot for an account.
// If the account is at max concurrency, it waits until a slot is available or timeout.
// Returns a release function that MUST be called when the request completes.
func (s *ConcurrencyService) acquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (*AcquireResult, error) {
	// If maxConcurrency is 0 or negative, no limit
	if maxConcurrency <= 0 {
		return &AcquireResult{
			Acquired:    true,
			ReleaseFunc: func() {}, // no-op
		}, nil
	}

	// Generate unique request ID for this slot
	requestID := GenerateRequestID()

	acquired, err := s.cache.AcquireAccountSlot(ctx, accountID, maxConcurrency, requestID)
	if err != nil {
		return nil, err
	}

	if acquired {
		return &AcquireResult{
			Acquired: true,
			ReleaseFunc: func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseAccountSlot(bgCtx, accountID, requestID); err != nil {
					s.diagnostics.printf("service.concurrency", "Warning: failed to release account slot for %d (req=%s): %v", accountID, requestID, err)
				}
			},
		}, nil
	}

	return &AcquireResult{
		Acquired:    false,
		ReleaseFunc: nil,
	}, nil
}

// AcquireUserSlot attempts to acquire a concurrency slot for a user.
// If the user is at max concurrency, it waits until a slot is available or timeout.
// Returns a release function that MUST be called when the request completes.
func (s *ConcurrencyService) acquireUserSlot(ctx context.Context, userID int64, maxConcurrency int) (*AcquireResult, error) {
	// If maxConcurrency is 0 or negative, no limit
	if maxConcurrency <= 0 {
		return &AcquireResult{
			Acquired:    true,
			ReleaseFunc: func() {}, // no-op
		}, nil
	}

	// Generate unique request ID for this slot
	requestID := GenerateRequestID()

	acquired, err := s.cache.AcquireUserSlot(ctx, userID, maxConcurrency, requestID)
	if err != nil {
		return nil, err
	}

	if acquired {
		return &AcquireResult{
			Acquired: true,
			ReleaseFunc: func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseUserSlot(bgCtx, userID, requestID); err != nil {
					s.diagnostics.printf("service.concurrency", "Warning: failed to release user slot for %d (req=%s): %v", userID, requestID, err)
				}
			},
		}, nil
	}

	return &AcquireResult{
		Acquired:    false,
		ReleaseFunc: nil,
	}, nil
}

// TrackAPIKeySlot 记录一个 API Key 活跃请求槽位，但不施加 Key 级并发上限。
// 统计采用故障放行：Redis 出错时记录日志并返回空操作释放函数。
func (s *ConcurrencyService) trackAPIKeySlot(ctx context.Context, apiKeyID int64) func() {
	if s == nil || s.cache == nil || apiKeyID <= 0 {
		return func() {}
	}
	cache, ok := s.cache.(APIKeyConcurrencyCache)
	if !ok {
		return func() {}
	}

	requestID := GenerateRequestID()
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = ctx
	}
	trackCtx, cancel := context.WithTimeout(baseCtx, apiKeySlotTrackTimeout)
	err := cache.TrackAPIKeySlot(trackCtx, apiKeyID, requestID)
	cancel()
	if err != nil {
		s.diagnostics.printf("service.concurrency", "Warning: failed to track api key slot for %d (req=%s): %v", apiKeyID, requestID, err)
		return func() {}
	}

	return func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cache.ReleaseAPIKeySlot(bgCtx, apiKeyID, requestID); err != nil {
			s.diagnostics.printf("service.concurrency", "Warning: failed to release api key slot for %d (req=%s): %v", apiKeyID, requestID, err)
		}
	}
}

// GetAPIKeyConcurrencyBatch 批量获取 API Key 的实时活跃请求数。
// 统计采用尽力而为语义：缓存不支持或 Redis 出错时返回零值。
func (s *ConcurrencyService) GetAPIKeyConcurrencyBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]int, error) {
	result := zeroAPIKeyConcurrencyMap(apiKeyIDs)
	if len(apiKeyIDs) == 0 {
		return result, nil
	}
	if s == nil || s.cache == nil {
		return result, nil
	}
	cache, ok := s.cache.(APIKeyConcurrencyCache)
	if !ok {
		return result, nil
	}

	operation, done, err := s.runtime.Enter(context.Background(), "GetAPIKeyConcurrencyBatch")
	if err != nil {
		return result, err
	}
	defer done()
	redisCtx, cancel := context.WithTimeout(operation, apiKeyConcurrencyFetchTimeout)
	defer cancel()

	counts, err := cache.GetAPIKeyConcurrencyBatch(redisCtx, apiKeyIDs)
	if err != nil {
		s.diagnostics.printf("service.concurrency", "Warning: get api key concurrency batch failed: %v", err)
		return result, nil
	}
	for _, apiKeyID := range apiKeyIDs {
		result[apiKeyID] = counts[apiKeyID]
	}
	return result, nil
}

func zeroAPIKeyConcurrencyMap(apiKeyIDs []int64) map[int64]int {
	result := make(map[int64]int, len(apiKeyIDs))
	for _, apiKeyID := range apiKeyIDs {
		result[apiKeyID] = 0
	}
	return result
}

// ============================================
// Wait Queue Count Methods
// ============================================

// GetAccountWaitingCount gets current wait queue count for an account.
func (s *ConcurrencyService) GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error) {
	if s.cache == nil {
		return 0, nil
	}
	return s.cache.GetAccountWaitingCount(ctx, accountID)
}

// CalculateMaxWait calculates the maximum wait queue size for a user
// maxWait = userConcurrency + defaultExtraWaitSlots
func CalculateMaxWait(userConcurrency int) int {
	if userConcurrency <= 0 {
		userConcurrency = 1
	}
	return userConcurrency + defaultExtraWaitSlots
}

// GetAccountsLoadBatch 批量获取账号负载信息。
func (s *ConcurrencyService) GetAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	return s.getAccountsLoadBatch(ctx, accounts, true)
}

// GetAccountsLoadBatchFresh 绕过极短 TTL 缓存，用于抢槽失败后的实时刷新兜底。
func (s *ConcurrencyService) GetAccountsLoadBatchFresh(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	return s.getAccountsLoadBatch(ctx, accounts, false)
}

func (s *ConcurrencyService) getAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency, allowCache bool) (map[int64]*AccountLoadInfo, error) {
	if len(accounts) == 0 {
		return map[int64]*AccountLoadInfo{}, nil
	}
	if s.cache == nil {
		return map[int64]*AccountLoadInfo{}, nil
	}

	ttl := time.Duration(s.accountLoadCacheTTL.Load())
	if !allowCache || ttl <= 0 {
		return s.fetchAccountsLoadBatch(ctx, accounts)
	}

	key := accountLoadBatchCacheKey(accounts)
	if cached, ok := s.getCachedAccountLoadBatch(key, time.Now()); ok {
		return cached, nil
	}

	value, err, _ := s.accountLoadGroup.Do(key, func() (any, error) {
		now := time.Now()
		if cached, ok := s.getCachedAccountLoadBatch(key, now); ok {
			return cached, nil
		}
		loadMap, fetchErr := s.fetchAccountsLoadBatch(ctx, accounts)
		if fetchErr != nil {
			return nil, fetchErr
		}
		cached := cloneAccountLoadMap(loadMap)
		s.storeCachedAccountLoadBatch(key, cached, now.Add(ttl))
		return cached, nil
	})
	if err != nil {
		return nil, err
	}
	loadMap, _ := value.(map[int64]*AccountLoadInfo)
	if loadMap == nil {
		return map[int64]*AccountLoadInfo{}, nil
	}
	return loadMap, nil
}

func (s *ConcurrencyService) fetchAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	if s.cache == nil {
		return map[int64]*AccountLoadInfo{}, nil
	}
	operation, done, err := s.runtime.Enter(context.WithoutCancel(nonNilContext(ctx)), "fetchAccountsLoadBatch")
	if err != nil {
		return nil, err
	}
	defer done()
	redisCtx, cancel := context.WithTimeout(operation, accountLoadBatchFetchTimeout)
	defer cancel()
	return s.cache.GetAccountsLoadBatch(redisCtx, accounts)
}

func (s *ConcurrencyService) getCachedAccountLoadBatch(key string, now time.Time) (map[int64]*AccountLoadInfo, bool) {
	s.accountLoadCacheMu.RLock()
	cached, ok := s.accountLoadCache[key]
	s.accountLoadCacheMu.RUnlock()
	if !ok {
		return nil, false
	}
	if !now.Before(cached.expiresAt) {
		s.accountLoadCacheMu.Lock()
		if current, exists := s.accountLoadCache[key]; exists && !now.Before(current.expiresAt) {
			delete(s.accountLoadCache, key)
		}
		s.accountLoadCacheMu.Unlock()
		return nil, false
	}
	return cached.loadMap, true
}

func (s *ConcurrencyService) storeCachedAccountLoadBatch(key string, loadMap map[int64]*AccountLoadInfo, expiresAt time.Time) {
	s.accountLoadCacheMu.Lock()
	if s.accountLoadCache == nil {
		s.accountLoadCache = make(map[string]cachedAccountLoadBatch)
	}
	if len(s.accountLoadCache) >= maxAccountLoadBatchCacheEntries {
		now := time.Now()
		for cacheKey, cached := range s.accountLoadCache {
			if !now.Before(cached.expiresAt) {
				delete(s.accountLoadCache, cacheKey)
			}
		}
		for len(s.accountLoadCache) >= maxAccountLoadBatchCacheEntries {
			for cacheKey := range s.accountLoadCache {
				delete(s.accountLoadCache, cacheKey)
				break
			}
		}
	}
	s.accountLoadCache[key] = cachedAccountLoadBatch{
		loadMap:   loadMap,
		expiresAt: expiresAt,
	}
	s.accountLoadCacheMu.Unlock()
}

func accountLoadBatchCacheKey(accounts []AccountWithConcurrency) string {
	hash := sha256.New()
	var buf [16]byte
	for _, account := range accounts {
		binary.LittleEndian.PutUint64(buf[:8], uint64(account.ID))
		binary.LittleEndian.PutUint64(buf[8:], uint64(int64(account.MaxConcurrency)))
		_, _ = hash.Write(buf[:])
	}
	sum := hash.Sum(nil)
	return strconv.Itoa(len(accounts)) + ":" + hex.EncodeToString(sum)
}

func cloneAccountLoadMap(loadMap map[int64]*AccountLoadInfo) map[int64]*AccountLoadInfo {
	if len(loadMap) == 0 {
		return map[int64]*AccountLoadInfo{}
	}
	clone := make(map[int64]*AccountLoadInfo, len(loadMap))
	for accountID, loadInfo := range loadMap {
		if loadInfo == nil {
			clone[accountID] = nil
			continue
		}
		copied := *loadInfo
		clone[accountID] = &copied
	}
	return clone
}

// GetUsersLoadBatch returns load info for multiple users.
func (s *ConcurrencyService) GetUsersLoadBatch(ctx context.Context, users []UserWithConcurrency) (map[int64]*UserLoadInfo, error) {
	if s.cache == nil {
		return map[int64]*UserLoadInfo{}, nil
	}
	return s.cache.GetUsersLoadBatch(ctx, users)
}

// CleanupExpiredAccountSlots removes expired slots for one account (background task).
func (s *ConcurrencyService) CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.CleanupExpiredAccountSlots(ctx, accountID)
}

// StartSlotCleanupWorker 保持立即首轮和原周期，实际清理受运行取消约束。
func (s *ConcurrencyService) StartSlotCleanupWorker(interval time.Duration) {
	if s == nil || s.cache == nil || interval <= 0 {
		return
	}
	s.runtime.Start(RuntimeTask{Name: "slot-cleanup", Interval: interval, Immediate: true, Run: func(ctx context.Context) {
		cleanup, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := s.cache.CleanupExpiredAccountSlotKeys(cleanup); err != nil {
			s.diagnostics.printf("service.concurrency", "Warning: cleanup expired account slots failed: %v", err)
		}
	}})
}

// Stop 为旧消费者保留有界委托；应用使用统一剩余预算。
func (s *ConcurrencyService) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = s.StopContext(ctx)
}
func (s *ConcurrencyService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.runtime.StopContext(ctx)
}

// GetAccountConcurrencyBatch gets current concurrency counts for multiple accounts.
// Uses a detached context with timeout to prevent HTTP request cancellation from
// causing the entire batch to fail (which would show all concurrency as 0).
func (s *ConcurrencyService) GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error) {
	if len(accountIDs) == 0 {
		return map[int64]int{}, nil
	}
	if s.cache == nil {
		result := make(map[int64]int, len(accountIDs))
		for _, accountID := range accountIDs {
			result[accountID] = 0
		}
		return result, nil
	}

	// Use a detached context so that a cancelled HTTP request doesn't cause
	// the Redis pipeline to fail and return all-zero concurrency counts.
	operation, done, err := s.runtime.Enter(context.Background(), "GetAccountConcurrencyBatch")
	if err != nil {
		return nil, err
	}
	defer done()
	redisCtx, cancel := context.WithTimeout(operation, 3*time.Second)
	defer cancel()

	return s.cache.GetAccountConcurrencyBatch(redisCtx, accountIDs)
}

// EnterUserWait 与 EnterAccountWait 将等待计数的释放责任交给 scheduler。
func (s *ConcurrencyService) EnterUserWait(ctx context.Context, id int64, limit int) (WaitResult, error) {
	operation, done, err := s.runtime.Enter(ctx, "EnterUserWait")
	if err != nil {
		return WaitResult{}, err
	}
	result, err := EnterUserWait(operation, s.cache, id, limit, s.diagnostics)
	if err != nil || !result.Allowed {
		done()
		return result, err
	}
	previous := result.resource
	var release func()
	if previous != nil {
		release = previous.Release
	}
	result.resource = NewLease(context.Background(), ReleaseOnCompletion, done, release)
	return result, nil
}
func (s *ConcurrencyService) EnterAccountWait(ctx context.Context, id int64, limit int) (WaitResult, error) {
	operation, done, err := s.runtime.Enter(ctx, "EnterAccountWait")
	if err != nil {
		return WaitResult{}, err
	}
	result, err := EnterAccountWait(operation, s.cache, id, limit, s.diagnostics)
	if err != nil || !result.Allowed {
		done()
		return result, err
	}
	previous := result.resource
	var release func()
	if previous != nil {
		release = previous.Release
	}
	result.resource = NewLease(context.Background(), ReleaseOnCompletion, done, release)
	return result, nil
}

// LiveLeases 仅暴露长会话租约端口，不让旧执行层访问整个并发缓存。
func (s *ConcurrencyService) LiveLeases() LiveConcurrencyCache {
	if s == nil || s.cache == nil {
		return nil
	}
	cache, _ := s.cache.(LiveConcurrencyCache)
	return cache
}

// AcquireAccountSlot 在 I/O 前登记，返回时将停止等待责任转交给实际槽位租约。
func (s *ConcurrencyService) AcquireAccountSlot(ctx context.Context, accountID int64, limit int) (*AcquireResult, error) {
	operation, done, err := s.runtime.Enter(ctx, fmt.Sprintf("account-slot:%d", accountID))
	if err != nil {
		return nil, err
	}
	result, err := s.acquireAccountSlot(operation, accountID, limit)
	if err != nil || result == nil || !result.Acquired {
		done()
		return result, err
	}
	release := NewLease(context.Background(), ReleaseOnCompletion, done, result.ReleaseFunc).Release
	// 只补偿应用停止时的在途获取；普通请求取消仍由调用方释放模式处理。
	if stopErr := s.runtime.Context().Err(); stopErr != nil {
		release()
		return nil, stopErr
	}
	ownRequestResource(ctx, release)
	result.ReleaseFunc = release
	return result, nil
}

// AcquireUserSlot 在 I/O 前登记，返回时将停止等待责任转交给实际槽位租约。
func (s *ConcurrencyService) AcquireUserSlot(ctx context.Context, userID int64, limit int) (*AcquireResult, error) {
	operation, done, err := s.runtime.Enter(ctx, fmt.Sprintf("user-slot:%d", userID))
	if err != nil {
		return nil, err
	}
	result, err := s.acquireUserSlot(operation, userID, limit)
	if err != nil || result == nil || !result.Acquired {
		done()
		return result, err
	}
	release := NewLease(context.Background(), ReleaseOnCompletion, done, result.ReleaseFunc).Release
	// 只补偿应用停止时的在途获取；普通请求取消仍由调用方释放模式处理。
	if stopErr := s.runtime.Context().Err(); stopErr != nil {
		release()
		return nil, stopErr
	}
	result.ReleaseFunc = release
	return result, nil
}

// TrackAPIKeySlot 与原统计查询预算一致，关闭后拒绝新的统计写入。
func (s *ConcurrencyService) TrackAPIKeySlot(ctx context.Context, id int64) func() {
	if s == nil {
		return func() {}
	}
	operation, done, err := s.runtime.Enter(context.WithoutCancel(nonNilContext(ctx)), fmt.Sprintf("apikey-slot:%d", id))
	if err != nil {
		return func() {}
	}
	release := s.trackAPIKeySlot(operation, id)
	return NewLease(context.Background(), ReleaseOnCompletion, done, release).Release
}
func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
