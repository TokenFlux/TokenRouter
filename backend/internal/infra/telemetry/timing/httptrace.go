package timing

import (
	"context"
	"net/http/httptrace"
	"sync"
	"time"
)

// HTTPTraceSnapshot 是一次出站 HTTP 请求的关键传输阶段快照。
// 所有时间均相对于请求进入网关后的 started_at，便于和下游日志逐条对齐。
type HTTPTraceSnapshot struct {
	StartedAt                 time.Time
	GetConnAt                 time.Time
	GotConnAt                 time.Time
	WroteRequestAt            time.Time
	GotFirstResponseByteAt    time.Time
	GetConnCount              int
	GotConnCount              int
	WroteRequestCount         int
	FirstResponseByteCount    int
	ConnectionReused          bool
	WroteRequestErrorObserved bool
}

type httpTraceContextKey struct{}

type httpTraceState struct {
	mu                        sync.Mutex
	startedAt                 time.Time
	getConnAt                 time.Time
	gotConnAt                 time.Time
	wroteRequestAt            time.Time
	gotFirstResponseByteAt    time.Time
	getConnCount              int
	gotConnCount              int
	wroteRequestCount         int
	firstResponseByteCount    int
	connectionReused          bool
	wroteRequestErrorObserved bool
	upstreamStarted           bool
}

// WithHTTPTrace 为请求安装低开销的 net/http 阶段回调。
// 同一个 context 重复调用时复用已有状态，避免覆盖其它请求链路信息。
func WithHTTPTrace(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(httpTraceContextKey{}).(*httpTraceState); ok {
		return ctx
	}
	state := &httpTraceState{startedAt: time.Now()}
	ctx = context.WithValue(ctx, httpTraceContextKey{}, state)
	return withHTTPTraceClient(ctx, state)
}

// BeginHTTPTrace 标记首次上游 HTTP 尝试并清除此前其它依赖请求的回调。
// 后续重试复用同一状态，从而既不污染阶段数据又能统计尝试次数。
func BeginHTTPTrace(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	state, ok := ctx.Value(httpTraceContextKey{}).(*httpTraceState)
	if !ok || state == nil {
		ctx = WithHTTPTrace(ctx)
		state, _ = ctx.Value(httpTraceContextKey{}).(*httpTraceState)
	}
	if state == nil {
		return ctx
	}
	state.mu.Lock()
	if !state.upstreamStarted {
		state.getConnAt = time.Time{}
		state.gotConnAt = time.Time{}
		state.wroteRequestAt = time.Time{}
		state.gotFirstResponseByteAt = time.Time{}
		state.getConnCount = 0
		state.gotConnCount = 0
		state.wroteRequestCount = 0
		state.firstResponseByteCount = 0
		state.connectionReused = false
		state.wroteRequestErrorObserved = false
		state.upstreamStarted = true
	}
	state.mu.Unlock()
	return ctx
}

func withHTTPTraceClient(ctx context.Context, state *httpTraceState) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GetConn: func(string) {
			state.markGetConn(time.Now())
		},
		GotConn: func(info httptrace.GotConnInfo) {
			state.markGotConn(time.Now(), info.Reused)
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			state.markWroteRequest(time.Now(), info.Err != nil)
		},
		GotFirstResponseByte: func() {
			state.markFirstResponseByte(time.Now())
		},
	})
}

func (s *httpTraceState) markGetConn(at time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.getConnAt.IsZero() {
		s.getConnAt = at
	}
	s.getConnCount++
	s.mu.Unlock()
}

func (s *httpTraceState) markGotConn(at time.Time, reused bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.gotConnAt.IsZero() {
		s.gotConnAt = at
	}
	s.gotConnCount++
	s.connectionReused = s.connectionReused || reused
	s.mu.Unlock()
}

func (s *httpTraceState) markWroteRequest(at time.Time, hasError bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.wroteRequestAt.IsZero() {
		s.wroteRequestAt = at
	}
	s.wroteRequestCount++
	s.wroteRequestErrorObserved = s.wroteRequestErrorObserved || hasError
	s.mu.Unlock()
}

func (s *httpTraceState) markFirstResponseByte(at time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.gotFirstResponseByteAt.IsZero() {
		s.gotFirstResponseByteAt = at
	}
	s.firstResponseByteCount++
	s.mu.Unlock()
}

// HTTPTraceSnapshotFromContext 返回当前请求已观测到的传输阶段。
func HTTPTraceSnapshotFromContext(ctx context.Context) (HTTPTraceSnapshot, bool) {
	if ctx == nil {
		return HTTPTraceSnapshot{}, false
	}
	state, ok := ctx.Value(httpTraceContextKey{}).(*httpTraceState)
	if !ok || state == nil {
		return HTTPTraceSnapshot{}, false
	}
	state.mu.Lock()
	snapshot := HTTPTraceSnapshot{
		StartedAt:                 state.startedAt,
		GetConnAt:                 state.getConnAt,
		GotConnAt:                 state.gotConnAt,
		WroteRequestAt:            state.wroteRequestAt,
		GotFirstResponseByteAt:    state.gotFirstResponseByteAt,
		GetConnCount:              state.getConnCount,
		GotConnCount:              state.gotConnCount,
		WroteRequestCount:         state.wroteRequestCount,
		FirstResponseByteCount:    state.firstResponseByteCount,
		ConnectionReused:          state.connectionReused,
		WroteRequestErrorObserved: state.wroteRequestErrorObserved,
	}
	state.mu.Unlock()
	if snapshot.GetConnAt.IsZero() && snapshot.GotConnAt.IsZero() &&
		snapshot.WroteRequestAt.IsZero() && snapshot.GotFirstResponseByteAt.IsZero() &&
		snapshot.GotConnCount == 0 && snapshot.WroteRequestCount == 0 {
		return snapshot, false
	}
	return snapshot, true
}

// HTTPTraceElapsedMs 将阶段绝对时间转换为相对于 started_at 的毫秒数。
func HTTPTraceElapsedMs(snapshot HTTPTraceSnapshot, at time.Time) (int64, bool) {
	if snapshot.StartedAt.IsZero() || at.IsZero() || at.Before(snapshot.StartedAt) {
		return 0, false
	}
	return at.Sub(snapshot.StartedAt).Milliseconds(), true
}
