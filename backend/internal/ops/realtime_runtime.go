// 实时运行时保持原采样、容量和三十秒空闲停止语义。
package ops

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type opsWSQPSCache struct {
	refreshInterval    time.Duration
	requestCountWindow time.Duration

	lastUpdatedUnixNano atomic.Int64
	payload             atomic.Value // []byte

	opsService *OpsService
	cancel     context.CancelFunc
	done       chan struct{}

	mu      sync.Mutex
	running bool
	closed  bool
}

func (c *opsWSQPSCache) start(opsService *OpsService) {
	if c == nil || opsService == nil {
		return
	}

	for {
		c.mu.Lock()
		if c.running || c.closed {
			c.mu.Unlock()
			return
		}

		// If a previous refresh loop is currently stopping, wait for it to fully exit.
		done := c.done
		if done != nil {
			c.mu.Unlock()
			<-done

			c.mu.Lock()
			if c.done == done && !c.running {
				c.done = nil
			}
			c.mu.Unlock()
			continue
		}

		c.opsService = opsService
		ctx, cancel := context.WithCancel(context.Background())
		c.cancel = cancel
		c.done = make(chan struct{})
		done = c.done
		c.running = true
		c.mu.Unlock()

		go func() {
			defer close(done)
			c.refreshLoop(ctx)
		}()
		return
	}
}

// Stop stops the background refresh loop.
// It is safe to call multiple times.
func (c *opsWSQPSCache) Stop() {
	if c == nil {
		return
	}

	c.mu.Lock()
	if !c.running {
		done := c.done
		c.mu.Unlock()
		if done != nil {
			<-done
		}
		return
	}
	cancel := c.cancel
	c.cancel = nil
	c.running = false
	c.opsService = nil
	done := c.done
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	c.mu.Lock()
	if c.done == done && !c.running {
		c.done = nil
	}
	c.mu.Unlock()
}
func (c *opsWSQPSCache) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(c.refreshInterval)
	defer ticker.Stop()

	c.refresh(ctx)
	for {
		select {
		case <-ticker.C:
			c.refresh(ctx)
		case <-ctx.Done():
			return
		}
	}
}
func (c *opsWSQPSCache) refresh(parentCtx context.Context) {
	if c == nil {
		return
	}

	c.mu.Lock()
	opsService := c.opsService
	c.mu.Unlock()
	if opsService == nil {
		return
	}

	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	now := time.Now().UTC()
	stats, err := opsService.GetWindowStats(ctx, now.Add(-c.requestCountWindow), now)
	if err != nil || stats == nil {
		if err != nil {
			opsService.report("[OpsWS] refresh: get window stats failed: %v", err)
		}
		return
	}

	requestCount := stats.SuccessCount + stats.ErrorCountTotal
	qps := 0.0
	tps := 0.0
	if c.requestCountWindow > 0 {
		seconds := c.requestCountWindow.Seconds()
		qps = RoundTo1DP(float64(requestCount) / seconds)
		tps = RoundTo1DP(float64(stats.TokenConsumed) / seconds)
	}

	payload := map[string]any{
		"type":      "qps_update",
		"timestamp": now.Format(time.RFC3339),
		"data": map[string]any{
			"qps":           qps,
			"tps":           tps,
			"request_count": requestCount,
		},
	}

	msg, err := json.Marshal(payload)
	if err != nil {
		opsService.report("[OpsWS] refresh: marshal payload failed: %v", err)
		return
	}

	c.payload.Store(msg)
	c.lastUpdatedUnixNano.Store(now.UnixNano())
}
func (c *opsWSQPSCache) getPayload() []byte {
	if c == nil {
		return nil
	}
	if cached, ok := c.payload.Load().([]byte); ok && cached != nil {
		return append([]byte(nil), cached...)
	}
	return nil
}

// RealtimeRuntime 持有按需采样和订阅统计，HTTP 只负责握手与帧。
type RealtimeRuntime struct {
	cache              *opsWSQPSCache
	wsConnCount        atomic.Int32
	wsConnCountByIPMu  sync.Mutex
	wsConnCountByIP    map[string]int32
	qpsWSIdleStopMu    sync.Mutex
	qpsWSIdleStopTimer *time.Timer
	qpsWSIdleStopWG    sync.WaitGroup
	qpsWSRuntimeClosed bool
	idleDelay          time.Duration
}

func NewRealtimeRuntime() *RealtimeRuntime {
	return &RealtimeRuntime{cache: &opsWSQPSCache{refreshInterval: 5 * time.Second, requestCountWindow: time.Minute}, wsConnCountByIP: make(map[string]int32), idleDelay: 30 * time.Second}
}
func (r *RealtimeRuntime) Prepare(s *OpsService) { r.cancelQPSWSIdleStop(); r.cache.start(s) }
func (r *RealtimeRuntime) Total() int32          { return r.wsConnCount.Load() }
func (r *RealtimeRuntime) ReleaseTotal() {
	if r.wsConnCount.Add(-1) == 0 {
		r.scheduleQPSWSIdleStop()
	}
}
func (r *RealtimeRuntime) Payload() []byte { return r.cache.getPayload() }
func (s *OpsService) Realtime() *RealtimeRuntime {
	if s == nil {
		return nil
	}
	s.realtimeOnce.Do(func() { s.realtime = NewRealtimeRuntime() })
	return s.realtime
}
func (r *RealtimeRuntime) cancelQPSWSIdleStop() {
	r.qpsWSIdleStopMu.Lock()
	if r.qpsWSIdleStopTimer != nil {
		if r.qpsWSIdleStopTimer.Stop() {
			r.qpsWSIdleStopWG.Done()
		}
		r.qpsWSIdleStopTimer = nil
	}
	r.qpsWSIdleStopMu.Unlock()
}
func (r *RealtimeRuntime) scheduleQPSWSIdleStop() {
	r.qpsWSIdleStopMu.Lock()
	if r.qpsWSIdleStopTimer != nil || r.qpsWSRuntimeClosed {
		r.qpsWSIdleStopMu.Unlock()
		return
	}
	r.qpsWSIdleStopWG.Add(1)
	var timer *time.Timer
	timer = time.AfterFunc(r.idleDelay, func() {
		defer r.qpsWSIdleStopWG.Done()
		// Only stop if truly idle at fire time.
		if r.wsConnCount.Load() == 0 {
			r.cache.Stop()
		}
		r.qpsWSIdleStopMu.Lock()
		if r.qpsWSIdleStopTimer == timer {
			r.qpsWSIdleStopTimer = nil
		}
		r.qpsWSIdleStopMu.Unlock()
	})
	r.qpsWSIdleStopTimer = timer
	r.qpsWSIdleStopMu.Unlock()
}
func (r *RealtimeRuntime) AcquireTotal(limit int32) bool {
	if limit <= 0 {
		return true
	}
	for {
		current := r.wsConnCount.Load()
		if current >= limit {
			return false
		}
		if r.wsConnCount.CompareAndSwap(current, current+1) {
			return true
		}
	}
}
func (r *RealtimeRuntime) AcquireIP(clientIP string, limit int32) bool {
	if strings.TrimSpace(clientIP) == "" || limit <= 0 {
		return true
	}
	r.wsConnCountByIPMu.Lock()
	defer r.wsConnCountByIPMu.Unlock()
	current := r.wsConnCountByIP[clientIP]
	if current >= limit {
		return false
	}
	r.wsConnCountByIP[clientIP] = current + 1
	return true
}
func (r *RealtimeRuntime) ReleaseIP(clientIP string) {
	if strings.TrimSpace(clientIP) == "" {
		return
	}
	r.wsConnCountByIPMu.Lock()
	defer r.wsConnCountByIPMu.Unlock()
	current, ok := r.wsConnCountByIP[clientIP]
	if !ok {
		return
	}
	if current <= 1 {
		delete(r.wsConnCountByIP, clientIP)
		return
	}
	r.wsConnCountByIP[clientIP] = current - 1
}

// StopOpsWSRuntime 由组合根在 HTTP 请求完成后取消延迟停机并等待按需刷新退出。
func (r *RealtimeRuntime) Stop() {
	r.qpsWSIdleStopMu.Lock()
	r.qpsWSRuntimeClosed = true
	if r.qpsWSIdleStopTimer != nil {
		if r.qpsWSIdleStopTimer.Stop() {
			r.qpsWSIdleStopWG.Done()
		}
		r.qpsWSIdleStopTimer = nil
	}
	r.qpsWSIdleStopMu.Unlock()
	r.qpsWSIdleStopWG.Wait()
	r.cache.mu.Lock()
	r.cache.closed = true
	r.cache.mu.Unlock()
	r.cache.Stop()
}
