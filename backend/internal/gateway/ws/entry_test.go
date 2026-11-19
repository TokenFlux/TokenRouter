package ws

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/pkg/requestcontext"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// errKeyLimitRetry drives the fixture's first-attempt failure so tests can
// observe one Key reservation retained while entry selects a replacement
// provider.
var errKeyLimitRetry = errors.New("retry provider")

// keyLimitEntry records turn admission and slot lifetime while substituting
// execution dependencies.
type keyLimitEntry struct {
	cancelClient context.CancelFunc // test client cancellation callback
	// EntryPorts remains nil for unused dependencies, so an unexpected unstubbed
	// call fails the fixture. Explicit methods below model the paths exercised by
	// these entry tests.
	EntryPorts            // unused entry dependency stubs
	t          *testing.T // current test assertion owner
	mode       string     // fixture execution scenario label
	// acquired counts successful reservations, and active tracks those still
	// held until release. attempts and authorized distinguish provider execution
	// from later-turn verification, while closed records selected client
	// presentation on scenario exits.
	// acquired: successful Key reservations; active: held Key slots;
	// attempts: provider execution calls; authorized: later-turn checks;
	// closed: selected WebSocket close status.
	acquired, active, attempts, authorized, closed int
	target                                         *keyLimitTarget    // fixture provider execution target
	abortLease                                     context.CancelFunc // current Key lease abort callback
}

// quietEntryLog discards structured diagnostics while tests observe admission
// counters and callbacks.
type quietEntryLog struct{}

// keyLimitTarget drives the turn callbacks and scripted errors for its entry
// fixture.
type keyLimitTarget struct {
	// EntryTarget remains nil for unused provider operations. Explicit fixture
	// methods define the selected scenarios; an unexpected promoted method call
	// exposes missing fixture support.
	EntryTarget                // unused selected-provider dependency stubs
	root        *keyLimitEntry // owning entry resource fixture
}

// imageSelectionEntry captures the attribute snapshot seen during provider choice.
type imageSelectionEntry struct {
	*keyLimitEntry                                // shared admission fixture operations
	selected       *routing.ModelAttributeSession // selection attribute snapshot identity
}

// imageSelectionTarget compares execution with the captured selection context.
type imageSelectionTarget struct {
	*keyLimitTarget                      // shared provider fixture operations
	entry           *imageSelectionEntry // captured selection fixture state
}

// With discards supplied diagnostic fields and returns the fixture's quiet
// logger.
func (quietEntryLog) With(...EntryField) EntryLogger { return quietEntryLog{} }

// Info discards entry messages so admission counters remain the fixture
// observation source.
func (quietEntryLog) Info(string, ...EntryField) {}

// Warn discards warning diagnostics while the entry test observes chosen
// resource outcomes.
func (quietEntryLog) Warn(string, ...EntryField) {}

// Debug discards selection diagnostics while the fixture drives controlled
// provider execution.
func (quietEntryLog) Debug(string, ...EntryField) {}

// Error discards failure diagnostics while assertions inspect callback and slot
// state.
func (quietEntryLog) Error(string, ...EntryField) {}

// Logger returns the quiet structured logger used by this admission fixture.
func (p *keyLimitEntry) Logger() EntryLogger { return quietEntryLog{} }

// SetLogger discards the derived logger while this fixture tracks resources
// through counters.
func (p *keyLimitEntry) SetLogger(EntryLogger) {}

// Prompt returns the supplied fixture bytes for first-frame model parsing.
// The slice remains shared and receives no prompt substitution here; entry's
// later rewriting uses the test's ordinary protocol path.
func (p *keyLimitEntry) Prompt(_ context.Context, b []byte) []byte { return b }

// Redirect keeps the fixture context and model unchanged during admission and
// completion planning.
func (p *keyLimitEntry) Redirect(ctx context.Context, m string) (context.Context, string) {
	return ctx, m
}

// BindContext ignores HTTP binding because the fixture has no owning Gin
// request.
func (p *keyLimitEntry) BindContext(context.Context) {}

// ClassifyPrevious supplies an empty label because these requests carry no
// continuation identity.
func (p *keyLimitEntry) ClassifyPrevious(string) string { return "" }

// ObserveFirst skips Ops writes while the fixture observes the first turn
// through Key admission.
func (p *keyLimitEntry) ObserveFirst(string) {}

// CaptureCyber supplies empty risk evidence for the fixture's ordinary
// admission requests.
func (p *keyLimitEntry) CaptureCyber([]byte) EntryCyberSnapshot { return EntryCyberSnapshot{} }

// Moderate returns nil so these Key admission fixtures pass local moderation.
// Entry interprets that absent decision as permission to continue, letting the
// tests reach reservation and provider callbacks.
func (p *keyLimitEntry) Moderate(context.Context, string, []byte) *moderation.Decision { return nil }

// BlockedSession returns empty for every fixture payload before provider
// selection. Entry therefore skips blocked-session event and close handling,
// leaving each scenario to reach its scripted admission outcome.
func (p *keyLimitEntry) BlockedSession(context.Context, []byte) string { return "" }

// Plan supplies an empty mapping with the retained fixture execution context.
func (p *keyLimitEntry) Plan(ctx context.Context, _ string) (context.Context, routing.GroupMappingResult) {
	return ctx, routing.GroupMappingResult{}
}

// ImageIntent keeps request and model unchanged while reporting no image
// intent.
func (p *keyLimitEntry) ImageIntent(m string, b []byte, _ routing.GroupMappingResult) ([]byte, string, bool) {
	return b, m, false
}

// ExplicitImage reports ordinary text intent for all fixture request payloads.
func (p *keyLimitEntry) ExplicitImage(string, []byte) bool { return false }

// AcquireUser returns successful user admission with a no-op release callback.
// Entry can invoke that cleanup through its normal resource paths; this fixture
// counts Key reservations separately from user slots.
func (p *keyLimitEntry) AcquireUser(context.Context) (func(), bool, error) {
	return func() {}, true, nil
}

// AcquireProvider returns successful provider admission with a no-op release
// callback. Entry can invoke that callback on completion or replacement while
// assertions count the independent Key reservation lifetime.
func (p *keyLimitEntry) AcquireProvider(context.Context, int64, int) (func(), bool, error) {
	return func() {}, true, nil
}

// WrapRelease returns the supplied release callback for direct entry cleanup.
// The context adds no cancellation callback in this fixture, so the scripted
// path observes cleanup through entry's own release calls.
func (p *keyLimitEntry) WrapRelease(_ context.Context, f func()) func() { return f }

// LoadSubscription performs no funding setup because the fixture supplies
// unconditional eligibility.
func (p *keyLimitEntry) LoadSubscription() {}

// Platform selects OpenAI so entry follows the fixture's ordinary compatible
// text path.
func (p *keyLimitEntry) Platform() string { return "openai" }

// Eligibility admits initial fixture funding without introducing unrelated
// service dependencies.
func (p *keyLimitEntry) Eligibility(context.Context) error { return nil }

// AuthorizeTurn counts later-turn verification and admits the fixture's
// unchanged identity.
func (p *keyLimitEntry) AuthorizeTurn(context.Context) error {
	p.authorized++
	return nil
}

// SessionHash returns empty so the fixture supplies no scheduler session
// affinity. Selection ignores affinity inputs and follows the scripted target,
// letting assertions observe Key admission across provider execution.
func (p *keyLimitEntry) SessionHash([]byte, string) string { return "" }

// ExplicitHash returns empty so the fixture supplies no explicit session
// identity. Entry then skips its explicit-session isolation call, allowing these
// scenarios to exercise admission with the available dependency stubs.
func (p *keyLimitEntry) ExplicitHash([]byte) string { return "" }

// Guardian keeps the fixture execution context unchanged for provider
// selection.
func (p *keyLimitEntry) Guardian(ctx context.Context, _ []byte, _ string) context.Context { return ctx }

// BindSticky accepts fixture affinity writes without invoking an external
// session store.
func (p *keyLimitEntry) BindSticky(context.Context, string, int64) error { return nil }

// ClearCyber performs no cleanup because fixture requests publish no risk
// marker.
func (p *keyLimitEntry) ClearCyber() {}

// Close records the selected status for assertions on later-turn rejection.
func (p *keyLimitEntry) Close(status int, _ string) { p.closed = status }

// SessionPreempted excludes replacement-session handling from these scripted
// Key admission scenarios.
func (p *keyLimitEntry) SessionPreempted(error) bool { return false }

// LocalPolicyError excludes reasoning policy classification from these scripted
// Key admission scenarios.
func (p *keyLimitEntry) LocalPolicyError(error) bool { return false }

// EndedByClient leaves scripted execution failures for the fixture's explicit
// failover classification.
func (p *keyLimitEntry) EndedByClient(error) bool { return false }

// ReportFailure disables provider health callbacks so assertions isolate Key
// reservation behavior.
func (p *keyLimitEntry) ReportFailure(error) bool { return false }

// CloseInfo recognizes the fixture's Key RPM failure as a 1013 close
// instruction. Other errors return no instruction, keeping this stub specific
// to the later-turn allowance-rejection path.
func (p *keyLimitEntry) CloseInfo(err error) EntryClose {
	if errors.Is(err, apikey.ErrKeyRPMExceeded) {
		return EntryClose{Status: 1013, Reason: err.Error(), Present: true}
	}
	return EntryClose{}
}

// AcquireKey records a successful Key reservation or rejects the second
// RPM-mode admission. It asserts that no earlier turn still owns a slot before
// incrementing acquired and active.
//
// Each accepted reservation carries a requestcontext abort signal, retained in
// abortLease for the lost-lease scenario. Its release decrements active and
// signals that reservation's abort. The fixture lets entry detach normal
// downstream lifetime while separately observing internal lease loss;
// resource-count assertions establish whether retries retained a reservation or
// later turns acquired a fresh one.
func (p *keyLimitEntry) AcquireKey(ctx context.Context) (context.Context, func(), error) {
	if p.mode == "rpm" && p.acquired == 1 {
		return ctx, nil, apikey.ErrKeyRPMExceeded
	}
	require.Zero(p.t, p.active, "上一轮完成后才能取得下一轮的 Key 槽")
	p.acquired++
	p.active++
	// Attach an internal abort separately from client cancellation so the detached
	// bridge fixture can distinguish normal disconnect from a lost Key lease.
	ctx, abort := requestcontext.WithAbort(ctx)
	p.abortLease = abort
	return ctx, func() {
		p.active--
		abort()
	}, nil
}

// Select constructs provider-one selection marked acquired and returns the
// shared scripted target. Its slot release is a no-op, and supplied exclusion
// or affinity inputs do not alter fixture selection.
func (p *keyLimitEntry) Select(context.Context, string, string, string, map[int64]struct{}, bool, bool, string) (*EntrySelection, EntryDecision, error) {
	return &EntrySelection{Provider: &EntryProvider{ProviderSnapshot: provider.ProviderSnapshot{ID: 1, Concurrency: 1, Type: "apikey"}}, Acquired: true, ReleaseFunc: func() {}, Target: p.target}, EntryDecision{}, nil
}

// Failover recognizes the retry sentinel and permits replacement without
// provider-failure feedback. Other errors return false; this fixture drives
// entry's reservation retention using only its first-attempt scripted retry.
func (p *keyLimitEntry) Failover(err error) (*EntryFailure, bool) {
	if errors.Is(err, errKeyLimitRetry) {
		return &EntryFailure{Err: err, RetryNext: true}, true
	}
	return nil, false
}

// Credential admits fixture provider execution without requiring a stored
// access token.
func (*keyLimitTarget) Credential(context.Context) error { return nil }

// EnforceClient accepts the fixture's request without introducing provider
// client-policy checks.
func (*keyLimitTarget) EnforceClient(context.Context, []byte) error { return nil }

// BeginPreemption returns the supplied context with ownership unarmed for
// these admission fixtures. False tells entry to skip adopting a replacement
// context or invoking the returned nil cleanup callback.
func (*keyLimitTarget) BeginPreemption(ctx context.Context, _ []byte) (context.Context, func(), bool) {
	return ctx, nil, false
}

// RecordMarked reports no handled risk marker for the fixture's ordinary turn
// outcome.
func (*keyLimitTarget) RecordMarked(context.Context, string, bool, []byte, routing.PricingUsageFields, string) bool {
	return false
}

// Switched ignores scheduler statistics while the fixture counts actual
// execution attempts.
func (*keyLimitTarget) Switched() {}

// Stop429 allows the fixture's retry path to continue within its configured
// switch budget.
func (*keyLimitTarget) Stop429(int, int, *failover.OAuth429State) bool { return false }

// LogFailure discards diagnostics while the fixture asserts entry's slot and
// callback state.
func (*keyLimitTarget) LogFailure(error) {}

// Run drives entry callbacks with the Key reservation held for the selected
// fixture mode. It counts execution and checks that first-turn admission
// retains one active slot.
//
// Retry mode reports an error capture but keeps the reservation for a
// replacement attempt. Local warmup completes without later reauthentication,
// and ordinary modes finish one turn then attempt another. The
// disconnected-client case uses synctest to observe detached work remaining
// live, while lost-lease mode waits for that detached context's internal abort.
// These paths assert counters and callback-visible state rather than performing
// upstream transport I/O.
func (target *keyLimitTarget) Run(ctx context.Context, _ ClientSocket, _ []byte, hooks *EntryHooks) error {
	p := target.root
	p.attempts++
	require.Equal(p.t, 1, p.active)
	require.NoError(p.t, hooks.BeforeTurn(1))
	// Cancel only the client lifetime after detaching upstream work, then wait for
	// scheduling to expose accidental propagation into the detached execution
	// context.
	if p.mode == "client_disconnect" {
		detached := requestcontext.Detach(ctx)
		p.cancelClient()
		synctest.Wait()
		require.NoError(p.t, detached.Err(), "客户端断连后继续收集 HTTP 上游结果")
		return nil
	}
	// Abort the admitted reservation rather than the client, verifying the
	// internal lease signal still reaches work detached from client cancellation.
	if p.mode == "lost_lease" {
		detached := requestcontext.Detach(ctx)
		p.abortLease()
		select {
		case <-detached.Done():
		// Bound the fixture wait for internal abort delivery.
		case <-time.After(time.Second):
			p.t.Fatal("HTTP 桥接未收到租约失效信号")
		}
		return detached.Err()
	}

	if p.mode == "local_warmup" {
		hooks.AfterTurn(TurnCapture{Turn: 1, Result: &ForwardResult{LocalWarmup: true, RequestID: "resp_warmup"}})
		require.Zero(p.t, p.active)
		return nil
	}
	// Report a retryable first attempt while the logical turn remains unfinished,
	// making the next selected provider observe the same held Key reservation.
	if p.mode == "retry" && p.attempts == 1 {
		hooks.AfterTurn(TurnCapture{Turn: 1, Err: errKeyLimitRetry})
		require.Equal(p.t, 1, p.active, "故障转移期间持有本轮预占")
		return errKeyLimitRetry
	}
	hooks.AfterTurn(TurnCapture{Turn: 1})
	require.Zero(p.t, p.active, "空闲连接应释放 Key 槽")
	require.NoError(p.t, ctx.Err(), "正常释放一轮不能取消会话")
	if p.mode == "retry" {
		return nil
	}
	if err := hooks.BeforeTurn(2); err != nil {
		return err
	}
	require.Equal(p.t, 1, p.active)
	hooks.AfterTurn(TurnCapture{Turn: 2})
	require.Zero(p.t, p.active)
	return nil
}

// TestEntryKeyLimits exercises two-turn admission, later RPM denial, first-turn
// provider retry, lease abort, and local warmup. It checks no final held Key
// slot, expected reservation and attempt counts, selected close status, and
// later-turn authorization counts.
func TestEntryKeyLimits(t *testing.T) {
	for _, mode := range []string{"two_turns", "rpm", "retry", "lost_lease", "local_warmup"} {
		t.Run(mode, func(t *testing.T) {
			p := &keyLimitEntry{t: t, mode: mode}
			p.target = &keyLimitTarget{root: p}
			RunEntry(context.Background(), p, EntryInput{Key: &EntryKey{ID: 1}, MaxProviderSwitches: 1}, nil, []byte(`{"model":"test","type":"response.create"}`))
			require.Zero(t, p.active)
			if mode == "two_turns" {
				require.Equal(t, 2, p.acquired)
			} else {
				require.Equal(t, 1, p.acquired)
			}
			if mode == "rpm" {
				require.Equal(t, 1013, p.closed)
			}
			if mode == "retry" {
				require.Equal(t, 2, p.attempts)
			} else if mode == "local_warmup" {
				require.Zero(t, p.authorized)
			} else if mode != "lost_lease" {
				require.Equal(t, 1, p.authorized)
			}
		})
	}
}

// TestEntryClientDisconnectKeepsUpstream cancels the client while fixture
// execution observes a detached upstream context. The detached work remains
// live after synctest scheduling, and final entry cleanup releases the active
// Key reservation.
func TestEntryClientDisconnectKeepsUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		p := &keyLimitEntry{t: t, mode: "client_disconnect", cancelClient: cancel}
		p.target = &keyLimitTarget{root: p}
		RunEntry(ctx, p, EntryInput{Key: &EntryKey{ID: 1}}, nil, []byte(`{"model":"test","type":"response.create"}`))
		require.Zero(t, p.active)
	})
}

// Select records image requirements and exposes a target that checks snapshot reuse.
// The returned slot is acquired with a no-op cleanup, matching the base fixture's
// admission behavior while capturing the actual selection context.
func (p *imageSelectionEntry) Select(ctx context.Context, _ string, _ string, _ string, _ map[int64]struct{}, _ bool, _ bool, _ string) (*EntrySelection, EntryDecision, error) {
	requirements := media.ResponsesImageSelectionFromContext(ctx)
	require.Equal(p.t, []string{"canvas"}, requirements.Models)
	require.True(p.t, requirements.Codex)
	p.selected = routing.ModelAttributesFromContext(ctx)
	target := &imageSelectionTarget{keyLimitTarget: p.target, entry: p}
	return &EntrySelection{Provider: &EntryProvider{ProviderSnapshot: provider.ProviderSnapshot{ID: 1, Concurrency: 1, Type: "apikey"}}, Acquired: true, ReleaseFunc: func() {}, Target: target}, EntryDecision{}, nil
}

// Run verifies the selected turn snapshot before ending the fixture connection.
// No upstream operation occurs; RunEntry retains responsibility for releasing
// the request and provider reservations acquired before invoking this method.
func (p *imageSelectionTarget) Run(ctx context.Context, _ ClientSocket, _ []byte, hooks *EntryHooks) error {
	require.Same(p.root.t, p.entry.selected, routing.ModelAttributesFromContext(hooks.ContextForTurn(1)))
	return nil
}

// TestEntryImageSelectionSharesTurnAttributes supplies a hosted image request
// and a connection-level attribute session. Selection and forwarding must share
// the renewed turn snapshot, and connection cleanup releases every held slot.
func TestEntryImageSelectionSharesTurnAttributes(t *testing.T) {
	root := routing.WithModelAttributes(context.Background(), nil)
	base := &keyLimitEntry{t: t}
	base.target = &keyLimitTarget{root: base}
	entry := &imageSelectionEntry{keyLimitEntry: base}
	RunEntry(root, entry, EntryInput{Key: &EntryKey{ID: 1}, Codex: true}, nil, []byte(`{"model":"driver","type":"response.create","tools":[{"type":"image_generation","model":"canvas"}]}`))
	require.NotNil(t, entry.selected)
	require.NotSame(t, routing.ModelAttributesFromContext(root), entry.selected)
	require.Zero(t, base.active)
}

// TestRunEntryRejectsDuplicateAndCaseVariantFirstFrame submits duplicate model
// keys and uppercase Model variants as initial WebSocket payloads. RunEntry
// must close with status 1008 before acquiring slots or selecting a provider.
func TestRunEntryRejectsDuplicateAndCaseVariantFirstFrame(t *testing.T) {
	for _, firstFrame := range []string{
		`{"model":"gpt-5-mini","type":"response.create","model":"gpt-5.4"}`,
		`{"model":"gpt-5-mini","Model":"gpt-5.4","type":"response.create"}`,
	} {
		p := &keyLimitEntry{t: t}
		p.target = &keyLimitTarget{root: p}
		RunEntry(context.Background(), p, EntryInput{Key: &EntryKey{ID: 1}}, nil, []byte(firstFrame))
		require.Equal(t, 1008, p.closed, firstFrame)
		require.Zero(t, p.acquired, firstFrame)
		require.Zero(t, p.attempts, firstFrame)
	}
}
