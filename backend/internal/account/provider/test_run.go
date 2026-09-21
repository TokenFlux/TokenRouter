package provider

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
)

// TestRun 持有一次平台测试的输出和显式状态，不使用 HTTP Context 的通用键值容器。
type TestRun struct {
	Context context.Context
	Cancel  context.CancelFunc
	Headers http.Header
	// 本次执行状态只由同步测试链使用，不再放入 context 的隐式键。
	RequestedProtocol  account.TextProtocol
	TaskRecoveryTried  bool
	Automatic          bool
	sink               account.TestEventSink
	mu                 sync.Mutex
	err                error
	automaticRoute     egress.TLSFingerprintRouterMatchResult
	hasAutomaticRoute  bool
	suppressCompletion bool
}

func NewTestRun(ctx context.Context, headers http.Header, sink account.TestEventSink) *TestRun {
	ctx, cancel := context.WithCancel(ctx)
	if headers == nil {
		headers = make(http.Header)
	}
	return &TestRun{Context: ctx, Cancel: cancel, Headers: headers, sink: sink}
}

func (r *TestRun) GetHeader(name string) string { return r.Headers.Get(name) }

func (r *TestRun) SetAutomaticRoute(value egress.TLSFingerprintRouterMatchResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.automaticRoute = value
	r.hasAutomaticRoute = true
}

func (r *TestRun) AutomaticRoute() (egress.TLSFingerprintRouterMatchResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.automaticRoute, r.hasAutomaticRoute
}

func (r *TestRun) SetSuppressCompletion(value bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.suppressCompletion = value
}

func (r *TestRun) SuppressCompletion() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.suppressCompletion
}

// 输出失败只登记首次错误并取消本次执行，后续写入不再访问下游。
func (r *TestRun) output(write func() error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		if err := write(); err != nil {
			r.err = err
			r.Cancel()
		}
	}
	return r.err
}

func (r *TestRun) Begin(commit bool) {
	_ = r.output(func() error { return r.sink.Begin(r.Context, commit) })
}

func (r *TestRun) Emit(event account.TestEvent) error {
	return r.output(func() error { return r.sink.Emit(r.Context, event) })
}

func (r *TestRun) Result(err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil && !errors.Is(err, r.err) {
		return errors.Join(err, r.err)
	}
	return err
}
