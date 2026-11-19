package openaiforward

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// messages.go converts Messages requests through Responses and manages cache
// identity, continuation, and provider-local recovery.
// Messages conversion needs a cache identity that can survive tool and
// instruction rewriting across turns. The selected provider and downstream key
// scope digest-prefix reuse and saved Responses continuation, while Grok can
// use Claude session metadata as its seed before platform cache normalization.
// Codex retains full replay history for its session and turn-state cache
// behavior.
//
// The upstream request streams regardless of the client's output preference,
// and response conversion chooses incremental Messages or buffered JSON.
// Provider-local recovery separately handles Grok ciphertext, invalid agent
// tasks, unavailable continuation, and client thinking signatures. Opaque turn-
// state headers can be saved before body reading; response IDs and digest
// relations are bound after successful output. Reader observations remain
// available beside errors, and cyber-policy observations can prevent returning
// completion data.

// RunMessages dispatches a selected Messages attempt or converts it to a
// streaming Responses exchange. It validates incoming effort, keeps caller,
// billing, and vendor models distinct, and derives missing cache identity
// before conversion rewrites prompt history. The client stream choice controls
// Messages output, while the upstream request always streams.
//
// Saved continuation trims input to the latest turn, and unavailable
// continuation changes shared session state before rebuilding from the client
// body. Grok ciphertext replay keeps the prepared credential and cache
// identity; request-context guards separately bound agent-task and client-
// signature recovery. Returned turn-state headers are cached before reading,
// while response IDs and digest relations require successful output. The runner
// closes response bodies through its own cleanup and error-reader callbacks,
// returning converted observations beside reader failures.
func RunMessages(ctx context.Context, body []byte, promptCacheKey, defaultMappedModel string, p MessagesPorts) (*Result, error) {
	profile, route, err := p.Prepare(ctx)
	if err != nil {
		return nil, err
	}
	if route != DispatchResponses {
		return p.Dispatch(ctx, route, body, promptCacheKey, defaultMappedModel)
	}
	startTime := time.Now()

	var anthropicReq protocolanthropic.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}
	if err := requeststate.ValidateOpenAIReasoningEffort(body, anthropicReq.Model); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	anthropicDigestReq := session.CloneAnthropicDigestRequest(&anthropicReq)
	originalModel := anthropicReq.Model
	normalizedModel := anthropicReq.Model
	clientStream := anthropicReq.Stream // client output stream choice

	billingModel := p.BillingModel(normalizedModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	anthropicReq.Model = upstreamModel
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	apiKeyID := p.APIKeyID()
	anthropicDigestChain := ""
	anthropicMatchedDigestChain := ""
	compatPromptCacheInjected := false
	// Use Claude Code's stable session identity as Grok's cache seed when available.
	// Grok skips the GPT/Codex key injector, so this lets successive Messages turns
	// reuse the xAI cache identity.
	if promptCacheKey == "" && profile.Platform == "grok" {
		if sessionSeed := p.ClaudeSession(body); sessionSeed != "" {
			promptCacheKey = sessionSeed
			compatPromptCacheInjected = true
		} else if sessionSeed := session.AnthropicMetadataPromptCacheKey(&anthropicReq); sessionSeed != "" {
			promptCacheKey = sessionSeed
			compatPromptCacheInjected = true
		}
	}
	// When compatibility injection is allowed, prefer metadata then cache-control
	// identity; digest-prefix lookup can reuse the established key before a new
	// chain seed is derived.
	if promptCacheKey == "" && gatewayprovider.ShouldAutoInjectPromptCacheKeyForCompat(upstreamModel) {
		promptCacheKey = session.AnthropicMetadataPromptCacheKey(&anthropicReq)
		if promptCacheKey == "" {
			promptCacheKey = gatewayprovider.DeriveAnthropicCacheControlPromptCacheKey(&anthropicReq)
		}
		if promptCacheKey == "" {
			anthropicDigestChain = session.BuildAnthropicDigestChain(anthropicDigestReq)
			if reusedKey, matchedChain := p.FindDigestKey(apiKeyID, anthropicDigestChain); reusedKey != "" {
				promptCacheKey = reusedKey
				anthropicMatchedDigestChain = matchedChain
			} else {
				promptCacheKey = session.AnthropicDigestPromptCacheKey(anthropicDigestChain)
			}
		}
		compatPromptCacheInjected = promptCacheKey != ""
	}
	compatReplayTrimmed := false
	compatReplayGuardEnabled := gatewayprovider.ShouldAutoInjectPromptCacheKeyForCompat(upstreamModel)
	compatContinuationEnabled := p.ContinuationEnabled(upstreamModel)
	previousResponseID := ""
	if compatContinuationEnabled {
		previousResponseID = p.ResponseID(ctx, promptCacheKey)
	}
	compatContinuationDisabled := compatContinuationEnabled &&
		p.ContinuationDisabled(ctx, promptCacheKey)
	compatTurnState := ""
	// Codex uses session_id and x-codex-turn-state for caching. Keep the full replay
	// so the cached prefix can grow beyond system instructions and tools between turns.
	if compatReplayGuardEnabled && !profile.UsesCodex && previousResponseID == "" && !compatContinuationDisabled {
		compatReplayTrimmed = protocolbridge.ApplyAnthropicCompatFullReplayGuard(&anthropicReq)
	}

	// Apply replay trimming before Messages-to-Responses conversion so the
	// outgoing history and tool continuation are generated from the adopted
	// compatible Messages request.
	responsesReq, err := p.Convert(&anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("convert anthropic to responses: %w", err)
	}

	// Force upstream streaming for endpoints that require it; client preference controls the response format.
	responsesReq.Stream = true
	isStream := true

	// Translate the Anthropic fast-mode beta into a Responses tier before fast-
	// policy processing, letting that later policy filter or force the outgoing
	// priority request.
	if p.BetaFast() {
		responsesReq.ServiceTier = "priority"
	}

	responsesReq.Model = upstreamModel
	if responsesReq.Reasoning != nil {
		responsesReq.Reasoning.Effort = p.MessagesEffort(&anthropicReq, upstreamModel, responsesReq.Reasoning.Effort)
	}
	// A saved response supplies prior turn context, so attach its ID before
	// trimming the outgoing history to the latest continuation turn.
	if previousResponseID != "" {
		responsesReq.PreviousResponseID = previousResponseID
		protocolbridge.TrimCompatResponsesInputToLatestTurn(responsesReq)
	}
	// Non-Codex replay policy inserts developer guard guidance before
	// serialization, keeping its position beside initial developer instructions
	// as later request preparation adopts the converted history.
	if compatReplayGuardEnabled && !profile.UsesCodex {
		gatewayprovider.AppendOpenAICompatClaudeCodeTodoGuard(responsesReq)
	}

	logFields := []zap.Field{
		zap.Int64("provider_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("normalized_model", normalizedModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", isStream),
		zap.Bool("compat_continuation_supported", profile.ContinuationSupported),
	}
	if compatPromptCacheInjected {
		logFields = append(logFields,
			zap.Bool("compat_prompt_cache_key_injected", true),
			zap.String("compat_prompt_cache_key_sha256", upstream.HashSensitiveValueForLog(promptCacheKey)),
		)
	}
	if compatReplayTrimmed {
		logFields = append(logFields,
			zap.Bool("compat_full_replay_trimmed", true),
			zap.Int("compat_messages_after_trim", len(anthropicReq.Messages)),
		)
	}
	if previousResponseID != "" {
		logFields = append(logFields,
			zap.Bool("compat_previous_response_id_attached", true),
			zap.String("compat_previous_response_id", gatewayprovider.TruncateOpenAIWSLogValue(previousResponseID, gatewayprovider.OpenAIWSIDValueMaxLen)),
		)
	}
	if compatTurnState != "" {
		logFields = append(logFields, zap.Bool("compat_turn_state_attached", true))
	}
	logging.L().Debug("openai messages: model mapping applied", logFields...)

	responsesBody, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("marshal responses request: %w", err)
	}

	// Codex conversion retains tool-call identities and supplied instructions for
	// Messages replay; save reverse names and normalize the model before forced
	// template rendering and session metadata.
	if profile.UsesCodex && profile.Platform != "grok" {
		var reqBody map[string]any
		if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
			return nil, fmt.Errorf("unmarshal for codex transform: %w", err)
		}
		codexResult := gatewayprovider.ApplyCodexOAuthTransformWithOptions(reqBody, openai.CodexOAuthTransformOptions{
			SkipDefaultInstructions: true,
			PreserveToolCallIDs:     true,
		})
		if codexResult.Error != nil {
			p.Error(http.StatusBadRequest, "invalid_request_error", codexResult.Error.Error())
			return nil, codexResult.Error
		}
		p.ToolNameReverse(codexResult.ToolNameReverse)
		forcedTemplateText := p.ForcedTemplate()
		templateUpstreamModel := upstreamModel
		if codexResult.NormalizedModel != "" {
			templateUpstreamModel = codexResult.NormalizedModel
		}
		existingInstructions, _ := reqBody["instructions"].(string)
		// A missing usable instruction string falls back to prompt-like input
		// text so forced template rendering can receive the converted request's
		// available instruction evidence.
		if strings.TrimSpace(existingInstructions) == "" {
			existingInstructions = openai.ExtractPromptLikeInstructionsFromInput(reqBody)
		}
		if _, err := p.ForcedInstructions(reqBody, forcedTemplateText, TemplateData{
			ExistingInstructions: strings.TrimSpace(existingInstructions),
			OriginalModel:        originalModel,
			NormalizedModel:      normalizedModel,
			BillingModel:         billingModel,
			UpstreamModel:        templateUpstreamModel,
		}); err != nil {
			return nil, err
		}
		protocolopenai.EnsureCodexInstructionsField(reqBody)
		if gatewayprovider.ShouldAutoInjectPromptCacheKeyForCompat(upstreamModel) {
			gatewayprovider.AppendOpenAICompatClaudeCodeTodoGuardToRequestBody(reqBody)
		}
		if codexResult.NormalizedModel != "" {
			upstreamModel = codexResult.NormalizedModel
		}
		if codexResult.PromptCacheKey != "" {
			promptCacheKey = codexResult.PromptCacheKey
		}
		// Provider namespace metadata is applied before removing the body cache key;
		// Codex session and turn-state handling use the separately retained prompt
		// seed.
		p.ProviderIdentity(reqBody, apiKeyID)
		delete(reqBody, "prompt_cache_key")
		if gatewayprovider.ShouldAutoInjectPromptCacheKeyForCompat(upstreamModel) {
			compatTurnState = p.TurnState(ctx, promptCacheKey)
		}
		// The Codex transform forces stream=true, so read its response as a stream.
		isStream = true
		responsesBody, err = json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("remarshal after codex transform: %w", err)
		}
	}

	// API key providers receive prompt_cache_key in the body, sharing the same
	// upstream cache identity convention as Responses clients.
	if profile.Type == "apikey" {
		if trimmedKey := strings.TrimSpace(promptCacheKey); trimmedKey != "" {
			var reqBody map[string]any
			if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
				return nil, fmt.Errorf("unmarshal for prompt cache key injection: %w", err)
			}
			if existing, ok := reqBody["prompt_cache_key"].(string); !ok || strings.TrimSpace(existing) == "" {
				reqBody["prompt_cache_key"] = trimmedKey
				updated, err := json.Marshal(reqBody)
				if err != nil {
					return nil, fmt.Errorf("remarshal after prompt cache key injection: %w", err)
				}
				responsesBody = updated
			}
		}
	}
	// Apply admitted group effort policy after model and prompt rewriting so
	// converted effort observations follow the same adopted body that will be
	// sent upstream.
	if profile.Platform == "openai" {
		policyBody, changed, policyErr := p.ApplyEffort(ctx, responsesBody)
		if policyErr != nil {
			return nil, policyErr
		}
		if changed {
			responsesBody = policyBody
			if responsesReq.Reasoning != nil {
				responsesReq.Reasoning.Effort = gjson.GetBytes(responsesBody, "reasoning.effort").String()
			}
		}
	}

	// Apply provider fast-mode policy after effort preparation, including the
	// Claude beta-derived tier, so the final response hint follows the outgoing
	// request body.
	updatedBody, policyErr := p.ApplyFast(ctx, upstreamModel, responsesBody)
	if policyErr != nil {
		return nil, policyErr
	}
	responsesBody = updatedBody
	if serviceTier := requeststate.ExtractOpenAIServiceTierFromBody(responsesBody); serviceTier != nil {
		responsesReq.ServiceTier = *serviceTier
	} else {
		responsesReq.ServiceTier = ""
	}
	grokCacheIdentity := ""
	// Capture converted Grok intent before cache mutation so model/tool
	// preparation, cache identity, and Free OAuth routing all refer to the same
	// incoming converted request.
	if profile.Platform == "grok" {
		grokIntentBody := responsesBody
		grokCacheIdentity = p.GrokCacheIdentity(grokIntentBody, promptCacheKey, upstreamModel)
		patchedBody, patchErr := p.PatchGrokBody(grokIntentBody, upstreamModel)
		if patchErr != nil {
			return nil, patchErr
		}
		responsesBody, patchErr = grok.ApplyGrokResponsesCacheIdentity(patchedBody, grokIntentBody, grokCacheIdentity, profile.GrokOAuth)
		if patchErr != nil {
			return nil, fmt.Errorf("apply grok prompt cache identity: %w", patchErr)
		}
		responsesBody, patchErr = p.GrokFreeToolRoute(responsesBody, grokIntentBody, grokCacheIdentity)
		if patchErr != nil {
			return nil, fmt.Errorf("apply grok Free function-tool cache route: %w", patchErr)
		}
	}

	token, err := p.Credential(ctx)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	if profile.UsesCodex && profile.Platform != "grok" {
		// Select Messages bridge construction even when the body lacks todo-guard or cache-key
		// markers, such as after mapping to another model family. This controls body and session handling.
		// Restore identity headers after construction.
		p.BindMessagesBridge(true)
	}
	upstreamCtx, releaseUpstreamCtx := gatewayprovider.DetachUpstreamContext(ctx)
	upstreamReq, err := p.Build(ctx, upstreamCtx, responsesBody, token, isStream, promptCacheKey, grokCacheIdentity)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	// Derive the session UUID using key and provider namespace so the same
	// prompt seed under another authenticated key receives a separate upstream
	// session identity.
	if profile.Platform != "grok" && promptCacheKey != "" {
		isolatedSessionID := p.IsolatedSessionID(apiKeyID, promptCacheKey)
		upstreamReq.Header.Set("session_id", isolatedSessionID)
		if upstreamReq.Header.Get("conversation_id") != "" {
			upstreamReq.Header.Set("conversation_id", isolatedSessionID)
		}
	}
	if profile.UsesCodex && profile.Platform != "grok" {
		// After bridge construction, restore missing Codex defaults and the
		// Responses beta header, then enforce the outgoing User-Agent/originator
		// pair before sending.
		openai.EnsureCodexIdentityHeaders(upstreamReq.Header)
		openai.EnforceCodexIdentityHeaders(upstreamReq.Header)
		logging.L().Debug("openai messages: upstream identity restored",
			zap.Int64("provider_id", profile.ID),
			zap.String("upstream_model", upstreamModel),
			zap.Bool("compat_identity_restored", true),
		)
	}
	// Keep generated session_id for cache identity, but remove conversation_id
	// when the client supplied no conversation header so synthesized session
	// metadata does not create a client conversation declaration.
	if profile.UsesCodex && promptCacheKey != "" && strings.TrimSpace(p.Header("conversation_id")) == "" {
		upstreamReq.Header.Del("conversation_id")
	}
	if compatTurnState != "" && upstreamReq.Header.Get("x-codex-turn-state") == "" {
		upstreamReq.Header.Set("x-codex-turn-state", compatTurnState)
	}

	// Resolve proxy state once for the selected provider; the Grok ciphertext
	// replay rebuilds its request while retaining this transport preparation and
	// credential.
	p.PrepareTransport()
	// Grok can reject encrypted reasoning replayed under another OAuth provider or cache identity.
	// Remove that ciphertext and retry once before treating the 400 as terminal or requesting failover.
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if profile.Platform != "grok" {
				break
			}
			upstreamCtxRetry, releaseRetry := gatewayprovider.DetachUpstreamContext(ctx)
			upstreamReq, err = p.Build(ctx, upstreamCtxRetry, responsesBody, token, isStream, promptCacheKey, grokCacheIdentity)
			releaseRetry()
			if err != nil {
				return nil, fmt.Errorf("build grok retry request: %w", err)
			}
		}
		resp, err = p.Send(upstreamReq)
		if err != nil {
			return nil, p.TransportError(ctx, err)
		}
		if profile.Platform != "grok" || attempt > 0 || resp.StatusCode != http.StatusBadRequest {
			break
		}
		// Capture the first Grok rejection before closing its network body; retain
		// those bytes in a replay reader when ciphertext repair is unavailable or
		// unchanged.
		respBody := p.ReadErrorBody(resp)
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		// On the first Grok 400, ciphertext presence permits removal even when error
		// text is opaque; changed bytes enable one replay before final health
		// handling.
		shouldStrip := gatewayprovider.GrokBodyCodec().IsGrokInvalidEncryptedContentResponse(resp.StatusCode, respBody) ||
			gatewayprovider.GrokBodyCodec().RequestHasGrokEncryptedReasoning(responsesBody)
		if !shouldStrip {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			break
		}
		retryBody, changed, trimErr := gatewayprovider.GrokBodyCodec().TrimGrokInvalidEncryptedContentRetryBody(responsesBody)
		if trimErr != nil {
			return nil, fmt.Errorf("prepare Grok invalid encrypted_content retry: %w", trimErr)
		}
		if !changed {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			break
		}
		responsesBody = retryBody
		logging.L().Info("openai messages: retrying after stripping invalid Grok encrypted_content",
			zap.Int64("provider_id", profile.ID),
			zap.Bool("cache_identity_present", strings.TrimSpace(grokCacheIdentity) != ""),
			zap.String("upstream_error_preview", gatewayprovider.TruncateOpenAIWSLogValue(string(respBody), 240)),
		)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := p.ReadUpstreamError(resp)
		// Record agent recovery in the request context before rebuilding from client
		// bytes, preventing repeated task rejection from recursively spending another
		// recovery opportunity.
		if !requeststate.AgentTaskRecoveryTried(ctx) && p.IsAgentIdentity(ctx) && openai.IsAgentTaskInvalidHTTPResponse(resp.StatusCode, respBody) {
			if err := p.RecoverAgentTask(ctx); err != nil {
				return nil, fmt.Errorf("agent identity task recovery failed: %w", err)
			}
			return RunMessages(requeststate.WithAgentTaskRecovery(ctx), body, promptCacheKey, defaultMappedModel, p)
		}
		respBody = p.RedactErrorBody(ctx, respBody)
		// The installed error reader already closed the network body; replace replay
		// storage with redacted bytes before continuation classification and final
		// failure presentation.
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
		// Unavailable continuation requires changing its provider/key/prompt binding
		// before recursion; capability rejection disables reuse, while a missing
		// response removes the saved ID for the rebuilt request.
		if previousResponseID != "" && (gatewayprovider.CompatPreviousResponseNotFound(resp.StatusCode, upstreamMsg, respBody) || gatewayprovider.CompatPreviousResponseUnsupported(resp.StatusCode, upstreamMsg, respBody)) {
			if gatewayprovider.CompatPreviousResponseUnsupported(resp.StatusCode, upstreamMsg, respBody) {
				p.DisableContinuation(ctx, promptCacheKey)
			} else {
				p.DeleteResponseID(ctx, promptCacheKey)
			}
			logging.L().Info("openai messages: previous_response_id unavailable, retrying without continuation",
				zap.Int64("provider_id", profile.ID),
				zap.String("previous_response_id", gatewayprovider.TruncateOpenAIWSLogValue(previousResponseID, gatewayprovider.OpenAIWSIDValueMaxLen)),
				zap.String("upstream_model", upstreamModel),
				zap.Bool("compat_continuation_supported", profile.ContinuationSupported),
			)
			return RunMessages(ctx, body, promptCacheKey, defaultMappedModel, p)
		}
		// A decryption failure can survive provider switching. Strip client-body thinking
		// signatures once so the next attempt can replay the tool continuation.
		if profile.Platform == "grok" &&
			gatewayprovider.GrokBodyCodec().IsGrokInvalidEncryptedContentResponse(resp.StatusCode, respBody) &&
			!gatewayprovider.GrokBodyCodec().GrokEncryptedContentStripRetried(ctx) {
			if strippedBody, ok := protocolanthropic.StripThinkingSignaturesJSON(body); ok {
				logging.L().Info("openai messages: stripping thinking signatures for Grok failover retry",
					zap.Int64("provider_id", profile.ID),
				)
				return RunMessages(gatewayprovider.GrokBodyCodec().MarkGrokEncryptedContentStripRetried(ctx), strippedBody, promptCacheKey, defaultMappedModel, p)
			}
		}
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		// Failures surviving provider health decisions use the shared Anthropic
		// presenter, retaining its client error shape and request observations after
		// recovery opportunities are exhausted.
		return p.ErrorResponse(resp, billingModel)
	}
	if profile.Platform == "grok" && profile.Type == "oauth" && !profile.Shadow {
		p.UpdateGrokUsage(ctx, upstreamModel, resp.Header, resp.StatusCode)
	}

	// Successful HTTP headers can refresh opaque turn-state before output reading;
	// this publication is distinct from response-ID and digest binding that wait
	// for successful conversion.
	if profile.UsesCodex && promptCacheKey != "" {
		if turnState := strings.TrimSpace(resp.Header.Get("x-codex-turn-state")); turnState != "" {
			p.BindTurnState(ctx, promptCacheKey, turnState)
		}
	}

	// The upstream streams for either client mode; choose the Messages converter
	// by saved client preference so buffered callers receive one assembled
	// response.
	var result *Result
	var handleErr error
	var nativeResult *openai.CompatResponseResult
	output := upstream.NewDeferredOutputContext(p.Sink())
	responseOptions := p.ResponseOptions(resp, originalModel, billingModel, upstreamModel)
	if clientStream {
		nativeResult, handleErr = openai.ReadMessagesStreaming(resp, output, responseOptions, originalModel, upstreamModel, startTime)
	} else {
		nativeResult, handleErr = openai.ReadMessagesBuffered(resp, output, responseOptions, originalModel, upstreamModel, startTime)
	}
	result = FromCompatResult(nativeResult, billingModel)
	// A cyber-policy observation prevents returning completion data even when
	// reading produced a result; retain a reader failure or return the warning-
	// specific error.
	if p.CyberPolicy() {
		if handleErr == nil {
			handleErr = forwardcore.ErrCyberPolicyForwarded
		}
		return nil, handleErr
	}

	// Bind response continuation and digest-prefix reuse only after successful
	// output, so a failed reader cannot publish its response identity as the next
	// turn's context.
	if handleErr == nil && result != nil {
		if compatContinuationEnabled && promptCacheKey != "" && result.ResponseID != "" {
			p.BindResponseID(ctx, promptCacheKey, result.ResponseID)
		}
		if promptCacheKey != "" && anthropicDigestChain != "" {
			p.BindDigestKey(apiKeyID, anthropicDigestChain, promptCacheKey, anthropicMatchedDigestChain)
		}
		// Prefer the upstream's echoed tier; otherwise use the outgoing body's tier
		// after fast-policy filtering or forcing.
		if tier := p.ResolvedServiceTier(requeststate.ExtractOpenAIServiceTierFromBody(responsesBody)); tier != nil {
			result.ServiceTier = tier
		}
		if responsesReq.Reasoning != nil && responsesReq.Reasoning.Effort != "" {
			re := responsesReq.Reasoning.Effort
			result.ReasoningEffort = &re
		}
	}

	// Update OAuth Codex usage from response headers.
	// Spark shadows obtain their codex_* snapshot through QueryUsage's bengalfox query.
	if handleErr == nil && profile.Type == "oauth" && !profile.Shadow && profile.Platform != "grok" {
		p.UpdateCodexUsage(ctx, resp.Header)
	}

	return result, handleErr
}
