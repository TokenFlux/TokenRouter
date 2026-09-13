// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	errors "errors"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	openai_compat "github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
	log "log"
	http "net/http"
	sync "sync"
	time "time"
)

type accountTestRun struct {
	ctx     context.Context
	cancel  context.CancelFunc
	headers http.Header
	sink    accountcore.TestEventSink
	mu      sync.Mutex
	values  map[string]any
	err     error
	get     func(string) (any, bool)
	set     func(string, any)
}

func newAccountTestRun(ctx context.Context, headers http.Header, sink accountcore.TestEventSink) *accountTestRun {
	ctx, cancel := context.WithCancel(ctx)
	if headers == nil {
		headers = make(http.Header)
	}
	return &accountTestRun{ctx: ctx, cancel: cancel, headers: headers, sink: sink, values: map[string]any{}}
}
func (r *accountTestRun) GetHeader(name string) string { return r.headers.Get(name) }
func (r *accountTestRun) Get(name string) (any, bool) {
	if r.get != nil {
		return r.get(name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.values[name]
	return v, ok
}
func (r *accountTestRun) Set(name string, v any) {
	if r.set != nil {
		r.set(name, v)
		return
	}
	r.mu.Lock()
	r.values[name] = v
	r.mu.Unlock()
}
func (r *accountTestRun) output(write func() error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		if err := write(); err != nil {
			r.err = err
			r.cancel()
		}
	}
	return r.err
}
func (r *accountTestRun) begin(commit bool) {
	_ = r.output(func() error { return r.sink.Begin(r.ctx, commit) })
}
func (r *accountTestRun) emit(event TestEvent) error {
	return r.output(func() error { return r.sink.Emit(r.ctx, event) })
}
func (r *accountTestRun) result(err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil && !errors.Is(err, r.err) {
		return errors.Join(err, r.err)
	}
	return err
}

// Tester 返回 app 绑定的唯一用例；旧独立构造只使用无共享状态的兼容装配。
func (s *AccountTestService) Tester() *accountcore.TestService {
	if s.tester != nil {
		return s.tester
	}
	return accountcore.NewTestService(s, LegacyAccountTestOptions())
}
func (s *AccountTestService) SetTester(core *accountcore.TestService) { s.tester = core }
func LegacyAccountTestOptions() accountcore.TestOptions {
	return accountcore.TestOptions{Now: time.Now, Error: func(message string) { log.Printf("Account test error: %s", message) }, WriteError: func(err error) { log.Printf("failed to write SSE event: %v", err) }}
}

// LoadTestTarget 只返回受控执行句柄，凭据留在平台 Adapter 内部。
func (s *AccountTestService) LoadTestTarget(ctx context.Context, request accountcore.TestRequest) (accountcore.TestTarget, error) {
	if request.Automatic {
		ctx = withAccountTestUserAgent(ctx, request.UserAgent)
	}
	value, err := s.accountRepo.GetByID(ctx, request.AccountID)
	if err != nil {
		return nil, err
	}
	return &accountTestTarget{source: s, account: value}, nil
}

type accountTestTarget struct {
	source  *AccountTestService
	account *Account
}

func (t *accountTestTarget) Information() accountcore.TestTargetInfo {
	return accountcore.TestTargetInfo{AccountSnapshot: AccountSnapshotView(t.account), APIProtocol: t.account.GetAPIProtocol()}
}
func (t *accountTestTarget) Execute(ctx context.Context, request accountcore.PreparedTestRequest, sink accountcore.TestEventSink) error {
	if request.Automatic {
		ctx = withAccountTestUserAgent(ctx, request.UserAgent)
	}
	if request.Protocol != "" {
		ctx = context.WithValue(ctx, accountTestProtocolContextKey{}, openai_compat.TextProtocol(request.Protocol))
	}
	headers := make(http.Header)
	headers.Set("User-Agent", request.UserAgent)
	headers.Set("originator", request.Originator)
	run := newAccountTestRun(ctx, headers, sink)
	defer run.cancel()
	err := t.execute(run, request)
	return run.result(err)
}
func (t *accountTestTarget) execute(c *accountTestRun, request accountcore.PreparedTestRequest) error {
	s, a := t.source, t.account
	switch request.Route {
	case accountcore.TestRouteCNAdaptive:
		return s.testCNProviderAdaptiveConnectionRun(c, a, request.Model, request.Prompt)
	case accountcore.TestRouteCNResponses:
		return s.testOpenAIAccountConnectionRun(c, a, request.Model, request.Prompt, request.Mode, request.TestType)
	case accountcore.TestRouteCNChat:
		return s.testCNProviderChatCompletionsConnectionRun(c, a, request.Model, request.Prompt)
	case accountcore.TestRouteCNAnthropic:
		return s.testCNProviderAccountConnectionRun(c, a, request.Model, request.Prompt)
	case accountcore.TestRouteOpenAI:
		if err := s.prepareOpenAIAutomaticProbeRun(c, a); err != nil {
			return s.sendTestErrorAndEnd(c, err.Error())
		}
		return s.testOpenAIAccountConnectionRun(c, a, request.Model, request.Prompt, request.Mode, request.TestType)
	case accountcore.TestRouteGemini:
		return s.testGeminiAccountConnectionRun(c, a, request.Model, request.Prompt, request.TestType)
	case accountcore.TestRouteGrok:
		return s.testGrokAccountConnectionRun(c, a, request.Model, request.Prompt, request.TestType)
	case accountcore.TestRouteAntigravity:
		return s.routeAntigravityTestRun(c, a, request.Model, request.Prompt, request.TestType)
	case accountcore.TestRouteQoder:
		return s.testQoderAccountConnectionRun(c, a, request.Model, request.Prompt)
	default:
		return s.testClaudeAccountConnectionRun(c, a, request.Model, request.Prompt)
	}
}
