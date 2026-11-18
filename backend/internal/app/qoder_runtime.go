package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	openaiwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoder_runtime.go adapts Qoder Chat routing, selection, session recovery, and
// completion recording for the gateway executor. Qoder Chat selection builds an
// executable target together with its route, release, and wait callbacks. A
// refreshed session produces another target for the same selection, but its
// earlier acquired-slot flag and release callback are cleared because the prior
// attempt has already finished its resource lease. When no original wait plan
// exists, the refreshed target gets a thirty-second wait that bypasses queue
// counting.
//
// Each completion callback captures provider, Key, payer, usage, and price
// mapping before creating worker work. Pool submission follows that capture;
// absent pool runs recording inline with a separate ten-second budget. Sticky
// binding likewise has its own five-second cleanup budget and reports no
// binding error to the completed request.

// qoderRuntime combines shared Chat routing, selection, funding, execution, and
// completion dependencies.
type qoderRuntime struct {
	Choices *selection.Generic                   // generic provider selection
	Routes  *gatewayprovider.RoutePlanner        // Chat route planning
	Qoder   *gatewayprovider.QoderRuntime        // Qoder attempt execution
	Refresh *provideradapter.QoderRequestRefresh // session refresh adapter
	// Billing may be nil for focused fixtures. Check then succeeds immediately;
	// configured production admission uses this shared service for both initial and post-wait checks.
	Billing *admission.FundingAdmission
	Keys    *apikey.APIKeyService // completion quota effects
	// Completions selects queued recording when present. A nil pool runs the
	// captured task inline under its own ten-second context before the completion callback returns.
	Completions *completion.UsageRecordWorkerPool
	Recorder    *completion.Recorder // captured usage recording
}

// provideQoderRequestRefresh binds session recovery to the execution token
// provider and credential coordinator.
func provideQoderRequestRefresh(store *providerpostgres.ProviderStore, tokens *provideradapter.QoderTokenProvider, coordinator *providercore.OAuthRefreshAPI, transport provideradapter.QoderTransport, profiles *egressprovider.TLSProfiles) *provideradapter.QoderRequestRefresh {
	return &provideradapter.QoderRequestRefresh{Store: store, Tokens: tokens, Coordinator: coordinator, Transport: transport, Profiles: profiles}
}

// provideQoderRuntime constructs Qoder execution using shared tokens,
// transport, health, and attributes.
func provideQoderRuntime(tokens *provideradapter.QoderTokenProvider, transport provideradapter.QoderTransport, profiles *egressprovider.TLSProfiles, store *providerpostgres.ProviderStore, attributes *routing.ModelAttributeService) *gatewayprovider.QoderRuntime {
	return gatewayprovider.NewQoderRuntime(gatewayprovider.QoderRuntimeOptions{Attributes: attributes, Tokens: tokens, Transport: transport, Profiles: profiles, Health: store})
}

// Prepare plans Chat routing using a copied funding Key view. It retains
// request.Body as AttemptBody unless the route maps the model, in which case
// the OpenAI wire helper produces the mapped attempt bytes.
func (b *qoderRuntime) Prepare(ctx context.Context, request gateway.Request) (gateway.Request, error) {
	key := apikey.CopyAPIKey(request.Funding.Key)
	request.Route = b.Routes.PlanKey(ctx, key, request.Model).WithClientProtocol(protocol.ProtocolOpenAIChatCompletions)
	mapping := request.Route.Mapping()
	request.AttemptBody = request.Body
	if mapping.Mapped {
		request.AttemptBody = openaiwire.ReplaceModelInBody(request.Body, mapping.MappedModel)
	}
	return request, nil
}

// Check runs funding admission using a copied request Key and its captured
// subscription. Missing Billing succeeds immediately, while afterWait selects
// the admission service's funding-only recheck rather than another RPM count.
func (b *qoderRuntime) Check(ctx context.Context, request gateway.Request, afterWait bool) error {
	if b.Billing == nil {
		return nil
	}
	key := apikey.CopyAPIKey(request.Funding.Key)
	return b.Billing.CheckKey(ctx, key, request.Funding.Subscription, "", afterWait)
}

// Select builds a Qoder target from generic selection and the prepared Chat
// route. The caller supplies a usable funding Key, attempt body, and shared
// selection/runtime dependencies; selected callbacks retain the request's price
// mapping and original body for completion.
//
// Session refresh rebuilds execution and clears old acquisition ownership
// before another slot wait. Completion captures a separate recorder input
// before queued or inline work, and sticky binding uses an independent bounded
// context. Recorder and binding errors are reported through their callback
// policies rather than retriggering provider execution.
func (b *qoderRuntime) Select(ctx context.Context, request gateway.Request, excluded map[int64]struct{}) (*gateway.Selection, error) {
	ctx = requeststate.WithRoutePlan(ctx, request.Route)
	key := apikey.CopyAPIKey(request.Funding.Key)
	plan := request.Route
	mapping := plan.Mapping()
	body := request.AttemptBody
	var project func(*gatewayprovider.SelectionResult, *gatewayprovider.ExecutionProvider, bool) *gateway.Selection
	project = func(selection *gatewayprovider.SelectionResult, provider *gatewayprovider.ExecutionProvider, refresh bool) *gateway.Selection {
		executor, input := b.Qoder.PrepareQoderTarget(qoder.RequestMetadata{APIKeyID: key.ID, ClaudeCode: request.Metadata.ClaudeCode, Headers: http.Header(request.Metadata.Headers).Clone()}, gatewayprovider.ExecutionRecord(provider), body, protocol.ProtocolOpenAIChatCompletions, request.Model)
		snapshot := gatewayprovider.ExecutionSnapshot(provider)
		candidate, _ := plan.ResolveCandidate(snapshot)
		selected := &gateway.Selection{Snapshot: snapshot, Plan: candidate, Acquired: selection.Acquired, Release: selection.ReleaseFunc, WaitPlan: selection.WaitPlan, Executor: executor, Input: input}
		// The prior attempt has released its lease before refresh returns here.
		// Clear original acquisition ownership so another execution obtains a new slot through its wait plan.
		if refresh {
			selected.Acquired = false
			selected.Release = nil
			// A refreshed target without an original wait plan still needs provider
			// admission. Supply a bounded wait while bypassing the separate queue-counter claim for this retry.
			if selected.WaitPlan == nil {
				selected.WaitWithoutCounter = true
				selected.WaitPlan = &scheduler.ProviderWaitPlan{ProviderID: provider.Record.ID, MaxConcurrency: provider.Record.Concurrency, Timeout: 30 * time.Second, MaxWaiting: 0}
			}
		}
		selected.Observe = func(result upstream.AttemptResult, err error) {
			// A pre-dispatch validation failure has no provider observation to contribute
			// to the health store or to advanced scheduling success-rate samples.
			if gatewayprovider.IsLocalQoderRejection(err) {
				return
			}
			b.Qoder.ObserveQoderFailure(ctx, gatewayprovider.ExecutionRecord(provider), err)
			var legacy *forwardcore.MessagesResult
			if err == nil || result.Served && result.HasUsage {
				legacy = forwardcore.MessagesFromAttempt(result)
			}
			b.Choices.ReportAdvancedProviderScheduleResult(selection, provider.Record.ID, err == nil, legacy)
		}
		selected.Switched = func() { b.Choices.RecordAdvancedProviderSwitch(selection) }
		selected.Refresh = func(ctx context.Context) (*gateway.Selection, error) {
			updated, err := b.Refresh.RefreshProviderSession(ctx, gatewayprovider.ExecutionRecord(provider))
			if err != nil || updated == nil {
				return nil, err
			}
			return project(selection, gatewayprovider.NewExecutionProvider(updated), true), nil
		}
		selected.Bind = func(ctx context.Context, _ upstream.AttemptResult) {
			// Completed request cancellation need not prevent best-effort affinity binding.
			// Detach that signal and apply a five-second budget; the callback discards binding errors.
			bindCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = b.Choices.BindStickySession(bindCtx, key.GroupID, request.SessionHash, provider.Record.ID)
		}

		selected.Complete = func(callCtx context.Context, result upstream.AttemptResult) {
			// Capture request-owned provider and funding inputs before scheduling work.
			// The recorder task then reads that snapshot while Gin and the original request can finish.
			snapshot := gatewayprovider.CaptureMessages(callCtx, &gatewayprovider.MessagesCapture{
				Result: forwardcore.MessagesFromAttempt(result),
				APIKey: key, User: key.User, Provider: gatewayprovider.ExecutionCompletionRecord(provider), Subscription: request.Funding.Subscription,
				InboundEndpoint: request.Metadata.InboundEndpoint, UpstreamEndpoint: request.Metadata.UpstreamEndpoint,
				UserAgent: request.Metadata.UserAgent, IPAddress: request.Metadata.ClientIP,
				RequestPayloadHash: billing.HashUsageRequestPayload(request.Body), RequestBody: request.Body,
				APIKeyService: b.Keys, PricingUsageFields: mapping.ToUsageFields(request.Model, result.UpstreamModel),
			})
			task := completion.WrapTaskContext(callCtx, func(workerCtx context.Context) {
				// Recording work has no error result in this task callback. Report
				// settlement failure through diagnostics while retaining the captured request and provider attribution.
				if err := b.Recorder.Record(workerCtx, snapshot, false); err != nil {
					logging.LegacyPrintf("handler.qoder_gateway", "record usage failed provider=%d: %v", snapshot.Provider.ID, err)
				}
			})
			if b.Completions != nil {
				b.Completions.Submit(task)
				return
			}
			// With no worker pool, run the captured completion inline under a new
			// ten-second budget so request cancellation does not skip the recording callback altogether.
			completionCtx, cancel := context.WithTimeout(context.WithoutCancel(callCtx), 10*time.Second)
			defer cancel()
			task(completionCtx)
		}
		return selected
	}
	selection, err := b.Choices.SelectProviderWithLoadAwareness(ctx, key.GroupID, request.SessionHash, request.Model, excluded)
	if err != nil {
		return nil, err
	}
	return project(selection, selection.Provider, false), nil
}

// CanRefresh delegates session-refresh eligibility to Qoder's typed
// attempt-error policy.
func (b *qoderRuntime) CanRefresh(err error) bool { return qoder.MayRefreshAttempt(err) }

// CanFailover delegates provider-switch eligibility to Qoder's typed
// attempt-error policy.
func (b *qoderRuntime) CanFailover(err error) bool { return qoder.MaySwitchAttempt(err) }

// RefreshPending reports whether the error chain identifies an in-progress
// Qoder refresh.
func (b *qoderRuntime) RefreshPending(err error) bool {
	return errors.Is(err, providercore.ErrQoderRefreshInProgress)
}

// QueueFailure logs a user or provider waiting-counter failure for Qoder
// orchestration.
func (b *qoderRuntime) QueueFailure(kind string, err error) {
	logging.LegacyPrintf("handler.qoder_gateway", "%s wait counter failed: %v", kind, err)
}
