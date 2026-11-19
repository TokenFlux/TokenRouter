package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// completion_capture.go captures completion inputs and associates billing
// identities with diagnostic request records.
// Completion can run after the HTTP request or WS turn has ended, so capture
// resolves usage, payer, provider, and pricing evidence synchronously before a
// worker receives it. Dedicated snapshot helpers copy the mutable fields that
// worker normalization uses. Callers serialize source writers with this
// capture phase.
//
// Financial deduplication and diagnostic correlation can select different IDs.
// Ordinary telemetry seeds feed the shared billing-key resolver, while
// SettlementKey applies WS or video operation overrides. Related video
// completions derive stable diagnostic records from Key and financial
// identity; ordinary completions update the active request and its aliases.
// Requested reasoning is extracted from original bytes before capture returns,
// and a WS turn can retain its start time for price selection.

// MessagesCapture supplies request-owned inputs for synchronous Messages
// completion snapshot capture.
type MessagesCapture struct {
	// Result, APIKey, User, and Provider are read during synchronous capture.
	// Callers serialize source mutation until capture has copied worker-owned data;
	// returning from capture permits submitting that snapshot to asynchronous completion.
	//
	// forwarding result usage evidence
	Result *forwardcore.MessagesResult
	// admitted request API Key
	APIKey *apikey.APIKey
	// completion payer user input
	User *identity.User
	// executed provider configuration input
	Provider *provider.Record
	// optional subscription pricing evidence
	Subscription *billing.UserSubscription
	// client request endpoint path
	InboundEndpoint string
	// normalized upstream endpoint path
	UpstreamEndpoint string
	// client request User-Agent text
	UserAgent string
	// observed client IP address
	IPAddress string
	// explicit client session correlation ID
	ClientSessionID string
	// semantic request payload digest
	RequestPayloadHash string
	// RequestBody is read synchronously for requested reasoning effort. Keep these
	// bytes stable during capture; they are not retained by the completion snapshot.
	RequestBody       []byte // original requested reasoning body
	ForceCacheBilling bool   // requested cache-read billing override
	// APIKeyService is checked for interface presence and is never invoked during
	// capture; an interface containing a typed nil still records QuotaUpdates=true.
	APIKeyService QuotaUpdater // supplied Key quota-update capability

	// Group mappings resolved by the handler before forwarding.
	routing.PricingUsageFields
}

// QuotaUpdater identifies a supplied API Key quota-invalidation capability
// during completion capture.
type QuotaUpdater = completion.AuthInvalidator

// OpenAICapture supplies request or WS-turn observations for synchronous
// completion capture.
type OpenAICapture struct {
	// Result, APIKey, User, and Provider are read during synchronous capture.
	// Callers serialize source mutation until capture has copied worker-owned data;
	// returning from capture permits submitting that snapshot to asynchronous completion.
	//
	// forwarding result usage evidence
	Result *forwardcore.OpenAIResult
	APIKey *apikey.APIKey // admitted request API Key
	// completion payer user input
	User *identity.User
	// executed provider configuration input
	Provider *provider.Record
	// optional subscription pricing evidence
	Subscription *billing.UserSubscription
	// client request endpoint path
	InboundEndpoint string
	// normalized upstream endpoint path
	UpstreamEndpoint string
	// client request User-Agent text
	UserAgent string
	// observed client IP address
	IPAddress string
	// explicit client session correlation ID
	ClientSessionID string
	// semantic request payload digest
	RequestPayloadHash string
	// RequestBody is read synchronously for requested reasoning effort. Keep these
	// bytes stable during capture; they are not retained by the completion snapshot.
	RequestBody []byte // original requested reasoning body
	// PricingAt supplies the WS turn start for time-sensitive prices; zero makes
	// completion use its recording time, as ordinary HTTP capture callers intend.
	PricingAt time.Time
	// APIKeyService is checked for interface presence and is never invoked during
	// capture; an interface containing a typed nil still records QuotaUpdates=true.
	APIKeyService QuotaUpdater // supplied Key quota-update capability
	CyberBlocked  bool         // request cyber failure marker
	// NativeCompactionV2 reports whether runtime inspection identified native remote compaction v2.
	NativeCompactionV2 bool // native compaction v2 observation
	routing.PricingUsageFields
}

// CyberCapture supplies cyber-failure usage and request identity for
// completion recording.
type CyberCapture struct {
	APIKey *apikey.APIKey // admitted request API Key
	// executed provider configuration input
	Provider *provider.Record
	// optional subscription pricing evidence
	Subscription *billing.UserSubscription
	// observed upstream operation identity
	RequestID string
	// trimmed cyber billing model
	Model string
	// request streaming mode flag
	Stream bool
	// observed aggregate input tokens
	InputTokens int
	// observed aggregate output tokens
	OutputTokens int
	// client request endpoint path
	InboundEndpoint string
	// normalized upstream endpoint path
	UpstreamEndpoint string
	// client request User-Agent text
	UserAgent string
	// observed client IP address
	IPAddress string
	// explicit client session correlation ID
	ClientSessionID string
	// semantic request payload digest
	RequestPayloadHash string
	// APIKeyService is checked for interface presence and is never invoked during
	// capture; an interface containing a typed nil still records QuotaUpdates=true.
	APIKeyService QuotaUpdater // supplied Key quota-update capability

	// NativeCompactionV2 carries the native compaction flag through failure handling.
	NativeCompactionV2 bool // native compaction v2 observation
	routing.PricingUsageFields
}

// CompletionBillingKey delegates billing deduplication selection using context
// request seeds and the upstream operation ID. Recognized operation prefixes
// win; the shared resolver supplies compatibility/local namespaces or a
// generated key when evidence is absent.
func CompletionBillingKey(ctx context.Context, upstreamRequestID string) string {
	return completion.ResolveBillingKey(RequestIdentity(ctx, upstreamRequestID, ""), scheduler.GenerateRequestID)
}

// StableAudioBillingRequestID creates the stable Grok audio operation key from
// a trimmed upstream ID. Existing audio prefixes survive, and blank input
// generates a new ID that callers retain for subsequent completion.
func StableAudioBillingRequestID(upstreamRequestID string) string {
	return completion.StableAudioRequestID(upstreamRequestID, scheduler.GenerateRequestID)
}

// StableRealtimeBillingRequestID creates a stable Grok Realtime operation key
// from a trimmed session ID. Existing prefixes survive, while blank input
// generates an ID that callers retain across that session's completion work.
func StableRealtimeBillingRequestID(sessionID string) string {
	return completion.StableRealtimeRequestID(sessionID, scheduler.GenerateRequestID)
}

// CompletionPayloadFingerprint selects supplied semantic payload evidence or a
// prefixed context request seed. The shared resolver can return empty when
// only an upstream operation ID is available, keeping identity distinct from
// payload evidence.
func CompletionPayloadFingerprint(ctx context.Context, requestPayloadHash string) string {
	return completion.PayloadFingerprint(RequestIdentity(ctx, "", requestPayloadHash))
}

// CaptureMessages extracts Messages result, payer, Key, provider, and pricing
// fields before returning a worker snapshot. Nil input returns nil; callers
// serialize capture with source mutations, and supplied quota capability is
// recorded by interface presence without invocation.
func CaptureMessages(ctx context.Context, in *MessagesCapture) *completion.Input {
	if in == nil {
		return nil
	}
	out := &completion.Input{
		Result:             ProjectMessagesCompletionResult(in.Result, in.Provider),
		APIKey:             ProjectCompletionKey(in.APIKey),
		User:               ProjectCompletionPayer(in.User),
		Provider:           ProjectCompletionProvider(in.Provider),
		Subscription:       in.Subscription,
		InboundEndpoint:    in.InboundEndpoint,
		UpstreamEndpoint:   in.UpstreamEndpoint,
		UserAgent:          in.UserAgent,
		IPAddress:          in.IPAddress,
		ClientSessionID:    in.ClientSessionID,
		RequestPayloadHash: CompletionPayloadFingerprint(ctx, in.RequestPayloadHash),

		ForceCacheBilling:  in.ForceCacheBilling,
		QuotaUpdates:       in.APIKeyService != nil,
		PricingUsageFields: in.PricingUsageFields,
	}
	if out.Result != nil && out.Result.Timings == nil {
		if observed := timing.PerformanceFromContext(ctx); observed != nil {
			out.Result.Timings = observed
		}
	}
	if in.Result != nil {
		captureRequestIdentity(
			ctx, out, in.Result.RequestID, in.Result.UpstreamHeaders,
			provider.UpstreamRequestIDHeaderName(in.Provider), telemetry.RequestAttemptRef{},
		)
		out.RequestedReasoningEffort = requeststate.CanonicalRequestedReasoningEffort(in.RequestBody)
	}
	return completion.Snapshot(out)
}

// CaptureOpenAI extracts result, payer, pricing time, request reasoning, and
// provider fields before returning a completion snapshot. Nil input returns
// nil; native usage can enable context force-cache billing, and WS PricingAt
// remains the supplied turn time.
func CaptureOpenAI(ctx context.Context, in *OpenAICapture) *completion.Input {
	if in == nil {
		return nil
	}
	out := &completion.Input{
		ForceCacheBilling:  in.Result != nil && in.Result.NativeUsage != nil && requeststate.IsForceCacheBilling(ctx),
		Result:             ProjectOpenAICompletionResult(in.Result, in.Provider),
		APIKey:             ProjectCompletionKey(in.APIKey),
		User:               ProjectCompletionPayer(in.User),
		Provider:           ProjectCompletionProvider(in.Provider),
		Subscription:       in.Subscription,
		InboundEndpoint:    in.InboundEndpoint,
		UpstreamEndpoint:   in.UpstreamEndpoint,
		UserAgent:          in.UserAgent,
		IPAddress:          in.IPAddress,
		ClientSessionID:    in.ClientSessionID,
		RequestPayloadHash: CompletionPayloadFingerprint(ctx, in.RequestPayloadHash),

		QuotaUpdates:             in.APIKeyService != nil,
		CyberBlocked:             in.CyberBlocked,
		NativeCompactionV2:       in.NativeCompactionV2,
		PricingAt:                in.PricingAt,
		PricingUsageFields:       in.PricingUsageFields,
		RequestedReasoningEffort: requeststate.CanonicalRequestedReasoningEffort(in.RequestBody),
	}
	if out.Result != nil && out.Result.Timings == nil {
		if observed := timing.PerformanceFromContext(ctx); observed != nil {
			out.Result.Timings = observed
		}
	}
	if in.Result != nil {
		captureRequestIdentity(
			ctx, out, in.Result.RequestID, in.Result.UpstreamHeaders,
			provider.UpstreamRequestIDHeaderName(in.Provider), in.Result.RequestAttempt,
		)
	}
	return completion.Snapshot(out)
}

// RequestIdentity captures context request seeds plus the supplied upstream ID
// and payload digest. Nil context leaves context seeds empty;
// telemetry.ClientRequestID and RequestID currently use the same compatibility
// context key, while upstream/payload stay separate.
func RequestIdentity(ctx context.Context, upstream, payload string) completion.RequestIdentity {
	out := completion.RequestIdentity{Upstream: upstream, PayloadHash: payload}
	if ctx != nil {
		out.Client, _ = ctx.Value(telemetry.ClientRequestID).(string)
		out.Local, _ = ctx.Value(telemetry.RequestID).(string)
	}
	return out
}

// CaptureCyber builds a cyber-blocked completion input when Key, Key.User,
// provider, and trimmed model are present. Missing requirements return nil;
// the generated forwarding result carries observed tokens and uses the Key
// user as the payer input.
func CaptureCyber(ctx context.Context, in CyberCapture) *completion.Input {
	if in.APIKey == nil || in.APIKey.User == nil || in.Provider == nil || strings.TrimSpace(in.Model) == "" {
		return nil
	}
	result := &forwardcore.OpenAIResult{
		RequestID: in.RequestID,
		Model:     strings.TrimSpace(in.Model),
		Usage: openai.ForwardUsage{
			InputTokens:  in.InputTokens,
			OutputTokens: in.OutputTokens,
		},
		Stream:   in.Stream,
		Duration: 0,
	}
	return CaptureOpenAI(ctx, &OpenAICapture{
		Result:             result,
		APIKey:             in.APIKey,
		User:               in.APIKey.User,
		Provider:           in.Provider,
		Subscription:       in.Subscription,
		InboundEndpoint:    in.InboundEndpoint,
		UpstreamEndpoint:   in.UpstreamEndpoint,
		UserAgent:          in.UserAgent,
		IPAddress:          in.IPAddress,
		ClientSessionID:    in.ClientSessionID,
		RequestPayloadHash: in.RequestPayloadHash,
		// APIKeyService is checked for interface presence and is never invoked during
		// capture; an interface containing a typed nil still records QuotaUpdates=true.
		APIKeyService: in.APIKeyService,

		PricingUsageFields: in.PricingUsageFields,
		CyberBlocked:       true,
		NativeCompactionV2: in.NativeCompactionV2,
	})
}

// captureRequestIdentity binds completion to its financial and diagnostic identities.
// BillingKey and RequestID retain their existing settlement and logical-request
// rules. Call while the forwarding result and provider selection remain stable;
// headers are read synchronously before the worker snapshot is constructed.
//
// Media results carry their immutable HTTP dispatch reference through completion.
// An exact provider and attempt match owns header observations even when the
// selected response header is missing or reused. A missing attempt
// is constructed only for an otherwise empty nonchild record; ambiguous retries
// are never attributed to the latest exchange. The selected diagnostic header is
// independent of the configured usage field, and parsed response IDs enter their
// own alias category. Related tasks retain separate records and financial aliases.
func captureRequestIdentity(
	ctx context.Context,
	out *completion.Input,
	upstreamID string,
	headers http.Header,
	headerName string,
	dispatch telemetry.RequestAttemptRef,
) {
	out.BillingKey = CompletionBillingKey(ctx, upstreamID)
	out.BillingKey = out.SettlementKey()
	out.RequestID = telemetry.RequestIDValue(ctx)
	// Video polling and missing diagnostic IDs derive a stable related ID from
	// Key identity and settlement key, so repeated financial operations correlate to one record.
	if out.RequestID == "" || out.Result.VideoCount > 0 {
		var keyID int64
		if out.APIKey != nil {
			keyID = out.APIKey.ID
		}
		out.RequestID = telemetry.StableRequestID(fmt.Sprintf("%d/%s", keyID, out.BillingKey))
	}
	selected := telemetry.RequestProvider(ctx)
	if out.Provider != nil && selected.ProviderID != out.Provider.ID {
		selected = telemetry.RequestProvider(telemetry.WithRequestProvider(ctx, out.Provider.ID, headerName))
	}
	responseID := out.Result.ResponseID
	if out.Result.OpenAIWSMode && responseID == "" {
		responseID = out.Result.RequestID
	}
	if out.RequestID != telemetry.RequestIDValue(ctx) && out.Provider != nil && dispatch.ProviderID == out.Provider.ID && dispatch.Number > 0 {
		// Video billing publishes a related logical record. Finish its parent exchange
		// through the captured dispatch while preserving any already recorded failure.
		telemetry.UpdateRequest(ctx, func(record *telemetry.RequestRecord) {
			for i := range record.Attempts {
				attempt := &record.Attempts[i]
				if attempt.Number == dispatch.Number && attempt.ProviderID == dispatch.ProviderID && attempt.Outcome == "running" {
					attempt.Outcome = "completed"
				}
			}
		})
	}
	childRequest := telemetry.IsChildRequest(ctx)
	if out.RequestID == telemetry.RequestIDValue(ctx) {
		var ref telemetry.RequestAttemptRef
		if out.Provider != nil {
			number := 0
			if out.Result.Timings != nil {
				number = int(out.Result.Timings.Values["attempt_number"])
			}
			if dispatch.Number > 0 && dispatch.ProviderID == out.Provider.ID {
				// Media results retain their actual dispatch even when the chosen ID is
				// missing or reused, so completion does not infer ownership from header values.
				ref = telemetry.FindRequestAttempt(ctx, dispatch.Number, dispatch.ProviderID)
			} else {
				ref = telemetry.FindRequestAttempt(ctx, number, out.Provider.ID, headers)
			}
		}
		telemetry.UpdateRequest(ctx, func(record *telemetry.RequestRecord) {
			record.Model = out.Result.Model
			if out.Provider == nil {
				return
			}
			record.ProviderID = out.Provider.ID
			record.Platform = out.Provider.Platform
			// Completion can construct a diagnostic attempt for adapters without a
			// dispatch observer. Existing ambiguous attempts are never guessed or replaced.
			if ref.Number == 0 && len(record.Attempts) == 0 && !childRequest {
				ref = selected
				ref.Number = 1
				record.Attempts = append(record.Attempts, telemetry.RequestAttempt{Number: 1, ProviderID: ref.ProviderID, RequestIDHeader: ref.Header, Outcome: "completed"})
			}
			for i := range record.Attempts {
				attempt := &record.Attempts[i]
				if attempt.Number == ref.Number && attempt.ProviderID == ref.ProviderID && attempt.Outcome == "running" {
					attempt.Outcome = "completed"
				}
			}
		})
		if !out.Result.OpenAIWSMode {
			telemetry.ObserveRequestHeaders(ctx, ref, headers, 0)
		}
		if ref.ProviderID == 0 && out.Provider != nil {
			ref.ProviderID = out.Provider.ID
		}
		telemetry.AddRequestAlias(ctx, "billing", out.BillingKey)
		telemetry.ObserveResponseID(ctx, ref, responseID)
	}
	// A derived diagnostic ID needs its own completed record rather than rewriting
	// the active request record; carry settlement aliases and captured actor/provider identity.
	if out.RequestID != telemetry.RequestIDValue(ctx) {
		record := telemetry.RequestRecord{RequestID: out.RequestID, State: "completed", Model: out.Result.Model, StartedAt: out.PricingAt}
		// A task without an observed request start receives its capture time.
		// Upstream duration stays on usage and cannot reconstruct a request lifecycle.
		if record.StartedAt.IsZero() {
			record.StartedAt = time.Now()
		}
		record.Aliases = []telemetry.RequestAlias{{Kind: "billing", Value: out.BillingKey}}
		if id := strings.TrimSpace(headers.Get(selected.Header)); id != "" && !out.Result.OpenAIWSMode {
			record.Aliases = append(record.Aliases, telemetry.RequestAlias{Kind: "upstream_request", Value: id, Source: selected.Header, ProviderID: selected.ProviderID})
		}
		if responseID != "" {
			record.Aliases = append(record.Aliases, telemetry.RequestAlias{Kind: "response", Value: responseID, Source: "response.id", ProviderID: selected.ProviderID})
		}
		if out.APIKey != nil {
			record.UserID = out.APIKey.ActorUserID
			record.APIKeyID = out.APIKey.ID
			if out.APIKey.TeamID != nil {
				record.TeamID = *out.APIKey.TeamID
			}
		}
		if out.Provider != nil {
			record.ProviderID = out.Provider.ID
			record.Platform = out.Provider.Platform
		}
		telemetry.RecordRelatedRequest(ctx, record)
	}
}
