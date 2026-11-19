package ws

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNormalizeOpenAIWSRoutingAffinityPrefersCanonicalAndSortsVariants supplies
// blank/nonblank canonical values and an uppercase variant. The canonical
// nonblank value wins, and deleting that key permits the variant value.
func TestNormalizeOpenAIWSRoutingAffinityPrefersCanonicalAndSortsVariants(t *testing.T) {
	headers := http.Header{
		"X-CODEX-ROUTING-HINT": []string{" variant-uppercase "},
		"X-Codex-Routing-Hint": []string{" ", " canonical "},
	}

	require.Equal(t, "canonical", normalizeOpenAIWSRoutingAffinity(headers))

	delete(headers, "X-Codex-Routing-Hint")
	require.Equal(t, "variant-uppercase", normalizeOpenAIWSRoutingAffinity(headers))
}

// TestOpenAIWSConnLease_WriteJSONAndGuards writes through a retained fake
// connection with nonpositive timeout. Nil leases and missing connections
// reject contextual writes through the closed-resource sentinel.
func TestOpenAIWSConnLease_WriteJSONAndGuards(t *testing.T) {
	conn := NewWSConn("lease_write", 1, &openAIWSFakeConn{}, nil, nil, "")
	lease := &WSConnLease{Conn: conn}
	require.NoError(t, lease.WriteJSON(map[string]any{"type": "response.create"}, 0))

	var nilLease *WSConnLease
	err := nilLease.WriteJSONWithContextTimeout(context.Background(), map[string]any{"type": "response.create"}, time.Second)
	require.ErrorIs(t, err, errOpenAIWSConnClosed)

	err = (&WSConnLease{}).WriteJSONWithContextTimeout(context.Background(), map[string]any{"type": "response.create"}, time.Second)
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
}

// TestOpenAIWSConn_WriteJSONWithTimeout_NilParentContextUsesBackground
// supplies context.Background to the write helper. Its assertions check
// successful output and a nonnil context reaching the probe transport.
func TestOpenAIWSConn_WriteJSONWithTimeout_NilParentContextUsesBackground(t *testing.T) {
	probe := &openAIWSContextProbeConn{}
	conn := NewWSConn("ctx_probe", 1, probe, nil, nil, "")
	require.NoError(t, conn.writeJSONWithTimeout(context.Background(), map[string]any{"type": "response.create"}, 0))
	require.NotNil(t, probe.lastWriteCtx)
}

// TestOpenAIWSConnLease_ReadMessageWithContextTimeout_PerRead uses delayed fake
// reads with shorter/longer budgets and canceled parent. Outcomes distinguish
// timeout, successful completion payload, and parent cancellation for separate
// calls.
func TestOpenAIWSConnLease_ReadMessageWithContextTimeout_PerRead(t *testing.T) {
	conn := NewWSConn("timeout", 1, &openAIWSBlockingConn{readDelay: 80 * time.Millisecond}, nil, nil, "")
	lease := &WSConnLease{Conn: conn}

	_, err := lease.ReadMessageWithContextTimeout(context.Background(), 20*time.Millisecond)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	payload, err := lease.ReadMessageWithContextTimeout(context.Background(), 150*time.Millisecond)
	require.NoError(t, err)
	require.Contains(t, string(payload), "response.completed")

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = lease.ReadMessageWithContextTimeout(parentCtx, 150*time.Millisecond)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

// TestOpenAIWSConnLease_WriteJSONWithContextTimeout_RespectsParentContext
// cancels a blocked write before its large timeout. The write returns
// context.Canceled within the fixture margin, checking transport parent-context
// propagation.
func TestOpenAIWSConnLease_WriteJSONWithContextTimeout_RespectsParentContext(t *testing.T) {
	conn := NewWSConn("write_timeout_ctx", 1, &openAIWSWriteBlockingConn{}, nil, nil, "")
	lease := &WSConnLease{Conn: conn}

	parentCtx, cancel := context.WithCancel(context.Background())
	// Cancel the parent during the deliberately blocked transport write, checking
	// its propagation before the larger per-operation timeout can expire.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := lease.WriteJSONWithContextTimeout(parentCtx, map[string]any{"type": "response.create"}, 2*time.Minute)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, elapsed, 200*time.Millisecond)
}

// TestOpenAIWSConnLease_PingWithTimeout probes a valid fake socket and a nil
// lease. The first succeeds while the second returns the shared closed-resource
// sentinel.
func TestOpenAIWSConnLease_PingWithTimeout(t *testing.T) {
	conn := NewWSConn("ping_ok", 1, &openAIWSFakeConn{}, nil, nil, "")
	lease := &WSConnLease{Conn: conn}
	require.NoError(t, lease.PingWithTimeout(50*time.Millisecond))

	var nilLease *WSConnLease
	err := nilLease.PingWithTimeout(50 * time.Millisecond)
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
}

// TestOpenAIWSConn_ReadAndWriteCanProceedConcurrently starts a delayed read
// then invokes ping. Ping completes within its margin while the read finishes
// successfully, exercising separate read/write locks through that probe.
func TestOpenAIWSConn_ReadAndWriteCanProceedConcurrently(t *testing.T) {
	conn := NewWSConn("full_duplex", 1, &openAIWSBlockingConn{readDelay: 120 * time.Millisecond}, nil, nil, "")

	// Retain the delayed read outcome independently while timing ping, so read
	// completion can be joined after the direction-lock assertion.
	readDone := make(chan error, 1)
	go func() {
		_, err := conn.readMessageWithContextTimeout(context.Background(), 200*time.Millisecond)
		readDone <- err
	}()

	// Start the delayed read before timing ping, so the fixture measures
	// write-direction independence while that read is expected to hold readMu.
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	err := conn.pingWithTimeout(50 * time.Millisecond)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Less(t, elapsed, 80*time.Millisecond, "写路径不应被读锁长期阻塞")
	require.NoError(t, <-readDone)
}

// TestOpenAIWSConnLease_ReadWriteHelpersAndConnStats exercises context/budget
// wrappers, retained handshake text, activity clocks, and missing connections.
// Successful fake operations update/read metadata and guarded nil wrappers
// return the closed sentinel.
func TestOpenAIWSConnLease_ReadWriteHelpersAndConnStats(t *testing.T) {
	conn := NewWSConn("helper_conn", 1, &openAIWSFakeConn{}, http.Header{
		"X-Test": []string{" value "},
	}, nil, "")
	lease := &WSConnLease{Conn: conn}

	require.NoError(t, lease.WriteJSONContext(context.Background(), map[string]any{"type": "response.create"}))
	payload, err := lease.ReadMessage(100 * time.Millisecond)
	require.NoError(t, err)
	require.Contains(t, string(payload), "response.completed")

	payload, err = lease.ReadMessageContext(context.Background())
	require.NoError(t, err)
	require.Contains(t, string(payload), "response.completed")

	payload, err = conn.readMessageWithTimeout(100 * time.Millisecond)
	require.NoError(t, err)
	require.Contains(t, string(payload), "response.completed")

	require.Equal(t, "value", conn.handshakeHeader(" X-Test "))
	require.NotZero(t, conn.createdAt())
	require.NotZero(t, conn.lastUsedAt())
	require.GreaterOrEqual(t, conn.age(time.Now()), time.Duration(0))
	require.GreaterOrEqual(t, conn.idleDuration(time.Now()), time.Duration(0))
	require.False(t, conn.isLeased())

	_, err = conn.readMessage(context.Background())
	require.NoError(t, err)

	var nilConn *WSConn
	require.ErrorIs(t, nilConn.writeJSONWithTimeout(context.Background(), map[string]any{}, time.Second), errOpenAIWSConnClosed)
	_, err = nilConn.readMessageWithTimeout(10 * time.Millisecond)
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
	_, err = nilConn.readMessageWithContextTimeout(context.Background(), 10*time.Millisecond)
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
}

// TestOpenAIWSConnLease_BasicGetterBranches inspects nil and populated leases
// with stored timings and handshake data. Zero defaults, exact observations,
// protocol-prewarm marking, and tolerated release are checked independently of
// acquisition.
func TestOpenAIWSConnLease_BasicGetterBranches(t *testing.T) {
	var nilLease *WSConnLease
	require.Equal(t, "", nilLease.ConnID())
	require.Equal(t, time.Duration(0), nilLease.QueueWaitDuration())
	require.Equal(t, time.Duration(0), nilLease.ConnPickDuration())
	require.False(t, nilLease.Reused())
	require.Equal(t, "", nilLease.HandshakeHeader("x-test"))
	require.False(t, nilLease.IsPrewarmed())
	nilLease.MarkPrewarmed()
	nilLease.Release()

	conn := NewWSConn("getter_conn", 1, &openAIWSFakeConn{}, http.Header{"X-Test": []string{"ok"}}, nil, "")
	lease := &WSConnLease{
		Conn:      conn,
		queueWait: 3 * time.Millisecond,
		connPick:  4 * time.Millisecond,
		reused:    true,
	}
	require.Equal(t, "getter_conn", lease.ConnID())
	require.Equal(t, 3*time.Millisecond, lease.QueueWaitDuration())
	require.Equal(t, 4*time.Millisecond, lease.ConnPickDuration())
	require.True(t, lease.Reused())
	require.Equal(t, "ok", lease.HandshakeHeader("x-test"))
	require.False(t, lease.IsPrewarmed())
	lease.MarkPrewarmed()
	require.True(t, lease.IsPrewarmed())
	lease.Release()
}

// TestOpenAIWSConn_LeaseAndTimeHelpers_NilAndClosedBranches checks nil
// timestamp/token helpers and one constructed socket. Its token occupancy
// changes on release, and marked closure rejects another acquisition.
func TestOpenAIWSConn_LeaseAndTimeHelpers_NilAndClosedBranches(t *testing.T) {
	var nilConn *WSConn
	nilConn.touch()
	require.Equal(t, time.Time{}, nilConn.createdAt())
	require.Equal(t, time.Time{}, nilConn.lastUsedAt())
	require.Equal(t, time.Duration(0), nilConn.idleDuration(time.Now()))
	require.Equal(t, time.Duration(0), nilConn.age(time.Now()))
	require.False(t, nilConn.isLeased())
	require.False(t, nilConn.isPrewarmed())
	nilConn.markPrewarmed()

	conn := NewWSConn("lease_state", 1, &openAIWSFakeConn{}, nil, nil, "")
	require.True(t, conn.tryAcquire())
	require.True(t, conn.isLeased())
	conn.release()
	require.False(t, conn.isLeased())
	conn.close()
	require.False(t, conn.tryAcquire())
}

// TestOpenAIWSConnLease_ReadWriteNilConnBranches supplies a lease without
// transport association to each read/write wrapper. Every checked operation
// returns the shared closed-resource sentinel before transport work.
func TestOpenAIWSConnLease_ReadWriteNilConnBranches(t *testing.T) {
	lease := &WSConnLease{}
	require.ErrorIs(t, lease.WriteJSON(map[string]any{"k": "v"}, time.Second), errOpenAIWSConnClosed)
	require.ErrorIs(t, lease.WriteJSONContext(context.Background(), map[string]any{"k": "v"}), errOpenAIWSConnClosed)
	_, err := lease.ReadMessage(10 * time.Millisecond)
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
	_, err = lease.ReadMessageContext(context.Background())
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
	_, err = lease.ReadMessageWithContextTimeout(context.Background(), 10*time.Millisecond)
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
}

// TestOpenAIWSConnLease_ReleasedLeaseGuards releases one fake lease twice after
// a successful ping. Subsequent read/write/ping entry points all reject that
// released lease through the same closed-resource error.
func TestOpenAIWSConnLease_ReleasedLeaseGuards(t *testing.T) {
	conn := NewWSConn("released_guard", 1, &openAIWSFakeConn{}, nil, nil, "")
	lease := &WSConnLease{Conn: conn}

	require.NoError(t, lease.PingWithTimeout(50*time.Millisecond))

	lease.Release()
	lease.Release() // idempotent

	require.ErrorIs(t, lease.WriteJSON(map[string]any{"k": "v"}, time.Second), errOpenAIWSConnClosed)
	require.ErrorIs(t, lease.WriteJSONContext(context.Background(), map[string]any{"k": "v"}), errOpenAIWSConnClosed)
	require.ErrorIs(t, lease.WriteJSONWithContextTimeout(context.Background(), map[string]any{"k": "v"}, time.Second), errOpenAIWSConnClosed)

	_, err := lease.ReadMessage(10 * time.Millisecond)
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
	_, err = lease.ReadMessageContext(context.Background())
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
	_, err = lease.ReadMessageWithContextTimeout(context.Background(), 10*time.Millisecond)
	require.ErrorIs(t, err, errOpenAIWSConnClosed)

	require.ErrorIs(t, lease.PingWithTimeout(50*time.Millisecond), errOpenAIWSConnClosed)
}

// TestOpenAIWSConnLease_MarkBrokenAfterRelease_NoEviction releases a recorded
// lease before marking it broken. The provider entry remains, protecting the
// sequential released-lease guard from evicting reusable pool capacity.
func TestOpenAIWSConnLease_MarkBrokenAfterRelease_NoEviction(t *testing.T) {
	conn := NewWSConn("released_markbroken", 7, &openAIWSFakeConn{}, nil, nil, "")
	ap := &openAIWSProviderPool{
		conns: map[string]*WSConn{
			conn.id: conn,
		},
	}
	pool := &WSConnPool{}
	pool.providers.Store(int64(7), ap)

	lease := &WSConnLease{
		pool:       pool,
		ProviderID: 7,
		Conn:       conn,
	}

	lease.Release()
	lease.MarkBroken()

	ap.mu.Lock()
	_, exists := ap.conns[conn.id]
	ap.mu.Unlock()
	require.True(t, exists, "released lease should not evict active pool connection")
}

// TestOpenAIWSConn_AdditionalGuardBranches covers nil/closed/missing
// transports, nonpositive budgets, zero clocks, header cloning, and closing
// lists. Assertions protect selected helper defaults and error outcomes, with
// supplied context cases described literally.
func TestOpenAIWSConn_AdditionalGuardBranches(t *testing.T) {
	var nilConn *WSConn
	require.False(t, nilConn.tryAcquire())
	nilConn.release()
	nilConn.close()
	require.Equal(t, "", nilConn.handshakeHeader("x-test"))

	connClosed := NewWSConn("closed_guard", 1, &openAIWSFakeConn{}, nil, nil, "")
	connClosed.close()
	require.ErrorIs(
		t,
		connClosed.writeJSONWithTimeout(context.Background(), map[string]any{"k": "v"}, time.Second),
		errOpenAIWSConnClosed,
	)
	_, err := connClosed.readMessageWithContextTimeout(context.Background(), time.Second)
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
	require.ErrorIs(t, connClosed.pingWithTimeout(time.Second), errOpenAIWSConnClosed)

	connNoWS := NewWSConn("no_ws", 1, nil, nil, nil, "")
	require.ErrorIs(t, connNoWS.writeJSON(map[string]any{"k": "v"}, context.Background()), errOpenAIWSConnClosed)
	_, err = connNoWS.readMessage(context.Background())
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
	require.ErrorIs(t, connNoWS.pingWithTimeout(time.Second), errOpenAIWSConnClosed)
	require.Equal(t, "", connNoWS.handshakeHeader("x-test"))

	connOK := NewWSConn("ok", 1, &openAIWSFakeConn{}, nil, nil, "")
	require.NoError(t, connOK.writeJSON(map[string]any{"k": "v"}, nil))
	_, err = connOK.readMessageWithContextTimeout(context.Background(), 0)
	require.NoError(t, err)
	require.NoError(t, connOK.pingWithTimeout(0))

	connZero := NewWSConn("zero_ts", 1, &openAIWSFakeConn{}, nil, nil, "")
	connZero.createdAtNano.Store(0)
	connZero.lastUsedNano.Store(0)
	require.True(t, connZero.createdAt().IsZero())
	require.True(t, connZero.lastUsedAt().IsZero())
	require.Equal(t, time.Duration(0), connZero.idleDuration(time.Now()))
	require.Equal(t, time.Duration(0), connZero.age(time.Now()))

	require.Nil(t, CloneWSAcquireRequestPtr(nil))
	copied := cloneHeader(http.Header{
		"X-Empty": []string{},
		"X-Test":  []string{"v1"},
	})
	require.Contains(t, copied, "X-Empty")
	require.Nil(t, copied["X-Empty"])
	require.Equal(t, "v1", copied.Get("X-Test"))

	closeOpenAIWSConns([]*WSConn{nil, connOK})
}

// TestOpenAIWSConnLease_MarkBrokenEvictsConn marks a retained active
// association broken. Its provider entry disappears and the connection token
// cannot be acquired again after disposal.
func TestOpenAIWSConnLease_MarkBrokenEvictsConn(t *testing.T) {
	pool := newStartedWSConnPoolForTest(&WSPoolOptions{})
	providerID := int64(5001)
	conn := NewWSConn("broken_me", providerID, &openAIWSFakeConn{}, nil, nil, "")
	ap := pool.getOrCreateProviderPool(providerID)
	ap.mu.Lock()
	ap.conns[conn.id] = conn
	ap.mu.Unlock()

	lease := &WSConnLease{
		pool:       pool,
		ProviderID: providerID,
		Conn:       conn,
	}
	lease.MarkBroken()

	ap.mu.Lock()
	_, exists := ap.conns[conn.id]
	ap.mu.Unlock()
	require.False(t, exists)
	require.False(t, conn.tryAcquire(), "被标记为 broken 的连接应被关闭")
}
