package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testLoaderStub struct {
	target  TestTarget
	err     error
	loads   int
	request TestRequest
}

func (l *testLoaderStub) LoadTestTarget(_ context.Context, request TestRequest) (TestTarget, error) {
	l.loads++
	l.request = request
	return l.target, l.err
}

type testTargetStub struct {
	info TestTargetInfo
	run  func(context.Context, PreparedTestRequest, TestEventSink) error
}

func (t testTargetStub) Information() TestTargetInfo { return t.info }
func (t testTargetStub) Execute(ctx context.Context, request PreparedTestRequest, sink TestEventSink) error {
	return t.run(ctx, request, sink)
}

type testSinkStub struct {
	emitted []TestEvent
	begin   int
	err     error
}

func (s *testSinkStub) Begin(context.Context, bool) error { s.begin++; return s.err }
func (s *testSinkStub) Emit(_ context.Context, event TestEvent) error {
	s.emitted = append(s.emitted, event)
	return s.err
}

// 无效协议在读取账号之前失败；账号缺失仍只发送原通用错误，不提前提交 SSE Header。
func TestTestingValidatesBeforeLoadAndPreservesMissingAccount(t *testing.T) {
	loader := &testLoaderStub{err: errors.New("private database error")}
	svc := NewTestService(loader, TestOptions{})
	sink := &testSinkStub{}
	require.EqualError(t, svc.Test(context.Background(), TestRequest{Protocol: " responses "}, sink), "Invalid test protocol")
	require.Zero(t, loader.loads)
	require.Zero(t, sink.begin)
	require.EqualError(t, svc.Test(context.Background(), TestRequest{AccountID: 1}, sink), "Account not found")
	require.Equal(t, 1, loader.loads)
	require.Equal(t, "Account not found", sink.emitted[1].Error)
}

// 显式图片优先于 Compact；请求只读取一次账号，并将执行资格限定为安全投影。
func TestTestingDispatchesExplicitImageWithoutDuplicateLoad(t *testing.T) {
	var got PreparedTestRequest
	loader := &testLoaderStub{target: testTargetStub{info: TestTargetInfo{AccountSnapshot: AccountSnapshot{Platform: PlatformGemini, Type: AccountTypeAPIKey}}, run: func(_ context.Context, req PreparedTestRequest, _ TestEventSink) error { got = req; return nil }}}
	svc := NewTestService(loader, TestOptions{})
	kind := AccountTestTypeImage
	require.NoError(t, svc.Test(context.Background(), TestRequest{AccountID: 9, Model: "explicit", Mode: AccountTestModeCompact, Type: &kind}, &testSinkStub{}))
	require.Equal(t, 1, loader.loads)
	require.Equal(t, TestRouteGemini, got.Route)
	require.Equal(t, AccountTestModeDefault, got.Mode)
	require.True(t, got.ExplicitType)
}

// 即使平台执行器忽略输出错误，用例也必须取消其 context 并只尝试一次失败写入。
func TestTestingCancelsExecutionOnFirstWriteFailure(t *testing.T) {
	failed := errors.New("sink failed")
	sink := &testSinkStub{err: failed}
	loader := &testLoaderStub{target: testTargetStub{info: TestTargetInfo{}, run: func(ctx context.Context, _ PreparedTestRequest, out TestEventSink) error {
		require.ErrorIs(t, out.Emit(ctx, TestEvent{Type: "content", Text: "one"}), failed)
		require.ErrorIs(t, ctx.Err(), context.Canceled)
		require.ErrorIs(t, out.Emit(ctx, TestEvent{Type: "content", Text: "two"}), failed)
		return nil
	}}}
	require.ErrorIs(t, NewTestService(loader, TestOptions{}).Test(context.Background(), TestRequest{}, sink), failed)
	require.Len(t, sink.emitted, 1)
}

// 后台直接收集事件，仍保留 JSON 字符修复、不可编码事件丢弃及最后一个错误覆盖语义。
func TestTestingBackgroundPreservesWireTextAndClock(t *testing.T) {
	start := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	clockCalls := 0
	loader := &testLoaderStub{target: testTargetStub{info: TestTargetInfo{}, run: func(ctx context.Context, _ PreparedTestRequest, sink TestEventSink) error {
		for _, event := range []TestEvent{
			{Type: "content", Text: "a\xff"},
			{Type: "content", Text: "ignored", Data: make(chan int)},
			{Type: "image", ImageURL: "ignored"},
			{Type: "error", Error: "earlier"},
			{Type: "error", Error: ""},
		} {
			require.NoError(t, sink.Emit(ctx, event))
		}
		return nil
	}}}
	svc := NewTestService(loader, TestOptions{Now: func() time.Time {
		at := start.Add(time.Duration(clockCalls) * 500 * time.Millisecond)
		clockCalls++
		return at
	}})
	result, err := svc.RunTestBackgroundWithPromptAndUserAgent(context.Background(), 1, "model", "prompt", "probe/1.0")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Equal(t, "a\ufffd", result.ResponseText)
	require.Empty(t, result.ErrorMessage)
	require.Equal(t, int64(500), result.LatencyMs)
	require.Equal(t, start, result.StartedAt)
	require.True(t, loader.request.Automatic)
	require.Equal(t, "probe/1.0", loader.request.UserAgent)
}
