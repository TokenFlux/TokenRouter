package provider

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/opencode"
)

// execution_provider.go constructs execution targets and supplies provider
// views for routing, protocol handling, and completion.
// A selected provider has persistent configuration and a route resolved for
// one request attempt. ExecutionProvider keeps those values together, while
// the adapter functions choose which fields a protocol, scheduler, or
// completion consumer receives. Full-record conversions use the provider
// module's copying rules and attach the process clock and location loader.
// Reduced views borrow their nested values from the target.
//
// Keeping Record at the same address lets callbacks bound to the execution
// target observe later credential or configuration updates. Replacing Record
// leaves Route and Capabilities on that target intact; rebuilding a target
// starts those request-specific fields at their zero values. Callers
// synchronize mutation of targets and borrowed views.

// ExecutionProvider combines provider configuration with the route and
// optional capabilities resolved for a request attempt.
type ExecutionProvider struct {
	// Capabilities contains the resolution attached by request preparation;
	// ExecutionModelAttributes reuses it when its target model matches.
	Capabilities *modelrules.Resolution `json:"-"`
	// request provider configuration
	Record provider.Record `json:"-"`
	// request attempt route
	Route requeststate.AttemptRoute `json:"-"`
}

// BindExecutionHeaders returns a callback that applies the target's current
// provider header overrides to a writable header map. The callback rereads
// Record on each invocation, so callers synchronize target changes with header
// construction.
func BindExecutionHeaders(value *ExecutionProvider) func(http.Header) {
	return func(headers http.Header) {
		provideradapter.ApplyProviderHeaderOverrides(ExecutionProtocolRecord(value), headers)
	}
}

// FinalizeRequestHeaders installs the provider's final authentication and
// conversation identity after configured overrides. Callers validate targetURL
// before sending; read supplies inbound client headers and may be nil.
//
// An OpenCode provider owns its bearer key, agent, and session, so those
// replace whatever the protocol builder and overrides wrote. Other API-key
// providers addressed to HTTPS opencode.ai, such as an OpenAI relay pointed at
// that host, receive only the conversation ID; any other destination is left
// untouched so session data never reaches an untrusted origin.
func FinalizeRequestHeaders(value *ExecutionProvider, targetURL string, read func(string) string, headers http.Header) {
	if value == nil || headers == nil {
		return
	}
	if value.View().IsOpenCode() {
		opencode.ApplyHeaders(headers, value.View().GetCredential("api_key"), read)
		return
	}
	if value.Record.Type != capability.ProviderTypeAPIKey {
		return
	}
	parsed, err := url.Parse(targetURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "opencode.ai") {
		return
	}
	opencode.SetSession(headers, opencode.SessionID(read))
}

// BindExecutionHeaderValue returns a callback that looks up an override by
// header name in the target's current configuration. It rereads Record on
// every call; callers synchronize target changes with callback use.
func BindExecutionHeaderValue(value *ExecutionProvider) func(string) (string, bool) {
	return func(name string) (string, bool) {
		return provideradapter.HeaderOverrideValue(ExecutionProtocolRecord(value), name)
	}
}

// NewExecutionProvider copies a provider record into a request target using
// provider.CopyRecordInto and sets the process clock and location loader. Nil
// input returns nil; Route and Capabilities start unset.
func NewExecutionProvider(value *provider.Record) *ExecutionProvider {
	if value == nil {
		return nil
	}
	out := &ExecutionProvider{}
	provider.CopyRecordInto(&out.Record, value)
	out.Record.Now = time.Now
	out.Record.LoadLocation = time.LoadLocation
	return out
}

// View returns the embedded record at its existing address, or nil for a nil
// target. The record and its nested values remain owned by the target; callers
// synchronize reads and mutations.
func (value *ExecutionProvider) View() *provider.Record {
	if value == nil {
		return nil
	}
	return &value.Record
}

// ExecutionRecord returns a provider-module copy of the target's Record, or
// nil for a nil target. It uses CloneRecord's supported-container copying
// rules and supplies the process clock and location loader.
func ExecutionRecord(value *ExecutionProvider) *provider.Record {
	if value == nil {
		return nil
	}
	out := provider.CloneRecord(&value.Record)
	out.Now = time.Now
	out.LoadLocation = time.LoadLocation
	return out
}

// ApplyExecutionRecord copies provider fields into the existing Record address
// and installs the process clock and location loader. Nil arguments are
// ignored; the target's request Route and Capabilities remain available to
// bound callbacks and later execution.
func ApplyExecutionRecord(out *ExecutionProvider, value *provider.Record) {
	if out == nil || value == nil {
		return
	}
	provider.CopyRecordInto(&out.Record, value)
	out.Record.Now = time.Now
	out.Record.LoadLocation = time.LoadLocation
}

// ExecutionProviders converts provider records into execution targets using
// CopyRecordInto for each element. Nil input stays nil, empty input stays
// empty, and each target receives the process clock and location loader with
// an unset request route.
func ExecutionProviders(values []provider.Record) []ExecutionProvider {
	if values == nil {
		return nil
	}
	out := make([]ExecutionProvider, len(values))
	for i := range values {
		provider.CopyRecordInto(&out[i].Record, &values[i])
		out[i].Record.Now = time.Now
		out[i].Record.LoadLocation = time.LoadLocation
	}
	return out
}

// ExecutionRecords copies each execution Record into a provider-module slice
// using CopyRecordInto. Nil input stays nil, empty input stays empty, and
// every output record receives the process clock and location loader.
func ExecutionRecords(values []ExecutionProvider) []provider.Record {
	if values == nil {
		return nil
	}
	out := make([]provider.Record, len(values))
	for i := range values {
		provider.CopyRecordInto(&out[i], &values[i].Record)
		out[i].Now = time.Now
		out[i].LoadLocation = time.LoadLocation
	}
	return out
}

// ExecutionRecordPointers converts a target slice through ExecutionRecord,
// retaining nil input and nil elements. Each nonnil target receives the same
// supported-container copying and clock setup as an individual record
// conversion.
func ExecutionRecordPointers(values []*ExecutionProvider) []*provider.Record {
	if values == nil {
		return nil
	}
	out := make([]*provider.Record, len(values))
	for i, value := range values {
		out[i] = ExecutionRecord(value)
	}
	return out
}

// ExecutionModelPolicy combines a copied provider record with the target's
// current attempt route for model checks. A nil target produces a policy with
// both values unset; route assignment uses the route value as supplied.
func ExecutionModelPolicy(value *ExecutionProvider) ModelPolicy {
	out := ModelPolicy{Record: ExecutionRecord(value)}
	if value != nil {
		out.Route = value.Route
	}
	return out
}

// ExecutionProtocolRecord returns the platform, type, credentials, extras, and
// parent reference used by protocol policy. Nil targets return nil; nested
// values are borrowed from Record, so callers keep them stable while consumers
// read.
func ExecutionProtocolRecord(value *ExecutionProvider) *provider.Record {
	if value == nil {
		return nil
	}
	v := &value.Record
	return &provider.Record{Platform: v.Platform, Type: v.Type, Credentials: v.Credentials, Extra: v.Extra, ParentProviderID: v.ParentProviderID}
}

// ExecutionProtocolTarget combines the reduced provider view with the
// attempt's resolved upstream protocol. A nil target produces an empty target;
// the reduced record borrows credentials and extras from the execution target.
func ExecutionProtocolTarget(value *ExecutionProvider) provider.ProtocolTarget {
	out := provider.ProtocolTarget{Record: ExecutionProtocolRecord(value)}
	if value != nil {
		out.Protocol = value.Route.Protocol()
	}
	return out
}

// NormalizeExecutionProtocols applies provider protocol normalization to the
// reduced record and transfers its credentials and extras back to value. The
// transfer occurs even when normalization returns an error, retaining any
// partial changes made by that operation.
func NormalizeExecutionProtocols(value *ExecutionProvider) error {
	record := ExecutionProtocolRecord(value)
	err := provider.NormalizeProviderProtocols(record)
	if value != nil && record != nil {
		value.Record.Credentials = record.Credentials
		value.Record.Extra = record.Extra
	}
	return err
}

// ExecutionSnapshot builds the candidate snapshot used for protocol and model
// eligibility from the target's route and scheduling fields. Nil targets
// return an empty snapshot; candidate construction follows ModelPolicy's
// mapping and policy rules.
func ExecutionSnapshot(value *ExecutionProvider) provider.ProviderSnapshot {
	if value == nil {
		return provider.ProviderSnapshot{}
	}
	v := ExecutionProtocolRecord(value)
	v.ID = value.Record.ID
	v.Status = value.Record.Status
	v.Schedulable = value.Record.Schedulable
	v.Concurrency = value.Record.Concurrency
	v.Priority = value.Record.Priority
	v.ExpiresAt = value.Record.ExpiresAt
	return (ModelPolicy{Record: v, Route: value.Route}).CandidateSnapshot()
}

// ExecutionCandidatePlan returns the route's resolved candidate and its
// presence flag for selection capture. A nil target returns an empty plan with
// false, allowing consumers to distinguish an absent attempt route.
func ExecutionCandidatePlan(value *ExecutionProvider) (routing.CandidatePlan, bool) {
	if value == nil {
		return routing.CandidatePlan{}, false
	}
	return value.Route.Candidate()
}

// ExecutionRuntimeConfig returns the extras, concurrency, and session-window
// references used by provider runtime policy. The target must be nonnil;
// nested values are borrowed, so callers keep them stable throughout policy
// reads.
func ExecutionRuntimeConfig(value *ExecutionProvider) *provider.RuntimeConfig {
	v := &value.Record
	return &provider.RuntimeConfig{Extra: v.Extra, Concurrency: v.Concurrency, SessionWindowStart: v.SessionWindowStart, SessionWindowEnd: v.SessionWindowEnd}
}

// ExecutionCompletionRecord selects provider identity, credentials, extras,
// rate multiplier, and parent fields for completion capture. Nil targets
// return nil; nested values are borrowed until completion capture creates its
// worker-owned snapshot.
func ExecutionCompletionRecord(value *ExecutionProvider) *provider.Record {
	if value == nil {
		return nil
	}
	v := &value.Record
	return &provider.Record{ID: v.ID, Name: v.Name, Platform: v.Platform, Type: v.Type, Credentials: v.Credentials, Extra: v.Extra, RateMultiplier: v.RateMultiplier, ParentProviderID: v.ParentProviderID}
}

// ExecutionTLSSelection reads the target's TLS enablement and direct profile,
// then attaches the first router match when present. A nil target leaves
// direct settings empty, and later router matches are ignored.
func ExecutionTLSSelection(value *ExecutionProvider, routerMatch []egress.TLSFingerprintRouterMatchResult) egress.TLSSelection {
	selection := egress.TLSSelection{}
	if value != nil {
		selection.Enabled = value.View().IsTLSFingerprintEnabled()
		selection.DirectProfileID = value.View().GetTLSFingerprintProfileID()
	}
	if len(routerMatch) > 0 {
		selection.RouterMatched = routerMatch[0].Matched
		selection.RouterID = routerMatch[0].RouterID
		selection.RouterProfileID = routerMatch[0].TLSFingerprintProfileID
	}
	return selection
}
