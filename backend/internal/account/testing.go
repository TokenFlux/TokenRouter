// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// TestEvent represents a SSE event for account testing
type TestEvent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Model    string `json:"model,omitempty"`
	Status   string `json:"status,omitempty"`
	Code     string `json:"code,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Data     any    `json:"data,omitempty"`
	Success  bool   `json:"success,omitempty"`
	Error    string `json:"error,omitempty"`
}

// NormalizeAccountTestType 统一管理端传入的测试类型；空值由调用方视为旧版请求。
func NormalizeAccountTestType(testType string) string {
	switch strings.ToLower(strings.TrimSpace(testType)) {
	case AccountTestTypeImage:
		return AccountTestTypeImage
	case AccountTestTypeText:
		return AccountTestTypeText
	default:
		return AccountTestTypeText
	}
}

// AccountTestTypeFromArgs 返回类型以及是否由调用方明确指定。
// 旧版调用不传类型时保留按模型名兼容判断，新的管理端请求始终传入 text/image。
func AccountTestTypeFromArgs(testTypes ...string) (string, bool) {
	if len(testTypes) == 0 || strings.TrimSpace(testTypes[0]) == "" {
		return AccountTestTypeText, false
	}
	return NormalizeAccountTestType(testTypes[0]), true
}

// ResolveAccountTestModeAndType 兼容少量旧调用把 text/image 放在 mode 字段的情况。
func ResolveAccountTestModeAndType(mode string, testTypes ...string) (string, string, bool) {
	// 兼容新调用方把参数顺序写成 testType、mode 的形式。
	if len(testTypes) > 0 {
		rawMode := strings.ToLower(strings.TrimSpace(mode))
		rawTypeOrMode := strings.ToLower(strings.TrimSpace(testTypes[0]))
		if (rawMode == AccountTestTypeText || rawMode == AccountTestTypeImage) &&
			(rawTypeOrMode == AccountTestModeDefault || rawTypeOrMode == AccountTestModeCompact || rawTypeOrMode == AccountTestModeLegacyCompact) {
			return NormalizeAccountTestMode(testTypes[0]), NormalizeAccountTestType(mode), true
		}
	}
	testType, explicit := AccountTestTypeFromArgs(testTypes...)
	normalizedMode := NormalizeAccountTestMode(mode)
	if !explicit {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case AccountTestTypeText, AccountTestTypeImage:
			return AccountTestModeDefault, NormalizeAccountTestType(mode), true
		}
	}
	if !explicit {
		testType = ""
	}
	return normalizedMode, testType, explicit
}

func NormalizeAccountTestMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case AccountTestModeCompact:
		return AccountTestModeCompact
	case AccountTestModeLegacyCompact:
		return AccountTestModeLegacyCompact
	default:
		return AccountTestModeDefault
	}
}

const (
	AccountTestTypeText          = "text"
	AccountTestTypeImage         = "image"
	AccountTestModeDefault       = "default"
	AccountTestModeCompact       = "compact"
	AccountTestModeLegacyCompact = "legacy_compact"
)

// TestRequest 表达测试意图；Type 为 nil 时保留历史模型名推断，客户端元数据不包含凭据。
type TestRequest struct {
	AccountID             int64
	Model, Prompt, Mode   string
	Type                  *string
	Protocol              string
	UserAgent, Originator string
	Automatic             bool
}
type TestRoute uint8

const (
	TestRouteClaude TestRoute = iota
	TestRouteCNAdaptive
	TestRouteCNResponses
	TestRouteCNChat
	TestRouteCNAnthropic
	TestRouteOpenAI
	TestRouteGemini
	TestRouteGrok
	TestRouteAntigravity
	TestRouteQoder
)

type PreparedTestRequest struct {
	TestRequest
	TestType     string
	ExplicitType bool
	Route        TestRoute
}
type TestTargetInfo struct {
	AccountSnapshot
	APIProtocol string
}

// TestTarget 是受控执行句柄，只公开安全快照，不向调用者返回完整凭据。
type TestTarget interface {
	Information() TestTargetInfo
	Execute(context.Context, PreparedTestRequest, TestEventSink) error
}
type TestLoader interface {
	LoadTestTarget(context.Context, TestRequest) (TestTarget, error)
}

// Begin 的 commit 区分仅准备元数据与立即提交；HTTP Header/Flush 的实现由 Adapter 拥有。
type TestEventSink interface {
	Begin(context.Context, bool) error
	Emit(context.Context, TestEvent) error
}
type TestOptions struct {
	Now        func() time.Time
	Error      func(string)
	WriteError func(error)
}
type TestService struct {
	loader  TestLoader
	options TestOptions
}

func NewTestService(loader TestLoader, options TestOptions) *TestService {
	return &TestService{loader: loader, options: options}
}
func (s *TestService) Test(ctx context.Context, request TestRequest, sink TestEventSink) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	guarded := &testGuardedSink{next: sink, cancel: cancel}
	err := s.execute(runCtx, request, guarded)
	if writeErr := guarded.failure(); writeErr != nil && !errors.Is(err, writeErr) {
		return errors.Join(err, writeErr)
	}
	return err
}
func (s *TestService) execute(ctx context.Context, request TestRequest, sink TestEventSink) error {
	if request.Protocol != "" && request.Protocol != "responses" && request.Protocol != "chat_completions" {
		return s.fail(ctx, sink, "Invalid test protocol")
	}
	var types []string
	if request.Type != nil {
		types = []string{*request.Type}
	}
	mode, kind, explicit := ResolveAccountTestModeAndType(request.Mode, types...)
	if explicit && kind == AccountTestTypeImage {
		mode = AccountTestModeDefault
	}
	target, err := s.loader.LoadTestTarget(ctx, request)
	if err != nil {
		return s.fail(ctx, sink, "Account not found")
	}
	info := target.Information()
	if explicit && kind == AccountTestTypeImage && info.Platform != PlatformOpenAI && info.Platform != PlatformGemini && info.Platform != PlatformGrok && (info.Platform != PlatformAntigravity || info.Type != AccountTypeAPIKey) {
		return s.fail(ctx, sink, fmt.Sprintf("Image tests are not supported for platform %s", info.Platform))
	}
	prepared := PreparedTestRequest{TestRequest: request, TestType: kind, ExplicitType: explicit}
	prepared.Mode = mode
	switch info.Platform {
	case PlatformKimi, PlatformZhipu, PlatformDeepseek:
		switch info.APIProtocol {
		case APIProtocolAdaptive:
			prepared.Route = TestRouteCNAdaptive
		case APIProtocolResponses:
			prepared.Route = TestRouteCNResponses
		case APIProtocolChatCompletions:
			prepared.Route = TestRouteCNChat
		default:
			prepared.Route = TestRouteCNAnthropic
		}
	case PlatformOpenAI:
		prepared.Route = TestRouteOpenAI
	case PlatformGemini:
		prepared.Route = TestRouteGemini
	case PlatformGrok:
		prepared.Route = TestRouteGrok
	case PlatformAntigravity:
		prepared.Route = TestRouteAntigravity
	case PlatformQoder:
		prepared.Route = TestRouteQoder
	default:
		prepared.Route = TestRouteClaude
	}
	return target.Execute(ctx, prepared, sink)
}
func (s *TestService) fail(ctx context.Context, sink TestEventSink, message string) error {
	if s.options.Error != nil {
		s.options.Error(message)
	}
	if err := sink.Emit(ctx, TestEvent{Type: "error", Error: message}); err != nil && s.options.WriteError != nil {
		s.options.WriteError(err)
	}
	return errors.New(message)
}

// 首次输出失败立即取消执行上下文，后续写出复用相同错误。
type testGuardedSink struct {
	mu     sync.Mutex
	next   TestEventSink
	cancel context.CancelFunc
	err    error
}

func (s *testGuardedSink) apply(write func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if err := write(); err != nil {
		s.err = err
		s.cancel()
	}
	return s.err
}
func (s *testGuardedSink) Begin(ctx context.Context, commit bool) error {
	return s.apply(func() error { return s.next.Begin(ctx, commit) })
}
func (s *testGuardedSink) Emit(ctx context.Context, event TestEvent) error {
	return s.apply(func() error { return s.next.Emit(ctx, event) })
}
func (s *testGuardedSink) failure() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *TestService) RunTestBackground(ctx context.Context, id int64, model string) (*ScheduledTestResult, error) {
	return s.RunTestBackgroundWithPromptAndUserAgent(ctx, id, model, "", "")
}
func (s *TestService) RunTestBackgroundWithPrompt(ctx context.Context, id int64, model, prompt string) (*ScheduledTestResult, error) {
	return s.RunTestBackgroundWithPromptAndUserAgent(ctx, id, model, prompt, "")
}
func (s *TestService) RunTestBackgroundWithPromptAndUserAgent(ctx context.Context, id int64, model, prompt, userAgent string) (*ScheduledTestResult, error) {
	started := s.options.Now()
	sink := &testResultSink{}
	testErr := s.Test(ctx, TestRequest{AccountID: id, Model: model, Prompt: prompt, Mode: AccountTestModeDefault, Automatic: true, UserAgent: userAgent}, sink)
	finished := s.options.Now()
	text, message := sink.result()
	status := "success"
	if testErr != nil || message != "" {
		status = "failed"
		if message == "" && testErr != nil {
			message = testErr.Error()
		}
	}
	return &ScheduledTestResult{Status: status, ResponseText: text, ErrorMessage: message, LatencyMs: finished.Sub(started).Milliseconds(), StartedAt: started, FinishedAt: finished}, nil
}

// 后台直接聚合事件，保留原 JSON 编码对非法 UTF-8 和不可编码 Data 的处理，不构造 HTTP/SSE 缓冲。
type testResultSink struct {
	mu    sync.Mutex
	texts []string
	err   string
}

func (*testResultSink) Begin(context.Context, bool) error { return nil }
func (s *testResultSink) Emit(_ context.Context, event TestEvent) error {
	if event.Type != "content" && event.Type != "error" {
		return nil
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nil
	}
	var wire TestEvent
	if json.Unmarshal(raw, &wire) != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch wire.Type {
	case "content":
		if wire.Text != "" {
			s.texts = append(s.texts, wire.Text)
		}
	case "error":
		s.err = wire.Error
	}
	return nil
}
func (s *testResultSink) result() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.texts, ""), s.err
}
