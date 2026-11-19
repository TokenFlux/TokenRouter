package ws

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/pkg/requestcontext"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// entry.go orchestrates admission, provider attempts, and completion for
// upgraded Responses WebSocket connections. User and provider slots are
// released between turns, while the Key request reservation stays held across a
// classified retry of the same logical request and is released after that
// request finishes.
//
// RunEntry performs first-frame prompt, model, moderation, funding, and
// isolation checks before selecting a provider. Execution hooks reacquire
// later-turn resources, reauthenticate after user admission, preserve risk
// evidence by turn, and capture completion before background submission. A lost
// Key lease triggers an internal session abort that also reaches HTTP bridge
// work detached from ordinary client cancellation. Provider replacement retains
// request-record identity and requires a safe current-turn payload after a
// later failure; local policy and preemption exits keep their distinct close
// and health-reporting behavior.

// RunEntry performs upgraded connection admission, provider attempts, and
// per-turn completion through the supplied bindings. Callers supply a nonnil
// context, complete EntryKey, subject, and initialized ports after
// authenticating and reading the first client frame.
//
// Initial checks precede provider execution. The first turn owns user, Key, and
// provider reservations; later turns reauthenticate after user acquisition and
// reacquire their own slots. Classified provider retries retain the Key
// reservation and logical child request identity, release the old provider
// slot, and obey switch and OAuth-429 budgets. AfterTurn releases resources and
// captures accounting or risk outcomes, skipping normal submission for local
// warmup. Later-turn failover requires a self-contained retry payload so
// replacement cannot repeat the initial request.
func RunEntry(ctx context.Context, p EntryPorts, in EntryInput, client ClientSocket, firstMessage []byte) {
	// A lost Key turn lease signals the separate session abort, reaching HTTP
	// bridge work that detached itself from ordinary client cancellation.
	ctx, abortSession := requestcontext.WithAbort(ctx)
	defer abortSession()
	requestRecords := newEntryRequestRecords(ctx)
	defer requestRecords.close()
	apiKey, subject, reqLog := in.Key, in.Subject, p.Logger()
	clientLifecycleCtx, firstTurnStartedAt := in.ClientLifecycleContext, in.FirstTurnStartedAt
	var err error
	if err = wirejson.ValidateRequestObject(firstMessage); err != nil {
		p.Close(1008, "invalid JSON payload")
		return
	}
	// Apply user replacements before model parsing and moderation, then rewrite
	// additional tool models with Key rules so session hashing sees the same
	// accepted first request.
	firstMessage = p.Prompt(ctx, firstMessage)
	firstMessage, err = modeltrace.RewriteAPIKeyAdditionalModels(firstMessage, apiKey.ModelMapping)
	if err != nil {
		p.Close(1008, "invalid websocket tool model")
		return
	}

	reqModel := strings.TrimSpace(gjson.GetBytes(firstMessage, "model").String())
	if reqModel == "" {
		p.Close(1008, "model is required in first response.create payload")
		return
	}
	clientReqModel := reqModel
	telemetry.UpdateRequest(requestRecords.next, func(record *telemetry.RequestRecord) { record.Model = clientReqModel })
	ctx, reqModel = p.Redirect(ctx, clientReqModel)
	p.BindContext(ctx)
	previousResponseID := strings.TrimSpace(gjson.GetBytes(firstMessage, "previous_response_id").String())
	previousResponseIDKind := p.ClassifyPrevious(previousResponseID)
	if previousResponseID != "" && previousResponseIDKind == "message_id" {
		p.Close(1008, "previous_response_id must be a response.id (resp_*), not a message id")
		return
	}
	// Continuation can move between providers only when each supplied tool output
	// has its call context; otherwise that output still needs the prior upstream
	// chain.
	firstMessageToolCoverage := wire.AnalyzeToolCallOutputContextCoverageBytes(firstMessage)
	previousResponseCanMove := !firstMessageToolCoverage.HasFunctionCallOutput || firstMessageToolCoverage.ContextCoversAllCallIDs
	reqLog = reqLog.With(
		EntryBool("ws_ingress", true),
		EntryString("model", clientReqModel),
		EntryBool("has_previous_response_id", previousResponseID != ""),
		EntryString("previous_response_id_kind", previousResponseIDKind),
	)
	p.SetLogger(reqLog)
	p.ObserveFirst(clientReqModel)
	firstCyber := p.CaptureCyber(firstMessage)
	// Client-frame and upstream-event callbacks can run concurrently. Keep each
	// turn's excerpt and moderation input under one lock so warning capture reads
	// its selected turn's stored evidence.
	cyberPromptExcerptByTurn := map[int]string{}
	cyberSnapshotByTurn := map[int]moderation.ContentModerationInput{}
	var cyberPromptExcerptMu sync.RWMutex
	setCyberPromptExcerpt := func(turn int, promptExcerpt string, snapshot moderation.ContentModerationInput) {
		if turn <= 0 {
			return
		}
		cyberPromptExcerptMu.Lock()
		cyberPromptExcerptByTurn[turn] = strings.TrimSpace(promptExcerpt)
		cyberSnapshotByTurn[turn] = snapshot
		cyberPromptExcerptMu.Unlock()
	}
	getCyberSnapshot := func(turn int) moderation.ContentModerationInput {
		cyberPromptExcerptMu.RLock()
		defer cyberPromptExcerptMu.RUnlock()
		return cyberSnapshotByTurn[turn]
	}
	getCyberPromptExcerpt := func(turn int) string {
		cyberPromptExcerptMu.RLock()
		defer cyberPromptExcerptMu.RUnlock()
		return cyberPromptExcerptByTurn[turn]
	}
	clearCyberPromptExcerpt := func(turn int) {
		cyberPromptExcerptMu.Lock()
		delete(cyberPromptExcerptByTurn, turn)
		delete(cyberSnapshotByTurn, turn)
		cyberPromptExcerptMu.Unlock()
	}
	setCyberPromptExcerpt(1, firstCyber.Excerpt, firstCyber.Input)

	if decision := p.Moderate(ctx, reqModel, firstMessage); decision != nil && decision.Blocked {
		p.ModerationError(ctx, decision)
		p.Close(1008, decision.Message)
		return
	}
	// Check explicit and derived session blocks before provider admission, so a
	// previously blocked conversation ends before reserving upstream resources.
	if cyberBlockKey := p.BlockedSession(ctx, firstMessage); cyberBlockKey != "" {
		p.BlockedError(ctx)
		p.Close(1008, p.BlockedMessage())
		p.BlockedOps(reqModel, cyberBlockKey)
		return
	}

	// Save the group mapping before detecting image intent, so group-level model
	// eligibility decides the permission check rather than a later provider alias.
	ctx, groupMappingWS := p.Plan(ctx, reqModel)
	mappedFirstMessage, routingModelWS, _ := p.ImageIntent(reqModel, firstMessage, groupMappingWS)
	imageIntent := p.ExplicitImage(routingModelWS, mappedFirstMessage)
	initialSchedulingCtx := ctx
	if imageIntent {
		// Attach the image admission scope before provider selection, so explicit
		// first-turn image intent participates in model-level rate-limit eligibility
		// for that selection.
		initialSchedulingCtx = p.ImageContext(initialSchedulingCtx)
	}
	if imageIntent && !p.ImagesAllowed() {
		p.FeatureDenied()
		p.Close(1008, p.ImageDeniedMessage())
		return
	}

	var currentUserRelease func()
	var currentProviderRelease func()
	releaseProviderSlot := func() {
		if currentProviderRelease != nil {
			currentProviderRelease()
			currentProviderRelease = nil
		}
	}
	releaseTurnSlots := func() {
		releaseProviderSlot()
		if currentUserRelease != nil {
			currentUserRelease()
			currentUserRelease = nil
		}
	}
	// Register provider-then-user cleanup before admission begins, so every later
	// return releases acquired turn resources even when no execution callback
	// reports completion.
	defer releaseTurnSlots()

	userReleaseFunc, userAcquired, err := p.AcquireUser(ctx)
	if err != nil {
		reqLog.Warn("openai.websocket_user_slot_acquire_failed", EntryError(err))
		p.Close(1011, "failed to acquire user concurrency slot")
		return
	}
	if !userAcquired {
		p.Close(1013, "too many concurrent requests, please retry later")
		return
	}
	currentUserRelease = p.WrapRelease(ctx, userReleaseFunc)
	ensureUserSlotHeld := func() bool {
		if currentUserRelease != nil {
			return true
		}
		userReleaseFunc, userAcquired, err := p.AcquireUser(ctx)
		if err != nil {
			reqLog.Warn("openai.websocket_user_slot_reacquire_failed", EntryError(err))
			p.Close(1011, "failed to acquire user concurrency slot")
			return false
		}
		if !userAcquired {
			p.Close(1013, "too many concurrent requests, please retry later")
			return false
		}
		currentUserRelease = p.WrapRelease(ctx, userReleaseFunc)
		return true
	}

	p.LoadSubscription()
	requestPlatform := p.Platform()
	if err := p.Eligibility(ctx); err != nil {
		reqLog.Info("openai.websocket_billing_eligibility_check_failed", EntryError(err))
		p.Close(1008, "billing check failed")
		return
	}

	// Serialize Key reservation updates across turn callbacks, releasing the slot
	// while idle but retaining it through provider retries of the same logical
	// turn.
	var keyMu sync.Mutex
	var keyRelease func()
	acquireKey := func() error {
		keyMu.Lock()
		defer keyMu.Unlock()
		if keyRelease != nil {
			return nil
		}
		admitted, release, err := p.AcquireKey(ctx)
		if err != nil {
			return err
		}
		// Connect this Key lease's internal abort to session abort, so lost admission
		// also ends detached bridge work instead of leaving its generation running.
		stop := requestcontext.AfterAbort(admitted, abortSession)
		keyRelease = func() {
			// Stop a pending lease-abort callback before normal release signals that
			// lease; an already-started abort still represents the competing loss path.
			stop()
			release()
		}
		return nil
	}
	releaseKey := func() {
		// Remove the callback under the Key mutex and invoke it after unlocking,
		// allowing later admission or abort handling to inspect reservation state
		// without recursive locking.
		keyMu.Lock()
		release := keyRelease
		keyRelease = nil
		keyMu.Unlock()
		if release != nil {
			release()
		}
	}
	defer releaseKey()
	if err := acquireKey(); err != nil {
		closed := p.CloseInfo(err)
		p.Close(closed.Status, closed.Reason)
		return
	}

	sessionHash := p.SessionHash(
		firstMessage,
		EntryFallbackSeed(subject.UserID, apiKey.ID, apiKey.GroupID),
	)
	var cyberBlockedThisConn atomic.Bool
	explicitSessionHash := p.ExplicitHash(firstMessage)
	if explicitSessionHash != "" {
		if err := p.Isolate(ctx, "openai", explicitSessionHash); err != nil {
			p.IsolationError(ctx, err)
			p.Close(1008, p.IsolationReason(err))
			return
		}
	}
	if previousResponseID != "" {
		previousResponseHash := entrySeedHash(previousResponseID)
		if err := p.Isolate(ctx, "openai_previous_response", previousResponseHash); err != nil {
			p.IsolationError(ctx, err)
			p.Close(1008, p.IsolationReason(err))
			return
		}
	}
	ctx = p.Guardian(ctx, firstMessage, reqModel)
	maxProviderSwitches := in.MaxProviderSwitches
	switchCount := 0
	failedProviderIDs := make(map[int64]struct{})
	var lastFailoverErr *EntryFailure
	var oauth429FailoverState failover.OAuth429State
	wsAttemptMessage := append([]byte(nil), firstMessage...)
	// Provider replacement releases its slot before another selection but
	// preserves the logical request's Key reservation; cancellation and the switch
	// budget decide whether selection continues.
	handleWSFailover := func(selection *EntrySelection, provider *EntryProvider, failoverErr *EntryFailure) bool {
		if ctx.Err() != nil {
			return false
		}
		if failoverErr.ReportScheduleFailure {
			selection.Target.Report(selection.Target.MappedModel(groupMappingWS.MappedModel), false, nil)
		}
		releaseProviderSlot()
		if !failoverErr.RetryNext {
			p.CloseFailover(failoverErr)
			return false
		}
		if ctx.Err() != nil {
			return false
		}
		selection.Target.Switched()
		failedProviderIDs[provider.ID] = struct{}{}
		lastFailoverErr = failoverErr
		if switchCount >= maxProviderSwitches {
			p.CloseFailover(failoverErr)
			return false
		}
		switchCount++
		if selection.Target.Stop429(failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
			p.CloseFailover(failoverErr)
			return false
		}
		reqLog.Warn("openai.websocket_upstream_failover_switching",
			EntryInt64("provider_id", provider.ID),
			EntryInt("upstream_status", failoverErr.StatusCode),
			EntryInt("switch_count", switchCount),
			EntryInt("max_switches", maxProviderSwitches),
		)
		if ctx.Err() != nil {
			return false
		}
		return ensureUserSlotHeld()
	}

	// Explicit image intent in the OpenAI platform requires Responses capability.
	// Group-model detection keeps passive tool namespaces on ordinary text
	// eligibility rather than treating every namespace as image generation.
	requiredCapability := imageIntent && requestPlatform == "openai"

	for {
		if ctx.Err() != nil {
			return
		}
		reqLog.Debug("openai.websocket_provider_selecting", EntryInt("excluded_provider_count", len(failedProviderIDs)))
		// Selection and forwarding belong to the same logical turn. Reuse its
		// attribute session before inspecting candidates, including after provider failover.
		attemptRecords := requestRecords.attempt()
		selectionCtx := attemptRecords.context(initialSchedulingCtx, 1)
		selectionCtx = media.WithResponsesImageSelection(selectionCtx, wsAttemptMessage, in.Codex)
		selection, scheduleDecision, err := p.Select(selectionCtx, previousResponseID, sessionHash, reqModel, failedProviderIDs, requiredCapability, previousResponseCanMove, requestPlatform)
		if err != nil {
			reqLog.Warn("openai.websocket_provider_select_failed",
				EntryError(p.SelectionLogError(err, requestPlatform)),
				EntryInt("excluded_provider_count", len(failedProviderIDs)),
			)
			if local := EntryModelAttributeClose(err); local.Present {
				p.PolicyDenied()
				p.Close(local.Status, local.Reason)
				return
			}
			if lastFailoverErr != nil {
				p.CloseFailover(lastFailoverErr)
			} else {
				p.Close(1013, "no available provider")
			}
			return
		}
		if selection == nil || selection.Provider == nil {
			if lastFailoverErr != nil {
				p.CloseFailover(lastFailoverErr)
			} else {
				p.Close(1013, "no available provider")
			}
			return
		}

		provider := selection.Provider
		providerMaxConcurrency := provider.Concurrency
		if selection.WaitPlan != nil && selection.WaitPlan.MaxConcurrency > 0 {
			providerMaxConcurrency = selection.WaitPlan.MaxConcurrency
		}
		providerReleaseFunc := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				p.Close(1013, "provider is busy, please retry later")
				return
			}
			fastReleaseFunc, fastAcquired, err := p.AcquireProvider(
				ctx,
				provider.ID,
				selection.WaitPlan.MaxConcurrency,
			)
			if err != nil {
				reqLog.Warn("openai.websocket_provider_slot_acquire_failed", EntryInt64("provider_id", provider.ID), EntryError(err))
				p.Close(1011, "failed to acquire provider concurrency slot")
				return
			}
			if !fastAcquired {
				p.Close(1013, "provider is busy, please retry later")
				return
			}
			providerReleaseFunc = fastReleaseFunc
		}
		currentProviderRelease = p.WrapRelease(ctx, providerReleaseFunc)
		// Affinity persistence is advisory for future requests: record its failure
		// while continuing the already admitted selection and its acquired provider
		// slot.
		if err := p.BindSticky(ctx, sessionHash, provider.ID); err != nil {
			reqLog.Warn("openai.websocket_bind_sticky_session_failed", EntryInt64("provider_id", provider.ID), EntryError(err))
		}

		err = selection.Target.Credential(ctx)
		if err != nil {
			reqLog.Warn("openai.websocket_get_access_token_failed", EntryInt64("provider_id", provider.ID), EntryError(err))
			if ctx.Err() != nil {
				return
			}
			if failoverErr, ok := p.Failover(err); ok {
				if handleWSFailover(selection, provider, failoverErr) {
					continue
				}
				return
			}
			p.Close(1011, "failed to get access token")
			return
		}
		if err := selection.Target.EnforceClient(ctx, firstMessage); err != nil {
			reqLog.Warn("openai.websocket_client_policy_rejected", EntryInt64("provider_id", provider.ID), EntryError(err))
			p.Close(1008, "client is not allowed")
			return
		}

		reqLog.Debug("openai.websocket_provider_selected",
			EntryInt64("provider_id", provider.ID),
			EntryString("provider_name", provider.Name),
			EntryString("schedule_layer", scheduleDecision.Layer),
			EntryInt("candidate_count", scheduleDecision.CandidateCount),
		)

		// Retain first-request bytes for completion when a turn capture omits its
		// body. The executor resolves each client model through group and provider
		// mapping separately.
		wsFirstMessageForUsageFallback := append([]byte(nil), firstMessage...)
		// Initialize policy from the authenticated Key and publish refreshed usable
		// values per turn; cache-read failure retains the last accepted value in this
		// provider attempt.
		var currentFastModePolicy atomic.Value
		currentFastModePolicy.Store(apiKey.FastModePolicy)
		maxReasoningEffort := ""
		maxReasoningEffortOverLimit := ""
		var reasoningEffortMappings []routing.ReasoningEffortMapping
		if apiKey.Group != nil {
			maxReasoningEffort = apiKey.Group.MaxReasoningEffort
			maxReasoningEffortOverLimit = apiKey.Group.MaxReasoningEffortOverLimit
			reasoningEffortMappings = apiKey.Group.ReasoningEffortMappings
		}
		// Bind diagnostic selection to this provider while retaining the current
		// execution cancellation context used by the existing turn callbacks.
		contextForAttempt := func() context.Context {
			return telemetry.WithRequestProvider(ctx, provider.ID, provider.RequestIDHeader)
		}
		hooks := &EntryHooks{
			ContextForTurn:              func(turn int) context.Context { return attemptRecords.context(contextForAttempt(), turn) },
			TurnStarted:                 func(turn int, _ time.Time) { _ = attemptRecords.context(contextForAttempt(), turn) },
			ClientLifecycleContext:      clientLifecycleCtx,
			InitialRequestModel:         reqModel,
			InitialTurnStartedAt:        firstTurnStartedAt,
			MaxReasoningEffort:          maxReasoningEffort,
			MaxReasoningEffortOverLimit: maxReasoningEffortOverLimit,
			ReasoningEffortMappings:     reasoningEffortMappings,
			ResolveFastModePolicy: func(_ int) string {
				fallback, _ := currentFastModePolicy.Load().(string)
				if !apiKey.RefreshFastPolicy {
					return fallback
				}
				policy, ok := p.RefreshFast(ctx)
				// An unsuccessful refresh supplies no usable policy, so keep this attempt's
				// last accepted value for its per-frame Fast decision.
				if !ok {
					return fallback
				}

				currentFastModePolicy.Store(policy)
				return policy
			},
			ResolveRoutingModel: func(turn int, requestedModel string, payload []byte) (string, error) {
				requestedModel = strings.TrimSpace(requestedModel)
				if requestedModel == "" {
					requestedModel = clientReqModel
				}
				turnCtx, redirectedModel := p.Redirect(attemptRecords.context(ctx, turn), requestedModel)
				// Build this turn's route plan after Key redirection, so image intent and
				// selected-provider eligibility use the current group model while the
				// client identifier remains separate.
				turnCtx, turnMapping := p.Plan(turnCtx, redirectedModel)
				mappedPayload, turnRoutingModel, _ := p.ImageIntent(redirectedModel, payload, turnMapping)
				turnImageIntent := p.ExplicitImage(turnRoutingModel, mappedPayload)
				turnCtx = media.WithResponsesImageSelection(turnCtx, mappedPayload, in.Codex)

				if turnImageIntent && !p.ImagesAllowed() {
					p.FeatureDenied()
					return "", p.CloseError(
						1008,
						p.ImageDeniedMessage(),
						nil,
					)
				}
				if turnImageIntent {
					// Attach this turn's explicit image scope before rechecking the already
					// selected provider, so model-level image admission follows its current
					// payload intent.
					turnCtx = p.ImageContext(turnCtx)
				}
				turnCapability := turnImageIntent && requestPlatform == "openai"
				routingModel, resolveErr := selection.Target.ResolveRouting(turnCtx, redirectedModel, turnCapability)
				if resolveErr != nil {
					return "", p.CloseError(1008, EntryLocalRoutingErrorReason(redirectedModel, resolveErr), EntryLocalRoutingCause(resolveErr))
				}
				return routingModel, nil
			},
			BeforeRequest: func(turn int, payload []byte, originalModel, _ string) ([]byte, error) {
				turnContext := attemptRecords.context(contextForAttempt(), turn)
				telemetry.UpdateRequest(turnContext, func(record *telemetry.RequestRecord) { record.Model = originalModel })
				if turn == 1 {
					return payload, nil
				}
				if err := wirejson.ValidateRequestObject(payload); err != nil {
					return payload, p.CloseError(1008, "invalid websocket request payload", err)
				}
				rewrittenPayload, rewriteErr := modeltrace.RewriteAPIKeyAdditionalModels(payload, apiKey.ModelMapping)
				if rewriteErr != nil {
					return payload, p.CloseError(1008, "invalid websocket tool model", rewriteErr)
				}
				payload = rewrittenPayload
				payloadPreviousResponseID := strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String())
				model := strings.TrimSpace(originalModel)
				if model == "" {
					model = strings.TrimSpace(gjson.GetBytes(payload, "model").String())
				}
				if model == "" {
					model = clientReqModel
				}
				_, model = p.Redirect(ctx, model)
				snapshot := p.CaptureCyber(payload)
				setCyberPromptExcerpt(turn, snapshot.Excerpt, snapshot.Input)
				if decision := p.Moderate(ctx, model, payload); decision != nil && decision.Blocked {
					p.ModerationError(ctx, decision)
					return payload, p.CloseError(1008, decision.Message, nil)
				}
				if payloadPreviousResponseID != "" {
					previousResponseHash := entrySeedHash(payloadPreviousResponseID)
					if err := p.Isolate(ctx, "openai_previous_response", previousResponseHash); err != nil {
						p.IsolationError(ctx, err)
						return payload, p.CloseError(1008, p.IsolationReason(err), err)
					}
				}
				if explicitHash := p.ExplicitHash(payload); explicitHash != "" {
					if err := p.Isolate(ctx, "openai", explicitHash); err != nil {
						p.IsolationError(ctx, err)
						return payload, p.CloseError(1008, p.IsolationReason(err), err)
					}
				}
				return payload, nil
			},
			BeforeTurn: func(turn int) error {
				_ = attemptRecords.context(contextForAttempt(), turn)
				if cyberBlockedThisConn.Load() {
					return p.CloseError(1008, p.BlockedMessage(), nil)
				}
				if turn == 1 {
					return nil
				}
				// Clear provider and user ownership before reacquiring later-turn slots, so
				// replacing stored callbacks cannot abandon an earlier reservation during
				// this admission sequence.
				releaseTurnSlots()
				userReleaseFunc, userAcquired, err := p.AcquireUser(ctx)
				if err != nil {
					return p.CloseError(1011, "failed to acquire user concurrency slot", err)
				}
				if !userAcquired {
					return p.CloseError(1013, "too many concurrent requests, please retry later", nil)
				}
				// After waiting for the user slot, reauthenticate identity, protocol
				// permission, and funding before Key or provider admission; release that
				// user slot if verification fails.
				if err := p.AuthorizeTurn(ctx); err != nil {
					if userReleaseFunc != nil {
						userReleaseFunc()
					}
					return p.CloseError(1008, "API key or billing eligibility changed; reconnect required", err)
				}
				// Key admission follows successful reauthorization. If later provider
				// acquisition fails, deferred connection cleanup releases this Key
				// reservation while the immediate branch returns the user slot.
				if err := acquireKey(); err != nil {
					if userReleaseFunc != nil {
						userReleaseFunc()
					}
					return err
				}
				providerReleaseFunc, providerAcquired, err := p.AcquireProvider(ctx, provider.ID, providerMaxConcurrency)
				if err != nil {
					if userReleaseFunc != nil {
						userReleaseFunc()
					}
					return p.CloseError(1011, "failed to acquire provider concurrency slot", err)
				}
				if !providerAcquired {
					if userReleaseFunc != nil {
						userReleaseFunc()
					}
					return p.CloseError(1013, "provider is busy, please retry later", nil)
				}
				currentUserRelease = p.WrapRelease(ctx, userReleaseFunc)
				currentProviderRelease = p.WrapRelease(ctx, providerReleaseFunc)
				return nil
			},
			OnUpstreamError: func(turn int, originalModel string, statusCode int, responseBody []byte, warningText string) {
				model := strings.TrimSpace(originalModel)
				if model == "" {
					model = clientReqModel
				}
				selection.Target.Warning(ctx, model, statusCode, responseBody, warningText, EntryCyberSnapshot{Excerpt: getCyberPromptExcerpt(turn), Input: getCyberSnapshot(turn)})
			},
			AfterTurn: func(capture TurnCapture) {
				turn := capture.Turn
				result := capture.Result
				turnErr := capture.Err
				// Record retry classification before releasing the Key reservation: a
				// failed attempt that can select another provider retains the same logical
				// turn identity and allowance.
				_, requestRetry := p.Failover(turnErr)
				requestCtx := attemptRecords.finish(contextForAttempt(), capture, provider.ID, provider.Platform, turnErr != nil && requestRetry)
				if turnErr == nil || !requestRetry {
					releaseKey()
				}
				turnClientModel := strings.TrimSpace(capture.OriginalModel)
				if turnClientModel == "" {
					turnClientModel = clientReqModel
				}
				turnCtx, turnModel := p.Redirect(requestCtx, turnClientModel)
				// Resolve completion from this turn's client model and request context, so
				// its group billing source and redirect trace do not borrow another turn's
				// mapped identity.
				turnCtx, turnGroupMapping := p.Plan(turnCtx, turnModel)
				releaseTurnSlots()
				defer p.ClearCyber()
				turnRequestBodyForCyber := capture.RequestBody
				// A capture without request bytes uses the retained first-request body for
				// risk recording, preserving an available payload hash and policy input on
				// that path.
				if len(turnRequestBodyForCyber) == 0 {
					turnRequestBodyForCyber = wsFirstMessageForUsageFallback
				}
				cyberPolicyHandled := selection.Target.RecordMarked(turnCtx, turnModel, turnErr != nil, turnRequestBodyForCyber, turnGroupMapping.ToUsageFields(turnModel, ""), billing.HashUsageRequestPayload(turnRequestBodyForCyber))

				if cyberPolicyHandled {
					cyberBlockedThisConn.Store(true)
				}
				defer clearCyberPromptExcerpt(turn)
				if turnErr != nil {
					if result == nil || result.ImageCount <= 0 {
						return
					}
					if cyberPolicyHandled {
						return
					}
					reqLog.Warn("openai.websocket_partial_error_with_image_result",
						EntryInt64("provider_id", provider.ID),
						EntryInt("image_count", result.ImageCount),
						EntryError(turnErr),
					)
				}
				// A local warmup supplies no billable upstream exchange. After releasing
				// resources and risk state, skip quota refresh, health feedback, and normal
				// usage submission for it.
				if result == nil || result.LocalWarmup {
					return
				}
				// Select this turn's billing identity before completion capture, allowing
				// the group's requested or mapped source to override the result's initial
				// billing candidate.
				result.BillingModel = EntryBillingModel(result, turnGroupMapping, turnModel, result.UpstreamModel)
				// Ordinary OAuth targets accept header quota observations here. Shadow
				// targets use their separate quota refresh path, so the parent-like headers
				// are not written to the shadow snapshot.
				if provider.Type == "oauth" && !provider.Shadow {
					selection.Target.UpdateUsage(ctx, result.ResponseHeaders)
				}
				scheduleModel := strings.TrimSpace(result.UpstreamModel)
				// Use provider mapping when the result omits its upstream model, keeping
				// health feedback attached to the selected turn's scheduling identity.
				if scheduleModel == "" {
					scheduleModel = selection.Target.MappedModel(turnGroupMapping.MappedModel)
				}
				selection.Target.Report(scheduleModel, entrySucceeded(result), result.Timings.TTFTMs())

				turnRequestBody := capture.RequestBody
				// When capture omits its body, completion uses the retained first-request
				// bytes, matching the risk and payload-hash source available on this path.
				if len(turnRequestBody) == 0 {
					turnRequestBody = wsFirstMessageForUsageFallback
				}
				// Prepare HTTP-backed completion data and capture the recorder and error
				// observer before submission, so background work uses this logical turn's
				// identity and accounting inputs.
				completionInput := selection.Target.PrepareCompletion(turnCtx, result, capture, turnModel, turnGroupMapping, turnRequestBody, cyberPolicyHandled)
				recorder, report := p.CompletionRecorder(), p.CompletionObserver()
				p.SubmitCompletion(timing.WithPerformance(turnCtx, result.Timings), result, func(taskCtx context.Context) {
					if err := recorder.Record(taskCtx, completionInput, true); err != nil {
						report(completionInput.Provider.ID, completionInput.Result.RequestID, err)
					}
				})
			},
		}

		// Prepare an attempt-owned continuation payload while leaving client-model
		// mapping to frame normalization, so the next provider can apply its own
		// route and identity policies.
		wsFirstMessage := append([]byte(nil), wsAttemptMessage...)
		// A continuation without a matching sticky hit can belong to another target.
		// Strip its anchor when tool context permits moving the first request, so
		// the selected provider receives a request it can execute independently.
		if previousResponseID != "" && !scheduleDecision.StickyPreviousHit && previousResponseCanMove {
			wsFirstMessage = p.RemovePrevious(wsFirstMessage)
			reqLog.Debug("openai.websocket_previous_response_id_stripped_cross_group",
				EntryInt64("provider_id", provider.ID),
				EntryString("schedule_layer", scheduleDecision.Layer),
			)
		}

		if preemptCtx, cleanupPreempt, armed := selection.Target.BeginPreemption(ctx, wsFirstMessage); armed {
			ctx = preemptCtx
			defer cleanupPreempt()
		}

		if err := selection.Target.Run(contextForAttempt(), client, wsFirstMessage, hooks); err != nil {
			if p.SessionPreempted(err) {
				return
			}
			if failoverErr, ok := p.Failover(err); ok {
				// Extract later-turn replay separately from initial request bytes; an empty
				// recognized current-turn payload closes the request rather than repeating
				// an earlier turn.
				retryPayload, retryCurrentTurn := CurrentTurnRetryPayload(err)
				nextAttemptMessage, retrySafe := EntryNextAttemptMessage(wsAttemptMessage, retryPayload, retryCurrentTurn)
				if !retrySafe {
					p.CloseFailover(failoverErr)
					return
				}
				wsAttemptMessage = nextAttemptMessage
				if retryCurrentTurn {
					previousResponseID = ""
					reqLog.Warn("openai.websocket_current_turn_failover_retry",
						EntryInt64("provider_id", provider.ID),
						EntryInt("upstream_status", failoverErr.StatusCode),
						EntryInt("retry_payload_bytes", len(retryPayload)),
					)
				}
				if handleWSFailover(selection, provider, failoverErr) {
					continue
				}
				return
			}

			if errors.Is(context.Cause(ctx), scheduler.ErrOpenAIWSIngressLeaseLost) {
				reqLog.Warn("openai.websocket_ingress_lease_lost",
					EntryInt64("provider_id", provider.ID),
					EntryError(err),
				)
				p.Close(1013, "websocket ingress capacity lease lost; please reconnect")
				return
			}

			if local := EntryModelAttributeClose(err); local.Present {
				p.PolicyDenied()
				p.Close(local.Status, local.Reason)
				return
			}
			closeErr := p.CloseInfo(err)
			hasClientCloseErr := closeErr.Present
			if p.LocalPolicyError(err) {
				p.PolicyDenied()
			}

			if p.EndedByClient(err) {
				closedFields := []EntryField{EntryInt64("provider_id", provider.ID)}
				if hasClientCloseErr {
					closedFields = append(closedFields, EntryString("reason", closeErr.Reason))
				} else {
					closedFields = append(closedFields, EntryError(err))
				}
				reqLog.Info("openai.websocket_ingress_closed_normally", closedFields...)
				// A clean untyped client exit has no gateway-selected close instruction;
				// send normal 1000 so it does not receive the proxy-failure status used by
				// the tail below.
				if hasClientCloseErr {
					p.Close(closeErr.Status, closeErr.Reason)
				} else {
					p.Close(1000, "")
				}
				return
			}

			if p.ReportFailure(err) {
				selection.Target.Report(selection.Target.MappedModel(groupMappingWS.MappedModel), false, nil)
			}
			selection.Target.LogFailure(err)
			if hasClientCloseErr {
				p.Close(closeErr.Status, closeErr.Reason)
				return
			}
			p.Close(1011, "upstream websocket proxy failed")
			return
		}
		reqLog.Info("openai.websocket_ingress_closed", EntryInt64("provider_id", provider.ID))
		return
	}
}
