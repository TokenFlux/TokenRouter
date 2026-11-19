package ws

import (
	"context"
	"math"
	"time"
)

// prewarm.go computes adaptive provider connection targets and runs background
// WebSocket prewarm batches for WSConnPool. Demand combines leased sockets,
// waiter reservations, utilization headroom, and configured idle bounds while
// clamping targets to each provider's effective connection cap.
//
// Prewarm batches reserve creation slots before spawning under pool shutdown
// coordination and recheck provider generation, hard compatibility keys, and
// remaining idle demand both before and after each dial. Recent dial failures
// and cooldown windows suppress redundant background dials without blocking
// foreground acquisitions.

// recordLastSuccessfulAcquire stores a cloned successful request under provider
// locking when its generation still matches. Missing pools or invalid IDs are
// ignored. The stored provider/profile/callback references remain shared;
// generation rejection prevents a pre-clear request from restoring the prewarm
// target.
func (p *WSConnPool) recordLastSuccessfulAcquire(providerID int64, generation uint64, req WSAcquireRequest) {
	if p == nil || providerID <= 0 {
		return
	}
	ap, ok := p.getProviderPool(providerID)
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

// providerPoolLoadLocked counts occupied tokens and both forced/general waiter
// reservations under ap.mu. Nil state returns zeros. Token occupancy can change
// on release outside that mutex, so the values are current sampled demand
// rather than a persisted admission ledger.
func providerPoolLoadLocked(ap *openAIWSProviderPool) (inflight int, waiters int) {
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
	for _, count := range ap.waiters {
		waiters += count
	}
	return inflight, waiters
}

// ensureTargetIdleAsync starts one eligible prewarm batch for a provider using
// its latest successful request. Missing state/target, active or
// cooled/suppressed prewarm, satisfied demand, and shutdown prevent another
// batch. runtimeMu orders registration against Close while ap.mu protects
// reservations.
//
// The current target includes demand and in-progress dials. Required capacity
// is reserved before launching the batch, and its generation/transport target
// are captured for post-dial checks. Start is not required by this helper; the
// lifecycle owner still joins prewarm through Close. Shared request
// provider/profile/callback data remains stable while the retained batch uses
// it.
func (p *WSConnPool) ensureTargetIdleAsync(providerID int64) {
	if p == nil || providerID <= 0 {
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
	ap, ok := p.getProviderPool(providerID)
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
	if !p.prewarmNeededLocked(ap) {
		return
	}
	effectiveMaxConns := p.maxConnsHardCap()
	if ap.lastAcquire != nil && ap.lastAcquire.Provider != nil {
		effectiveMaxConns = p.effectiveMaxConnsByProvider(ap.lastAcquire.Provider)
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

	// Publish batch activity and reserved slots before spawning; Close excludes
	// further registration through runtimeMu and waits for this complete resource batch.
	p.prewarmWG.Go(func() { ; p.prewarmConns(providerID, req, need, generation) })
}

// targetConnCountLocked derives bounded connection demand from occupied tokens,
// waiter reservations, minimum idle, and target utilization. Callers hold
// ap.mu; nil state or nonpositive maxConns returns zero. No demand selects the
// clamped idle floor.
//
// Demand above one is divided by utilization and rounded up, allowing spare
// capacity. Waiting demand also requests at least one more connection than
// currently exists before hard-cap clamping. These combined observations
// prevent a low utilization target or an idle floor from exceeding the
// effective capacity, while busy/pinned sockets can keep actual maps larger
// until cleanup becomes eligible.
func (p *WSConnPool) targetConnCountLocked(ap *openAIWSProviderPool, maxConns int) int {
	if ap == nil {
		return 0
	}

	if maxConns <= 0 {
		return 0
	}

	minIdle := min(max(p.minIdlePerProvider(), 0), maxConns)

	inflight, waiters := providerPoolLoadLocked(ap)
	utilization := p.targetUtilization()
	demand := inflight + waiters
	if demand <= 0 {
		return minIdle
	}

	target := 1
	if demand > 1 {
		// Round spare-capacity demand upward before clamping, so fractional target
		// calculations still reserve a complete connection for the current queue/load observation.
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

// prewarmNeededLocked checks whether latest-request demand exceeds current
// sockets and the eligible idle count is below the idle ceiling. Unleased,
// unpinned sockets without forced continuation waiters contribute to that idle
// count; general compatibility waiters remain separate demand observations.
// Callers hold ap.mu and supply initialized provider state. Its decision is
// sampled again after dialing because capacity and demand can change meanwhile.
func (p *WSConnPool) prewarmNeededLocked(ap *openAIWSProviderPool) bool {
	if ap.lastAcquire == nil {
		return false
	}
	limit := p.effectiveMaxConnsByProvider(ap.lastAcquire.Provider)
	if len(ap.conns) >= p.targetConnCountLocked(ap, limit) {
		return false
	}
	idle := 0
	for id, conn := range ap.conns {
		if conn != nil && !conn.isLeased() && conn.waiters.Load() == 0 && !p.isConnPinnedLocked(ap, id) {
			idle++
		}
	}
	return idle < p.maxIdlePerProvider()
}

// prewarmConns consumes reserved dial capacity for one captured provider target
// and generation, using the first optional generation or zero. The enclosing
// registration owns joining. Caller reservations include total and stay
// protected by provider locking as each dial finishes.
//
// Target identity and need are checked before and after each dial. Rejected
// late sockets close outside locking; unused reservations return in the
// deferred phase and wake selectors. A changed hard compatibility target schedules
// recomputation using the latest successful request. Failed dials advance
// suppression evidence but this batch can continue its reserved attempts;
// suppression controls later batch admission. Prewarm uses its cancellable
// parent plus dial budget and extra delay.
func (p *WSConnPool) prewarmConns(providerID int64, req WSAcquireRequest, total int, generations ...uint64) {
	generation := uint64(0)
	if len(generations) > 0 {
		generation = generations[0]
	}
	staleTarget := false
	remaining := total
	defer func() {
		if ap, ok := p.getProviderPool(providerID); ok && ap != nil {
			ap.mu.Lock()
			// Return this batch's unused reservations before waking selectors,
			// keeping other registered dials counted while newly available capacity becomes selectable.
			ap.creating = max(0, ap.creating-remaining)
			ap.prewarmActive = false
			ap.signalChangedLocked()
			ap.mu.Unlock()
		}
		if staleTarget {
			// A stale target has lost its retained request ownership; recompute
			// demand after reservations return so the next batch captures current compatible inputs.
			p.ensureTargetIdleAsync(providerID)
		}
	}()

	for remaining > 0 {
		parent := p.prewarmCtx
		if parent == nil {
			parent = context.Background()
		}
		if parent.Err() != nil {
			return
		}
		ap, ok := p.getProviderPool(providerID)
		if !ok || ap == nil {
			return
		}
		ap.mu.Lock()
		if ap.generation != generation || ap.lastAcquire == nil {
			ap.mu.Unlock()
			return
		}
		if !sameOpenAIWSPrewarmTarget(req, *ap.lastAcquire) {
			staleTarget = true
			ap.mu.Unlock()
			return
		}
		if !p.prewarmNeededLocked(ap) {
			ap.mu.Unlock()
			return
		}
		ap.mu.Unlock()
		ctx, cancel := context.WithTimeout(parent, p.dialTimeout()+openAIWSConnPrewarmExtraDelay)
		conn, err := p.dialConn(ctx, req)
		cancel()

		ap.mu.Lock()
		remaining--
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
			return
		}
		if !sameOpenAIWSPrewarmTarget(req, *ap.lastAcquire) {
			staleTarget = true
			ap.signalChangedLocked()
			ap.mu.Unlock()
			conn.close()
			return
		}
		// Dialing runs without provider locking; recheck target demand and
		// cancellation before publication so a late socket cannot exceed changed idle/capacity needs.
		if parent.Err() != nil || !p.prewarmNeededLocked(ap) {
			ap.signalChangedLocked()
			ap.mu.Unlock()
			conn.close()
			return
		}
		ap.conns[conn.id] = conn
		ap.prewarmFails = 0
		ap.prewarmFailAt = time.Time{}
		ap.signalChangedLocked()
		ap.mu.Unlock()
	}
}

// prewarmCooldown delegates the current option snapshot's delay between
// eligible prewarm batches.
func (p *WSConnPool) prewarmCooldown() time.Duration {
	return p.nativeOptions().PrewarmCooldown()
}

// shouldSuppressPrewarmLocked checks recent dial-failure evidence under ap.mu.
// Nil provider state suppresses admission; missing or sufficiently old
// last-failure time clears accumulated evidence. Recent count at the configured
// threshold suppresses a new batch.
//
// The count includes failed acquisition and prewarm dials, and age is measured
// from the latest failure rather than each failure's timestamp. New successful
// dials or provider clearing reset it elsewhere. This helper can mutate stale
// counters during selection, so callers hold the provider lock even when they
// expect only a boolean query.
func (p *WSConnPool) shouldSuppressPrewarmLocked(ap *openAIWSProviderPool, now time.Time) bool {
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

// sameOpenAIWSPrewarmTarget compares the destination/proxy/TLS and selected
// handshake identity used for dial/reuse. Routing hints remain advisory and are
// excluded. Credential-header refresh is also outside that key; caller provider
// invalidation handles credential recovery.
func sameOpenAIWSPrewarmTarget(a, b WSAcquireRequest) bool {
	return wsCompatibilityForRequest(a) == wsCompatibilityForRequest(b)
}
