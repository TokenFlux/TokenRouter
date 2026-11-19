package ws

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPrewarmUpdatesDuringDial blocks the first prewarm dial while changing
// idle/capacity demand and unrelated reservations. Assertions retain busy
// leases, return unused batch slots, and accept or close the late socket
// according to updated need.
func TestPrewarmUpdatesDuringDial(t *testing.T) {
	for _, tc := range []struct {
		name string // prewarm demand scenario label
		// minimum is the idle floor, maximum the idle ceiling, capacity the total
		// cap, busy the retained leased count, otherCreating another dial's reserved
		// slots, and wantIdle the accepted late idle count. Together they change
		// demand while the first captured dial remains held by the test gate.
		minimum, maximum, capacity, busy, otherCreating, wantIdle int
	}{
		{name: "zero_idle", capacity: 4},
		{name: "lower_idle", minimum: 1, maximum: 1, capacity: 4, wantIdle: 1},
		{name: "lower_target", minimum: 1, maximum: 4, capacity: 4, wantIdle: 1},
		{name: "no_idle_target", maximum: 4, capacity: 4},
		{name: "lower_capacity", minimum: 1, maximum: 1, capacity: 1, wantIdle: 1},
		{name: "busy_connections", capacity: 1, busy: 2},
		{name: "busy_with_idle_budget", maximum: 1, capacity: 4, busy: 2, wantIdle: 1},
		{name: "other_dial_reservation", capacity: 4, otherCreating: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial := WSPoolOptions{MaxConnsPerProvider: 4, MinIdlePerProvider: 4, MaxIdlePerProvider: 4, DialTimeoutSeconds: 2}
			pool := NewWSConnPool(&initial)
			t.Cleanup(pool.Close)
			dialer := newOpenAIWSFirstDialBlockingCaptureDialer()
			pool.SetClientDialerForTest(dialer)
			// Normal test completion and cleanup share one release latch, so either can
			// unblock the held first dial before pool shutdown waits.
			var releaseOnce sync.Once
			releaseDial := func() { releaseOnce.Do(func() { close(dialer.releaseFirst) }) }
			t.Cleanup(releaseDial)
			req := WSAcquireRequest{Provider: &WSPoolProvider{ID: 9001, Type: "apikey"}, WSURL: "wss://example.test/responses"}
			ap := pool.getOrCreateProviderPool(9001)
			ap.lastAcquire = CloneWSAcquireRequestPtr(&req)
			var busy []*WSConnLease
			for range tc.busy {
				conn := NewWSConn(pool.nextConnID(9001), 9001, &openAIWSFakeConn{}, nil, nil, "")
				require.True(t, conn.tryAcquire())
				ap.conns[conn.id] = conn
				lease := &WSConnLease{pool: pool, ProviderID: 9001, Conn: conn}
				busy = append(busy, lease)
				t.Cleanup(lease.Release)
			}
			pool.ensureTargetIdleAsync(9001)
			select {
			case <-dialer.firstStarted:
			case <-time.After(time.Second):
				t.Fatal("预热拨号未开始")
			}
			ap.mu.Lock()
			ap.creating += tc.otherCreating
			ap.mu.Unlock()
			next := initial
			next.MinIdlePerProvider, next.MaxIdlePerProvider, next.MaxConnsPerProvider = tc.minimum, tc.maximum, tc.capacity
			require.NoError(t, pool.UpdateOptions(next))
			releaseDial()
			pool.prewarmWG.Wait()
			state, ok := pool.SnapshotProviderState(9001)
			require.True(t, ok)
			require.Equal(t, tc.wantIdle+tc.busy, state.Connections)
			require.Equal(t, tc.busy, state.LeasedConnections)
			require.Equal(t, 1, dialer.DialCount(), "需求降低后应停止本批剩余拨号")
			ap.mu.Lock()
			creating, active := ap.creating, ap.prewarmActive
			ap.creating -= tc.otherCreating
			ap.mu.Unlock()
			require.Equal(t, tc.otherCreating, creating, "应归还本批的剩余名额，保留其他请求的名额")
			require.False(t, active)
			dialer.mu.Lock()
			connections := append([]*openAIWSFakeConn(nil), dialer.connections...)
			dialer.mu.Unlock()
			require.Len(t, connections, 1)
			connections[0].mu.Lock()
			closed := connections[0].closed
			connections[0].mu.Unlock()
			require.Equal(t, tc.wantIdle == 0, closed)
			for _, lease := range busy {
				require.NoError(t, lease.WriteJSON(map[string]any{"type": "response.create"}, time.Second))
				lease.Release()
			}
			// A zero idle limit still permits on-demand dialing; release then reclaims the connection.
			if tc.maximum == 0 {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				lease, err := pool.Acquire(ctx, req)
				require.NoError(t, err)
				lease.Release()
				state, _ = pool.SnapshotProviderState(9001)
				require.Zero(t, state.Connections)
			}
		})
	}
}

// TestWSPoolPrewarmDropsChangedDestination holds old-target prewarming while
// acquiring a new URL/proxy target. The late socket closes, the selected new
// socket stays open, and only that connection remains recorded.
func TestWSPoolPrewarmDropsChangedDestination(t *testing.T) {
	for _, destination := range []string{"url", "proxy"} {
		t.Run(destination, func(t *testing.T) {
			pool, req := newWSReuseTestPool(2, 2)
			defer pool.Close()
			dialer := newOpenAIWSFirstDialBlockingCaptureDialer()
			pool.SetClientDialerForTest(dialer)
			// Gate the old-target first dial across the new acquire, with idempotent
			// release available during cleanup so a failed assertion cannot leave prewarm
			// held.
			var release sync.Once
			unblock := func() { release.Do(func() { close(dialer.releaseFirst) }) }
			defer unblock()
			ap := pool.getOrCreateProviderPool(req.Provider.ID)
			ap.lastAcquire = CloneWSAcquireRequestPtr(&req)
			options := *pool.nativeOptions()
			options.MinIdlePerProvider = 1
			require.NoError(t, pool.UpdateOptions(options))
			<-dialer.firstStarted
			if destination == "url" {
				req.WSURL = "wss://new.example/v1/responses"
			} else {
				req.ProxyURL = "http://proxy.example:8080"
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			lease, err := pool.Acquire(ctx, req)
			require.NoError(t, err)
			defer lease.Release()
			unblock()
			pool.prewarmWG.Wait()
			state, _ := pool.SnapshotProviderState(req.Provider.ID)
			require.Equal(t, 1, state.Connections)
			dialer.mu.Lock()
			connections := append([]*openAIWSFakeConn(nil), dialer.connections...)
			dialer.mu.Unlock()
			require.Len(t, connections, 2)
			for _, conn := range connections {
				conn.mu.Lock()
				closed := conn.closed
				conn.mu.Unlock()
				require.Equal(t, conn != lease.Conn.ws, closed)
			}
		})
	}
}

// TestSameOpenAIWSPrewarmTargetKeepsRoutingHintSoftAndTLSHard changes routing,
// TLS key, and beta values independently. Target equality survives the hint
// change and fails for each hard transport identity change.
func TestSameOpenAIWSPrewarmTargetKeepsRoutingHintSoftAndTLSHard(t *testing.T) {
	baseHeaders := http.Header{
		"X-Codex-Beta-Features":      {"responses_websockets_v2"},
		openAICodexRoutingHintHeader: {"model=gpt-5.6-codex"},
	}
	base := WSAcquireRequest{
		WSURL:         "wss://example.com/v1/responses",
		ProxyURL:      "http://proxy.example.com",
		Headers:       baseHeaders,
		TLSProfileKey: "tls-profile-a",
	}

	hintChanged := CloneWSAcquireRequest(base)
	hintChanged.Headers.Set(openAICodexRoutingHintHeader, "model=gpt-5.6-codex;tier=priority")
	require.True(t, sameOpenAIWSPrewarmTarget(base, hintChanged), "路由提示变化不应使预热拨号失效")

	tlsChanged := CloneWSAcquireRequest(base)
	tlsChanged.TLSProfileKey = "tls-profile-b"
	require.False(t, sameOpenAIWSPrewarmTarget(base, tlsChanged), "TLS 指纹变化必须使预热拨号失效")

	betaChanged := CloneWSAcquireRequest(base)
	betaChanged.Headers.Set("X-Codex-Beta-Features", "remote_compaction_v2")
	require.False(t, sameOpenAIWSPrewarmTarget(base, betaChanged), "beta feature 变化必须使预热拨号失效")
}

// TestOpenAIWSConnPool_TargetConnCountAdaptive combines two occupied tokens and
// two forced waiters under half utilization. The target reaches the cap, then
// returns to the idle floor after those observations are cleared.
func TestOpenAIWSConnPool_TargetConnCountAdaptive(t *testing.T) {
	cfg := &WSPoolOptions{}
	cfg.MaxConnsPerProvider = 6
	cfg.MinIdlePerProvider = 1
	cfg.PoolTargetUtilization = 0.5

	pool := newStartedWSConnPoolForTest(cfg)
	ap := pool.getOrCreateProviderPool(88)

	conn1 := NewWSConn("c1", 88, nil, nil, nil, "")
	conn2 := NewWSConn("c2", 88, nil, nil, nil, "")
	require.True(t, conn1.tryAcquire())
	require.True(t, conn2.tryAcquire())
	conn1.waiters.Store(1)
	conn2.waiters.Store(1)

	ap.conns[conn1.id] = conn1
	ap.conns[conn2.id] = conn2

	target := pool.targetConnCountLocked(ap, pool.maxConnsHardCap())
	require.Equal(t, 6, target, "应按 inflight+waiters 与 target_utilization 自适应扩容到上限")

	conn1.release()
	conn2.release()
	conn1.waiters.Store(0)
	conn2.waiters.Store(0)
	target = pool.targetConnCountLocked(ap, pool.maxConnsHardCap())
	require.Equal(t, 1, target, "低负载时应缩回到最小空闲连接")
}

// TestOpenAIWSConnPool_TargetConnCountMinIdleZero uses an idle floor of zero
// with no recorded demand. Target calculation returns zero, protecting the
// configured empty-pool case.
func TestOpenAIWSConnPool_TargetConnCountMinIdleZero(t *testing.T) {
	cfg := &WSPoolOptions{}
	cfg.MaxConnsPerProvider = 4
	cfg.MinIdlePerProvider = 0
	cfg.PoolTargetUtilization = 0.8

	pool := newStartedWSConnPoolForTest(cfg)
	ap := pool.getOrCreateProviderPool(66)

	target := pool.targetConnCountLocked(ap, pool.maxConnsHardCap())
	require.Equal(t, 0, target, "min_idle=0 且无负载时应允许缩容到 0")
}

// TestOpenAIWSConnPool_EnsureTargetIdleAsync installs a retained provider
// request and an idle target of two. The pool eventually records both sockets
// and counts at least two reserved scale-up slots.
func TestOpenAIWSConnPool_EnsureTargetIdleAsync(t *testing.T) {
	cfg := &WSPoolOptions{}
	cfg.MaxConnsPerProvider = 4
	cfg.MinIdlePerProvider = 2
	cfg.MaxIdlePerProvider = cfg.MinIdlePerProvider
	cfg.PoolTargetUtilization = 0.8
	cfg.DialTimeoutSeconds = 1

	pool := newStartedWSConnPoolForTest(cfg)
	pool.SetClientDialerForTest(&openAIWSFakeDialer{})

	providerID := int64(77)
	provider := &WSPoolProvider{ID: providerID, Type: "apikey"}
	ap := pool.getOrCreateProviderPool(providerID)
	ap.mu.Lock()
	ap.lastAcquire = &WSAcquireRequest{
		Provider: provider,
		WSURL:    "wss://example.com/v1/responses",
	}
	ap.mu.Unlock()

	pool.ensureTargetIdleAsync(providerID)

	require.Eventually(t, func() bool {
		ap, ok := pool.getProviderPool(providerID)
		if !ok || ap == nil {
			return false
		}
		ap.mu.Lock()
		defer ap.mu.Unlock()
		return len(ap.conns) >= 2
	}, 2*time.Second, 20*time.Millisecond)

	metrics := pool.SnapshotMetrics()
	require.GreaterOrEqual(t, metrics.ScaleUpTotal, int64(2))
}

// TestOpenAIWSConnPool_EnsureTargetIdleAsyncCooldown finishes a first batch,
// removes one socket, and probes the cooldown window. Dial count stays fixed
// before expiry and rises after a later eligible request.
func TestOpenAIWSConnPool_EnsureTargetIdleAsyncCooldown(t *testing.T) {
	cfg := &WSPoolOptions{}
	cfg.MaxConnsPerProvider = 4
	cfg.MinIdlePerProvider = 2
	cfg.MaxIdlePerProvider = cfg.MinIdlePerProvider
	cfg.PoolTargetUtilization = 0.8
	cfg.DialTimeoutSeconds = 1
	cfg.PrewarmCooldownMS = 500

	pool := newStartedWSConnPoolForTest(cfg)
	dialer := &openAIWSCountingDialer{}
	pool.SetClientDialerForTest(dialer)

	providerID := int64(178)
	provider := &WSPoolProvider{ID: providerID, Type: "apikey"}
	ap := pool.getOrCreateProviderPool(providerID)
	ap.mu.Lock()
	ap.lastAcquire = &WSAcquireRequest{
		Provider: provider,
		WSURL:    "wss://example.com/v1/responses",
	}
	ap.mu.Unlock()

	pool.ensureTargetIdleAsync(providerID)
	require.Eventually(t, func() bool {
		ap, ok := pool.getProviderPool(providerID)
		if !ok || ap == nil {
			return false
		}
		ap.mu.Lock()
		defer ap.mu.Unlock()
		return len(ap.conns) >= 2 && !ap.prewarmActive
	}, 2*time.Second, 20*time.Millisecond)
	firstDialCount := dialer.DialCount()
	require.GreaterOrEqual(t, firstDialCount, 2)

	// Remove an idle entry after the first batch settles, producing new
	// demand while retaining its cooldown timestamp for the next admission check.
	ap, ok := pool.getProviderPool(providerID)
	require.True(t, ok)
	require.NotNil(t, ap)
	ap.mu.Lock()
	for id := range ap.conns {
		delete(ap.conns, id)
		break
	}
	ap.mu.Unlock()

	pool.ensureTargetIdleAsync(providerID)
	time.Sleep(120 * time.Millisecond)
	require.Equal(t, firstDialCount, dialer.DialCount(), "cooldown 窗口内不应再次触发预热")

	time.Sleep(450 * time.Millisecond)
	pool.ensureTargetIdleAsync(providerID)
	require.Eventually(t, func() bool {
		return dialer.DialCount() > firstDialCount
	}, 2*time.Second, 20*time.Millisecond)
}

// TestOpenAIWSConnPool_EnsureTargetIdleAsyncFailureSuppress uses an
// always-failing dialer for two one-slot batches. The next admission attempt
// stays suppressed and the observed dial count remains two.
func TestOpenAIWSConnPool_EnsureTargetIdleAsyncFailureSuppress(t *testing.T) {
	cfg := &WSPoolOptions{}
	cfg.MaxConnsPerProvider = 2
	cfg.MinIdlePerProvider = 1
	cfg.MaxIdlePerProvider = cfg.MinIdlePerProvider
	cfg.PoolTargetUtilization = 0.8
	cfg.DialTimeoutSeconds = 1
	cfg.PrewarmCooldownMS = 0

	pool := newStartedWSConnPoolForTest(cfg)
	dialer := &openAIWSAlwaysFailDialer{}
	pool.SetClientDialerForTest(dialer)

	providerID := int64(279)
	provider := &WSPoolProvider{ID: providerID, Type: "apikey"}
	ap := pool.getOrCreateProviderPool(providerID)
	ap.mu.Lock()
	ap.lastAcquire = &WSAcquireRequest{
		Provider: provider,
		WSURL:    "wss://example.com/v1/responses",
	}
	ap.mu.Unlock()

	pool.ensureTargetIdleAsync(providerID)
	require.Eventually(t, func() bool {
		ap, ok := pool.getProviderPool(providerID)
		if !ok || ap == nil {
			return false
		}
		ap.mu.Lock()
		defer ap.mu.Unlock()
		return !ap.prewarmActive
	}, 2*time.Second, 20*time.Millisecond)

	pool.ensureTargetIdleAsync(providerID)
	require.Eventually(t, func() bool {
		ap, ok := pool.getProviderPool(providerID)
		if !ok || ap == nil {
			return false
		}
		ap.mu.Lock()
		defer ap.mu.Unlock()
		return !ap.prewarmActive
	}, 2*time.Second, 20*time.Millisecond)
	require.Equal(t, 2, dialer.DialCount())

	// Two completed failed batches leave recent suppression evidence; another
	// admission call must not increase the dial count while that failure state remains current.
	pool.ensureTargetIdleAsync(providerID)
	time.Sleep(120 * time.Millisecond)
	require.Equal(t, 2, dialer.DialCount())
}

// TestOpenAIWSConnPool_PrewarmHintChangeDoesNotInvalidateHealthyDial changes
// only routing preference during a held prewarm. One dial remains admitted with
// its captured old affinity, protecting advisory hints from becoming hard
// compatibility.
func TestOpenAIWSConnPool_PrewarmHintChangeDoesNotInvalidateHealthyDial(t *testing.T) {
	cfg := &WSPoolOptions{}
	cfg.MaxConnsPerProvider = 1
	cfg.MinIdlePerProvider = 1
	cfg.MaxIdlePerProvider = 1
	cfg.DialTimeoutSeconds = 2

	pool := newStartedWSConnPoolForTest(cfg)
	dialer := newOpenAIWSFirstDialBlockingCaptureDialer()
	pool.SetClientDialerForTest(dialer)
	provider := &WSPoolProvider{ID: 992, Type: "oauth"}
	oldHeaders := make(http.Header)
	oldHeaders.Set(openAICodexRoutingHintHeader, "model=gpt-5.6-codex")
	newHeaders := make(http.Header)
	newHeaders.Set(openAICodexRoutingHintHeader, "model=gpt-5.6-codex;tier=priority")
	ap := pool.getOrCreateProviderPool(provider.ID)
	ap.mu.Lock()
	ap.lastAcquire = &WSAcquireRequest{
		Provider: provider,
		WSURL:    "wss://example.com/v1/responses",
		Headers:  oldHeaders,
	}
	ap.mu.Unlock()

	// Hold first prewarm after capturing its hint, then replace only the retained
	// request preference to test compatibility admission of that late result.
	pool.ensureTargetIdleAsync(provider.ID)
	<-dialer.firstStarted

	// A routing hint change during prewarming still allows an otherwise compatible
	// connection into the pool.
	ap.mu.Lock()
	ap.lastAcquire = &WSAcquireRequest{
		Provider: provider,
		WSURL:    "wss://example.com/v1/responses",
		Headers:  newHeaders,
	}
	ap.mu.Unlock()
	close(dialer.releaseFirst)

	require.Eventually(t, func() bool {
		ap.mu.Lock()
		defer ap.mu.Unlock()
		if ap.prewarmActive || len(ap.conns) != 1 {
			return false
		}
		for _, conn := range ap.conns {
			return conn != nil && conn.routingAffinity == "model=gpt-5.6-codex"
		}
		return false
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, 1, dialer.DialCount(), "routing-hint-only changes must not turn advisory metadata into hard reconnects")
}

// TestOpenAIWSConnPool_TargetConnCountAndPrewarmBranches supplies invalid
// capacity, bounded idle floor, waiters, absent providers, and failed prewarm.
// Assertions protect target limits and failure evidence after a reserved fake
// dial fails.
func TestOpenAIWSConnPool_TargetConnCountAndPrewarmBranches(t *testing.T) {
	cfg := &WSPoolOptions{}
	cfg.MaxConnsPerProvider = 1
	pool := newStartedWSConnPoolForTest(cfg)

	require.Equal(t, 0, pool.targetConnCountLocked(nil, 1))
	ap := &openAIWSProviderPool{conns: map[string]*WSConn{}}
	require.Equal(t, 0, pool.targetConnCountLocked(ap, 0))

	cfg.MinIdlePerProvider = 3
	require.NoError(t, pool.UpdateOptions(*cfg))
	require.Equal(t, 1, pool.targetConnCountLocked(ap, 1), "minIdle 应被 maxConns 截断")

	// Waiters require a target of at least one more than the current connection count.
	cfg.MinIdlePerProvider = 0
	cfg.PoolTargetUtilization = 0.9
	require.NoError(t, pool.UpdateOptions(*cfg))
	busy := NewWSConn("busy_target", 2, &openAIWSFakeConn{}, nil, nil, "")
	require.True(t, busy.tryAcquire())
	busy.waiters.Store(1)
	ap.conns[busy.id] = busy
	target := pool.targetConnCountLocked(ap, 4)
	require.GreaterOrEqual(t, target, len(ap.conns)+1)

	// Skip prewarm dialing when the provider pool is missing.
	req := WSAcquireRequest{
		Provider: &WSPoolProvider{ID: 999, Type: "apikey"},
		WSURL:    "wss://example.com/v1/responses",
	}
	pool.prewarmConns(999, req, 1)

	// Failed prewarm dials increment the failure count.
	providerID := int64(1000)
	cfg.MinIdlePerProvider = 1
	cfg.MaxIdlePerProvider = 1
	failPool := newStartedWSConnPoolForTest(cfg)
	failPool.SetClientDialerForTest(&openAIWSAlwaysFailDialer{})
	apFail := failPool.getOrCreateProviderPool(providerID)
	apFail.mu.Lock()
	apFail.creating = 1
	req.Provider.ID = providerID
	apFail.lastAcquire = CloneWSAcquireRequestPtr(&req)
	apFail.mu.Unlock()
	failPool.prewarmConns(providerID, req, 1)
	apFail.mu.Lock()
	require.GreaterOrEqual(t, apFail.prewarmFails, 1)
	apFail.mu.Unlock()
}

// TestOpenAIWSConnPoolHeadersFactoryRunsAtDialAndStalePrewarmIsDiscarded
// generates authorization inside a direct prewarm dial. Clearing advances
// generation and removes target/sockets, then an old-generation batch cannot
// reenter provider state.
func TestOpenAIWSConnPoolHeadersFactoryRunsAtDialAndStalePrewarmIsDiscarded(t *testing.T) {
	cfg := &WSPoolOptions{}
	cfg.MaxConnsPerProvider = 1
	cfg.MinIdlePerProvider = 1
	cfg.MaxIdlePerProvider = 1
	pool := newStartedWSConnPoolForTest(cfg)
	defer pool.Close()
	pool.SetClientDialerForTest(&openAIWSFakeDialer{})

	providerID := int64(22)
	ap := pool.getOrCreateProviderPool(providerID)
	factoryCalls := 0
	latestHeader := ""
	req := WSAcquireRequest{
		Provider: &WSPoolProvider{ID: providerID, Type: "oauth"},
		WSURL:    "wss://example.com/v1/responses",
		HeadersFactory: func(_ context.Context, headers http.Header) (http.Header, error) {
			factoryCalls++
			// Generate one character of fake authorization identity for the first
			// asserted dial, keeping this fixture focused on factory invocation rather
			// than real signing.
			latestHeader = "AgentAssertion dial-" + string(rune('0'+factoryCalls))
			if headers == nil {
				headers = make(http.Header)
			}
			headers.Set("Authorization", latestHeader)
			return headers, nil
		},
	}
	ap.mu.Lock()
	ap.lastAcquire = &req
	generation := ap.generation
	ap.mu.Unlock()

	pool.prewarmConns(providerID, req, 1, generation)
	require.Equal(t, 1, factoryCalls, "prewarm must generate authorization inside the actual dial")
	require.Equal(t, "AgentAssertion dial-1", latestHeader)

	pool.ClearProvider(providerID)
	ap.mu.Lock()
	require.Empty(t, ap.conns, "credential recovery must remove pooled connections")
	require.Nil(t, ap.lastAcquire, "credential recovery must discard delayed acquire state")
	require.Equal(t, generation+1, ap.generation)
	ap.mu.Unlock()

	// Reuse the captured old generation after clearing its target, checking
	// that delayed prewarm work cannot publish sockets into the replacement provider state.
	pool.prewarmConns(providerID, req, 1, generation)
	ap.mu.Lock()
	require.Empty(t, ap.conns)
	ap.mu.Unlock()
}
