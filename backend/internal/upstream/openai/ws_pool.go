package openai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"golang.org/x/sync/errgroup"
)

const (
	openAIWSConnHealthCheckIdle = 90 * time.Second
	// coder/websocket 没有 reader 时无法消费 pong 帧；在上游 keepalive 窗口到期前
	// 主动回收不支持无 reader 探活的空闲连接。
	openAIWSConnIdleRecycleAfter   = 90 * time.Second
	openAIWSConnHealthCheckTO      = 2 * time.Second
	openAIWSConnPrewarmExtraDelay  = 2 * time.Second
	openAIWSAcquireCleanupInterval = 3 * time.Second
	openAIWSBackgroundPingInterval = 30 * time.Second
	openAIWSBackgroundSweepTicker  = 30 * time.Second

	openAIWSPrewarmFailureWindow   = 30 * time.Second
	openAIWSPrewarmFailureSuppress = 2
)

var (
	errOpenAIWSConnClosed               = ErrWSConnClosed
	ErrOpenAIWSConnQueueFull            = errors.New("openai ws connection queue full")
	ErrOpenAIWSPreferredConnUnavailable = errors.New("openai ws preferred connection unavailable")
)

type WSDialError struct {
	StatusCode      int
	ResponseHeaders http.Header
	ResponseBody    []byte
	Err             error
}

func (e *WSDialError) Error() string {
	if e == nil {
		return ""
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("openai ws dial failed: status=%d err=%v", e.StatusCode, e.Err)
	}
	return fmt.Sprintf("openai ws dial failed: %v", e.Err)
}

func (e *WSDialError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type WSAcquireRequest struct {
	Account *WSPoolAccount
	WSURL   string
	Headers http.Header
	// HeadersFactory 在实际拨号前生成认证头，避免缓存或预热复用 Agent Assertion。
	HeadersFactory  func(context.Context, http.Header) (http.Header, error)
	ProxyURL        string
	TLSProfile      *tlsfingerprint.Profile
	TLSProfileKey   string
	PreferredConnID string
	// ForceNewConn: 强制本次获取新连接（避免复用导致连接内续链状态互相污染）。
	ForceNewConn bool
	// ForcePreferredConn: 强制本次只使用 PreferredConnID，禁止漂移到其它连接。
	ForcePreferredConn bool
}

type openAIWSHandshakeCompatibilityKey struct {
	betaFeatures        string
	codexInstallationID string
	sessionIDHyphen     string
	sessionIDUnderscore string
	threadID            string
	clientRequestID     string
	codexWindowID       string
}

type WSConnLease struct {
	pool      *WSConnPool
	AccountID int64
	Conn      *WSConn
	queueWait time.Duration
	connPick  time.Duration
	reused    bool
	released  atomic.Bool
}

func (l *WSConnLease) activeConn() (*WSConn, error) {
	if l == nil || l.Conn == nil {
		return nil, errOpenAIWSConnClosed
	}
	if l.released.Load() {
		return nil, errOpenAIWSConnClosed
	}
	return l.Conn, nil
}

func (l *WSConnLease) ConnID() string {
	if l == nil || l.Conn == nil {
		return ""
	}
	return l.Conn.id
}

func (l *WSConnLease) QueueWaitDuration() time.Duration {
	if l == nil {
		return 0
	}
	return l.queueWait
}

func (l *WSConnLease) ConnPickDuration() time.Duration {
	if l == nil {
		return 0
	}
	return l.connPick
}

func (l *WSConnLease) Reused() bool {
	if l == nil {
		return false
	}
	return l.reused
}

func (l *WSConnLease) HandshakeHeader(name string) string {
	if l == nil || l.Conn == nil {
		return ""
	}
	return l.Conn.handshakeHeader(name)
}

func (l *WSConnLease) HandshakeHeaders() http.Header {
	if l == nil || l.Conn == nil {
		return nil
	}
	return cloneHeader(l.Conn.handshakeHeaders)
}

func (l *WSConnLease) IsPrewarmed() bool {
	if l == nil || l.Conn == nil {
		return false
	}
	return l.Conn.isPrewarmed()
}

func (l *WSConnLease) MarkPrewarmed() {
	if l == nil || l.Conn == nil {
		return
	}
	l.Conn.markPrewarmed()
}

func (l *WSConnLease) WriteJSON(value any, timeout time.Duration) error {
	conn, err := l.activeConn()
	if err != nil {
		return err
	}
	return conn.writeJSONWithTimeout(context.Background(), value, timeout)
}

func (l *WSConnLease) WriteJSONWithContextTimeout(ctx context.Context, value any, timeout time.Duration) error {
	conn, err := l.activeConn()
	if err != nil {
		return err
	}
	return conn.writeJSONWithTimeout(ctx, value, timeout)
}

func (l *WSConnLease) WriteJSONContext(ctx context.Context, value any) error {
	conn, err := l.activeConn()
	if err != nil {
		return err
	}
	return conn.writeJSON(value, ctx)
}

func (l *WSConnLease) ReadMessage(timeout time.Duration) ([]byte, error) {
	conn, err := l.activeConn()
	if err != nil {
		return nil, err
	}
	return conn.readMessageWithTimeout(timeout)
}

func (l *WSConnLease) ReadMessageContext(ctx context.Context) ([]byte, error) {
	conn, err := l.activeConn()
	if err != nil {
		return nil, err
	}
	return conn.readMessage(ctx)
}

func (l *WSConnLease) ReadMessageWithContextTimeout(ctx context.Context, timeout time.Duration) ([]byte, error) {
	conn, err := l.activeConn()
	if err != nil {
		return nil, err
	}
	return conn.readMessageWithContextTimeout(ctx, timeout)
}

func (l *WSConnLease) PingWithTimeout(timeout time.Duration) error {
	conn, err := l.activeConn()
	if err != nil {
		return err
	}
	return conn.pingWithTimeout(timeout)
}

func (l *WSConnLease) SupportsIdlePingWithoutReader() bool {
	conn, err := l.activeConn()
	if err != nil {
		return false
	}
	return conn.supportsIdlePingWithoutReader()
}

func (l *WSConnLease) MarkBroken() {
	if l == nil || l.pool == nil || l.Conn == nil || l.released.Load() {
		return
	}
	l.pool.evictConn(l.AccountID, l.Conn.id)
}

func (l *WSConnLease) Release() {
	if l == nil || l.Conn == nil {
		return
	}
	if !l.released.CompareAndSwap(false, true) {
		return
	}
	l.Conn.release()
	if l.pool != nil {
		l.pool.runtimeMu.Lock()
		closed := l.pool.closed
		l.pool.runtimeMu.Unlock()
		if closed {
			l.pool.evictConn(l.AccountID, l.Conn.id)
			return
		}
		l.pool.notifyAccountPoolChanged(l.AccountID)
	}
}

type WSConn struct {
	id string
	ws WSClientConn

	handshakeHeaders       http.Header
	handshakeCompatibility openAIWSHandshakeCompatibilityKey
	tlsProfileKey          string
	routingAffinity        string

	leaseCh   chan struct{}
	closedCh  chan struct{}
	closeOnce sync.Once

	readMu  sync.Mutex
	writeMu sync.Mutex

	waiters       atomic.Int32
	createdAtNano atomic.Int64
	lastUsedNano  atomic.Int64
	prewarmed     atomic.Bool
}

func NewWSConn(id string, _ int64, ws WSClientConn, handshakeHeaders http.Header, profile *tlsfingerprint.Profile, profileKey string) *WSConn {
	now := time.Now()
	conn := &WSConn{

		id: id,

		ws: ws,

		handshakeHeaders: cloneHeader(handshakeHeaders),

		tlsProfileKey: openAIWSTLSProfileKey(profile, profileKey),

		leaseCh: make(chan struct{}, 1),

		closedCh: make(chan struct{}),
	}
	conn.leaseCh <- struct{}{}
	conn.createdAtNano.Store(now.UnixNano())
	conn.lastUsedNano.Store(now.UnixNano())
	return conn
}

func (c *WSConn) tryAcquire() bool {
	if c == nil {
		return false
	}
	select {
	case <-c.closedCh:
		return false
	default:
	}
	select {
	case <-c.leaseCh:
		select {
		case <-c.closedCh:
			c.release()
			return false
		default:
		}
		return true
	default:
		return false
	}
}

func (c *WSConn) acquire(ctx context.Context) error {
	if c == nil {
		return errOpenAIWSConnClosed
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.closedCh:
			return errOpenAIWSConnClosed
		case <-c.leaseCh:
			// 取消信号与租约可能同时就绪；消费信号量后再次检查上下文，并在
			// 返回取消错误前归还租约，避免已取消的等待者占死池化连接。
			if err := ctx.Err(); err != nil {
				c.release()
				return err
			}
			select {
			case <-c.closedCh:
				c.release()
				return errOpenAIWSConnClosed
			default:
			}
			return nil
		}
	}
}

func (c *WSConn) release() {
	if c == nil {
		return
	}
	select {
	case c.leaseCh <- struct{}{}:
	default:
	}
	c.touch()
}

func (c *WSConn) close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.closedCh)
		if c.ws != nil {
			_ = c.ws.Close()
		}
		select {
		case c.leaseCh <- struct{}{}:
		default:
		}
	})
}

func (c *WSConn) writeJSONWithTimeout(parent context.Context, value any, timeout time.Duration) error {
	if c == nil {
		return errOpenAIWSConnClosed
	}
	select {
	case <-c.closedCh:
		return errOpenAIWSConnClosed
	default:
	}

	writeCtx := parent
	if writeCtx == nil {
		writeCtx = context.Background()
	}
	if timeout <= 0 {
		return c.writeJSON(value, writeCtx)
	}
	var cancel context.CancelFunc
	writeCtx, cancel = context.WithTimeout(writeCtx, timeout)
	defer cancel()
	return c.writeJSON(value, writeCtx)
}

func (c *WSConn) writeJSON(value any, writeCtx context.Context) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.ws == nil {
		return errOpenAIWSConnClosed
	}
	if writeCtx == nil {
		writeCtx = context.Background()
	}
	if err := c.ws.WriteJSON(writeCtx, value); err != nil {
		return err
	}
	c.touch()
	return nil
}

func (c *WSConn) readMessageWithTimeout(timeout time.Duration) ([]byte, error) {
	return c.readMessageWithContextTimeout(context.Background(), timeout)
}

func (c *WSConn) readMessageWithContextTimeout(parent context.Context, timeout time.Duration) ([]byte, error) {
	if c == nil {
		return nil, errOpenAIWSConnClosed
	}
	select {
	case <-c.closedCh:
		return nil, errOpenAIWSConnClosed
	default:
	}

	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		return c.readMessage(parent)
	}
	readCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return c.readMessage(readCtx)
}

func (c *WSConn) readMessage(readCtx context.Context) ([]byte, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if c.ws == nil {
		return nil, errOpenAIWSConnClosed
	}
	if readCtx == nil {
		readCtx = context.Background()
	}
	payload, err := c.ws.ReadMessage(readCtx)
	if err != nil {
		return nil, err
	}
	c.touch()
	return payload, nil
}

func (c *WSConn) pingWithTimeout(timeout time.Duration) error {
	if c == nil {
		return errOpenAIWSConnClosed
	}
	select {
	case <-c.closedCh:
		return errOpenAIWSConnClosed
	default:
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.ws == nil {
		return errOpenAIWSConnClosed
	}
	if timeout <= 0 {
		timeout = openAIWSConnHealthCheckTO
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := c.ws.Ping(pingCtx); err != nil {
		return err
	}
	return nil
}

func (c *WSConn) supportsIdlePingWithoutReader() bool {
	if c == nil || c.ws == nil {
		return false
	}
	capable, ok := c.ws.(WSIdlePingCapable)
	// 测试与替代实现沿用历史探测行为，除非显式声明不支持无人读取时 Ping。
	return !ok || capable.SupportsIdlePingWithoutReader()
}

func (c *WSConn) touch() {
	if c == nil {
		return
	}
	c.lastUsedNano.Store(time.Now().UnixNano())
}

func (c *WSConn) createdAt() time.Time {
	if c == nil {
		return time.Time{}
	}
	nano := c.createdAtNano.Load()
	if nano <= 0 {
		return time.Time{}
	}
	return time.Unix(0, nano)
}

func (c *WSConn) lastUsedAt() time.Time {
	if c == nil {
		return time.Time{}
	}
	nano := c.lastUsedNano.Load()
	if nano <= 0 {
		return time.Time{}
	}
	return time.Unix(0, nano)
}

func (c *WSConn) idleDuration(now time.Time) time.Duration {
	if c == nil {
		return 0
	}
	last := c.lastUsedAt()
	if last.IsZero() {
		return 0
	}
	return now.Sub(last)
}

func (c *WSConn) age(now time.Time) time.Duration {
	if c == nil {
		return 0
	}
	created := c.createdAt()
	if created.IsZero() {
		return 0
	}
	return now.Sub(created)
}

func (c *WSConn) isLeased() bool {
	if c == nil {
		return false
	}
	return len(c.leaseCh) == 0
}

func (c *WSConn) handshakeHeader(name string) string {
	if c == nil || c.handshakeHeaders == nil {
		return ""
	}
	return strings.TrimSpace(c.handshakeHeaders.Get(strings.TrimSpace(name)))
}

// matchesHandshakeCompatibility 保证只复用握手阶段启用了相同硬兼容特征的连接。
func (c *WSConn) matchesHandshakeCompatibility(compatibility openAIWSHandshakeCompatibilityKey) bool {
	return c != nil && c.handshakeCompatibility == compatibility
}

func (c *WSConn) matchesRoutingAffinity(routingAffinity string) bool {
	return c != nil && c.routingAffinity == routingAffinity
}

func (c *WSConn) isPrewarmed() bool {
	if c == nil {
		return false
	}
	return c.prewarmed.Load()
}

func (c *WSConn) markPrewarmed() {
	if c == nil {
		return
	}
	c.prewarmed.Store(true)
}

func (c *WSConn) matchesTLSProfile(profile *tlsfingerprint.Profile, profileKey string) bool {
	if c == nil {
		return false
	}
	return c.tlsProfileKey == openAIWSTLSProfileKey(profile, profileKey)
}

func openAIWSTLSProfileKey(profile *tlsfingerprint.Profile, profileKey string) string {
	if key := stringsTrim(profileKey); key != "" {
		return key
	}
	return tlsfingerprint.CacheKey(profile)
}

type openAIWSAccountPool struct {
	mu            sync.Mutex
	conns         map[string]*WSConn
	pinnedConns   map[string]int
	changedCh     chan struct{}
	creating      int
	generation    uint64
	lastCleanupAt time.Time
	lastAcquire   *WSAcquireRequest
	prewarmActive bool
	prewarmUntil  time.Time
	prewarmFails  int
	prewarmFailAt time.Time
}

// changeChannelLocked 返回连接池状态变化时会关闭的通知通道，调用方必须持锁。
func (ap *openAIWSAccountPool) changeChannelLocked() chan struct{} {
	if ap.changedCh == nil {
		ap.changedCh = make(chan struct{})
	}
	return ap.changedCh
}

// signalChangedLocked 唤醒等待不兼容连接释放的请求，调用方必须持锁。
func (ap *openAIWSAccountPool) signalChangedLocked() {
	if ap == nil {
		return
	}
	if ap.changedCh != nil {
		close(ap.changedCh)
	}
	ap.changedCh = make(chan struct{})
}

type WSPoolMetricsSnapshot struct {
	AcquireTotal            int64
	AcquireReuseTotal       int64
	AcquireCreateTotal      int64
	AcquireQueueWaitTotal   int64
	AcquireQueueWaitMsTotal int64
	ConnPickTotal           int64
	ConnPickMsTotal         int64
	ScaleUpTotal            int64
	ScaleDownTotal          int64
}

type openAIWSPoolMetrics struct {
	acquireTotal          atomic.Int64
	acquireReuseTotal     atomic.Int64
	acquireCreateTotal    atomic.Int64
	acquireQueueWaitTotal atomic.Int64
	acquireQueueWaitMs    atomic.Int64
	connPickTotal         atomic.Int64
	connPickMs            atomic.Int64
	scaleUpTotal          atomic.Int64
	scaleDownTotal        atomic.Int64
}

type WSConnPool struct {
	started       bool
	runtimeMu     sync.Mutex
	closed        bool
	acquireWG     sync.WaitGroup
	prewarmWG     sync.WaitGroup
	prewarmCtx    context.Context
	prewarmCancel context.CancelFunc
	cfg           *WSPoolOptions
	// 通过接口解耦底层 WS 客户端实现，默认使用 coder/websocket。
	clientDialer WSClientDialer

	accounts sync.Map // key: int64(accountID), value: *openAIWSAccountPool
	seq      atomic.Uint64

	metrics openAIWSPoolMetrics

	workerStopCh chan struct{}
	workerWg     sync.WaitGroup
	closeOnce    sync.Once
}

func NewWSConnPool(cfg *WSPoolOptions) *WSConnPool {
	pool := &WSConnPool{
		cfg:          cfg,
		clientDialer: NewDefaultWSClientDialer(),
		workerStopCh: make(chan struct{}),
	}
	pool.prewarmCtx, pool.prewarmCancel = context.WithCancel(context.Background())
	return pool
}

func (p *WSConnPool) SnapshotMetrics() WSPoolMetricsSnapshot {
	if p == nil {
		return WSPoolMetricsSnapshot{}
	}
	return WSPoolMetricsSnapshot{

		AcquireTotal: p.metrics.acquireTotal.Load(),

		AcquireReuseTotal: p.metrics.acquireReuseTotal.Load(),

		AcquireCreateTotal: p.metrics.acquireCreateTotal.Load(),

		AcquireQueueWaitTotal: p.metrics.acquireQueueWaitTotal.Load(),

		AcquireQueueWaitMsTotal: p.metrics.acquireQueueWaitMs.Load(),

		ConnPickTotal: p.metrics.connPickTotal.Load(),

		ConnPickMsTotal: p.metrics.connPickMs.Load(),

		ScaleUpTotal: p.metrics.scaleUpTotal.Load(),

		ScaleDownTotal: p.metrics.scaleDownTotal.Load(),
	}
}

func (p *WSConnPool) SnapshotTransportMetrics() WSTransportMetricsSnapshot {
	if p == nil {
		return WSTransportMetricsSnapshot{}
	}
	if dialer, ok := p.clientDialer.(WSTransportMetricsDialer); ok {
		return dialer.SnapshotTransportMetrics()
	}
	return WSTransportMetricsSnapshot{}
}

func (p *WSConnPool) SetClientDialerForTest(dialer WSClientDialer) {
	if p == nil || dialer == nil {
		return
	}
	p.clientDialer = dialer
}

// Close 停止后台 worker 并关闭所有空闲连接，应在优雅关闭时调用。
func (p *WSConnPool) Close() {
	if p == nil {
		return
	}

	p.closeOnce.Do(func() {
		p.runtimeMu.Lock()
		p.closed = true
		if p.prewarmCancel != nil {
			p.prewarmCancel()
		}
		if p.workerStopCh != nil {
			close(p.workerStopCh)
		}
		p.runtimeMu.Unlock()
		closeConnections := func() {
			p.accounts.Range(func(_, value any) bool {
				ap, ok := value.(*openAIWSAccountPool)
				if !ok || ap == nil {
					return true
				}
				ap.mu.Lock()
				for _, conn := range ap.conns {
					if conn != nil && !conn.isLeased() {
						conn.close()
					}
				}
				ap.signalChangedLocked()
				ap.mu.Unlock()
				return true
			})
		}
		// 保留在途租约的原请求取消策略；租约返回时再关闭，空闲和迟到连接在这里回收。
		closeConnections()
		p.workerWg.Wait()
		p.acquireWG.Wait()
		p.prewarmWG.Wait()
		closeConnections()
	})
}

func (p *WSConnPool) startBackgroundWorkers() {
	if p == nil || p.workerStopCh == nil {
		return
	}
	p.workerWg.Add(2)
	go func() {
		defer p.workerWg.Done()
		p.runBackgroundPingWorker()
	}()
	go func() {
		defer p.workerWg.Done()
		p.runBackgroundCleanupWorker()
	}()
}

type openAIWSIdlePingCandidate struct {
	accountID int64
	conn      *WSConn
}

func (p *WSConnPool) runBackgroundPingWorker() {
	if p == nil {
		return
	}
	ticker := time.NewTicker(openAIWSBackgroundPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.runBackgroundPingSweep()
		case <-p.workerStopCh:
			return
		}
	}
}

func (p *WSConnPool) runBackgroundPingSweep() {
	if p == nil {
		return
	}
	candidates := p.snapshotIdleConnsForPing()
	var g errgroup.Group
	g.SetLimit(10)
	for _, item := range candidates {
		item := item
		if item.conn == nil || item.conn.isLeased() || item.conn.waiters.Load() > 0 || !item.conn.supportsIdlePingWithoutReader() {
			continue
		}
		g.Go(func() error {
			if err := item.conn.pingWithTimeout(openAIWSConnHealthCheckTO); err != nil {
				p.evictConn(item.accountID, item.conn.id)
			}
			return nil
		})
	}
	_ = g.Wait()
}

func (p *WSConnPool) snapshotIdleConnsForPing() []openAIWSIdlePingCandidate {
	if p == nil {
		return nil
	}
	candidates := make([]openAIWSIdlePingCandidate, 0)
	p.accounts.Range(func(key, value any) bool {
		accountID, ok := key.(int64)
		if !ok || accountID <= 0 {
			return true
		}
		ap, ok := value.(*openAIWSAccountPool)
		if !ok || ap == nil {
			return true
		}
		ap.mu.Lock()
		for _, conn := range ap.conns {
			if conn == nil || conn.isLeased() || conn.waiters.Load() > 0 {
				continue
			}
			candidates = append(candidates, openAIWSIdlePingCandidate{
				accountID: accountID,
				conn:      conn,
			})
		}
		ap.mu.Unlock()
		return true
	})
	return candidates
}

func (p *WSConnPool) runBackgroundCleanupWorker() {
	if p == nil {
		return
	}
	ticker := time.NewTicker(openAIWSBackgroundSweepTicker)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.runBackgroundCleanupSweep(time.Now())
		case <-p.workerStopCh:
			return
		}
	}
}

func (p *WSConnPool) runBackgroundCleanupSweep(now time.Time) {
	if p == nil {
		return
	}
	type cleanupResult struct {
		evicted []*WSConn
	}
	results := make([]cleanupResult, 0)
	p.accounts.Range(func(_ any, value any) bool {
		ap, ok := value.(*openAIWSAccountPool)
		if !ok || ap == nil {
			return true
		}
		maxConns := p.maxConnsHardCap()
		ap.mu.Lock()
		if ap.lastAcquire != nil && ap.lastAcquire.Account != nil {
			maxConns = p.effectiveMaxConnsByAccount(ap.lastAcquire.Account)
		}
		evicted := p.cleanupAccountLocked(ap, now, maxConns)
		ap.lastCleanupAt = now
		ap.mu.Unlock()
		if len(evicted) > 0 {
			results = append(results, cleanupResult{evicted: evicted})
		}
		return true
	})
	for _, result := range results {
		closeOpenAIWSConns(result.evicted)
	}
}

func (p *WSConnPool) Acquire(ctx context.Context, req WSAcquireRequest) (*WSConnLease, error) {
	if p != nil {
		p.runtimeMu.Lock()
		if p.closed {
			p.runtimeMu.Unlock()
			return nil, errOpenAIWSConnClosed
		}
		p.acquireWG.Add(1)
		p.runtimeMu.Unlock()
		defer p.acquireWG.Done()
	}
	if p != nil {
		p.metrics.acquireTotal.Add(1)
	}
	return p.acquire(ctx, CloneWSAcquireRequest(req), 0)
}

func (p *WSConnPool) acquire(ctx context.Context, req WSAcquireRequest, retry int) (*WSConnLease, error) {
	if p == nil || req.Account == nil || req.Account.ID <= 0 {
		return nil, errors.New("invalid ws acquire request")
	}
	if stringsTrim(req.WSURL) == "" {
		return nil, errors.New("ws url is empty")
	}

retryAcquire:
	p.runtimeMu.Lock()
	closed := p.closed
	p.runtimeMu.Unlock()
	if closed {
		return nil, errOpenAIWSConnClosed
	}
	accountID := req.Account.ID
	compatibility := normalizeOpenAIWSHandshakeCompatibility(req.Account, req.Headers)
	routingAffinity := normalizeOpenAIWSRoutingAffinity(req.Headers)
	effectiveMaxConns := p.effectiveMaxConnsByAccount(req.Account)
	if effectiveMaxConns <= 0 {
		return nil, ErrOpenAIWSConnQueueFull
	}
	var evicted []*WSConn
	ap := p.getOrCreateAccountPool(accountID)
	ap.mu.Lock()
	acquireGeneration := ap.generation
	now := time.Now()
	if ap.lastCleanupAt.IsZero() || now.Sub(ap.lastCleanupAt) >= openAIWSAcquireCleanupInterval {
		evicted = p.cleanupAccountLocked(ap, now, effectiveMaxConns)
		ap.lastCleanupAt = now
	}
	pickStartedAt := time.Now()
	allowReuse := !req.ForceNewConn
	preferredConnID := stringsTrim(req.PreferredConnID)
	forcePreferredConn := allowReuse && req.ForcePreferredConn

	if allowReuse {
		if forcePreferredConn {
			if preferredConnID == "" {
				p.recordConnPickDuration(time.Since(pickStartedAt))
				ap.mu.Unlock()
				closeOpenAIWSConns(evicted)
				return nil, ErrOpenAIWSPreferredConnUnavailable
			}
			preferredConn, ok := ap.conns[preferredConnID]
			if !ok || !preferredConn.matchesHandshakeCompatibility(compatibility) {
				p.recordConnPickDuration(time.Since(pickStartedAt))
				ap.mu.Unlock()
				closeOpenAIWSConns(evicted)
				return nil, ErrOpenAIWSPreferredConnUnavailable
			}
			if !preferredConn.matchesTLSProfile(req.TLSProfile, req.TLSProfileKey) {
				p.recordConnPickDuration(time.Since(pickStartedAt))
				ap.mu.Unlock()
				closeOpenAIWSConns(evicted)
				return nil, ErrOpenAIWSPreferredConnUnavailable
			}
			if preferredConn.tryAcquire() {
				connPick := time.Since(pickStartedAt)
				p.recordConnPickDuration(connPick)
				ap.mu.Unlock()
				closeOpenAIWSConns(evicted)
				if p.shouldHealthCheckConn(preferredConn) {
					if err := preferredConn.pingWithTimeout(openAIWSConnHealthCheckTO); err != nil {
						preferredConn.close()
						p.evictConn(accountID, preferredConn.id)
						if retry < 1 {
							return p.acquire(ctx, req, retry+1)
						}
						return nil, err
					}
				}
				lease := &WSConnLease{
					pool:      p,
					AccountID: accountID,
					Conn:      preferredConn,
					connPick:  connPick,
					reused:    true,
				}
				p.metrics.acquireReuseTotal.Add(1)
				p.recordLastSuccessfulAcquire(accountID, acquireGeneration, req)
				p.ensureTargetIdleAsync(accountID)
				return lease, nil
			}

			connPick := time.Since(pickStartedAt)
			p.recordConnPickDuration(connPick)
			if int(preferredConn.waiters.Load()) >= p.queueLimitPerConn() {
				ap.mu.Unlock()
				closeOpenAIWSConns(evicted)
				return nil, ErrOpenAIWSConnQueueFull
			}
			preferredConn.waiters.Add(1)
			ap.mu.Unlock()
			closeOpenAIWSConns(evicted)
			defer preferredConn.waiters.Add(-1)
			waitStart := time.Now()
			p.metrics.acquireQueueWaitTotal.Add(1)

			if err := preferredConn.acquire(ctx); err != nil {
				if errors.Is(err, errOpenAIWSConnClosed) && retry < 1 {
					return p.acquire(ctx, req, retry+1)
				}
				return nil, err
			}
			if p.shouldHealthCheckConn(preferredConn) {
				if err := preferredConn.pingWithTimeout(openAIWSConnHealthCheckTO); err != nil {
					preferredConn.release()
					preferredConn.close()
					p.evictConn(accountID, preferredConn.id)
					if retry < 1 {
						return p.acquire(ctx, req, retry+1)
					}
					return nil, err
				}
			}

			queueWait := time.Since(waitStart)
			p.metrics.acquireQueueWaitMs.Add(queueWait.Milliseconds())
			lease := &WSConnLease{

				pool: p,

				AccountID: accountID,

				Conn: preferredConn,

				queueWait: queueWait,

				connPick: connPick,

				reused: true,
			}
			p.metrics.acquireReuseTotal.Add(1)
			p.recordLastSuccessfulAcquire(accountID, acquireGeneration, req)
			p.ensureTargetIdleAsync(accountID)
			return lease, nil
		}

		if preferredConnID != "" {
			if conn, ok := ap.conns[preferredConnID]; ok &&
				conn.matchesTLSProfile(req.TLSProfile, req.TLSProfileKey) &&
				conn.matchesHandshakeCompatibility(compatibility) &&
				conn.tryAcquire() {
				connPick := time.Since(pickStartedAt)
				p.recordConnPickDuration(connPick)
				ap.mu.Unlock()
				closeOpenAIWSConns(evicted)
				if p.shouldHealthCheckConn(conn) {
					if err := conn.pingWithTimeout(openAIWSConnHealthCheckTO); err != nil {
						conn.close()
						p.evictConn(accountID, conn.id)
						if retry < 1 {
							return p.acquire(ctx, req, retry+1)
						}
						return nil, err
					}
				}
				lease := &WSConnLease{pool: p, AccountID: accountID, Conn: conn, connPick: connPick, reused: true}
				p.metrics.acquireReuseTotal.Add(1)
				p.recordLastSuccessfulAcquire(accountID, acquireGeneration, req)
				p.ensureTargetIdleAsync(accountID)
				return lease, nil
			}
		}

		// routing hint 只在拨号和普通复用时提供软亲和；连接的硬兼容性仍由
		// beta feature 与 TLS 指纹共同决定。
		best := p.pickLeastBusyConnWithRoutingAffinityLocked(
			ap, req.TLSProfile, req.TLSProfileKey, compatibility, routingAffinity,
		)
		if best != nil && best.tryAcquire() {
			connPick := time.Since(pickStartedAt)
			p.recordConnPickDuration(connPick)
			ap.mu.Unlock()
			closeOpenAIWSConns(evicted)
			if p.shouldHealthCheckConn(best) {
				if err := best.pingWithTimeout(openAIWSConnHealthCheckTO); err != nil {
					best.close()
					p.evictConn(accountID, best.id)
					if retry < 1 {
						return p.acquire(ctx, req, retry+1)
					}
					return nil, err
				}
			}
			lease := &WSConnLease{pool: p, AccountID: accountID, Conn: best, connPick: connPick, reused: true}
			p.metrics.acquireReuseTotal.Add(1)
			p.recordLastSuccessfulAcquire(accountID, acquireGeneration, req)
			p.ensureTargetIdleAsync(accountID)
			return lease, nil
		}
		if routingAffinity == "" || len(ap.conns)+ap.creating >= effectiveMaxConns {
			for _, conn := range ap.conns {
				if conn == nil || conn == best || !conn.matchesHandshakeCompatibility(compatibility) {
					continue
				}
				if !conn.matchesTLSProfile(req.TLSProfile, req.TLSProfileKey) {
					continue
				}
				if conn.tryAcquire() {
					connPick := time.Since(pickStartedAt)
					p.recordConnPickDuration(connPick)
					ap.mu.Unlock()
					closeOpenAIWSConns(evicted)
					if p.shouldHealthCheckConn(conn) {
						if err := conn.pingWithTimeout(openAIWSConnHealthCheckTO); err != nil {
							conn.close()
							p.evictConn(accountID, conn.id)
							if retry < 1 {
								return p.acquire(ctx, req, retry+1)
							}
							return nil, err
						}
					}
					lease := &WSConnLease{pool: p, AccountID: accountID, Conn: conn, connPick: connPick, reused: true}
					p.metrics.acquireReuseTotal.Add(1)
					p.recordLastSuccessfulAcquire(accountID, acquireGeneration, req)
					p.ensureTargetIdleAsync(accountID)
					return lease, nil
				}
			}
		}
	}

	if !req.ForceNewConn && len(ap.conns)+ap.creating >= effectiveMaxConns {
		affine := p.pickLeastBusyConnWithRoutingAffinityLocked(
			ap, req.TLSProfile, req.TLSProfileKey, compatibility, routingAffinity,
		)
		if idle := p.pickOldestIdleConnWithoutHandshakeCompatibilityLocked(
			ap, req.TLSProfile, req.TLSProfileKey, compatibility,
		); idle != nil {
			delete(ap.conns, idle.id)
			evicted = append(evicted, idle)
			p.metrics.scaleDownTotal.Add(1)
		} else if affine == nil {
			compatible := p.pickLeastBusyConnLocked(
				ap, "", req.TLSProfile, req.TLSProfileKey, compatibility,
			)
			if compatible != nil {
				// 池已满且硬兼容连接都在忙时，hint 保持软约束，转到下方排队。
				goto acquireAtCapacity
			}
			hasConnection := false
			for _, conn := range ap.conns {
				if conn != nil {
					hasConnection = true
					break
				}
			}
			if !hasConnection && ap.creating == 0 {
				ap.mu.Unlock()
				closeOpenAIWSConns(evicted)
				return nil, errOpenAIWSConnClosed
			}
			changedCh := ap.changeChannelLocked()
			ap.mu.Unlock()
			closeOpenAIWSConns(evicted)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-changedCh:
				goto retryAcquire
			}
		}
	}

	if req.ForceNewConn && len(ap.conns)+ap.creating >= effectiveMaxConns {
		if idle := p.pickOldestIdleConnLocked(ap); idle != nil {
			delete(ap.conns, idle.id)
			evicted = append(evicted, idle)
			p.metrics.scaleDownTotal.Add(1)
		}
	}
	if len(ap.conns)+ap.creating >= effectiveMaxConns {
		if idle := p.pickOldestIdleMismatchedTLSConnLocked(ap, req.TLSProfile, req.TLSProfileKey); idle != nil {
			delete(ap.conns, idle.id)
			evicted = append(evicted, idle)
			p.metrics.scaleDownTotal.Add(1)
		}
	}

	if len(ap.conns)+ap.creating < effectiveMaxConns {
		connPick := time.Since(pickStartedAt)
		p.recordConnPickDuration(connPick)
		ap.creating++
		ap.mu.Unlock()
		closeOpenAIWSConns(evicted)

		conn, dialErr := p.dialConn(ctx, req)

		ap = p.getOrCreateAccountPool(accountID)
		ap.mu.Lock()
		ap.creating--
		if ap.generation != acquireGeneration {
			ap.signalChangedLocked()
			ap.mu.Unlock()
			if conn != nil {
				conn.close()
			}
			if retry < 1 {
				return p.acquire(ctx, req, retry+1)
			}
			return nil, errOpenAIWSConnClosed
		}
		if dialErr != nil {
			ap.prewarmFails++
			ap.prewarmFailAt = time.Now()
			ap.signalChangedLocked()
			ap.mu.Unlock()
			return nil, dialErr
		}
		// 新连接发布到池前先领取租约，避免下方唤醒的拓扑等待者抢先获取，
		// 导致发起拨号的请求反而排在其后。
		if !conn.tryAcquire() {
			ap.signalChangedLocked()
			ap.mu.Unlock()
			conn.close()
			return nil, errOpenAIWSConnClosed
		}
		ap.conns[conn.id] = conn
		ap.prewarmFails = 0
		ap.prewarmFailAt = time.Time{}
		// 唤醒曾观察到正在创建连接但池内无兼容连接的请求；否则即使拓扑已
		// 变化，它们仍可能一直等待到新租约释放。
		ap.signalChangedLocked()
		ap.mu.Unlock()
		p.metrics.acquireCreateTotal.Add(1)
		lease := &WSConnLease{pool: p, AccountID: accountID, Conn: conn, connPick: connPick}
		p.recordLastSuccessfulAcquire(accountID, acquireGeneration, req)
		p.ensureTargetIdleAsync(accountID)
		return lease, nil
	}

	if req.ForceNewConn {
		p.recordConnPickDuration(time.Since(pickStartedAt))
		ap.mu.Unlock()
		closeOpenAIWSConns(evicted)
		return nil, ErrOpenAIWSConnQueueFull
	}

acquireAtCapacity:
	target := p.pickLeastBusyConnLocked(
		ap, req.PreferredConnID, req.TLSProfile, req.TLSProfileKey, compatibility,
	)
	connPick := time.Since(pickStartedAt)
	p.recordConnPickDuration(connPick)
	if target == nil {
		ap.mu.Unlock()
		closeOpenAIWSConns(evicted)
		return nil, errOpenAIWSConnClosed
	}
	if int(target.waiters.Load()) >= p.queueLimitPerConn() {
		ap.mu.Unlock()
		closeOpenAIWSConns(evicted)
		return nil, ErrOpenAIWSConnQueueFull
	}
	target.waiters.Add(1)
	ap.mu.Unlock()
	closeOpenAIWSConns(evicted)
	defer target.waiters.Add(-1)
	waitStart := time.Now()
	p.metrics.acquireQueueWaitTotal.Add(1)

	if err := target.acquire(ctx); err != nil {
		if errors.Is(err, errOpenAIWSConnClosed) && retry < 1 {
			return p.acquire(ctx, req, retry+1)
		}
		return nil, err
	}
	if p.shouldHealthCheckConn(target) {
		if err := target.pingWithTimeout(openAIWSConnHealthCheckTO); err != nil {
			target.release()
			target.close()
			p.evictConn(accountID, target.id)
			if retry < 1 {
				return p.acquire(ctx, req, retry+1)
			}
			return nil, err
		}
	}

	queueWait := time.Since(waitStart)
	p.metrics.acquireQueueWaitMs.Add(queueWait.Milliseconds())
	lease := &WSConnLease{pool: p, AccountID: accountID, Conn: target, queueWait: queueWait, connPick: connPick, reused: true}
	p.metrics.acquireReuseTotal.Add(1)
	p.recordLastSuccessfulAcquire(accountID, acquireGeneration, req)
	p.ensureTargetIdleAsync(accountID)
	return lease, nil
}

func (p *WSConnPool) recordConnPickDuration(duration time.Duration) {
	if p == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	p.metrics.connPickTotal.Add(1)
	p.metrics.connPickMs.Add(duration.Milliseconds())
}

func (p *WSConnPool) recordLastSuccessfulAcquire(accountID int64, generation uint64, req WSAcquireRequest) {
	if p == nil || accountID <= 0 {
		return
	}
	ap, ok := p.getAccountPool(accountID)
	if !ok || ap == nil {
		return
	}
	ap.mu.Lock()
	if ap.generation != generation {
		ap.mu.Unlock()
		return
	}
	ap.lastAcquire = CloneWSAcquireRequestPtr(&req)
	ap.mu.Unlock()
}

func (p *WSConnPool) pickOldestIdleConnLocked(ap *openAIWSAccountPool) *WSConn {
	if ap == nil || len(ap.conns) == 0 {
		return nil
	}
	var oldest *WSConn
	for _, conn := range ap.conns {
		if conn == nil || conn.isLeased() || conn.waiters.Load() > 0 || p.isConnPinnedLocked(ap, conn.id) {
			continue
		}
		if oldest == nil || conn.lastUsedAt().Before(oldest.lastUsedAt()) {
			oldest = conn
		}
	}
	return oldest
}

// pickOldestIdleMismatchedTLSConnLocked 选取可淘汰的 TLS 配置不兼容空闲连接。
func (p *WSConnPool) pickOldestIdleMismatchedTLSConnLocked(ap *openAIWSAccountPool, profile *tlsfingerprint.Profile, profileKey string) *WSConn {
	if ap == nil || len(ap.conns) == 0 {
		return nil
	}
	var oldest *WSConn
	for _, conn := range ap.conns {
		if conn == nil || conn.isLeased() || conn.waiters.Load() > 0 || p.isConnPinnedLocked(ap, conn.id) || conn.matchesTLSProfile(profile, profileKey) {
			continue
		}
		if oldest == nil || conn.lastUsedAt().Before(oldest.lastUsedAt()) {
			oldest = conn
		}
	}
	return oldest
}

// pickOldestIdleConnWithoutHandshakeCompatibilityLocked 选取握手或 TLS 不兼容的空闲连接，
// routing hint 属于软亲和，不应阻止池在容量受限时回收连接。
func (p *WSConnPool) pickOldestIdleConnWithoutHandshakeCompatibilityLocked(
	ap *openAIWSAccountPool,
	profile *tlsfingerprint.Profile,
	profileKey string,
	compatibility openAIWSHandshakeCompatibilityKey,
) *WSConn {
	if ap == nil || len(ap.conns) == 0 {
		return nil
	}
	var oldest *WSConn
	for _, conn := range ap.conns {
		if conn == nil ||
			(conn.matchesTLSProfile(profile, profileKey) &&
				conn.matchesHandshakeCompatibility(compatibility)) ||
			conn.isLeased() || conn.waiters.Load() > 0 || p.isConnPinnedLocked(ap, conn.id) {
			continue
		}
		if oldest == nil || conn.lastUsedAt().Before(oldest.lastUsedAt()) {
			oldest = conn
		}
	}
	return oldest
}

func (p *WSConnPool) getOrCreateAccountPool(accountID int64) *openAIWSAccountPool {
	if p == nil || accountID <= 0 {
		return nil
	}
	if existing, ok := p.accounts.Load(accountID); ok {
		if ap, typed := existing.(*openAIWSAccountPool); typed && ap != nil {
			return ap
		}
	}
	ap := &openAIWSAccountPool{
		conns:       make(map[string]*WSConn),
		pinnedConns: make(map[string]int),
		changedCh:   make(chan struct{}),
	}
	actual, _ := p.accounts.LoadOrStore(accountID, ap)
	if typed, ok := actual.(*openAIWSAccountPool); ok && typed != nil {
		return typed
	}
	return ap
}

// ensureAccountPoolLocked 兼容旧调用。
func (p *WSConnPool) ensureAccountPoolLocked(accountID int64) *openAIWSAccountPool {
	return p.getOrCreateAccountPool(accountID)
}

func (p *WSConnPool) getAccountPool(accountID int64) (*openAIWSAccountPool, bool) {
	if p == nil || accountID <= 0 {
		return nil, false
	}
	value, ok := p.accounts.Load(accountID)
	if !ok || value == nil {
		return nil, false
	}
	ap, typed := value.(*openAIWSAccountPool)
	return ap, typed && ap != nil
}

// notifyAccountPoolChanged 唤醒等待该账号连接池出现兼容空闲连接的请求。
func (p *WSConnPool) notifyAccountPoolChanged(accountID int64) {
	ap, ok := p.getAccountPool(accountID)
	if !ok || ap == nil {
		return
	}
	ap.mu.Lock()
	ap.signalChangedLocked()
	ap.mu.Unlock()
}

func (p *WSConnPool) isConnPinnedLocked(ap *openAIWSAccountPool, connID string) bool {
	if ap == nil || connID == "" || len(ap.pinnedConns) == 0 {
		return false
	}
	return ap.pinnedConns[connID] > 0
}

func (p *WSConnPool) cleanupAccountLocked(ap *openAIWSAccountPool, now time.Time, maxConns int) []*WSConn {
	if ap == nil {
		return nil
	}
	maxAge := p.maxConnAge()

	evicted := make([]*WSConn, 0)
	for id, conn := range ap.conns {
		if conn == nil {
			delete(ap.conns, id)
			if len(ap.pinnedConns) > 0 {
				delete(ap.pinnedConns, id)
			}
			continue
		}
		select {
		case <-conn.closedCh:
			delete(ap.conns, id)
			if len(ap.pinnedConns) > 0 {
				delete(ap.pinnedConns, id)
			}
			evicted = append(evicted, conn)
			continue
		default:
		}
		if p.isConnPinnedLocked(ap, id) {
			continue
		}
		if !conn.isLeased() && conn.waiters.Load() == 0 &&
			!conn.supportsIdlePingWithoutReader() &&
			conn.idleDuration(now) >= openAIWSConnIdleRecycleAfter {
			delete(ap.conns, id)
			if len(ap.pinnedConns) > 0 {
				delete(ap.pinnedConns, id)
			}
			evicted = append(evicted, conn)
			p.metrics.scaleDownTotal.Add(1)
			continue
		}
		if maxAge > 0 && !conn.isLeased() && conn.age(now) > maxAge {
			delete(ap.conns, id)
			if len(ap.pinnedConns) > 0 {
				delete(ap.pinnedConns, id)
			}
			evicted = append(evicted, conn)
		}
	}

	if maxConns <= 0 {
		maxConns = p.maxConnsHardCap()
	}
	maxIdle := p.maxIdlePerAccount()
	if maxIdle < 0 || maxIdle > maxConns {
		maxIdle = maxConns
	}
	if maxIdle >= 0 && len(ap.conns) > maxIdle {
		idleConns := make([]*WSConn, 0, len(ap.conns))
		for id, conn := range ap.conns {
			if conn == nil {
				delete(ap.conns, id)
				if len(ap.pinnedConns) > 0 {
					delete(ap.pinnedConns, id)
				}
				continue
			}
			// 有等待者的连接不能在清理阶段被淘汰，否则等待中的 acquire 会收到 closed 错误。
			if conn.isLeased() || conn.waiters.Load() > 0 || p.isConnPinnedLocked(ap, conn.id) {
				continue
			}
			idleConns = append(idleConns, conn)
		}
		sort.SliceStable(idleConns, func(i, j int) bool {
			return idleConns[i].lastUsedAt().Before(idleConns[j].lastUsedAt())
		})
		redundant := len(ap.conns) - maxIdle
		if redundant > len(idleConns) {
			redundant = len(idleConns)
		}
		for i := 0; i < redundant; i++ {
			conn := idleConns[i]
			delete(ap.conns, conn.id)
			if len(ap.pinnedConns) > 0 {
				delete(ap.pinnedConns, conn.id)
			}
			evicted = append(evicted, conn)
		}
		if redundant > 0 {
			p.metrics.scaleDownTotal.Add(int64(redundant))
		}
	}
	if len(evicted) > 0 {
		ap.signalChangedLocked()
	}

	return evicted
}

func (p *WSConnPool) pickLeastBusyConnLocked(
	ap *openAIWSAccountPool,
	preferredConnID string,
	profile *tlsfingerprint.Profile,
	profileKey string,
	compatibility openAIWSHandshakeCompatibilityKey,
) *WSConn {
	if ap == nil || len(ap.conns) == 0 {
		return nil
	}
	preferredConnID = stringsTrim(preferredConnID)
	if preferredConnID != "" {
		if conn, ok := ap.conns[preferredConnID]; ok {
			if conn.matchesTLSProfile(profile, profileKey) && conn.matchesHandshakeCompatibility(compatibility) {
				return conn
			}
			return nil
		}
	}
	var best *WSConn
	var bestWaiters int32
	var bestLastUsed time.Time
	for _, conn := range ap.conns {
		if conn == nil || !conn.matchesHandshakeCompatibility(compatibility) {
			continue
		}
		if !conn.matchesTLSProfile(profile, profileKey) {
			continue
		}
		waiters := conn.waiters.Load()
		lastUsed := conn.lastUsedAt()
		if best == nil ||
			waiters < bestWaiters ||
			(waiters == bestWaiters && lastUsed.Before(bestLastUsed)) {
			best = conn
			bestWaiters = waiters
			bestLastUsed = lastUsed
		}
	}
	return best
}

func (p *WSConnPool) pickLeastBusyConnWithRoutingAffinityLocked(
	ap *openAIWSAccountPool,
	profile *tlsfingerprint.Profile,
	profileKey string,
	compatibility openAIWSHandshakeCompatibilityKey,
	routingAffinity string,
) *WSConn {
	if ap == nil || len(ap.conns) == 0 {
		return nil
	}
	var best *WSConn
	var bestWaiters int32
	var bestLastUsed time.Time
	for _, conn := range ap.conns {
		if conn == nil ||
			!conn.matchesTLSProfile(profile, profileKey) ||
			!conn.matchesHandshakeCompatibility(compatibility) ||
			!conn.matchesRoutingAffinity(routingAffinity) {
			continue
		}
		waiters := conn.waiters.Load()
		lastUsed := conn.lastUsedAt()
		if best == nil ||
			waiters < bestWaiters ||
			(waiters == bestWaiters && lastUsed.Before(bestLastUsed)) {
			best = conn
			bestWaiters = waiters
			bestLastUsed = lastUsed
		}
	}
	return best
}

func accountPoolLoadLocked(ap *openAIWSAccountPool) (inflight int, waiters int) {
	if ap == nil {
		return 0, 0
	}
	for _, conn := range ap.conns {
		if conn == nil {
			continue
		}
		if conn.isLeased() {
			inflight++
		}
		waiters += int(conn.waiters.Load())
	}
	return inflight, waiters
}

// AccountPoolLoad 返回指定账号连接池的并发与排队快照。
func (p *WSConnPool) AccountPoolLoad(accountID int64) (inflight int, waiters int, conns int) {
	if p == nil || accountID <= 0 {
		return 0, 0, 0
	}
	ap, ok := p.getAccountPool(accountID)
	if !ok || ap == nil {
		return 0, 0, 0
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	inflight, waiters = accountPoolLoadLocked(ap)
	return inflight, waiters, len(ap.conns)
}

func (p *WSConnPool) ensureTargetIdleAsync(accountID int64) {
	if p == nil || accountID <= 0 {
		return
	}

	p.runtimeMu.Lock()
	defer p.runtimeMu.Unlock()
	if p.closed {
		return
	}

	var req WSAcquireRequest
	generation := uint64(0)
	need := 0
	ap, ok := p.getAccountPool(accountID)
	if !ok || ap == nil {
		return
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if ap.lastAcquire == nil {
		return
	}
	if ap.prewarmActive {
		return
	}
	now := time.Now()
	if !ap.prewarmUntil.IsZero() && now.Before(ap.prewarmUntil) {
		return
	}
	if p.shouldSuppressPrewarmLocked(ap, now) {
		return
	}
	effectiveMaxConns := p.maxConnsHardCap()
	if ap.lastAcquire != nil && ap.lastAcquire.Account != nil {
		effectiveMaxConns = p.effectiveMaxConnsByAccount(ap.lastAcquire.Account)
	}
	target := p.targetConnCountLocked(ap, effectiveMaxConns)
	current := len(ap.conns) + ap.creating
	if current >= target {
		return
	}
	need = target - current
	if need <= 0 {
		return
	}
	req = CloneWSAcquireRequest(*ap.lastAcquire)
	generation = ap.generation
	ap.prewarmActive = true
	if cooldown := p.prewarmCooldown(); cooldown > 0 {
		ap.prewarmUntil = now.Add(cooldown)
	}
	ap.creating += need
	p.metrics.scaleUpTotal.Add(int64(need))

	p.prewarmWG.Add(1)
	go func() { defer p.prewarmWG.Done(); p.prewarmConns(accountID, req, need, generation) }()
}

func (p *WSConnPool) targetConnCountLocked(ap *openAIWSAccountPool, maxConns int) int {
	if ap == nil {
		return 0
	}

	if maxConns <= 0 {
		return 0
	}

	minIdle := p.minIdlePerAccount()
	if minIdle < 0 {
		minIdle = 0
	}
	if minIdle > maxConns {
		minIdle = maxConns
	}

	inflight, waiters := accountPoolLoadLocked(ap)
	utilization := p.targetUtilization()
	demand := inflight + waiters
	if demand <= 0 {
		return minIdle
	}

	target := 1
	if demand > 1 {
		target = int(math.Ceil(float64(demand) / utilization))
	}
	if waiters > 0 && target < len(ap.conns)+1 {
		target = len(ap.conns) + 1
	}
	if target < minIdle {
		target = minIdle
	}
	if target > maxConns {
		target = maxConns
	}
	return target
}

func (p *WSConnPool) prewarmConns(accountID int64, req WSAcquireRequest, total int, generations ...uint64) {
	generation := uint64(0)
	if len(generations) > 0 {
		generation = generations[0]
	}
	staleTarget := false
	defer func() {
		if ap, ok := p.getAccountPool(accountID); ok && ap != nil {
			ap.mu.Lock()
			ap.prewarmActive = false
			ap.signalChangedLocked()
			ap.mu.Unlock()
		}
		if staleTarget {
			// 旧拨号尚未结束时出现了更新的获取请求；先清除 prewarmActive，
			// 再按最新 beta/hint 目标重新计算空闲连接需求。
			p.ensureTargetIdleAsync(accountID)
		}
	}()

	for i := 0; i < total; i++ {
		parent := p.prewarmCtx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, p.dialTimeout()+openAIWSConnPrewarmExtraDelay)
		conn, err := p.dialConn(ctx, req)
		cancel()

		ap, ok := p.getAccountPool(accountID)
		if !ok || ap == nil {
			if conn != nil {
				conn.close()
			}
			return
		}
		ap.mu.Lock()
		if ap.creating > 0 {
			ap.creating--
		}
		if err != nil {
			ap.prewarmFails++
			ap.prewarmFailAt = time.Now()
			ap.signalChangedLocked()
			ap.mu.Unlock()
			continue
		}
		if ap.generation != generation || ap.lastAcquire == nil {
			ap.mu.Unlock()
			conn.close()
			continue
		}
		if !sameOpenAIWSPrewarmTarget(req, *ap.lastAcquire) {
			staleTarget = true
			ap.signalChangedLocked()
			ap.mu.Unlock()
			conn.close()
			continue
		}
		if len(ap.conns) >= p.effectiveMaxConnsByAccount(req.Account) {
			ap.signalChangedLocked()
			ap.mu.Unlock()
			conn.close()
			continue
		}
		ap.conns[conn.id] = conn
		ap.prewarmFails = 0
		ap.prewarmFailAt = time.Time{}
		ap.signalChangedLocked()
		ap.mu.Unlock()
	}
}

// ClearAccount 关闭账号的全部池化连接并丢弃延迟预热状态；代次检查阻止恢复前启动的预热连接重新入池。
func (p *WSConnPool) ClearAccount(accountID int64) {
	if p == nil || accountID <= 0 {
		return
	}
	ap, ok := p.getAccountPool(accountID)
	if !ok || ap == nil {
		return
	}
	ap.mu.Lock()
	ap.generation++
	conns := make([]*WSConn, 0, len(ap.conns))
	for id, conn := range ap.conns {
		delete(ap.conns, id)
		delete(ap.pinnedConns, id)
		if conn != nil {
			conns = append(conns, conn)
		}
	}
	ap.lastAcquire = nil
	ap.prewarmUntil = time.Time{}
	ap.prewarmFails = 0
	ap.prewarmFailAt = time.Time{}
	ap.signalChangedLocked()
	ap.mu.Unlock()
	closeOpenAIWSConns(conns)
}

func (p *WSConnPool) evictConn(accountID int64, connID string) {
	if p == nil || accountID <= 0 || stringsTrim(connID) == "" {
		return
	}
	var conn *WSConn
	ap, ok := p.getAccountPool(accountID)
	if ok && ap != nil {
		ap.mu.Lock()
		if c, exists := ap.conns[connID]; exists {
			conn = c
			delete(ap.conns, connID)
			if len(ap.pinnedConns) > 0 {
				delete(ap.pinnedConns, connID)
			}
			ap.signalChangedLocked()
		}
		ap.mu.Unlock()
	}
	if conn != nil {
		conn.close()
	}
}

func (p *WSConnPool) PinConn(accountID int64, connID string) bool {
	if p == nil || accountID <= 0 {
		return false
	}
	connID = stringsTrim(connID)
	if connID == "" {
		return false
	}
	ap, ok := p.getAccountPool(accountID)
	if !ok || ap == nil {
		return false
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if _, exists := ap.conns[connID]; !exists {
		return false
	}
	if ap.pinnedConns == nil {
		ap.pinnedConns = make(map[string]int)
	}
	ap.pinnedConns[connID]++
	return true
}

func (p *WSConnPool) UnpinConn(accountID int64, connID string) {
	if p == nil || accountID <= 0 {
		return
	}
	connID = stringsTrim(connID)
	if connID == "" {
		return
	}
	ap, ok := p.getAccountPool(accountID)
	if !ok || ap == nil {
		return
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if len(ap.pinnedConns) == 0 {
		return
	}
	count := ap.pinnedConns[connID]
	if count <= 1 {
		delete(ap.pinnedConns, connID)
		ap.signalChangedLocked()
		return
	}
	ap.pinnedConns[connID] = count - 1
	ap.signalChangedLocked()
}

func (p *WSConnPool) dialConn(ctx context.Context, req WSAcquireRequest) (*WSConn, error) {
	if p == nil || p.clientDialer == nil {
		return nil, errors.New("openai ws client dialer is nil")
	}
	headers := cloneHeader(req.Headers)
	var err error
	if req.HeadersFactory != nil {
		headers, err = req.HeadersFactory(ctx, headers)
		if err != nil {
			return nil, err
		}
	}
	conn, status, handshakeHeaders, err := p.clientDialer.Dial(ctx, req.WSURL, headers, req.ProxyURL, req.TLSProfile)
	if err != nil {
		var handshakeErr *WSHandshakeError
		var responseBody []byte
		if errors.As(err, &handshakeErr) && handshakeErr != nil {
			responseBody = append([]byte(nil), handshakeErr.Body...)
		}
		return nil, &WSDialError{

			StatusCode: status,

			ResponseHeaders: cloneHeader(handshakeHeaders),

			ResponseBody: responseBody,

			Err: err,
		}
	}
	if conn == nil {
		return nil, &WSDialError{

			StatusCode: status,

			ResponseHeaders: cloneHeader(handshakeHeaders),

			Err: errors.New("openai ws dialer returned nil connection"),
		}
	}
	id := p.nextConnID(req.Account.ID)
	pooledConn := NewWSConn(id, req.Account.ID, conn, handshakeHeaders, req.TLSProfile, req.TLSProfileKey)
	pooledConn.handshakeCompatibility = normalizeOpenAIWSHandshakeCompatibility(req.Account, req.Headers)
	pooledConn.routingAffinity = normalizeOpenAIWSRoutingAffinity(req.Headers)
	return pooledConn, nil
}

func (p *WSConnPool) nextConnID(accountID int64) string {
	seq := p.seq.Add(1)
	buf := make([]byte, 0, 32)
	buf = append(buf, "oa_ws_"...)
	buf = strconv.AppendInt(buf, accountID, 10)
	buf = append(buf, '_')
	buf = strconv.AppendUint(buf, seq, 10)
	return string(buf)
}

func (p *WSConnPool) shouldHealthCheckConn(conn *WSConn) bool {
	if conn == nil || !conn.supportsIdlePingWithoutReader() {
		return false
	}
	return conn.idleDuration(time.Now()) >= openAIWSConnHealthCheckIdle
}

func (p *WSConnPool) maxConnsHardCap() int { return p.nativeOptions().MaxConnsHardCap() }

func (p *WSConnPool) dynamicMaxConnsEnabled() bool {
	return p.nativeOptions().DynamicMaxConnsEnabled()
}

func (p *WSConnPool) maxConnsFactorByAccount(account *WSPoolAccount) float64 {
	return p.nativeOptions().MaxConnsFactorByAccount(account)
}

func (p *WSConnPool) effectiveMaxConnsByAccount(account *WSPoolAccount) int {
	return p.nativeOptions().EffectiveMaxConnsByAccount(account)
}

func (p *WSConnPool) minIdlePerAccount() int { return p.nativeOptions().MinIdle() }

func (p *WSConnPool) maxIdlePerAccount() int { return p.nativeOptions().MaxIdle() }

func (p *WSConnPool) maxConnAge() time.Duration { return p.nativeOptions().MaxConnAge() }

func (p *WSConnPool) queueLimitPerConn() int { return p.nativeOptions().QueueLimit() }

func (p *WSConnPool) targetUtilization() float64 { return p.nativeOptions().TargetUtilization() }

func (p *WSConnPool) prewarmCooldown() time.Duration {
	return p.nativeOptions().PrewarmCooldown()
}

func (p *WSConnPool) shouldSuppressPrewarmLocked(ap *openAIWSAccountPool, now time.Time) bool {
	if ap == nil {
		return true
	}
	if ap.prewarmFails <= 0 {
		return false
	}
	if ap.prewarmFailAt.IsZero() {
		ap.prewarmFails = 0
		return false
	}
	if now.Sub(ap.prewarmFailAt) > openAIWSPrewarmFailureWindow {
		ap.prewarmFails = 0
		ap.prewarmFailAt = time.Time{}
		return false
	}
	return ap.prewarmFails >= openAIWSPrewarmFailureSuppress
}

func (p *WSConnPool) dialTimeout() time.Duration { return p.nativeOptions().DialTimeout() }

func CloneWSAcquireRequest(req WSAcquireRequest) WSAcquireRequest {
	copied := req
	copied.Headers = cloneHeader(req.Headers)
	copied.WSURL = stringsTrim(req.WSURL)
	copied.ProxyURL = stringsTrim(req.ProxyURL)
	copied.PreferredConnID = stringsTrim(req.PreferredConnID)
	return copied
}

func CloneWSAcquireRequestPtr(req *WSAcquireRequest) *WSAcquireRequest {
	if req == nil {
		return nil
	}
	copied := CloneWSAcquireRequest(*req)
	return &copied
}

// sameOpenAIWSPrewarmTarget 判断预热拨号的硬兼容目标是否仍然有效。
// routing hint 仅用于软亲和，变化时无需丢弃已经建立的兼容连接。
func sameOpenAIWSPrewarmTarget(a, b WSAcquireRequest) bool {
	return stringsTrim(a.WSURL) == stringsTrim(b.WSURL) &&
		stringsTrim(a.ProxyURL) == stringsTrim(b.ProxyURL) &&
		normalizeOpenAIWSHandshakeCompatibility(a.Account, a.Headers) == normalizeOpenAIWSHandshakeCompatibility(b.Account, b.Headers) &&
		openAIWSTLSProfileKey(a.TLSProfile, a.TLSProfileKey) == openAIWSTLSProfileKey(b.TLSProfile, b.TLSProfileKey)
}

// normalizeOpenAIWSBetaFeatures 将握手 beta feature 去重排序，生成稳定的连接兼容键。
func normalizeOpenAIWSBetaFeatures(headers http.Header) string {
	features := make(map[string]struct{})
	for name, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(name), "x-codex-beta-features") {
			continue
		}
		for _, value := range values {
			for _, feature := range strings.Split(value, ",") {
				if feature = strings.TrimSpace(feature); feature != "" {
					features[feature] = struct{}{}
				}
			}
		}
	}
	if len(features) == 0 {
		return ""
	}
	normalized := make([]string, 0, len(features))
	for feature := range features {
		normalized = append(normalized, feature)
	}
	sort.Strings(normalized)
	return strings.Join(normalized, ",")
}

func normalizeOpenAIWSHandshakeCompatibility(account *WSPoolAccount, headers http.Header) openAIWSHandshakeCompatibilityKey {
	key := openAIWSHandshakeCompatibilityKey{
		betaFeatures: normalizeOpenAIWSBetaFeatures(headers),
	}
	mode := activeCodexFingerprintMode(account)
	if mode == codexFingerprintOff {
		return key
	}
	key.codexInstallationID = normalizeOpenAIWSStableIdentityHeader(headers, "x-codex-installation-id")
	if mode == codexFingerprintDevice {
		return key
	}
	key.sessionIDHyphen = normalizeOpenAIWSStableIdentityHeader(headers, "session-id")
	key.sessionIDUnderscore = normalizeOpenAIWSStableIdentityHeader(headers, "session_id")
	key.threadID = normalizeOpenAIWSStableIdentityHeader(headers, "thread-id")
	key.clientRequestID = normalizeOpenAIWSStableIdentityHeader(headers, "x-client-request-id")
	key.codexWindowID = normalizeOpenAIWSStableIdentityHeader(headers, "x-codex-window-id")
	return key
}

func normalizeOpenAIWSStableIdentityHeader(headers http.Header, name string) string {
	if headers == nil {
		return ""
	}
	return strings.TrimSpace(headers.Get(name))
}

func normalizeOpenAIWSRoutingAffinity(headers http.Header) string {
	canonicalName := http.CanonicalHeaderKey(openAICodexRoutingHintHeader)
	if values, ok := headers[canonicalName]; ok {
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}

	variantNames := make([]string, 0)
	for name := range headers {
		if name != canonicalName && strings.EqualFold(strings.TrimSpace(name), openAICodexRoutingHintHeader) {
			variantNames = append(variantNames, name)
		}
	}
	sort.Strings(variantNames)
	for _, name := range variantNames {
		for _, value := range headers[name] {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func cloneHeader(src http.Header) http.Header { return upstream.CloneHeader(src) }

func closeOpenAIWSConns(conns []*WSConn) {
	if len(conns) == 0 {
		return
	}
	for _, conn := range conns {
		if conn == nil {
			continue
		}
		conn.close()
	}
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}

// Start 仅在拥有者按需启用连接池时运行，重复启动或关闭后启动不再创建 worker。
func (p *WSConnPool) Start() {
	if p == nil {
		return
	}
	p.runtimeMu.Lock()
	defer p.runtimeMu.Unlock()
	if p.closed || p.started {
		return
	}
	p.started = true
	p.startBackgroundWorkers()
}
func (p *WSConnPool) nativeOptions() *WSPoolOptions {
	if p == nil {
		return nil
	}
	return p.cfg
}

type codexFingerprintMode = string

const (
	codexFingerprintOff          = "off"
	codexFingerprintDevice       = "device"
	openAICodexRoutingHintHeader = "x-codex-routing-hint"
)

func activeCodexFingerprintMode(account *WSPoolAccount) codexFingerprintMode {
	if account == nil || account.FingerprintMode == "" {
		return codexFingerprintOff
	}
	return account.FingerprintMode
}

// SnapshotAccountState 只投影连接和租约数量，不暴露连接或认证头。
type WSPoolAccountState struct{ Connections, LeasedConnections, PinnedConnections int }

func (p *WSConnPool) SnapshotAccountState(id int64) (WSPoolAccountState, bool) {
	account, ok := p.getAccountPool(id)
	if !ok {
		return WSPoolAccountState{}, false
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	state := WSPoolAccountState{Connections: len(account.conns), PinnedConnections: len(account.pinnedConns)}
	for _, connection := range account.conns {
		if connection != nil && connection.isLeased() {
			state.LeasedConnections++
		}
	}
	return state, true
}

// EvictConnection 复用原连接淘汰操作，重连与重试仍由调用者决定。
func (p *WSConnPool) EvictConnection(id int64, connectionID string) { p.evictConn(id, connectionID) }

const WSConnHealthCheckTimeout = openAIWSConnHealthCheckTO
