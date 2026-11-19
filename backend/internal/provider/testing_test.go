package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// Administrator test input can select an explicit kind or leave it for
// inference, and legacy mode/type arguments can arrive in two orders. The
// normalization tables keep the selected mode, kind, and presence flag
// observable together. Provider metadata then decides which prepared request
// reaches execution, including Jev decision input and Gemini image handling.
//
// A target can ignore the first sink failure and return nil. The fixture makes
// two emission attempts while recording only the first, so the public service
// has to expose output failure and cancel the context. Background fixtures use
// invalid UTF-8, unencodable event data, and a later empty error to observe wire
// normalization and final status. A controlled clock checks stored latency
// independently of elapsed test runtime.

// testLoaderStub records one service invocation and supplies configured test
// loading outcomes.
type testLoaderStub struct {
	// target and err are returned together after recording the load. Tests
	// configure them before synchronous use, supplying a usable target for
	// successful loading.
	target TestTarget // configured provider test target
	err    error      // injected target loading error
	loads  int        // synchronous target load count
	// request records the loader input by value. Its Type pointer and SystemOne
	// bytes stay shared with that invocation and remain stable during the test.
	request TestRequest
}

// testTargetStub exposes fixture metadata and a callback for prepared test
// execution.
type testTargetStub struct {
	// info is returned by value for eligibility checks. Nested protocol slices and
	// optional snapshot pointers remain shared and stable during the fixture
	// invocation.
	info TestTargetInfo
	// run is required on the execution path and receives the supplied context,
	// prepared request, and guard directly. Tests use it to observe dispatch and
	// output.
	run func(context.Context, PreparedTestRequest, TestEventSink) error
}

// testSinkStub records synchronous preparation and events while injecting output
// failures.
type testSinkStub struct {
	// emitted keeps event values from synchronous writes. Mutable Data values are
	// still borrowed from the producer and remain stable until fixture assertions
	// finish.
	emitted []TestEvent
	begin   int // synchronous preparation call count
	// err is configured before use and returned after recording each write. The
	// guard decides whether later operations may reach this fixture again.
	err error
}

// TestNormalizeProviderTestMode supplies omitted, padded, uppercase, known, and
// unknown mode strings. It verifies the recognized compact spellings survive
// normalization and absent or unknown choices select default mode.
func TestNormalizeProviderTestMode(t *testing.T) {
	tests := []struct {
		input string // supplied mode argument text
		want  string // expected normalized mode value
	}{
		{input: "", want: ProviderTestModeDefault},
		{input: "default", want: ProviderTestModeDefault},
		{input: " compact ", want: ProviderTestModeCompact},
		{input: "COMPACT", want: ProviderTestModeCompact},
		{input: " legacy_compact ", want: ProviderTestModeLegacyCompact},
		{input: "unknown", want: ProviderTestModeDefault},
	}

	for _, tt := range tests {
		if got := NormalizeProviderTestMode(tt.input); got != tt.want {
			t.Fatalf("normalizeProviderTestMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestDecisionTestingRequiresSystemOne sends a structured explicit decision
// request to Jev, OpenCode, and OpenAI metadata. Platforms whose native
// protocols include SystemOne receive their family, kind and original payload,
// while OpenAI rejects the request before execution.
func TestDecisionTestingRequiresSystemOne(t *testing.T) {
	for _, platform := range []string{PlatformJev, PlatformOpenCode, PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			called := false
			kind := ProviderTestTypeDecision
			request := TestRequest{Type: &kind, SystemOne: []byte(`{"state":["ready"],"questions":{"x":{"type":"noul","instructions":"Ready?"}}}`)}
			loader := &testLoaderStub{target: testTargetStub{info: TestTargetInfo{ProviderSnapshot: ProviderSnapshot{Platform: platform, Type: ProviderTypeAPIKey}}, run: func(_ context.Context, got PreparedTestRequest, _ TestEventSink) error {
				called = true
				require.Equal(t, TestRouteDefault, got.Route)
				require.Equal(t, kind, got.TestType)
				require.Equal(t, request.SystemOne, got.SystemOne)
				return nil
			}}}
			err := NewTestService(loader, TestOptions{}).Test(context.Background(), request, &testSinkStub{})
			if platform != PlatformOpenAI {
				require.NoError(t, err)
				require.True(t, called)
			} else {
				require.EqualError(t, err, "Decision tests require a provider with SystemOne support")
				require.False(t, called)
			}
		})
	}
}

// TestResolveProviderTestModeAndType supplies explicit kinds, an omitted kind, a
// mode alias, and reversed arguments. It verifies the complete
// mode/type/presence triple, including empty kind for legacy compact inference.
func TestResolveProviderTestModeAndType(t *testing.T) {
	tests := []struct {
		name      string   // normalization scenario display name
		mode      string   // supplied mode slot argument
		testTypes []string // supplied kind slot arguments
		// wantMode, wantType, and explicit describe one expected normalized choice.
		// The omitted-kind row keeps an empty wantType with false so inference stays
		// observable.
		wantMode string // expected normalized execution mode
		wantType string // expected normalized test kind
		explicit bool   // expected kind presence flag
	}{
		{name: "explicit image", mode: "default", testTypes: []string{"image"}, wantMode: ProviderTestModeDefault, wantType: ProviderTestTypeImage, explicit: true},
		{name: "explicit text", mode: "default", testTypes: []string{"text"}, wantMode: ProviderTestModeDefault, wantType: ProviderTestTypeText, explicit: true},
		{name: "legacy compact", mode: "compact", wantMode: ProviderTestModeCompact, wantType: "", explicit: false},
		{name: "mode alias", mode: "image", wantMode: ProviderTestModeDefault, wantType: ProviderTestTypeImage, explicit: true},
		{name: "swapped new call", mode: "image", testTypes: []string{"compact"}, wantMode: ProviderTestModeCompact, wantType: ProviderTestTypeImage, explicit: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, testType, explicit := ResolveProviderTestModeAndType(tt.mode, tt.testTypes...)
			if mode != tt.wantMode || testType != tt.wantType || explicit != tt.explicit {
				t.Fatalf("resolveProviderTestModeAndType(%q, %#v) = (%q, %q, %v), want (%q, %q, %v)", tt.mode, tt.testTypes, mode, testType, explicit, tt.wantMode, tt.wantType, tt.explicit)
			}
		})
	}
}

// LoadTestTarget counts loading, records the original request, and returns the
// configured target and error. The request copy retains its Type pointer and
// SystemOne storage, which the synchronous fixture caller keeps stable.
func (l *testLoaderStub) LoadTestTarget(_ context.Context, request TestRequest) (TestTarget, error) {
	l.loads++
	l.request = request
	return l.target, l.err
}

// Information returns the configured provider metadata by value for eligibility
// checks. Its nested protocol slice and optional pointers remain fixture-owned,
// so callers keep them stable during the synchronous test.
func (t testTargetStub) Information() TestTargetInfo { return t.info }

// Execute calls the configured fixture callback and forwards its result. Tests
// supply a usable callback before execution; the context, prepared request, and
// sink reach it directly for dispatch and cancellation assertions.
func (t testTargetStub) Execute(ctx context.Context, request PreparedTestRequest, sink TestEventSink) error {
	return t.run(ctx, request, sink)
}

// Begin counts a synchronous preparation call and returns the injected write
// error. The context and commit flag are unused; validation tests inspect begin
// to verify rejection before stream preparation occurs.
func (s *testSinkStub) Begin(context.Context, bool) error { s.begin++; return s.err }

// Emit appends a synchronous event and returns the injected output error. Event
// values are retained directly, so any mutable payload remains producer-owned
// and stable until this fixture has finished its assertions.
func (s *testSinkStub) Emit(_ context.Context, event TestEvent) error {
	s.emitted = append(s.emitted, event)
	return s.err
}

// TestTestingValidatesBeforeLoadAndPreservesMissingProvider supplies padded
// invalid protocol syntax and a private loading error. It verifies syntax fails
// before loading or preparation and loading failure emits the sanitized Provider
// not found message.
func TestTestingValidatesBeforeLoadAndPreservesMissingProvider(t *testing.T) {
	loader := &testLoaderStub{err: errors.New("private database error")}
	svc := NewTestService(loader, TestOptions{})
	sink := &testSinkStub{}
	require.EqualError(t, svc.Test(context.Background(), TestRequest{Protocol: " responses "}, sink), "Invalid test protocol")
	require.Zero(t, loader.loads)
	require.Zero(t, sink.begin)
	require.EqualError(t, svc.Test(context.Background(), TestRequest{ProviderID: 1}, sink), "Provider not found")
	require.Equal(t, 1, loader.loads)
	require.Equal(t, "Provider not found", sink.emitted[1].Error)
}

// TestTestingDispatchesExplicitImageWithoutDuplicateLoad requests an image with
// compact mode from Gemini metadata. It verifies one load, Gemini routing,
// default prepared mode, and the explicit-kind flag.
func TestTestingDispatchesExplicitImageWithoutDuplicateLoad(t *testing.T) {
	var got PreparedTestRequest
	loader := &testLoaderStub{target: testTargetStub{info: TestTargetInfo{ProviderSnapshot: ProviderSnapshot{Platform: PlatformGemini, Type: ProviderTypeAPIKey}}, run: func(_ context.Context, req PreparedTestRequest, _ TestEventSink) error { got = req; return nil }}}
	svc := NewTestService(loader, TestOptions{})
	kind := ProviderTestTypeImage
	require.NoError(t, svc.Test(context.Background(), TestRequest{ProviderID: 9, Model: "explicit", Mode: ProviderTestModeCompact, Type: &kind}, &testSinkStub{}))
	require.Equal(t, 1, loader.loads)
	require.Equal(t, TestRouteDefault, got.Route)
	require.Equal(t, ProviderTestModeDefault, got.Mode)
	require.True(t, got.ExplicitType)
}

// TestTestingCancelsExecutionOnFirstWriteFailure has a target emit twice and
// return nil after its first write fails. It verifies immediate context
// cancellation, errors.Is matching for both attempts and the public result,
// and one invocation of the underlying sink.
func TestTestingCancelsExecutionOnFirstWriteFailure(t *testing.T) {
	failed := errors.New("sink failed")
	sink := &testSinkStub{err: failed}
	loader := &testLoaderStub{target: testTargetStub{info: TestTargetInfo{}, run: func(ctx context.Context, _ PreparedTestRequest, out TestEventSink) error {
		require.ErrorIs(t, out.Emit(ctx, TestEvent{Type: "content", Text: "one"}), failed)
		require.ErrorIs(t, ctx.Err(), context.Canceled)
		require.ErrorIs(t, out.Emit(ctx, TestEvent{Type: "content", Text: "two"}), failed)
		// Return nil from the fixture target after its write failure so Test must
		// surface the guard error even when execution omits it.
		return nil
	}}}
	require.ErrorIs(t, NewTestService(loader, TestOptions{}).Test(context.Background(), TestRequest{}, sink), failed)
	require.Len(t, sink.emitted, 1)
}

// TestTestingBackgroundPreservesWireTextAndClock emits invalid UTF-8,
// unencodable content data, an image, and an error followed by empty error text.
// It verifies normalized retained text, successful final status, half-second
// latency, and automatic/user-agent forwarding.
func TestTestingBackgroundPreservesWireTextAndClock(t *testing.T) {
	start := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	clockCalls := 0
	loader := &testLoaderStub{target: testTargetStub{info: TestTargetInfo{}, run: func(ctx context.Context, _ PreparedTestRequest, sink TestEventSink) error {
		// A channel in Data makes the entire content event unencodable. Combine it
		// with a later empty error so collection can be checked independently from
		// execution failure.
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
	// Advance the injected clock by half a second per reading to verify latency
	// uses the surrounding clock samples rather than elapsed test runtime.
	svc := NewTestService(loader, TestOptions{Now: func() time.Time {
		at := start.Add(time.Duration(clockCalls) * 500 * time.Millisecond)
		clockCalls++
		return at
	}})
	result, err := svc.RunTestBackgroundRequest(context.Background(), TestRequest{ProviderID: 1, GroupID: 59, Model: "model", Prompt: "prompt", UserAgent: "probe/1.0"})
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Equal(t, "a\ufffd", result.ResponseText)
	require.Empty(t, result.ErrorMessage)
	require.Equal(t, int64(500), result.LatencyMs)
	require.Equal(t, start, result.StartedAt)
	require.True(t, loader.request.Automatic)
	require.Equal(t, "probe/1.0", loader.request.UserAgent)
	require.Equal(t, int64(59), loader.request.GroupID)
}

// TestTestingRejectsProtocolOutsideProviderCapability supplies OpenAI and Kimi,
// Zhipu, and DeepSeek text protocol choices with differing eligibility. It
// verifies execution agrees with each expected decision and a Gemini image
// accepts a known explicit protocol.
func TestTestingRejectsProtocolOutsideProviderCapability(t *testing.T) {
	cases := []struct {
		name     string           // protocol scenario display name
		snapshot ProviderSnapshot // supplied provider eligibility metadata
		protocol string           // requested administrator protocol spelling
		allowed  bool             // expected request execution admission
	}{
		{"OpenAI API Key 可选 Chat", ProviderSnapshot{Platform: PlatformOpenAI, Type: ProviderTypeAPIKey}, APIProtocolChatCompletions, true},
		{"OpenAI API Key 不支持 Messages", ProviderSnapshot{Platform: PlatformOpenAI, Type: ProviderTypeAPIKey}, APIProtocolAnthropic, false},
		{"OpenAI OAuth 只有 Responses", ProviderSnapshot{Platform: PlatformOpenAI, Type: ProviderTypeOAuth}, APIProtocolChatCompletions, false},
		{"国产平台已启用 Messages", ProviderSnapshot{Platform: PlatformZhipu, Type: ProviderTypeAPIKey, EnabledProtocols: []capability.ProtocolID{capability.ProtocolAnthropicMessages}}, APIProtocolAnthropic, true},
		{"国产平台未启用 Responses", ProviderSnapshot{Platform: PlatformKimi, Type: ProviderTypeAPIKey, EnabledProtocols: []capability.ProtocolID{capability.ProtocolAnthropicMessages}}, APIProtocolResponses, false},
		{"其他平台不接受显式协议", ProviderSnapshot{Platform: PlatformAnthropic, Type: ProviderTypeAPIKey}, APIProtocolAnthropic, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executed := false
			loader := &testLoaderStub{target: testTargetStub{info: TestTargetInfo{ProviderSnapshot: tc.snapshot}, run: func(context.Context, PreparedTestRequest, TestEventSink) error {
				executed = true
				return nil
			}}}
			err := NewTestService(loader, TestOptions{}).Test(context.Background(), TestRequest{ProviderID: 1, Protocol: tc.protocol}, &testSinkStub{})
			require.Equal(t, tc.allowed, err == nil)
			require.Equal(t, tc.allowed, executed)
		})
	}

	kind := ProviderTestTypeImage
	loader := &testLoaderStub{target: testTargetStub{info: TestTargetInfo{ProviderSnapshot: ProviderSnapshot{Platform: PlatformGemini, Type: ProviderTypeAPIKey}}, run: func(context.Context, PreparedTestRequest, TestEventSink) error { return nil }}}
	require.NoError(t, NewTestService(loader, TestOptions{}).Test(context.Background(), TestRequest{ProviderID: 1, Type: &kind, Protocol: APIProtocolResponses}, &testSinkStub{}))
}
