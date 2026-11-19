package ws

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// delayedCloseConn blocks its handshake to expose pool access during socket
// cleanup.
type delayedCloseConn struct {
	openAIWSFakeConn               // embedded fake transport fixture
	entered          chan struct{} // close handshake arrival channel
	release          chan struct{} // close handshake release channel
}

// wsAcquireTestResult carries the lease or failure from one background
// acquisition.
type wsAcquireTestResult struct {
	lease *WSConnLease // background acquisition lease value
	err   error        // background acquisition failure value
}

// openAIWSCountingDialer records synchronized dial count and the latest TLS
// profile.
type openAIWSCountingDialer struct {
	// mu protects dialCount and lastTLSProfile for concurrent pool dial paths
	// and test snapshots; the profile pointer remains shared with the caller fixture.
	mu             sync.Mutex              // fixture mutable state mutex
	dialCount      int                     // observed fake dial count
	lastTLSProfile *tlsfingerprint.Profile // latest supplied TLS profile
}

// openAIWSFakeConn records fake writes and closed-state checks under one mutex.
type openAIWSFakeConn struct {
	// mu protects closed and payload so concurrent fixture operations observe
	// one connection state; writes append markers while reads check the same closure flag.
	mu      sync.Mutex // fixture mutable state mutex
	closed  bool       // closed fake connection flag
	payload [][]byte   // recorded fake write markers
}

// openAIWSFakeDialer returns successful in-memory sockets without consulting
// request parameters.
type openAIWSFakeDialer struct{}

// openAIWSFirstDialBlockingCaptureDialer captures headers/sockets while holding
// its first dial for controlled publication tests.
type openAIWSFirstDialBlockingCaptureDialer struct {
	mu        sync.Mutex // captured dial state mutex
	dialCount int        // observed dial attempt count
	// headers stores cloned request header snapshots under mu; callers inspect that
	// slice under the same lock while dials may append.
	headers []http.Header
	// connections stores shared fake pointers after successful creation; inspect
	// the slice under mu and each socket state under its own mutex.
	connections []*openAIWSFakeConn
	// firstStarted closes once when the first counted dial begins waiting, letting
	// the test change provider state before releasing that captured dial.
	firstStarted chan struct{}
	// releaseFirst is the fixture-owned first-dial gate; cleanup closes it once or
	// cancels the dial context before waiting for pool resources.
	releaseFirst chan struct{}
}

// openAIWSAlwaysFailDialer records attempts and returns a fixed transport
// failure.
type openAIWSAlwaysFailDialer struct {
	mu        sync.Mutex // captured dial state mutex
	dialCount int        // observed dial attempt count
}

// openAIWSPingBlockingConn tracks simultaneous probes until cancellation or
// test release.
type openAIWSPingBlockingConn struct {
	// current counts active probes and maxConcurrent remembers their peak across
	// all fixture sockets. Ping updates both atomics and returns current in a
	// defer after either release or cancellation completes this probe.
	current       *atomic.Int32 // shared active probe counter
	maxConcurrent *atomic.Int32 // shared peak overlapping probes
	// release ends held ping probes when closed; this stub's Close does not signal
	// it, so fixture owners release or cancel pending probes.
	release <-chan struct{}
}

// openAIWSIdlePingUnsupportedConn exposes the shared fake socket with
// reader-dependent ping capability.
type openAIWSIdlePingUnsupportedConn struct {
	// openAIWSFakeConn supplies ordinary read/write/close behavior while the outer
	// fixture overrides idle-ping capability for retirement checks.
	openAIWSFakeConn
}

// openAIWSBlockingConn delays reads under context while permitting immediate
// writes and ping.
type openAIWSBlockingConn struct {
	readDelay time.Duration // configured fake read delay
}

// openAIWSWriteBlockingConn waits for write cancellation while other operations
// return immediately.
type openAIWSWriteBlockingConn struct{}

// openAIWSPingFailConn returns fixed messages and fails every health probe.
type openAIWSPingFailConn struct{}

// openAIWSContextProbeConn retains the context supplied by a serialized write.
type openAIWSContextProbeConn struct {
	// lastWriteCtx retains the supplied write context without locking; the test
	// reads it after the connection wrapper has completed its serialized write.
	lastWriteCtx context.Context
}

// openAIWSNilConnDialer reports HTTP success while returning no transport
// object.
type openAIWSNilConnDialer struct{}

// Close signals entry and blocks until the fixture release channel closes. Call
// this fixture close once; tests own release so a pool update can finish after
// concurrent lookup assertions.
func (c *delayedCloseConn) Close() error {
	close(c.entered)
	<-c.release
	return nil
}

// ProviderPoolLoad snapshots lease, waiter, and connection counts under the
// provider mutex. Nil pools, nonpositive IDs, and absent provider pools return
// zero counts, allowing cleanup tests to inspect missing state safely.
func (p *WSConnPool) ProviderPoolLoad(providerID int64) (inflight int, waiters int, conns int) {
	if p == nil || providerID <= 0 {
		return 0, 0, 0
	}
	ap, ok := p.getProviderPool(providerID)
	if !ok || ap == nil {
		return 0, 0, 0
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	inflight, waiters = providerPoolLoadLocked(ap)
	return inflight, waiters, len(ap.conns)
}

// newWSReuseTestPool creates a fake-dialer pool and API-key acquisition target
// for reuse assertions. Target utilization one suppresses utilization-based
// spare connections; callers close the returned pool and release acquired
// leases.
func newWSReuseTestPool(capacity, idle int) (*WSConnPool, WSAcquireRequest) {
	pool := NewWSConnPool(&WSPoolOptions{
		MaxConnsPerProvider:   capacity,
		MaxIdlePerProvider:    idle,
		PoolTargetUtilization: 1,
		QueueLimitPerConn:     2,
	})
	pool.SetClientDialerForTest(&openAIWSCountingDialer{})
	return pool, WSAcquireRequest{
		Provider: &WSPoolProvider{ID: 1, Type: "apikey"},
		WSURL:    "wss://example.com/v1/responses",
	}
}

// acquireWSInBackground starts one asynchronous Acquire and returns its
// buffered result channel. The caller owns context cancellation, reads the
// result, and releases a successful lease so test shutdown can finish.
func acquireWSInBackground(pool *WSConnPool, ctx context.Context, req WSAcquireRequest) <-chan wsAcquireTestResult {
	result := make(chan wsAcquireTestResult, 1)
	// Buffer the acquisition outcome so completion cannot block waiting for
	// the test reader, which may first inspect queue counts or cancellation effects.
	go func() {
		lease, err := pool.Acquire(ctx, req)
		result <- wsAcquireTestResult{lease: lease, err: err}
	}()
	return result
}

// Dial increments the fixture dial count and stores profile under its mutex. It
// ignores transport/context arguments and returns an open fake connection with
// zero handshake status and no headers for pool tests.
func (d *openAIWSCountingDialer) Dial(
	ctx context.Context,
	wsURL string,
	headers http.Header,
	proxyURL string,
	profile *tlsfingerprint.Profile,
) (openai.WSClientConn, int, http.Header, error) {
	_ = ctx
	_ = wsURL
	_ = headers
	_ = proxyURL
	d.mu.Lock()
	d.dialCount++
	d.lastTLSProfile = profile
	d.mu.Unlock()
	return &openAIWSFakeConn{}, 0, nil, nil
}

// DialCount returns the recorded dial count under the fixture mutex.
func (d *openAIWSCountingDialer) DialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dialCount
}

// LastTLSProfile returns the recorded profile pointer under the fixture mutex.
func (d *openAIWSCountingDialer) LastTLSProfile() *tlsfingerprint.Profile {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastTLSProfile
}

// WriteJSON records one fake payload marker while the connection remains open.
// It ignores ctx and value content; closed fixtures return an error and callers
// use the marker count for pool observations.
func (c *openAIWSFakeConn) WriteJSON(ctx context.Context, value any) error {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("closed")
	}
	c.payload = append(c.payload, []byte("ok"))
	_ = value
	return nil
}

// ReadMessage returns a fixed completed-response fixture while the connection
// is open. It ignores ctx and returns an error after Close, keeping transport
// timing under the surrounding test fixture.
func (c *openAIWSFakeConn) ReadMessage(ctx context.Context) ([]byte, error) {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("closed")
	}
	return []byte(`{"type":"response.completed","response":{"id":"resp_fake"}}`), nil
}

// Ping reports successful fake liveness while ignoring the supplied context. It
// performs no transport read or write, allowing tests to use the same fixture
// for pool idle-ping checks.
func (c *openAIWSFakeConn) Ping(ctx context.Context) error {
	_ = ctx
	return nil
}

// Close marks the fake connection closed under its state mutex. Repeated calls
// retain that state, and later fake reads or writes return the closed error
// without performing transport work.
func (c *openAIWSFakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// Dial returns a new shared fake connection with zero status and no
// headers/error. It ignores context, URL, proxy, profile, and request headers;
// use it for pool mechanics, not transport validation or cancellation
// assertions.
func (d *openAIWSFakeDialer) Dial(
	ctx context.Context,
	wsURL string,
	headers http.Header,
	proxyURL string,
	profile *tlsfingerprint.Profile,
) (openai.WSClientConn, int, http.Header, error) {
	_ = ctx
	_ = wsURL
	_ = headers
	_ = proxyURL
	_ = profile
	return &openAIWSFakeConn{}, 0, nil, nil
}

// newOpenAIWSFirstDialBlockingCaptureDialer creates fresh first-start and
// release gates for a capturing dialer. Callers eventually release the first
// gate or cancel its context, and inspect captured state under the dialer
// mutex.
func newOpenAIWSFirstDialBlockingCaptureDialer() *openAIWSFirstDialBlockingCaptureDialer {
	return &openAIWSFirstDialBlockingCaptureDialer{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
}

// SupportsIdlePingWithoutReader reports false for the reader-dependent
// idle-ping fixture.
func (c *openAIWSIdlePingUnsupportedConn) SupportsIdlePingWithoutReader() bool {
	return false
}

// WriteJSON returns successful fixture output without retaining payload or
// modeling cancellation.
func (c *openAIWSPingBlockingConn) WriteJSON(context.Context, any) error {
	return nil
}

// ReadMessage returns the fixed completed-response bytes used by the ping
// concurrency fixture.
func (c *openAIWSPingBlockingConn) ReadMessage(context.Context) ([]byte, error) {
	return []byte(`{"type":"response.completed","response":{"id":"resp_blocking_ping"}}`), nil
}

// Ping increments shared overlap counters, updates the observed maximum, and
// waits for context or release when counters exist. Missing counters return
// success immediately. Callers share the same atomics across all candidates and
// eventually release the gate.
//
// The maximum uses compare-and-swap while active count is returned in a defer,
// so cancellation and ordinary release both stop counting this probe. Close is
// a no-op and cannot release the wait; pool tests therefore own the gate or
// context lifetime. This exposes actual overlapping probe calls rather than
// total scheduled candidates.
func (c *openAIWSPingBlockingConn) Ping(ctx context.Context) error {
	if c.current == nil || c.maxConcurrent == nil {
		return nil
	}

	// Count active entry before waiting and return it on either outcome; retain the
	// peak so overlap remains observable after the shared release.
	now := c.current.Add(1)
	for {
		prev := c.maxConcurrent.Load()
		if now <= prev || c.maxConcurrent.CompareAndSwap(prev, now) {
			break
		}
	}
	defer c.current.Add(-1)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.release:
		return nil
	}
}

// Close returns nil for the ping fixture and does not release its gate.
// Existing Ping calls still depend on caller context or release-channel
// completion; use the fixture shutdown gate before waiting for them.
func (c *openAIWSPingBlockingConn) Close() error {
	return nil
}

// Dial counts attempts and clones supplied headers under its mutex, holding
// only the first call on the fixture gate. The first-start channel closes
// before waiting; cancellation returns its error without recording a new
// connection.
//
// Other dial calls can complete while that first request is held. Successfully
// created fake pointers are appended under the mutex after the gate releases.
// Captured headers remain stable copies, while connection objects are shared
// references whose own state needs their mutex. Callers release/cancel the held
// first request before fixture cleanup waits for pool resources.
func (d *openAIWSFirstDialBlockingCaptureDialer) Dial(
	ctx context.Context,
	wsURL string,
	headers http.Header,
	proxyURL string,
	profile *tlsfingerprint.Profile,
) (openai.WSClientConn, int, http.Header, error) {
	_ = wsURL
	_ = proxyURL
	_ = profile
	// Count and clone headers before first-start publication, letting concurrent
	// dials proceed while the fixture holds the first request on its gate.
	d.mu.Lock()
	d.dialCount++
	dialNumber := d.dialCount
	d.headers = append(d.headers, cloneHeader(headers))
	d.mu.Unlock()
	if dialNumber == 1 {
		close(d.firstStarted)
		select {
		case <-ctx.Done():
			return nil, 0, nil, ctx.Err()
		case <-d.releaseFirst:
		}
	}
	conn := &openAIWSFakeConn{}
	d.mu.Lock()
	d.connections = append(d.connections, conn)
	d.mu.Unlock()
	return conn, 0, nil, nil
}

// DialCount reads the captured attempt count under the fixture mutex. It can
// count a canceled first call before any socket is appended, so consumers
// distinguish attempts from recorded connection objects.
func (d *openAIWSFirstDialBlockingCaptureDialer) DialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dialCount
}

// Dial increments its synchronized attempt count and returns nil transport,
// HTTP 503, and the fixed dial failure. Request parameters/context are ignored;
// this fixture models suppression evidence rather than transport cancellation
// behavior.
func (d *openAIWSAlwaysFailDialer) Dial(
	ctx context.Context,
	wsURL string,
	headers http.Header,
	proxyURL string,
	profile *tlsfingerprint.Profile,
) (openai.WSClientConn, int, http.Header, error) {
	_ = ctx
	_ = wsURL
	_ = headers
	_ = proxyURL
	_ = profile
	// Record the failed attempt under the same lock used by DialCount before
	// returning its fixed error for later suppression assertions.
	d.mu.Lock()
	d.dialCount++
	d.mu.Unlock()
	return nil, 503, nil, errors.New("dial failed")
}

// DialCount returns the synchronized number of always-failing dial attempts.
// The counter advances before each fixed error is returned, allowing admission
// tests to inspect completed or currently observed attempt evidence.
func (d *openAIWSAlwaysFailDialer) DialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dialCount
}

// WriteJSON succeeds immediately for the delayed-read fixture while ignoring
// context and value.
func (c *openAIWSBlockingConn) WriteJSON(ctx context.Context, value any) error {
	_ = ctx
	_ = value
	return nil
}

// ReadMessage waits for its configured delay or caller cancellation, then
// returns the fixed completed response. Nonpositive delay selects ten
// milliseconds. Close does not release this wait, so tests retain context/timer
// ownership until reading finishes.
func (c *openAIWSBlockingConn) ReadMessage(ctx context.Context) ([]byte, error) {
	delay := c.readDelay
	if delay <= 0 {
		delay = 10 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return []byte(`{"type":"response.completed","response":{"id":"resp_blocking"}}`), nil
	}
}

// Ping succeeds immediately for the delayed-read fixture, independently of its
// pending read.
func (c *openAIWSBlockingConn) Ping(ctx context.Context) error {
	_ = ctx
	return nil
}

// Close returns nil for the delayed-read fixture and leaves pending reads to
// their timer/context. It provides no transport-unblocking effect, so callers
// cannot infer real socket close behavior from this stub.
func (c *openAIWSBlockingConn) Close() error {
	return nil
}

// WriteJSON waits exclusively for the supplied context to finish and returns
// its error. Pass a nonnil cancelable context; Close is a no-op, so fixture
// owners cancel or set a deadline before waiting for write completion.
func (c *openAIWSWriteBlockingConn) WriteJSON(ctx context.Context, _ any) error {
	<-ctx.Done()
	return ctx.Err()
}

// ReadMessage returns fixed completed-response bytes independently of the
// blocked-write fixture state.
func (c *openAIWSWriteBlockingConn) ReadMessage(context.Context) ([]byte, error) {
	return []byte(`{"type":"response.completed","response":{"id":"resp_write_block"}}`), nil
}

// Ping reports immediate success for the write-cancellation fixture without
// consulting context.
func (c *openAIWSWriteBlockingConn) Ping(context.Context) error {
	return nil
}

// Close returns nil for the write-cancellation fixture and does not unblock
// WriteJSON. The caller owns the write context that eventually releases its
// wait; this stub models cancellation rather than socket disposal.
func (c *openAIWSWriteBlockingConn) Close() error {
	return nil
}

// WriteJSON reports successful fixture output while leaving the failing-ping
// state unchanged.
func (c *openAIWSPingFailConn) WriteJSON(context.Context, any) error {
	return nil
}

// ReadMessage returns the fixed response used independently of failed
// health-probe assertions.
func (c *openAIWSPingFailConn) ReadMessage(context.Context) ([]byte, error) {
	return []byte(`{"type":"response.completed","response":{"id":"resp_ping_fail"}}`), nil
}

// Ping returns the fixed probe error for every failing-health fixture
// invocation.
func (c *openAIWSPingFailConn) Ping(context.Context) error {
	return errors.New("ping failed")
}

// Close returns nil after failing health probes without modeling a transport
// resource.
func (c *openAIWSPingFailConn) Close() error {
	return nil
}

// WriteJSON stores its supplied context and reports successful fixture output.
// Callers serialize probe access through the connection wrapper before
// inspecting lastWriteCtx, because the fixture itself has no mutex.
func (c *openAIWSContextProbeConn) WriteJSON(ctx context.Context, _ any) error {
	c.lastWriteCtx = ctx
	return nil
}

// ReadMessage returns fixed completed-response bytes for the write-context
// capture fixture.
func (c *openAIWSContextProbeConn) ReadMessage(context.Context) ([]byte, error) {
	return []byte(`{"type":"response.completed","response":{"id":"resp_ctx_probe"}}`), nil
}

// Ping returns successful health status for the context-capture fixture without
// inspecting context.
func (c *openAIWSContextProbeConn) Ping(context.Context) error {
	return nil
}

// Close returns nil for the context probe without clearing captured write
// context.
func (c *openAIWSContextProbeConn) Close() error {
	return nil
}

// Dial returns nil transport with HTTP 200 and nil error, ignoring request
// inputs. It models the inconsistent dialer outcome that acquisition must
// reject rather than a successful usable WebSocket.
func (d *openAIWSNilConnDialer) Dial(
	ctx context.Context,
	wsURL string,
	headers http.Header,
	proxyURL string,
	profile *tlsfingerprint.Profile,
) (openai.WSClientConn, int, http.Header, error) {
	_ = ctx
	_ = wsURL
	_ = headers
	_ = proxyURL
	_ = profile
	return nil, 200, nil, nil
}

// newStartedWSConnPoolForTest constructs a pool and starts its periodic workers
// before returning it. It installs no automatic test cleanup; fixture owners
// call Close when they need worker/resource completion before their test ends.
func newStartedWSConnPoolForTest(options *WSPoolOptions) *WSConnPool {
	p := NewWSConnPool(options)
	p.Start()
	return p
}
