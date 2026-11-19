package ws

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// pool.go arbitrates WebSocket lease acquisition, queueing, idle health sweeps,
// and provider pool lifecycle for OpenAI Responses WebSocket transport.
// Capacity checks count both established sockets and reserved in-flight dials
// so concurrent callers cannot oversubscribe a provider limit while handshakes
// are still running outside the provider lock.
//
// Acquisitions register against pool shutdown before waiting or dialing, wake
// queued selectors through generation-replaced channels, and close evicted or
// expired sockets after releasing provider locks. Companion connection types
// live in conn.go and adaptive background prewarming lives in prewarm.go.

// These intervals and labels govern lease selection, background health/cleanup,
// and prewarm suppression; fingerprint labels select which handshake identities
// enter reuse keys.
const (
	openAIWSConnHealthCheckIdle = 90 * time.Second
	// Reader-dependent transports cannot complete an idle ping on their own;
	// retirement uses this idle threshold to reclaim them before the next attempted reuse.
	openAIWSConnIdleRecycleAfter   = 90 * time.Second
	openAIWSConnHealthCheckTO      = 2 * time.Second
	openAIWSConnPrewarmExtraDelay  = 2 * time.Second
	openAIWSAcquireCleanupInterval = 3 * time.Second
	openAIWSBackgroundPingInterval = 30 * time.Second
	openAIWSBackgroundSweepTicker  = 30 * time.Second

	// These suppression values limit new batch admission from recent accumulated
	// dial failures; the latest failure time controls when that evidence resets.
	openAIWSPrewarmFailureWindow   = 30 * time.Second
	openAIWSPrewarmFailureSuppress = 2

	// Fingerprint modes select beta-only compatibility for off and installation
	// identity for device, while other supplied modes include full stable identity.
	codexFingerprintOff    = "off"
	codexFingerprintDevice = "device"
	// openAICodexRoutingHintHeader names advisory routing metadata; changing it
	// affects preferred selection while compatible prewarm/socket identity remains usable.
	openAICodexRoutingHintHeader = "x-codex-routing-hint"

	// WSConnHealthCheckTimeout exposes the fixed probe budget to transport/session
	// callers so their health checks can use the same timeout as pool acquisition.
	WSConnHealthCheckTimeout = openAIWSConnHealthCheckTO
)

// errOpenAIWSConnClosed shares the upstream closed-resource sentinel so lease
// entry checks and transport adapters support the same errors.Is
// classification.
var errOpenAIWSConnClosed = openai.ErrWSConnClosed

// openAIWSProviderPool stores a provider's connections and admission/prewarm
// state under its mutex.
// mu protects conns, pinnedConns, changedCh, waiters, creating, generation,
// lastCleanupAt, lastAcquire, prewarmActive, prewarmUntil, prewarmFails, and
// prewarmFailAt. Callers mutate admission/target state and publish changedCh
// under that lock, then close retired transports after unlocking.
type openAIWSProviderPool struct {
	mu          sync.Mutex         // provider admission state mutex
	conns       map[string]*WSConn // recorded provider socket entries
	pinnedConns map[string]int     // socket pin reference counts
	// changedCh is closed and replaced under mu after provider change; selectors
	// retain an earlier channel while waiting and recheck state once it closes.
	changedCh chan struct{}
	waiters   map[wsConnCompatibility]int // general compatibility waiter counts
	// creating counts reserved unfinished dials alongside conns during capacity
	// checks; each batch/acquisition returns only its owned reservation when work finishes.
	creating int
	// generation advances on provider clear and qualifies late dial/retained-target
	// publication; active lease delivery still follows its separate transport-error
	// lifetime handling.
	generation    uint64
	lastCleanupAt time.Time // latest locked cleanup time
	// lastAcquire retains a successful generation-qualified request for future
	// demand prewarming; its header map is cloned while provider/profile/callback
	// references stay shared.
	lastAcquire *WSAcquireRequest
	// prewarmActive excludes another batch for this provider until the current
	// batch returns reservations and clears its activity flag in deferred settlement.
	prewarmActive bool
	// prewarmUntil postpones new batch admission while current time precedes it;
	// an expired or zero deadline leaves failure/demand eligibility to their separate checks.
	prewarmUntil time.Time
	// prewarmFails counts acquisition/prewarm dial failures and prewarmFailAt
	// records the latest one. Admission ages that latest time, while successful
	// new dials, stale-time selection, and provider clearing reset the evidence.
	prewarmFails  int       // accumulated failed dial count
	prewarmFailAt time.Time // latest failed dial time
}

// WSPoolMetricsSnapshot contains independently loaded lifetime counters for
// acquisition and pool scaling. AcquireQueueWaitTotal counts queue entrants;
// AcquireQueueWaitMsTotal receives elapsed reservation time at exit. ConnPickTotal
// and ConnPickMsTotal count recorded picks and their summed milliseconds, so
// paired count/time fields can advance at separate operations.
type WSPoolMetricsSnapshot struct {
	AcquireTotal            int64 // registered acquisition call count
	AcquireReuseTotal       int64 // successful reused lease count
	AcquireCreateTotal      int64 // successful fresh lease count
	AcquireQueueWaitTotal   int64 // acquisitions entering queue count
	AcquireQueueWaitMsTotal int64 // summed queue wait milliseconds
	ConnPickTotal           int64 // recorded connection selection count
	ConnPickMsTotal         int64 // summed selection duration milliseconds
	ScaleUpTotal            int64 // reserved prewarm dial count
	ScaleDownTotal          int64 // selected cleanup retirement count
}

// openAIWSPoolMetrics accumulates atomic acquisition, selection, queue-time,
// and scaling counters across providers.
// acquireTotal, acquireReuseTotal, acquireCreateTotal, acquireQueueWaitTotal,
// acquireQueueWaitMs, connPickTotal, connPickMs, scaleUpTotal, and scaleDownTotal
// advance independently through atomic mutation and are sampled separately.
// acquireQueueWaitMs and connPickMs store whole-millisecond sums; their paired
// event counts can be sampled before or after those sums during concurrent
// snapshot creation.
type openAIWSPoolMetrics struct {
	acquireTotal          atomic.Int64 // registered acquisition call counter
	acquireReuseTotal     atomic.Int64 // successful reused lease counter
	acquireCreateTotal    atomic.Int64 // successful fresh lease counter
	acquireQueueWaitTotal atomic.Int64 // queued acquisition entry counter
	acquireQueueWaitMs    atomic.Int64 // summed queue wait milliseconds
	connPickTotal         atomic.Int64 // recorded connection selection counter
	connPickMs            atomic.Int64 // summed selection duration milliseconds
	scaleUpTotal          atomic.Int64 // reserved prewarm dial counter
	scaleDownTotal        atomic.Int64 // selected cleanup retirement counter
}

// WSConnPool owns provider pools, registered acquisitions/prewarm work, and
// optional background worker lifecycle.
// runtimeMu protects started and closed and orders acquireWG/prewarmWG/workerWg
// registration against shutdown waits. cfg publishes scalar option snapshots
// independently; provider state has its own mutex. clientDialer is installed
// before concurrent operation and is not synchronized by the test setter.
type WSConnPool struct {
	started   bool           // periodic worker startup latch
	runtimeMu sync.Mutex     // shutdown and registration mutex
	closed    bool           // pool shutdown publication flag
	acquireWG sync.WaitGroup // registered acquisition completion group
	prewarmWG sync.WaitGroup // registered prewarm completion group
	// prewarmCtx supplies the pool-owned parent for demand prewarming, and
	// prewarmCancel closes that lifecycle on shutdown. Ordinary acquisition dials
	// keep their caller contexts, so canceling this pair does not cancel them.
	prewarmCtx    context.Context    // pool prewarm cancellation context
	prewarmCancel context.CancelFunc // pool prewarm cancellation handle
	// cfg publishes copied scalar option values atomically; internal readers treat
	// returned pointers as immutable and select defaults when no snapshot exists.
	cfg atomic.Pointer[WSPoolOptions]
	// clientDialer supplies transport creation and optional metrics; set the test
	// replacement before workers/acquisitions because its pointer publication is
	// unsynchronized.
	clientDialer openai.WSClientDialer

	// providers maps positive int64 provider IDs to provider pools; LoadOrStore
	// publication owns that expected value shape and each value protects its
	// internal state.
	providers sync.Map
	seq       atomic.Uint64 // pool-local socket identity sequence

	metrics openAIWSPoolMetrics // lifetime atomic pool observations

	workerStopCh chan struct{} // periodic worker shutdown channel
	// cleanupWakeCh coalesces cleanup requests in one slot; the cleanup worker
	// reads current provider state, so repeated reservation exits can share one
	// wake.
	cleanupWakeCh chan struct{}
	workerWg      sync.WaitGroup // registered periodic worker group
	closeOnce     sync.Once      // pool shutdown completion latch
}

// openAIWSIdlePingCandidate associates an idle connection reference with its
// provider for ping eviction.
type openAIWSIdlePingCandidate struct {
	providerID int64   // selected ping provider identity
	conn       *WSConn // sampled idle connection reference
}

// WSPoolProviderState reports map-based provider counts with sampled lease
// state. Connections is the connection-entry count. LeasedConnections is the
// sampled occupied-token count. PinnedConnections is the pin-entry count.
// Categories overlap, and lease tokens can change independently while the
// provider map lock is held.
type WSPoolProviderState struct{ Connections, LeasedConnections, PinnedConnections int }

// activeConn rejects nil, missing-connection, and already released leases,
// returning the retained connection otherwise. Its atomic released check is an
// operation-entry check; callers finish existing I/O before Release because
// this helper does not reserve an additional lifetime token.
func (l *WSConnLease) activeConn() (*WSConn, error) {
	if l == nil || l.Conn == nil {
		return nil, errOpenAIWSConnClosed
	}
	if l.released.Load() {
		return nil, errOpenAIWSConnClosed
	}
	return l.Conn, nil
}

// ConnID returns the retained connection ID, or empty text for a nil lease or
// missing connection. It remains an inspection method after release; successful
// inspection does not make a released lease usable for I/O.
func (l *WSConnLease) ConnID() string {
	if l == nil || l.Conn == nil {
		return ""
	}
	return l.Conn.id
}

// QueueWaitDuration returns the accumulated queue-reservation duration recorded
// before acquisition returns. Nil leases report zero. It excludes dialing and
// other selection work, which have their own observations rather than being
// included in this queue metric.
func (l *WSConnLease) QueueWaitDuration() time.Duration {
	if l == nil {
		return 0
	}
	return l.queueWait
}

// ConnPickDuration returns the selected loop's connection-pick duration, or
// zero for a nil lease. Pool metrics also count unsuccessful selection loops,
// so this per-lease observation is not the sum of every pick performed during
// acquisition.
func (l *WSConnLease) ConnPickDuration() time.Duration {
	if l == nil {
		return 0
	}
	return l.connPick
}

// Reused reports whether acquisition delivered an existing pool connection. Nil
// leases return false. The flag describes that acquisition's selection and
// remains available after release; it does not establish current socket health.
func (l *WSConnLease) Reused() bool {
	if l == nil {
		return false
	}
	return l.reused
}

// HandshakeHeader returns a trimmed retained handshake value, or empty text for
// a nil/missing connection. Inspection remains available after release. Header
// name normalization follows the connection helper and http.Header.Get.
func (l *WSConnLease) HandshakeHeader(name string) string {
	if l == nil || l.Conn == nil {
		return ""
	}
	return l.Conn.handshakeHeader(name)
}

// HandshakeHeaders returns a copied retained handshake map and value slices for
// caller inspection. Nil leases, missing connections, and absent header storage
// return nil. Non-nil copies can be edited independently of the stored headers.
func (l *WSConnLease) HandshakeHeaders() http.Header {
	if l == nil || l.Conn == nil {
		return nil
	}
	return cloneHeader(l.Conn.handshakeHeaders)
}

// IsPrewarmed reports the retained connection's atomic protocol-prewarm marker.
// Nil or missing connections return false. This inspection does not check lease
// release or transport health, and pool prewarm dialing alone does not set the
// marker.
func (l *WSConnLease) IsPrewarmed() bool {
	if l == nil || l.Conn == nil {
		return false
	}
	return l.Conn.isPrewarmed()
}

// MarkPrewarmed sets the retained connection's protocol-prewarm marker when a
// connection exists. Nil or missing connections are ignored. It does not check
// release; callers invoke it under their session's connection-lifetime policy.
func (l *WSConnLease) MarkPrewarmed() {
	if l == nil || l.Conn == nil {
		return
	}
	l.Conn.markPrewarmed()
}

// WriteJSON delegates JSON output for an active lease using background context
// and an optional positive timeout. Nil/missing/released leases return the
// closed-lease error. Finish writes before Release; waiting for the direction
// mutex is not interruptible by context.
func (l *WSConnLease) WriteJSON(value any, timeout time.Duration) error {
	conn, err := l.activeConn()
	if err != nil {
		return err
	}
	return conn.writeJSONWithTimeout(context.Background(), value, timeout)
}

// WriteJSONWithContextTimeout delegates active-lease output with parent context
// and a positive timeout, treating nil parent as background. Nonpositive
// timeout keeps the parent budget. Context cancellation reaches transport I/O
// after ordinary write-mutex acquisition, so mutex waiting has no separate
// cancellation path.
func (l *WSConnLease) WriteJSONWithContextTimeout(ctx context.Context, value any, timeout time.Duration) error {
	conn, err := l.activeConn()
	if err != nil {
		return err
	}
	return conn.writeJSONWithTimeout(ctx, value, timeout)
}

// WriteJSONContext serializes active-lease JSON output using the supplied
// context, with nil selecting background. Nil/missing/released leases fail at
// entry. Transport closure determines errors after that check; callers complete
// I/O before releasing the lease.
func (l *WSConnLease) WriteJSONContext(ctx context.Context, value any) error {
	conn, err := l.activeConn()
	if err != nil {
		return err
	}
	return conn.writeJSON(value, ctx)
}

// ReadMessage reads an active lease using background context and an optional
// positive timeout. Nil/missing/released leases return the closed-lease error.
// Read-mutex waiting is ordinary blocking; the transport sees the derived
// context after that wait.
func (l *WSConnLease) ReadMessage(timeout time.Duration) ([]byte, error) {
	conn, err := l.activeConn()
	if err != nil {
		return nil, err
	}
	return conn.readMessageWithTimeout(timeout)
}

// ReadMessageContext serializes an active lease's transport read, using
// background for nil context. Nil/missing/released leases fail at entry.
// Transport errors remain supplied, and callers mark broken connections under
// their recovery policy before returning the lease.
func (l *WSConnLease) ReadMessageContext(ctx context.Context) ([]byte, error) {
	conn, err := l.activeConn()
	if err != nil {
		return nil, err
	}
	return conn.readMessage(ctx)
}

// ReadMessageWithContextTimeout applies parent context and a positive per-read
// timeout to active-lease transport input. Nil parent selects background;
// nonpositive timeout retains the parent budget. The read mutex serializes
// reads, and context cannot interrupt waiting for that mutex.
func (l *WSConnLease) ReadMessageWithContextTimeout(ctx context.Context, timeout time.Duration) ([]byte, error) {
	conn, err := l.activeConn()
	if err != nil {
		return nil, err
	}
	return conn.readMessageWithContextTimeout(ctx, timeout)
}

// PingWithTimeout delegates an active-lease ping under the connection's write
// mutex and timeout policy. Nil/missing/released leases fail at entry. Callers
// account for transport reader requirements before invoking ping on an
// otherwise idle connection.
func (l *WSConnLease) PingWithTimeout(timeout time.Duration) error {
	conn, err := l.activeConn()
	if err != nil {
		return err
	}
	return conn.pingWithTimeout(timeout)
}

// SupportsIdlePingWithoutReader reads the active connection's optional
// idle-ping capability. Nil/missing/released leases return false. Transports
// lacking the capability interface are assumed usable for idle ping, so
// implement that interface when a reader is required.
func (l *WSConnLease) SupportsIdlePingWithoutReader() bool {
	conn, err := l.activeConn()
	if err != nil {
		return false
	}
	return conn.supportsIdlePingWithoutReader()
}

// MarkBroken removes and closes the retained connection through its pool when
// the lease is still active. Nil/missing/released associations are ignored.
// Closing can interrupt ongoing transport work, so callers select this outcome
// before ordinary Release when session recovery requires disposal.
func (l *WSConnLease) MarkBroken() {
	if l == nil || l.pool == nil || l.Conn == nil || l.released.Load() {
		return
	}
	l.pool.evictConn(l.ProviderID, l.Conn.id)
}

// Release returns a lease token once and reconciles provider capacity before
// notifying waiters. Nil/missing leases are ignored. Callers finish their I/O
// first and keep lease association fields stable; the release latch prevents
// duplicate returns but does not join concurrent I/O.
//
// The token is returned before pool policy is examined. Closed pools evict it,
// while open pools reclaim excess idle capacity and wake selectors.
// Reconciliation closes sockets outside provider locking but can still block
// this Release call on a handshake. Cancellation recovery before delivery uses
// a separate background-cleanup path.
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
			l.pool.evictConn(l.ProviderID, l.Conn.id)
			return
		}
		l.pool.reconcileProvider(l.ProviderID)
		l.pool.notifyProviderPoolChanged(l.ProviderID)
	}
}

// tryAcquire nonblockingly takes the connection's one available token unless
// closure is observed. Nil connections return false. Closure is checked before
// and after token receipt, and a token received during closure is returned.
//
// A true result gives the caller exclusive pool lease ownership until release.
// Concurrent Close can still occur after that final check, so transport work
// follows socket error handling and caller shutdown policy. The channel token
// coordinates lease selection; it is separate from the read/write mutexes used
// to serialize direction operations.
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
			// Closure won after token receipt; return availability before declining
			// this lease, leaving the closed-channel check to reject subsequent acquisition attempts.
			c.release()
			return false
		default:
		}
		return true
	default:
		return false
	}
}

// release nonblockingly returns a token and updates last-used wall time. Nil
// connections are ignored. A full token channel suppresses duplicate token
// insertion; the enclosing WSConnLease owns its stricter release-once latch and
// provider notifications.
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

// close publishes connection closure once, closes a supplied transport, then
// makes the token channel available for blocked selectors. Nil connections are
// ignored. The closed channel is published before a potentially slow transport
// Close call.
//
// Transport-close errors are discarded, and the close-once guard covers both
// resource cleanup and token publication. Acquire checks the separate closed
// channel so returning a token does not reopen the socket. The caller removes
// provider/pin bookkeeping and releases provider locking before selecting this
// close operation.
func (c *WSConn) close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.closedCh)
		// Close errors do not reopen published closure; continue token wakeup
		// after transport cleanup so selectors still observe the closed socket as unavailable.
		if c.ws != nil {
			_ = c.ws.Close()
		}
		select {
		case c.leaseCh <- struct{}{}:
		default:
		}
	})
}

// writeJSONWithTimeout rejects nil/already marked closed connections, treats
// nil parent as background, and derives a context for positive timeout.
// Nonpositive timeout delegates the parent. The derived deadline can elapse
// while waiting for writeMu; that mutex wait itself has no context-select path.
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

// writeJSON requires a nonnil connection, serializes transport writes, and
// substitutes background for nil context. A missing transport returns the
// closed-connection error. Success updates last-used time; transport errors
// propagate and closure observation otherwise belongs to the caller/transport.
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

// readMessageWithTimeout delegates a read using background and the supplied
// per-read timeout. Nonpositive timeout retains an unbounded background budget.
// Connection checks and read-direction serialization are performed by the
// contextual read helper.
func (c *WSConn) readMessageWithTimeout(timeout time.Duration) ([]byte, error) {
	return c.readMessageWithContextTimeout(context.Background(), timeout)
}

// readMessageWithContextTimeout rejects nil/marked closed connections and
// derives a positive timeout from a supplied or background parent. Nonpositive
// timeout keeps that parent. The derived deadline applies to transport reading
// after ordinary read-mutex acquisition.
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

// readMessage requires a nonnil connection and serializes transport reads,
// using background for nil context. Missing transports fail with the closed
// error. Successful reads update activity time; other transport outcomes
// propagate without automatic pool eviction.
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

// pingWithTimeout rejects nil/marked closed connections, serializes against
// writes, and supplies the health-check default for nonpositive timeout. The
// timeout starts after acquiring writeMu and uses background context. Ping
// success does not update last-used time, keeping application activity distinct
// from health probes.
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
	return c.ws.Ping(pingCtx)
}

// supportsIdlePingWithoutReader reports the optional transport capability for a
// present connection. Nil or missing transports return false. Missing
// capability interfaces default to true, while coder/websocket adapters
// advertise their reader requirement explicitly.
func (c *WSConn) supportsIdlePingWithoutReader() bool {
	if c == nil || c.ws == nil {
		return false
	}
	capable, ok := c.ws.(openai.WSIdlePingCapable)
	// A missing capability interface is treated as legacy idle-ping support;
	// transports whose ping needs a reader advertise false to avoid an unsupported
	// background probe.
	return !ok || capable.SupportsIdlePingWithoutReader()
}

// touch records current Unix-nanosecond activity time atomically for a nonnil
// connection.
func (c *WSConn) touch() {
	if c == nil {
		return
	}
	c.lastUsedNano.Store(time.Now().UnixNano())
}

// createdAt reconstructs the stored Unix-nanosecond creation time, returning
// zero for nil or nonpositive storage. The reconstructed value has wall-clock
// time; monotonic clock metadata is not stored in this atomic representation.
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

// lastUsedAt reconstructs the stored Unix-nanosecond activity time, returning
// zero for nil or nonpositive storage. Reads sample the atomic timestamp
// independently of creation time and lease ownership.
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

// idleDuration reports now minus known last-used time, or zero when the
// connection/time is absent. A clock reversal can produce a negative duration.
// Callers choose idle eligibility thresholds from this wall-clock observation.
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

// age reports now minus known creation time, or zero when the connection/time
// is absent. It uses reconstructed wall time, so a clock reversal can produce a
// negative result before expiry checks.
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

// isLeased samples whether a nonnil connection's token channel is empty. Nil
// returns false. It describes token occupancy at that instant, independently of
// transport closure and pin membership.
func (c *WSConn) isLeased() bool {
	if c == nil {
		return false
	}
	return len(c.leaseCh) == 0
}

// handshakeHeader returns a trimmed retained header value after trimming its
// name. Nil connection or header storage returns empty text. Header lookup uses
// http.Header.Get, whose key normalization differs from the routing-variant
// scanner.
func (c *WSConn) handshakeHeader(name string) string {
	if c == nil || c.handshakeHeaders == nil {
		return ""
	}
	return strings.TrimSpace(c.handshakeHeaders.Get(strings.TrimSpace(name)))
}

// matchesRoutingAffinity reports exact routing-preference equality for a nonnil
// retained connection.
func (c *WSConn) matchesRoutingAffinity(routingAffinity string) bool {
	return c != nil && c.routingAffinity == routingAffinity
}

// isPrewarmed reads the connection's protocol-warmup flag atomically, with nil
// returning false. The marker is set by session handling after protocol warmup;
// merely creating an idle socket does not establish warmup completion.
func (c *WSConn) isPrewarmed() bool {
	if c == nil {
		return false
	}
	return c.prewarmed.Load()
}

// markPrewarmed atomically marks a nonnil connection as having completed
// protocol warmup. Nil receivers are ignored. Session callers determine when
// the upstream warmup exchange supplies that evidence.
func (c *WSConn) markPrewarmed() {
	if c == nil {
		return
	}
	c.prewarmed.Store(true)
}

// changeChannelLocked returns the provider change channel, lazily creating it
// when absent. Callers hold ap.mu and retain the returned channel while
// releasing the lock to wait; the next signal closes that generation.
func (ap *openAIWSProviderPool) changeChannelLocked() chan struct{} {
	if ap.changedCh == nil {
		ap.changedCh = make(chan struct{})
	}
	return ap.changedCh
}

// signalChangedLocked closes the current provider change channel and replaces
// it under ap.mu. Nil providers are ignored. Existing selectors wake on the
// closed generation, while new selectors subscribe to a fresh channel for the
// next state change.
func (ap *openAIWSProviderPool) signalChangedLocked() {
	if ap == nil {
		return
	}
	if ap.changedCh != nil {
		close(ap.changedCh)
	}
	ap.changedCh = make(chan struct{})
}

// NewWSConnPool initializes a usable pool, default dialer, cleanup channels,
// and prewarm cancellation context, copying supplied scalar options.
// Construction does not start periodic workers; Start does. Acquisition can
// still dial or launch demand prewarming before Start, and the lifecycle owner
// eventually calls Close.
func NewWSConnPool(cfg *WSPoolOptions) *WSConnPool {
	pool := &WSConnPool{
		clientDialer:  openai.NewDefaultWSClientDialer(),
		workerStopCh:  make(chan struct{}),
		cleanupWakeCh: make(chan struct{}, 1),
	}
	if cfg != nil {
		copy := *cfg
		pool.cfg.Store(&copy)
	}
	pool.prewarmCtx, pool.prewarmCancel = context.WithCancel(context.Background())
	return pool
}

// SnapshotMetrics loads lifetime counters independently, returning zero for a
// nil pool. The fields are individually atomic but do not form one
// transactional sample; consumers use them for diagnostics rather than
// admission or accounting decisions requiring a consistent tuple.
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

// SnapshotTransportMetrics delegates to a dialer exposing transport metrics,
// otherwise returning a zero snapshot. Nil pools also return zero. The dialer
// owns its sampling and reuse-ratio rules independently of pool acquisition
// counters.
func (p *WSConnPool) SnapshotTransportMetrics() openai.WSTransportMetricsSnapshot {
	if p == nil {
		return openai.WSTransportMetricsSnapshot{}
	}
	if dialer, ok := p.clientDialer.(openai.WSTransportMetricsDialer); ok {
		return dialer.SnapshotTransportMetrics()
	}
	return openai.WSTransportMetricsSnapshot{}
}

// SetClientDialerForTest installs a nonnil test dialer on a nonnil pool. It has
// no synchronization with workers or acquisitions; callers set it before
// concurrent use, and a nil argument leaves the existing dialer installed.
func (p *WSConnPool) SetClientDialerForTest(dialer openai.WSClientDialer) {
	if p == nil || dialer == nil {
		return
	}
	p.clientDialer = dialer
}

// Close records shutdown, cancels prewarming, wakes selectors, and joins
// registered workers, acquisitions, and prewarm batches. Nil pools and repeated
// calls are accepted. Active leases remain transport-usable until their release
// or another explicit eviction.
//
// Idle maps are swept before joins and again afterward to collect late sockets.
// Closing runs outside provider locking, but this call waits for those
// handshakes and registered work. Ordinary dials keep their caller contexts;
// Close does not independently cancel them, so their completion still depends
// on caller budget and dialer behavior. Start and new Acquire registration are
// excluded through runtimeMu before waiting.
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
			var idle []*WSConn
			p.providers.Range(func(_, value any) bool {
				ap, ok := value.(*openAIWSProviderPool)
				if !ok || ap == nil {
					return true
				}
				ap.mu.Lock()
				for id, conn := range ap.conns {
					if conn != nil && !conn.isLeased() {
						delete(ap.conns, id)
						delete(ap.pinnedConns, id)
						idle = append(idle, conn)
					}
				}
				ap.signalChangedLocked()
				ap.mu.Unlock()
				return true
			})
			closeOpenAIWSConns(idle)
		}
		// Publish shutdown and wake waiters before waiting for resource work;
		// sweep again after joins because a registered dial can finish after the first idle snapshot.
		closeConnections()
		p.workerWg.Wait()
		p.acquireWG.Wait()
		p.prewarmWG.Wait()
		closeConnections()
	})
}

// startBackgroundWorkers registers and starts one ping worker and one cleanup
// worker when a pool/stop channel exists. It has no own once guard. Start
// invokes it under lifecycle locking; direct callers serialize invocation and
// ensure shutdown can wait for both registrations.
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

// runBackgroundPingWorker periodically runs idle ping sweeps until workerStopCh
// closes. Nil pools return immediately. The enclosing worker registration owns
// its join; each sweep completes its own selected ping work before the next
// tick is handled.
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

// runBackgroundPingSweep probes eligible idle connections with at most ten
// concurrent ping tasks and evicts failed transports. Nil pools are ignored.
// Candidates are sampled under provider locking, then eligibility is checked
// again before scheduling.
//
// Those checks are observations, not lease reservations: a selected candidate
// can become leased after checking, and writeMu serializes its ping with
// writes. Reader-requiring transports are excluded by capability. Each task
// handles its own ping failure through eviction, and the sweep waits for the
// task group while suppressing its unused error result.
func (p *WSConnPool) runBackgroundPingSweep() {
	if p == nil {
		return
	}
	candidates := p.snapshotIdleConnsForPing()
	var g errgroup.Group
	// Limit simultaneous probes to ten transports while waiting for all selected
	// tasks, keeping one sweep from creating unbounded concurrent socket work.
	g.SetLimit(10)
	for _, item := range candidates {
		if item.conn == nil || item.conn.isLeased() || item.conn.waiters.Load() > 0 || !item.conn.supportsIdlePingWithoutReader() {
			continue
		}
		g.Go(func() error {
			if err := item.conn.pingWithTimeout(openAIWSConnHealthCheckTO); err != nil {
				p.evictConn(item.providerID, item.conn.id)
			}
			return nil
		})
	}
	// Probe errors are handled through per-task eviction; join remaining probes
	// before returning, while the task-group error value has no additional policy role.
	_ = g.Wait()
}

// snapshotIdleConnsForPing gathers valid idle sockets without forced/general
// compatible waiters under each provider mutex. Nil pools return nil. Returned
// references are not leased or pinned by this snapshot, so sweep eligibility
// must be rechecked before probing.
func (p *WSConnPool) snapshotIdleConnsForPing() []openAIWSIdlePingCandidate {
	if p == nil {
		return nil
	}
	candidates := make([]openAIWSIdlePingCandidate, 0)
	p.providers.Range(func(key, value any) bool {
		providerID, ok := key.(int64)
		if !ok || providerID <= 0 {
			return true
		}
		ap, ok := value.(*openAIWSProviderPool)
		if !ok || ap == nil {
			return true
		}
		ap.mu.Lock()
		for _, conn := range ap.conns {
			if conn == nil || conn.isLeased() || conn.waiters.Load() > 0 || hasCompatibleWaiterLocked(ap, conn) {
				continue
			}
			candidates = append(candidates, openAIWSIdlePingCandidate{
				providerID: providerID,
				conn:       conn,
			})
		}
		ap.mu.Unlock()
		return true
	})
	return candidates
}

// runBackgroundCleanupWorker sweeps on periodic ticks or coalesced cleanup
// wakes until shutdown. Nil pools return immediately. Its enclosing worker
// registration owns joining; actual connection closing happens in each sweep
// outside provider locking.
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
		case <-p.cleanupWakeCh:
			p.runBackgroundCleanupSweep(time.Now())
		case <-p.workerStopCh:
			return
		}
	}
}

// runBackgroundCleanupSweep retires provider connections using current time and
// each provider's effective capacity. Invalid stored provider values are
// skipped. The caller's timestamp is recorded after each locked cleanup
// decision.
//
// Evicted sockets are collected under provider locking and closed after
// provider enumeration releases those locks. The latest successful request
// supplies provider-specific limits when present, otherwise the configured hard
// cap is used. This keeps close handshakes out of shared selection locks while
// preserving map/pin mutation and waiter notifications under their owning
// mutex.
func (p *WSConnPool) runBackgroundCleanupSweep(now time.Time) {
	if p == nil {
		return
	}
	// cleanupResult retains retired socket references for closing after provider locks
	// have been released by the cleanup sweep.
	type cleanupResult struct {
		evicted []*WSConn // retired sockets awaiting closure
	}
	results := make([]cleanupResult, 0)
	p.providers.Range(func(_ any, value any) bool {
		ap, ok := value.(*openAIWSProviderPool)
		if !ok || ap == nil {
			return true
		}
		maxConns := p.maxConnsHardCap()
		ap.mu.Lock()
		if ap.lastAcquire != nil && ap.lastAcquire.Provider != nil {
			maxConns = p.effectiveMaxConnsByProvider(ap.lastAcquire.Provider)
		}
		evicted := p.cleanupProviderLocked(ap, now, maxConns)
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

// Acquire registers one acquisition against pool shutdown, clones request
// headers, and delegates selection within ctx. Closed pools return before
// registration; nil pools reach request validation. The caller releases a
// successful lease after its I/O ends, and nil context selects background in
// the selector.
//
// runtimeMu orders WaitGroup registration before Close starts joining, so new
// acquisitions cannot appear after shutdown publication. Queueing wakes on stop
// while ordinary dials retain the supplied context. The clone copies headers
// and trimmed strings but shares Provider, TLSProfile, and callback references,
// which callers keep stable throughout selection and later retained prewarm
// use.
func (p *WSConnPool) Acquire(ctx context.Context, req WSAcquireRequest) (*WSConnLease, error) {
	if p != nil {
		p.runtimeMu.Lock()
		if p.closed {
			p.runtimeMu.Unlock()
			return nil, errOpenAIWSConnClosed
		}
		// Register under lifecycle locking before Close can begin waiting, so
		// this acquisition retains shutdown ownership throughout delegated selection and dialing.
		p.acquireWG.Add(1)
		p.runtimeMu.Unlock()
		defer p.acquireWG.Done()
	}
	if p != nil {
		p.metrics.acquireTotal.Add(1)
	}
	return p.acquire(ctx, CloneWSAcquireRequest(req))
}

// acquire selects a compatible token, reserves a dial slot, or waits for
// provider change using one caller context. Nil context selects background;
// valid provider identity and nonblank target are required. It returns a lease
// whose I/O must finish before release, and makes no FIFO waiter guarantee.
//
// Provider locking orders retirement, reservation, token selection, and
// generation checks. A new dial reserves its token before map publication.
// Forced continuation waits only for the compatible selected ID; forced-new
// acquisition never queues. Ordinary selection can relax routing preference
// when queued/full and reclaim incompatible idle capacity. Canceled undelivered
// tokens return through asynchronous cleanup, while health/generation recovery
// has one shared retry allowance.
func (p *WSConnPool) acquire(ctx context.Context, req WSAcquireRequest) (lease *WSConnLease, err error) {
	if p == nil || req.Provider == nil || req.Provider.ID <= 0 {
		return nil, errors.New("invalid ws acquire request")
	}
	if stringsTrim(req.WSURL) == "" {
		return nil, errors.New("ws url is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	providerID := req.Provider.ID
	compatibility := wsCompatibilityForRequest(req)
	routingAffinity := normalizeOpenAIWSRoutingAffinity(req.Headers)
	forcePreferred := !req.ForceNewConn && req.ForcePreferredConn
	ap := p.getOrCreateProviderPool(providerID)
	var waiter *wsPoolWaiter
	var queueWait time.Duration
	queued := false
	retry := 0
	finishWaitingLocked := func() bool {
		if waiter == nil {
			return false
		}
		queueWait += removeWaiterLocked(ap, waiter)
		waiter = nil
		return true
	}
	defer func() {
		ap.mu.Lock()
		removed := finishWaitingLocked()
		ap.mu.Unlock()
		if removed {
			// Return its queue reservation before requesting coalesced cleanup,
			// so cancellation completes independently of any later idle socket close handshake.
			p.requestCleanup()
		}
		if queued {
			p.metrics.acquireQueueWaitMs.Add(queueWait.Milliseconds())
		}
		if lease != nil {
			lease.queueWait = queueWait
		}
	}()

	for {
		p.runtimeMu.Lock()
		closed := p.closed
		p.runtimeMu.Unlock()
		if closed {
			return nil, errOpenAIWSConnClosed
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		maxConns := p.effectiveMaxConnsByProvider(req.Provider)
		ap.mu.Lock()
		generation := ap.generation
		now := time.Now()
		evicted := p.retireExpiredConnsLocked(ap, now)
		if ap.lastCleanupAt.IsZero() || now.Sub(ap.lastCleanupAt) >= openAIWSAcquireCleanupInterval {
			evicted = append(evicted, p.cleanupProviderLocked(ap, now, maxConns)...)
			ap.lastCleanupAt = now
		}
		pickStarted := time.Now()
		var preferred, selected *WSConn
		if forcePreferred {
			preferred = ap.conns[req.PreferredConnID]
			if !preferred.matchesCompatibility(compatibility) || p.connMaxAgeReached(preferred, now) {
				ap.mu.Unlock()
				closeOpenAIWSConns(evicted)
				return nil, openai.ErrOpenAIWSPreferredConnUnavailable
			}
			if preferred.tryAcquire() {
				selected = preferred
			}
		} else if !req.ForceNewConn {
			allowAny := routingAffinity == "" || waiter != nil || len(ap.conns)+ap.creating >= maxConns
			selected = p.pickAvailableConnLocked(ap, req.PreferredConnID, compatibility, routingAffinity, allowAny)
		}
		if selected != nil {
			if finishWaitingLocked() {
				// Wake background cleanup after returning this waiter slot, letting
				// lease delivery proceed while unrelated surplus idle sockets close through the worker.
				p.requestCleanup()
			}
			connPick := time.Since(pickStarted)
			p.recordConnPickDuration(connPick)
			ap.mu.Unlock()
			closeOpenAIWSConns(evicted)
			if p.shouldHealthCheckConn(selected) {
				if pingErr := selected.pingWithTimeout(openAIWSConnHealthCheckTO); pingErr != nil {
					p.evictConn(providerID, selected.id)
					// One shared recovery ticket covers health and generation races;
					// eviction has removed this failed socket before another selection loop begins.
					if retry < 1 {
						retry++
						continue
					}
					return nil, pingErr
				}
			}
			return p.deliverLease(ctx, req, generation, selected, true, connPick)
		}

		if !forcePreferred && len(ap.conns)+ap.creating >= maxConns {
			var idle *WSConn
			if req.ForceNewConn {
				idle = p.pickOldestIdleConnLocked(ap)
			} else {
				idle = p.pickOldestIncompatibleIdleConnLocked(ap, compatibility)
			}
			if idle != nil {
				delete(ap.conns, idle.id)
				evicted = append(evicted, idle)
				p.metrics.scaleDownTotal.Add(1)
			}
		}
		connPick := time.Since(pickStarted)
		p.recordConnPickDuration(connPick)
		if !forcePreferred && len(ap.conns)+ap.creating < maxConns {
			finishWaitingLocked()
			// Reserve capacity before releasing provider locking for network work;
			// concurrent selectors include this unfinished dial when checking the effective cap.
			ap.creating++
			ap.mu.Unlock()
			closeOpenAIWSConns(evicted)
			conn, dialErr := p.dialConn(ctx, req)
			ap.mu.Lock()
			ap.creating--
			ap.signalChangedLocked()
			// A provider reset invalidates the dial captured under its old generation;
			// return the reservation, wake selectors, and discard that late socket before retry.
			if ap.generation != generation {
				ap.mu.Unlock()
				conn.close()
				if retry < 1 {
					retry++
					continue
				}
				return nil, errOpenAIWSConnClosed
			}
			if dialErr != nil {
				ap.prewarmFails++
				ap.prewarmFailAt = time.Now()
				ap.mu.Unlock()
				return nil, dialErr
			}
			if len(ap.conns) >= p.effectiveMaxConnsByProvider(req.Provider) {
				ap.mu.Unlock()
				conn.close()
				return nil, openai.ErrOpenAIWSConnQueueFull
			}
			// Consume the fresh token before map publication, preventing another
			// selector from taking the dialing request's connection during the handoff window.
			if !conn.tryAcquire() {
				ap.mu.Unlock()
				conn.close()
				return nil, errOpenAIWSConnClosed
			}
			ap.conns[conn.id] = conn
			ap.prewarmFails = 0
			ap.prewarmFailAt = time.Time{}
			ap.mu.Unlock()
			return p.deliverLease(ctx, req, generation, conn, false, connPick)
		}
		if req.ForceNewConn {
			ap.mu.Unlock()
			closeOpenAIWSConns(evicted)
			return nil, openai.ErrOpenAIWSConnQueueFull
		}
		if waiter == nil {
			waiter, err = p.addWaiterLocked(ap, ctx, compatibility, preferred, maxConns)
			if err != nil {
				ap.mu.Unlock()
				closeOpenAIWSConns(evicted)
				return nil, err
			}
			if !queued {
				p.metrics.acquireQueueWaitTotal.Add(1)
				queued = true
			}
		}
		changed := ap.changeChannelLocked()
		ap.mu.Unlock()
		closeOpenAIWSConns(evicted)
		// Keep this one waiter reservation across change wakeups; selection can
		// retry without spending another queue slot or resetting its original context budget.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.workerStopCh:
			return nil, errOpenAIWSConnClosed
		case <-changed:
		}
	}
}

// deliverLease checks pool shutdown and caller cancellation after a token was
// selected, returning undelivered capacity before errors. Success records
// acquisition counters and a generation-qualified prewarm target, then
// schedules target work.
//
// Cancellation can race after the entry checks, so a successful return may
// still have a later canceled context. Generation qualifies retained request
// publication; it is not rechecked here as a lease-delivery condition, and
// explicit provider clearing can close a selected socket concurrently. Callers
// therefore follow transport errors and session recovery rather than treating
// delivery as a lifetime guarantee against eviction.
func (p *WSConnPool) deliverLease(ctx context.Context, req WSAcquireRequest, generation uint64, conn *WSConn, reused bool, pick time.Duration) (*WSConnLease, error) {
	lease := &WSConnLease{pool: p, ProviderID: req.Provider.ID, Conn: conn, connPick: pick, reused: reused}
	p.runtimeMu.Lock()
	closed := p.closed
	p.runtimeMu.Unlock()
	if closed {
		p.releaseUndeliveredConn(req.Provider.ID, conn)
		return nil, errOpenAIWSConnClosed
	}
	if err := ctx.Err(); err != nil {
		p.releaseUndeliveredConn(req.Provider.ID, conn)
		return nil, err
	}
	if reused {
		p.metrics.acquireReuseTotal.Add(1)
	} else {
		p.metrics.acquireCreateTotal.Add(1)
	}
	p.recordLastSuccessfulAcquire(req.Provider.ID, generation, req)
	p.ensureTargetIdleAsync(req.Provider.ID)
	return lease, nil
}

// recordConnPickDuration counts one selection-loop observation and adds its
// whole milliseconds. Negative duration contributes zero time while still
// counting the observation. Nil pools are ignored; this counter includes
// selection loops that did not return a lease.
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

// pickOldestIdleConnLocked selects the least recently used reclaimable socket
// under ap.mu. Leased, pinned, forced-waited, or generally compatible-waited
// sockets are excluded. Equal timestamps follow map iteration rather than a
// fixed tie order; the caller removes/closes the selection.
func (p *WSConnPool) pickOldestIdleConnLocked(ap *openAIWSProviderPool) *WSConn {
	if ap == nil || len(ap.conns) == 0 {
		return nil
	}
	var oldest *WSConn
	for _, conn := range ap.conns {
		if conn == nil || conn.isLeased() || conn.waiters.Load() > 0 || p.isConnPinnedLocked(ap, conn.id) || hasCompatibleWaiterLocked(ap, conn) {
			continue
		}
		if oldest == nil || conn.lastUsedAt().Before(oldest.lastUsedAt()) {
			oldest = conn
		}
	}
	return oldest
}

// pickOldestIncompatibleIdleConnLocked selects the least recently used
// reclaimable socket with a different compatibility tuple. Callers hold ap.mu.
// Leases, pins, and forced/general compatible waiters protect entries;
// equal-time ties follow map iteration.
func (p *WSConnPool) pickOldestIncompatibleIdleConnLocked(ap *openAIWSProviderPool, compatibility wsConnCompatibility) *WSConn {
	var oldest *WSConn
	for _, conn := range ap.conns {
		if conn == nil || conn.matchesCompatibility(compatibility) || conn.isLeased() ||
			conn.waiters.Load() > 0 || p.isConnPinnedLocked(ap, conn.id) || hasCompatibleWaiterLocked(ap, conn) {
			continue
		}
		if oldest == nil || conn.lastUsedAt().Before(oldest.lastUsedAt()) {
			oldest = conn
		}
	}
	return oldest
}

// getOrCreateProviderPool publishes one initialized provider pool with
// sync.Map.LoadOrStore for a positive ID. Nil/invalid input returns nil. An
// unexpected stored value leaves that value installed and returns the newly
// allocated private pool, so callers maintain the map's intended value type.
func (p *WSConnPool) getOrCreateProviderPool(providerID int64) *openAIWSProviderPool {
	if p == nil || providerID <= 0 {
		return nil
	}
	if existing, ok := p.providers.Load(providerID); ok {
		if ap, typed := existing.(*openAIWSProviderPool); typed && ap != nil {
			return ap
		}
	}
	ap := &openAIWSProviderPool{
		conns:       make(map[string]*WSConn),
		pinnedConns: make(map[string]int),
		changedCh:   make(chan struct{}),
	}
	actual, _ := p.providers.LoadOrStore(providerID, ap)
	if typed, ok := actual.(*openAIWSProviderPool); ok && typed != nil {
		return typed
	}
	return ap
}

// getProviderPool returns a typed existing provider pool for a positive ID. Nil
// pools, missing entries, and unexpected/nil stored values return false. It
// does not create replacement state or check lifecycle shutdown.
func (p *WSConnPool) getProviderPool(providerID int64) (*openAIWSProviderPool, bool) {
	if p == nil || providerID <= 0 {
		return nil, false
	}
	value, ok := p.providers.Load(providerID)
	if !ok || value == nil {
		return nil, false
	}
	ap, typed := value.(*openAIWSProviderPool)
	return ap, typed && ap != nil
}

// notifyProviderPoolChanged publishes a provider change under its mutex when a
// typed pool exists. Missing state is ignored. Selectors holding an earlier
// change channel wake and recheck the current map/reservation state.
func (p *WSConnPool) notifyProviderPoolChanged(providerID int64) {
	ap, ok := p.getProviderPool(providerID)
	if !ok || ap == nil {
		return
	}
	ap.mu.Lock()
	ap.signalChangedLocked()
	ap.mu.Unlock()
}

// isConnPinnedLocked reads a positive connection pin count under ap.mu,
// rejecting nil pools or empty IDs. Pin presence protects selected idle cleanup
// paths; explicit eviction, clearing, or shutdown still owns disposal.
func (p *WSConnPool) isConnPinnedLocked(ap *openAIWSProviderPool, connID string) bool {
	if ap == nil || connID == "" || len(ap.pinnedConns) == 0 {
		return false
	}
	return ap.pinnedConns[connID] > 0
}

// cleanupProviderLocked removes retired/excess idle sockets and returns
// references for closing after ap.mu is released. Nonpositive maxConns selects
// the hard cap. Pins, leases, forced waiters, and compatible general waiters
// protect live entries during capacity cleanup.
//
// Eligible idle sockets are ordered by last use. Removal is bounded by
// available idle entries and satisfies both idle and total capacity where
// possible; busy/pinned demand can leave excess total connections.
// Connection/pin entries change together, scaling metrics advance, and
// selectors are signaled after mutation. The caller performs transport closes
// outside this mutex.
func (p *WSConnPool) cleanupProviderLocked(ap *openAIWSProviderPool, now time.Time, maxConns int) []*WSConn {
	if ap == nil {
		return nil
	}
	evicted := p.retireExpiredConnsLocked(ap, now)

	if maxConns <= 0 {
		maxConns = p.maxConnsHardCap()
	}
	maxIdle := p.maxIdlePerProvider()
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
			// Retain live capacity for leases, pins, and matching waiters during
			// idle shrinking; later release/cleanup rechecks limits after those protections end.
			if conn.isLeased() || conn.waiters.Load() > 0 || p.isConnPinnedLocked(ap, conn.id) || hasCompatibleWaiterLocked(ap, conn) {
				continue
			}
			idleConns = append(idleConns, conn)
		}
		sort.SliceStable(idleConns, func(i, j int) bool {
			return idleConns[i].lastUsedAt().Before(idleConns[j].lastUsedAt())
		})
		redundant := min(max(0, len(idleConns)-maxIdle, len(ap.conns)-maxConns), len(idleConns))
		for i := range redundant {
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

// pickAvailableConnLocked nonblockingly reserves one compatible, unexpired,
// idle socket under ap.mu. Requested ID wins before routing affinity; otherwise
// least recent affinity wins, then an allowed other route. Forced-waiter counts
// exclude ordinary selection.
//
// The selected token is consumed before return so competing selectors cannot
// reserve it again. Routing preference can be relaxed by the acquisition owner
// when queued or full. Equal-time selection follows map iteration, and general
// waiter presence does not establish FIFO admission. Caller retirement and
// close policy remain separate from this selection helper.
func (p *WSConnPool) pickAvailableConnLocked(ap *openAIWSProviderPool, preferredID string, compatibility wsConnCompatibility, routingAffinity string, allowAny bool) *WSConn {
	now := time.Now()
	available := func(conn *WSConn) bool {
		return conn.matchesCompatibility(compatibility) && !conn.isLeased() &&
			conn.waiters.Load() == 0 && !p.connMaxAgeReached(conn, now)
	}
	if preferred := ap.conns[preferredID]; available(preferred) && preferred.tryAcquire() {
		return preferred
	}
	var affine, other *WSConn
	for _, conn := range ap.conns {
		if !available(conn) {
			continue
		}
		if conn.matchesRoutingAffinity(routingAffinity) {
			if affine == nil || conn.lastUsedAt().Before(affine.lastUsedAt()) {
				affine = conn
			}
		} else if allowAny && (other == nil || conn.lastUsedAt().Before(other.lastUsedAt())) {
			other = conn
		}
	}
	if affine != nil && affine.tryAcquire() {
		return affine
	}
	if other != nil && other.tryAcquire() {
		return other
	}
	return nil
}

// ClearProvider removes and closes every recorded provider socket, including
// active or pinned ones, and resets retained prewarm target/failure state.
// Nil/invalid/missing providers are ignored. Generation advances before map
// clearing.
//
// In-flight dial/prewarm publication checks reject the old generation, and
// waiting selectors wake against cleared maps. Closing runs after provider
// unlocking, allowing other selection work while handshakes finish. Existing
// lease objects can still refer to closed transports, so callers apply their
// recovery policy and cannot treat clearing as a graceful active-lease drain.
func (p *WSConnPool) ClearProvider(providerID int64) {
	if p == nil || providerID <= 0 {
		return
	}
	ap, ok := p.getProviderPool(providerID)
	if !ok || ap == nil {
		return
	}
	ap.mu.Lock()
	// Advance generation before clearing sockets/target and waking selectors;
	// a dial finishing afterward cannot restore resources captured before this provider reset.
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

// evictConn removes a selected connection and its pin bookkeeping under
// provider locking, signals selectors, then closes it after unlocking.
// Invalid/missing selections are ignored. Existing holders can observe
// transport closure; the caller chooses whether their request can recover.
func (p *WSConnPool) evictConn(providerID int64, connID string) {
	if p == nil || providerID <= 0 || stringsTrim(connID) == "" {
		return
	}
	var conn *WSConn
	ap, ok := p.getProviderPool(providerID)
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

// PinConn increments a retained connection's pin reference count under provider
// locking. Invalid or missing identities return false. Each successful pin
// needs a matching UnpinConn; pinning prevents eligible idle cleanup but does
// not reserve the lease token or defeat explicit eviction/shutdown.
func (p *WSConnPool) PinConn(providerID int64, connID string) bool {
	if p == nil || providerID <= 0 {
		return false
	}
	connID = stringsTrim(connID)
	if connID == "" {
		return false
	}
	ap, ok := p.getProviderPool(providerID)
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

// UnpinConn decrements pin references and removes the entry when its count is
// one or less. Invalid/missing state is ignored. It wakes selectors after
// updating the pin map but does not itself perform transport cleanup.
func (p *WSConnPool) UnpinConn(providerID int64, connID string) {
	if p == nil || providerID <= 0 {
		return
	}
	connID = stringsTrim(connID)
	if connID == "" {
		return
	}
	ap, ok := p.getProviderPool(providerID)
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

// dialConn clones request headers, optionally refreshes them through
// HeadersFactory, and delegates transport dialing. Factory errors propagate
// before transport work. Dial failures retain status, cloned handshake headers,
// and copied structured handshake body in WSDialError.
//
// A nil transport with nil error is converted into dial failure. Successful
// sockets receive a unique pool ID, TLS identity, and the request-derived
// destination/handshake compatibility and routing affinity. Those keys use
// req.Headers rather than factory-produced authentication headers, so callers
// keep transport identity stable while refreshing dial credentials and
// invalidate provider connections when credential policy requires it.
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
		var handshakeErr *openai.WSHandshakeError
		var responseBody []byte
		if errors.As(err, &handshakeErr) && handshakeErr != nil {
			responseBody = append([]byte(nil), handshakeErr.Body...)
		}
		return nil, &openai.WSDialError{
			StatusCode: status,

			ResponseHeaders: cloneHeader(handshakeHeaders),

			ResponseBody: responseBody,

			Err: err,
		}
	}
	if conn == nil {
		return nil, &openai.WSDialError{
			StatusCode: status,

			ResponseHeaders: cloneHeader(handshakeHeaders),

			Err: errors.New("openai ws dialer returned nil connection"),
		}
	}
	id := p.nextConnID(req.Provider.ID)
	pooledConn := NewWSConn(id, req.Provider.ID, conn, handshakeHeaders, req.TLSProfile, req.TLSProfileKey)
	pooledConn.compatibility = wsCompatibilityForRequest(req)
	pooledConn.routingAffinity = normalizeOpenAIWSRoutingAffinity(req.Headers)
	return pooledConn, nil
}

// nextConnID combines provider ID and an atomic increasing pool-local sequence
// into a connection identifier.
func (p *WSConnPool) nextConnID(providerID int64) string {
	seq := p.seq.Add(1)
	buf := make([]byte, 0, 32)
	buf = append(buf, "oa_ws_"...)
	buf = strconv.AppendInt(buf, providerID, 10)
	buf = append(buf, '_')
	buf = strconv.AppendUint(buf, seq, 10)
	return string(buf)
}

// shouldHealthCheckConn selects a known connection idle past the health-check
// threshold only when its transport supports ping without a reader.
// Missing/reader-dependent connections return false. Acquisition owns any
// eviction/retry after this eligibility observation.
func (p *WSConnPool) shouldHealthCheckConn(conn *WSConn) bool {
	if conn == nil || !conn.supportsIdlePingWithoutReader() {
		return false
	}
	return conn.idleDuration(time.Now()) >= openAIWSConnHealthCheckIdle
}

// maxConnsHardCap delegates the current option snapshot's configured or default
// provider connection ceiling.
func (p *WSConnPool) maxConnsHardCap() int { return p.nativeOptions().MaxConnsHardCap() }

// effectiveMaxConnsByProvider delegates provider-capacity selection to the
// current immutable option snapshot. Nil options/providers select the option
// helper's default hard cap. Callers keep provider concurrency stable while the
// calculation reads it.
func (p *WSConnPool) effectiveMaxConnsByProvider(provider *WSPoolProvider) int {
	return p.nativeOptions().EffectiveMaxConnsByProvider(provider)
}

// minIdlePerProvider delegates the current option snapshot's minimum idle
// socket setting.
func (p *WSConnPool) minIdlePerProvider() int { return p.nativeOptions().MinIdle() }

// maxIdlePerProvider delegates the current option snapshot's maximum retained
// idle socket setting.
func (p *WSConnPool) maxIdlePerProvider() int { return p.nativeOptions().MaxIdle() }

// maxConnAge delegates the current option snapshot's permitted connection
// lifetime limit.
func (p *WSConnPool) maxConnAge() time.Duration { return p.nativeOptions().MaxConnAge() }

// queueLimitPerConn delegates the current option snapshot's per-connection
// waiter-capacity setting.
func (p *WSConnPool) queueLimitPerConn() int { return p.nativeOptions().QueueLimit() }

// targetUtilization delegates the current option snapshot's validated demand
// utilization ratio.
func (p *WSConnPool) targetUtilization() float64 { return p.nativeOptions().TargetUtilization() }

// dialTimeout delegates the current option snapshot's positive or default
// prewarm dial budget.
func (p *WSConnPool) dialTimeout() time.Duration { return p.nativeOptions().DialTimeout() }

// Start launches periodic ping/cleanup workers once while the pool is open. Nil
// pools, repeated startup, and startup after Close are ignored. runtimeMu
// orders worker registration against shutdown, while acquisition and demand
// prewarming can operate before this explicit worker start.
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

// nativeOptions returns the current shared immutable option pointer, or nil for
// a nil pool. Callers read it without mutation and copy its scalar value before
// UpdateOptions. Option helpers provide defaults when the pointer itself is
// nil.
func (p *WSConnPool) nativeOptions() *WSPoolOptions {
	if p == nil {
		return nil
	}
	return p.cfg.Load()
}

// SnapshotProviderState returns provider map counts plus sampled occupied-token
// count under provider locking. Missing typed state returns false. Pinned and
// leased categories overlap, and pin count is the number of map entries rather
// than the sum of references.
//
// The provider map cannot change during enumeration, but lease token release
// can occur outside that lock. Consumers therefore use this snapshot for
// diagnostics rather than a transactional capacity decision. Nil connection
// entries can still count toward Connections until cleanup removes them; leased
// counting skips them.
func (p *WSConnPool) SnapshotProviderState(id int64) (WSPoolProviderState, bool) {
	provider, ok := p.getProviderPool(id)
	if !ok {
		return WSPoolProviderState{}, false
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	state := WSPoolProviderState{Connections: len(provider.conns), PinnedConnections: len(provider.pinnedConns)}
	for _, connection := range provider.conns {
		if connection != nil && connection.isLeased() {
			state.LeasedConnections++
		}
	}
	return state, true
}

// EvictConnection delegates removal and transport closing for a selected
// provider/socket identity. Missing selections are ignored. Callers choose
// retry/reconnection policy and expect active holders to observe transport
// closure when eviction applies.
func (p *WSConnPool) EvictConnection(id int64, connectionID string) { p.evictConn(id, connectionID) }

// UpdateOptions publishes a copied scalar option snapshot while the pool is
// open, then reconciles provider idle capacity and prewarm demand. A closed
// pool returns before publication; the receiver needs to be nonnil.
//
// runtimeMu protects publication against shutdown, and provider reconciliation
// runs afterward under each provider's own locking. Closing is outside those
// locks but can delay this update's return. Busy/pinned/waited entries retain
// their live protection during capacity cleanup, while later calls observe the
// newly published snapshot. Concurrent updates can interleave reconciliation
// using the latest current options.
func (p *WSConnPool) UpdateOptions(options WSPoolOptions) error {
	p.runtimeMu.Lock()
	if p.closed {
		p.runtimeMu.Unlock()
		return errOpenAIWSConnClosed
	}
	// Publish the copied scalar snapshot before provider reconciliation, allowing
	// concurrent readers to select current limits while slow idle closes finish outside lifecycle locking.
	p.cfg.Store(&options)
	p.runtimeMu.Unlock()
	p.providers.Range(func(key, _ any) bool {
		id, ok := key.(int64)
		if !ok {
			return true
		}
		p.reconcileProvider(id)
		p.ensureTargetIdleAsync(id)
		return true
	})
	return nil
}

// reconcileProvider samples current provider capacity, removes eligible excess
// idle sockets, and signals selectors under provider locking. Missing typed
// state is ignored. It closes returned sockets afterward, so callers may wait
// for cleanup despite lock-free transport closing.
func (p *WSConnPool) reconcileProvider(id int64) {
	ap, ok := p.getProviderPool(id)
	if !ok {
		return
	}
	ap.mu.Lock()
	limit := p.maxConnsHardCap()
	if ap.lastAcquire != nil {
		limit = p.effectiveMaxConnsByProvider(ap.lastAcquire.Provider)
	}
	evicted := p.cleanupProviderLocked(ap, time.Now(), limit)
	ap.signalChangedLocked()
	ap.mu.Unlock()
	closeOpenAIWSConns(evicted)
}

// matchesCompatibility reports exact transport-tuple equality for a nonnil
// retained connection.
func (c *WSConn) matchesCompatibility(compatibility wsConnCompatibility) bool {
	return c != nil && c.compatibility == compatibility
}
