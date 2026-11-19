package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// testing.go normalizes provider test requests, coordinates guarded execution,
// and aggregates background test results.
// Provider tests combine administrator input, platform eligibility, and typed
// output events. An omitted kind lets the execution adapter infer the test,
// while explicit image selection resets compact mode and checks platform support
// before execution. Legacy argument spellings retain their selected kind and
// mode during normalization, and protocol selection is checked against provider
// metadata. Group probes retain their resolved entry and upstream protocols,
// revalidate provider enablement, and choose their operation from the entry.
//
// Execution writes through one guarded sink. Its first output failure cancels
// the test context and is exposed even when a target ignores the write error.
// The HTTP sink flushes successful emissions as they arrive. Background
// aggregation accepts content and error events through JSON, normalizes their
// text, and retains the last error message. The returned executor error and that
// retained message jointly decide the stored test status.

// TestRoute values identify execution families selected from provider metadata
// before a prepared test request reaches its platform target.
const (
	TestRouteDefault             TestRoute = iota // dedicated platform execution target
	TestRouteTextAPIKeyAdaptive                   // Kimi/Zhipu/DeepSeek configured-protocol adaptive test
	TestRouteTextAPIKeyResponses                  // Kimi/Zhipu/DeepSeek shared Responses execution
	TestRouteTextAPIKeyFixed                      // Kimi/Zhipu/DeepSeek fixed Chat or Messages execution

	// Provider test-type and mode strings normalize administrator selections and
	// recognized legacy arguments before a request reaches its platform executor.
	ProviderTestTypeDecision      = "decision"       // structured SystemOne decision test
	ProviderTestTypeText          = "text"           // text connection test kind
	ProviderTestTypeImage         = "image"          // image connection test kind
	ProviderTestModeDefault       = "default"        // ordinary connection test mode
	ProviderTestModeCompact       = "compact"        // OpenAI compact connection test
	ProviderTestModeLegacyCompact = "legacy_compact" // OpenAI legacy compact test
)

// TestEvent is an SSE event emitted during provider testing.
type TestEvent struct {
	Type     string `json:"type"`                // event dispatch kind name
	Text     string `json:"text,omitempty"`      // emitted test content fragment
	Model    string `json:"model,omitempty"`     // test event model identifier
	Status   string `json:"status,omitempty"`    // platform test status label
	Code     string `json:"code,omitempty"`      // platform diagnostic code label
	ImageURL string `json:"image_url,omitempty"` // image URL or data URL
	MimeType string `json:"mime_type,omitempty"` // image MIME type
	// Data is included in JSON encoding of the whole event. Producers keep its
	// mutable values stable during synchronous sink use; unsupported values make
	// the background sink discard even otherwise usable content.
	Data    any    `json:"data,omitempty"`    // structured event detail payload
	Success bool   `json:"success,omitempty"` // successful completion event flag
	Error   string `json:"error,omitempty"`   // emitted test failure text
}

// TestRequest supplies provider selection and caller input for one synchronous
// test. Its pointed kind and SystemOne bytes remain caller-owned and stable
// until Test returns.
type TestRequest struct {
	RequestSource  string // internal caller source, never upstream controlled
	RequestedModel string // original model before group mapping
	OperatorUserID *int64 // optional initiating administrator identifier
	// ProtocolRoute fixes a group probe's entry and upstream endpoint together.
	// Its zero value retains the standalone Protocol selector and type inference.
	ProtocolRoute TestProtocolRoute
	// SystemOne carries structured decision input by shared byte storage. Nonempty
	// input requires a provider whose native protocols include SystemOne, and the
	// caller keeps its bytes stable through execution.
	SystemOne json.RawMessage
	// SystemOneImages carries the caller's explicit TypeSafe image opt-in header.
	// Scheduled probes leave it empty and retain their text-only request behavior.
	SystemOneImages string

	ProviderID int64 // selected provider record identifier
	// GroupID selects the model attribute profile for this test. Zero uses
	// catalogue defaults; the supplied model still follows provider mapping.
	GroupID int64
	// Model, Prompt, and Mode carry raw caller text into loading. Preparation
	// writes normalized Mode into the copied request, with Model and Prompt passed
	// to the target for its platform-specific handling.
	// Model: requested test model
	// Prompt: supplied test prompt
	// Mode: execution mode argument
	Model, Prompt, Mode string
	// Type is borrowed through preparation. Nil or blank input permits legacy
	// inference; outside a recognized reversed mode/type pair, a nonblank unknown
	// kind becomes explicit text.
	Type *string
	// Protocol uses exact administrator spellings checked before provider loading.
	// An explicit image kind skips provider protocol eligibility after that syntax
	// check.
	Protocol string
	// OpenAI target execution supplies UserAgent and Originator to its TestRun
	// headers before optional identity preparation. The same prepared request
	// carries both caller identity values through that target invocation.
	// UserAgent: caller user agent
	// Originator: caller origin label
	UserAgent, Originator string
	Automatic             bool // automatic background probe indicator
}

// TestRoute identifies the platform execution family chosen for a prepared test.
type TestRoute uint8

// PreparedTestRequest combines normalized test choices with the original caller
// input.
type PreparedTestRequest struct {
	// TestRequest carries the original scalar inputs and borrowed Type/SystemOne
	// storage. Preparation replaces Mode in this value while the caller's request
	// remains intact.
	TestRequest
	// TestType and ExplicitType retain normalized kind and presence together. An
	// empty kind with false leaves inference to the target; explicit image resets
	// Mode.
	TestType     string    // prepared test operation kind
	ExplicitType bool      // nonblank or legacy kind selection
	Route        TestRoute // selected platform execution family
}

// TestTargetInfo exposes provider eligibility and configured protocol for test
// preparation.
type TestTargetInfo struct {
	ProviderSnapshot        // provider identity and capability snapshot
	APIProtocol      string // configured provider API protocol
}

// TestTarget retains platform execution and exposes metadata used to validate
// one test.
type TestTarget interface {
	// Information returns the provider metadata used for test eligibility and
	// routing. Platform test adapters use their retained Record to obtain execution
	// credentials while this snapshot supplies selection fields.
	Information() TestTargetInfo
	// Execute performs the prepared test using ctx and emits typed output through
	// the supplied sink. Cooperate with cancellation and finish sink use before
	// returning; Test combines returned execution errors with the first sink
	// failure.
	Execute(context.Context, PreparedTestRequest, TestEventSink) error
}

// TestLoader resolves a request to its retained platform test execution target.
type TestLoader interface {
	// LoadTestTarget returns a nonnil target for successful loading. The target and
	// its retained provider data remain valid through execution; TestService
	// replaces any load error with the public Provider not found message.
	LoadTestTarget(context.Context, TestRequest) (TestTarget, error)
}

// TestEventSink receives synchronous provider-test preparation and typed output
// events.
type TestEventSink interface {
	// Begin prepares output and uses the boolean as a request to commit metadata.
	// The HTTP sink sets SSE headers and flushes committed preparation; background
	// result collection accepts preparation with no output.
	Begin(context.Context, bool) error
	// Emit consumes one test event and reports output failure to the test service.
	// Producers keep mutable event payloads stable through the call; the guard
	// cancels execution after the first returned error.
	Emit(context.Context, TestEvent) error
}

// TestOptions supplies background timing and optional service failure diagnostic
// callbacks.
type TestOptions struct {
	// PrepareContext creates per-test attribute state before loading the target.
	// Nil retains the supplied context; the callback must preserve cancellation.
	PrepareContext func(context.Context) context.Context
	// Now is required by background test methods and sampled around the whole Test
	// call. Its owner supports concurrent calls when tests share this service.
	Now func() time.Time
	// Error receives service validation and sanitized load failure messages before
	// their error event is emitted. Nil skips that diagnostic callback.
	Error func(string)
	// WriteError receives failure of the error-event emission performed by fail.
	// Nil skips reporting; Test still exposes the guard's retained error.
	WriteError func(error)
}

// TestService prepares provider tests and combines execution outcomes with
// output failures.
type TestService struct {
	// loader is retained for each synchronous test and can be called by concurrent
	// users. Its owner keeps loading dependencies and returned targets usable
	// through execution.
	loader  TestLoader
	options TestOptions // captured timing and diagnostic callbacks
}

// testGuardedSink serializes output and retains the first failure for test
// completion.
type testGuardedSink struct {
	// mu protects err and serializes writes through next. next and cancel are
	// invoked while it is held, so those callbacks avoid reentering this guard; the
	// first write failure stores err and closes later output admission.
	mu     sync.Mutex         // sink output serialization lock
	next   TestEventSink      // borrowed synchronous output sink
	cancel context.CancelFunc // owning test context cancellation
	err    error              // retained first output failure
}

// testResultSink accumulates normalized content and the latest background error
// event.
type testResultSink struct {
	// mu protects texts and err across emission and result reads. Accepted content
	// appends in lock order; each accepted error replaces err, including an empty
	// message.
	mu    sync.Mutex // result accumulation state lock
	texts []string   // ordered accepted content fragments
	err   string     // latest accepted error message
}

// NormalizeProviderTestType returns a lowercase known test kind after trimming
// whitespace. Empty and unknown values select text; callers track presence
// separately when omitted input should permit target inference.
func NormalizeProviderTestType(testType string) string {
	switch strings.ToLower(strings.TrimSpace(testType)) {
	case ProviderTestTypeDecision:
		return ProviderTestTypeDecision
	case ProviderTestTypeImage:
		return ProviderTestTypeImage
	case ProviderTestTypeText:
		return ProviderTestTypeText
	default:
		return ProviderTestTypeText
	}
}

// ProviderTestTypeFromArgs examines the first supplied kind and reports whether
// it was nonblank. Missing or blank input returns text with false; a nonblank
// unknown value normalizes to text with true, and later arguments are ignored.
func ProviderTestTypeFromArgs(testTypes ...string) (string, bool) {
	if len(testTypes) == 0 || strings.TrimSpace(testTypes[0]) == "" {
		return ProviderTestTypeText, false
	}
	return NormalizeProviderTestType(testTypes[0]), true
}

// ResolveProviderTestModeAndType returns normalized mode, kind, and whether kind
// was selected. It examines the first kind argument and supports text or image
// in the mode position, including a reversed pair whose other value names a
// known mode.
//
// Normalizing missing kind alone would produce text and lose the target
// inference signal. The resolver returns an empty kind with false when neither
// argument selects one. With no nonblank kind input, a text/image mode alias
// selects an explicit kind and default mode. A reversed recognized pair retains
// its mode; other nonblank kind input takes precedence over that alias. Decision
// kind uses the ordinary supplied-kind path.
func ResolveProviderTestModeAndType(mode string, testTypes ...string) (string, string, bool) {
	// When the first slot names text or image and the second names a recognized
	// mode, interpret the pair as reversed arguments so both selections survive
	// normalization.
	if len(testTypes) > 0 {
		rawMode := strings.ToLower(strings.TrimSpace(mode))
		rawTypeOrMode := strings.ToLower(strings.TrimSpace(testTypes[0]))
		if (rawMode == ProviderTestTypeText || rawMode == ProviderTestTypeImage) &&
			(rawTypeOrMode == ProviderTestModeDefault || rawTypeOrMode == ProviderTestModeCompact || rawTypeOrMode == ProviderTestModeLegacyCompact) {
			return NormalizeProviderTestMode(testTypes[0]), NormalizeProviderTestType(mode), true
		}
	}
	testType, explicit := ProviderTestTypeFromArgs(testTypes...)
	normalizedMode := NormalizeProviderTestMode(mode)
	if !explicit {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case ProviderTestTypeText, ProviderTestTypeImage:
			return ProviderTestModeDefault, NormalizeProviderTestType(mode), true
		}
	}
	// Keep an empty kind for omitted input so platform targets can apply
	// attribute-based automatic selection; the normalized text default alone would hide
	// that missing selection.
	if !explicit {
		testType = ""
	}
	return normalizedMode, testType, explicit
}

// NormalizeProviderTestMode trims and lowercases a mode, retaining compact and
// legacy_compact. All other spellings select default mode, so callers handle
// legacy kind aliases before relying on this result alone.
func NormalizeProviderTestMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ProviderTestModeCompact:
		return ProviderTestModeCompact
	case ProviderTestModeLegacyCompact:
		return ProviderTestModeLegacyCompact
	default:
		return ProviderTestModeDefault
	}
}

// NewTestService retains the loader and copies options for synchronous test
// calls. Supply a usable loader and sink at execution time; background methods
// also require Now, and shared dependencies support concurrent callers.
func NewTestService(loader TestLoader, options TestOptions) *TestService {
	return &TestService{loader: loader, options: options}
}

// Test prepares and executes one request through a guarded sink and returns its
// execution and output failures. Callers provide a nonnil context, usable loader
// and sink, and keep borrowed request storage stable until execution finishes.
//
// The first sink failure cancels the derived execution context and blocks
// subsequent output. Test waits for the target to return, so cancellation
// cooperation determines when that failure can finish execution. When errors.Is
// cannot match the retained write error in the target result, Test joins that
// error into the returned chain; a matching target result is returned unchanged.
func (s *TestService) Test(ctx context.Context, request TestRequest, sink TestEventSink) error {
	if _, exists := usage.SystemRequestFromContext(ctx); !exists {
		source := request.RequestSource
		if source == "" {
			source = usage.SourceProviderTest
			if request.Automatic {
				source = usage.SourceScheduledTest
			}
		}
		requestedModel := request.RequestedModel
		if requestedModel == "" {
			requestedModel = request.Model
		}
		ctx = usage.WithSystemRequest(ctx, usage.SystemRequest{MappedModel: request.Model, Source: source, OperatorUserID: request.OperatorUserID, GroupID: request.GroupID, RequestedModel: requestedModel, Automatic: request.Automatic})
	}

	if s.options.PrepareContext != nil {
		ctx = s.options.PrepareContext(ctx)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	guarded := &testGuardedSink{next: sink, cancel: cancel}
	err := s.execute(runCtx, request, guarded)
	// A target may ignore a failed emission and return success or another error.
	// Include the retained output failure so the caller can detect incomplete
	// delivery.
	if writeErr := guarded.failure(); writeErr != nil && !errors.Is(err, writeErr) {
		return errors.Join(err, writeErr)
	}
	return err
}

// execute validates test choices, loads one target, and passes it a prepared
// request. Invalid protocol spelling fails before loading, and every loader
// error becomes the public Provider not found message. The loader receives the
// original request before preparation replaces its copied Mode.
//
// Kind presence and platform metadata decide eligibility: structured decision
// input requires a platform whose native protocols include SystemOne; explicit
// images require OpenAI, Gemini, or Grok and select default mode. A known
// explicit image protocol bypasses the separate provider protocol check. Other
// selected protocols use testProtocolAllowed. Kimi, Zhipu, and DeepSeek routing
// uses APIProtocol to choose adaptive, Responses, or fixed execution, while
// remaining platforms use their dedicated target with TestRouteDefault. Routed
// probes validate the exact endpoint for the requested model and retain its
// protocol pair in the execution context. Target execution receives the
// original borrowed payload storage.
func (s *TestService) execute(ctx context.Context, request TestRequest, sink TestEventSink) error {
	if request.Protocol != "" && TestProtocolID(request.Protocol) == "" {
		return s.fail(ctx, sink, "Invalid test protocol")
	}
	var types []string
	if request.Type != nil {
		types = []string{*request.Type}
	}
	mode, kind, explicit := ResolveProviderTestModeAndType(request.Mode, types...)
	// Explicit image selection chooses the image execution path even with a compact
	// input mode. Reset the copied mode so compact connection payload handling
	// cannot take precedence.
	if explicit && kind == ProviderTestTypeImage {
		mode = ProviderTestModeDefault
	}
	target, err := s.loader.LoadTestTarget(ctx, request)
	// Load failures can contain private storage diagnostics. Emit the fixed public
	// message while retaining output failure separately in the surrounding Test
	// guard.
	if err != nil {
		return s.fail(ctx, sink, "Provider not found")
	}
	info := target.Information()
	if request.ProtocolRoute != (TestProtocolRoute{}) {
		if request.Protocol != "" || mode != ProviderTestModeDefault || !request.ProtocolRoute.SupportsProvider(info.ProviderSnapshot, request.Model) {
			return s.fail(ctx, sink, fmt.Sprintf("Provider test cannot execute route %s -> %s", request.ProtocolRoute.ClientProtocol, request.ProtocolRoute.UpstreamProtocol))
		}
		ctx = WithTestProtocolRoute(ctx, request.ProtocolRoute)
		// Routed probes select their operation from the client entry. Text entries
		// cannot switch to an Images endpoint because a model also declares images.
		kind, explicit = ProviderTestTypeText, true
		switch request.ProtocolRoute.ClientProtocol {
		case capability.ProtocolImagesGenerations:
			kind = ProviderTestTypeImage
		case capability.ProtocolSystemOne:
			kind = ProviderTestTypeDecision
		case capability.ProtocolGeminiGenerateContent:
			if info.Platform == PlatformGemini {
				kind, explicit = "", false
			}
		}
	}
	native := capability.NativeProtocolOptions(info.Platform, info.Type, info.AuthMode)
	if (kind == ProviderTestTypeDecision || len(request.SystemOne) > 0) && !slices.Contains(native, capability.ProtocolSystemOne) {
		return s.fail(ctx, sink, "Decision tests require a provider with SystemOne support")
	}
	if explicit && kind == ProviderTestTypeImage && info.Platform != PlatformOpenAI && info.Platform != PlatformGemini && info.Platform != PlatformGrok {
		return s.fail(ctx, sink, fmt.Sprintf("Image tests are not supported for platform %s", info.Platform))
	}
	if request.Protocol != "" && kind != ProviderTestTypeImage && !testProtocolAllowed(info, native, request.Protocol) {
		return s.fail(ctx, sink, fmt.Sprintf("Test protocol %s is not supported for this provider", request.Protocol))
	}
	prepared := PreparedTestRequest{TestRequest: request, TestType: kind, ExplicitType: explicit}
	// Kimi, Zhipu, and DeepSeek target execution consumes Route together with
	// its configured protocol. Choose that combination after eligibility
	// validation; dedicated platform targets use TestRouteDefault.
	prepared.Mode = mode
	if platformRuleRegistry[info.Platform].text != nil {
		switch info.APIProtocol {
		case APIProtocolAdaptive:
			prepared.Route = TestRouteTextAPIKeyAdaptive
		case APIProtocolResponses:
			prepared.Route = TestRouteTextAPIKeyResponses
		default:
			prepared.Route = TestRouteTextAPIKeyFixed
		}
	} else {
		prepared.Route = TestRouteDefault
	}
	return target.Execute(ctx, prepared, sink)
}

// TestProtocolID maps exact administrator protocol spellings to native
// capability IDs. Unknown values, including differently cased or padded
// spellings, return empty so execute can reject supplied invalid syntax before
// provider loading.
func TestProtocolID(protocol string) capability.ProtocolID {
	switch protocol {
	case "gemini":
		return capability.ProtocolGeminiGenerateContent
	case APIProtocolSystemOne:
		return capability.ProtocolSystemOne
	case APIProtocolAnthropic:
		return capability.ProtocolAnthropicMessages
	case APIProtocolResponses:
		return capability.ProtocolOpenAIResponses
	case APIProtocolChatCompletions:
		return capability.ProtocolOpenAIChatCompletions
	default:
		return ""
	}
}

// testProtocolAllowed reports whether provider metadata permits a selected test
// protocol. OpenAI API keys allow Responses or Chat regardless of enabled
// protocols, and other OpenAI types permit Responses. Platforms declaring text
// endpoint rules or explicitTestProtocol in platformRuleRegistry allow a
// protocol that is both enabled and among the supplied native options for the
// provider's type.
//
// Remaining platforms run one fixed probe and reject explicit text protocol
// selection. execute calls this helper after spelling validation and image
// eligibility; explicit images skip this check once their protocol spelling is
// known.
func testProtocolAllowed(info TestTargetInfo, native []capability.ProtocolID, protocol string) bool {
	rules := platformRuleRegistry[info.Platform]
	if rules.text != nil || rules.explicitTestProtocol {
		selected := TestProtocolID(protocol)
		return slices.Contains(info.EnabledProtocols, selected) && slices.Contains(native, selected)
	}
	if info.Platform == PlatformOpenAI {
		if info.Type == ProviderTypeAPIKey {
			return protocol == APIProtocolResponses || protocol == APIProtocolChatCompletions
		}
		return protocol == APIProtocolResponses
	}
	return false
}

// fail reports a service failure, emits its error event, and returns a new error
// with message. Optional WriteError observes emission failure; the guarded sink
// retains that output error for Test to combine with this service message.
func (s *TestService) fail(ctx context.Context, sink TestEventSink, message string) error {
	if s.options.Error != nil {
		s.options.Error(message)
	}
	// Report failure to deliver this diagnostic through WriteError while returning
	// the service message. Test reads the guard separately to retain both errors.
	if err := sink.Emit(ctx, TestEvent{Type: "error", Error: message}); err != nil && s.options.WriteError != nil {
		s.options.WriteError(err)
	}
	return errors.New(message)
}

// apply serializes one output call and returns the first retained write error.
// The callback runs under mu and avoids reentering guard methods. Once output
// fails, later calls return that stored error without invoking their write
// callback.
//
// Writing, storing failure, and cancelling the owning execution context happen
// in one locked sequence. A target emitting concurrently therefore cannot admit
// another write after the failure has been recorded. Cancellation asks the
// executor to stop, while Test still waits for its return before reporting the
// final error chain.
func (s *testGuardedSink) apply(write func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	// Begin and Emit share this failure state. Remember a preparation error so
	// later event calls remain blocked even when their target ignores Begin's
	// return.
	if err := write(); err != nil {
		s.err = err
		s.cancel()
	}
	return s.err
}

// Begin forwards preparation through the guard while output remains admitted. An
// earlier failure returns the stored error immediately; a failure from the
// underlying Begin cancels execution and becomes the result of later guarded
// writes.
func (s *testGuardedSink) Begin(ctx context.Context, commit bool) error {
	return s.apply(func() error { return s.next.Begin(ctx, commit) })
}

// Emit forwards one event while output remains admitted and returns the first
// failure. Keep mutable event data stable during the synchronous call; an
// earlier failure suppresses this emission and returns its retained error.
func (s *testGuardedSink) Emit(ctx context.Context, event TestEvent) error {
	return s.apply(func() error { return s.next.Emit(ctx, event) })
}

// failure returns the first output error recorded by this guard.
func (s *testGuardedSink) failure() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

// RunTestBackground collects a default-mode automatic test for the provider and
// model. It supplies empty prompt and user agent, and execution failures become
// failed result data with a nil Go error.
func (s *TestService) RunTestBackground(ctx context.Context, id int64, model string) (*ScheduledTestResult, error) {
	return s.RunTestBackgroundRequest(ctx, TestRequest{ProviderID: id, Model: model})
}

// RunTestBackgroundRequest returns a timed automatic default-mode
// result using the supplied model, group, prompt, and user agent. Mode is set to
// default and Automatic is enabled. Now is required and sampled
// before and after Test; latency is their difference in whole milliseconds. Test
// failures are represented in result data with a nil Go error.
//
// Collected content is concatenated, and the last accepted error event supplies
// the message. A later empty error clears an earlier message when execution
// returns nil; any returned execution error still selects failed status and
// supplies its text if that message is empty. Image and completion events
// contribute no response text or status decision to this collector.
func (s *TestService) RunTestBackgroundRequest(ctx context.Context, request TestRequest) (*ScheduledTestResult, error) {
	started := s.options.Now()
	sink := &testResultSink{}
	request.Mode = ProviderTestModeDefault
	request.Automatic = true
	testErr := s.Test(ctx, request, sink)
	finished := s.options.Now()
	text, message := sink.result()
	status := "success"
	if testErr != nil || message != "" {
		status = "failed"
		// Use returned executor error text when the retained message is empty, so
		// failed results report whichever diagnostic the executor actually supplied.
		if message == "" && testErr != nil {
			message = testErr.Error()
		}
	}
	return &ScheduledTestResult{Status: status, ResponseText: text, ErrorMessage: message, LatencyMs: finished.Sub(started).Milliseconds(), StartedAt: started, FinishedAt: finished}, nil
}

// Begin accepts stream preparation because background results require no HTTP
// metadata.
func (*testResultSink) Begin(context.Context, bool) error { return nil }

// Emit collects content and error events after JSON encoding and decoding. Other
// event kinds and encoding failures are accepted with a nil error and contribute
// nothing. The whole event is encoded, so unsupported Data can discard an
// otherwise usable text fragment.
//
// The JSON round trip substitutes invalid UTF-8 before text is accumulated,
// matching the string normalization of wire decoding. Accepted nonempty content
// appends under the result mutex. An accepted error replaces the previous
// message even when empty, and the sink supplies no execution error for a
// dropped event; the background method combines accepted messages with the
// target error.
func (s *testResultSink) Emit(_ context.Context, event TestEvent) error {
	if event.Type != "content" && event.Type != "error" {
		return nil
	}
	// Round-trip the whole event before accumulating text so UTF-8 replacement
	// follows JSON decoding and unencodable payload data cannot contribute a
	// partial text event.
	raw, err := json.Marshal(event)
	if err != nil {
		return nil
	}
	var wire TestEvent
	// Discard failed wire decoding with a successful sink return, leaving earlier
	// accumulated content and error state available for the background result
	// decision.
	if json.Unmarshal(raw, &wire) != nil {
		return nil
	}
	// Change content and the retained error under one mutex so result reads observe
	// accepted events coherently; a later empty error deliberately replaces an
	// earlier message.
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

// result returns concatenated accepted content and the latest accepted error
// message. It reads both under the accumulation mutex; an empty error can mean
// no error event or a later event that cleared the message.
func (s *testResultSink) result() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.texts, ""), s.err
}
