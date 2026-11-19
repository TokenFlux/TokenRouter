package openaiforward

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// chat.go dispatches Chat Completions requests and executes their conversion
// through Responses when that route is selected. Chat requests can contain
// Responses input, which a typed Chat round trip would discard. Dispatch
// examines the resolved upstream protocol and body shape before conversion;
// Kimi and DeepSeek, raw Chat, Anthropic, and Grok routes can therefore leave
// the common Responses path at different stages while retaining the requested
// client format.
//
// The common path maps billing and vendor models before deriving compatibility
// cache identity, then prepares Responses bytes and provider-scoped sessions.
// Agent-task recovery recursively rebuilds that same attempt with a context
// guard. Response readers convert back to Chat and expose partial observations
// beside errors; successful output attaches effective billing controls and
// eligible Codex rate-limit observations.

// CursorResponsesUnsupportedFields identifies extension fields removed from raw
// Responses-shaped Chat input before tier and OAuth compatibility preparation.
var CursorResponsesUnsupportedFields = []string{"prompt_cache_retention", "safety_identifier", "metadata", "stream_options"}

// RunChat dispatches a selected Chat attempt or converts its request to
// Responses and reads Chat output. Bodies with input and no messages are
// handled as Responses-shaped input, preserving that history during raw model
// patching before common OAuth preparation. Provider protocol and Grok
// eligibility determine whether alternate execution is selected.
//
// Billing and upstream models are resolved before deriving an injected cache
// seed, and API Key seeds can already carry downstream-key isolation. The
// session header applies isolation only when that seed is not already isolated.
// Agent recovery rebuilds from the client body with a request-context guard;
// response reading can return observations beside an error, while effective
// effort, tier, and Codex usage updates require successful reading.
func RunChat(ctx context.Context, body []byte, promptCacheKey, defaultMappedModel string, compatPromptCacheTenantIsolated bool, p ChatPorts) (*Result, error) {
	profile, err := p.PrepareChat(ctx)
	if err != nil {
		return nil, err
	}
	if !p.ClientAllowed(ctx, body) {
		p.PolicyDenied()
		p.Reject(403, "forbidden_error", "This provider only allows Codex official clients")
		return nil, errors.New("codex_cli_only restriction: only codex official clients are allowed")
	}
	// A fixed non-Responses route needs Chat messages even when the URL received
	// Responses input; convert that shape before dispatching the selected
	// protocol.
	if profile.Protocol != "" && profile.Protocol != protocol.ProtocolOpenAIResponses && !gjson.GetBytes(body, "messages").Exists() && gjson.GetBytes(body, "input").Exists() {
		var request protocolopenai.ResponsesRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		converted, err := p.ResponsesToChat(&request)
		if err != nil {
			return nil, err
		}
		body, err = json.Marshal(converted)
		if err != nil {
			return nil, err
		}
	}
	// An explicit Grok Responses route rejects unrepresentable conversion, while
	// eligible OAuth adaptation can choose the bridge and otherwise retain raw
	// Chat execution.
	if profile.Platform == "grok" {
		if profile.Protocol == protocol.ProtocolOpenAIChatCompletions {
			return p.DispatchChat(ctx, DispatchRawChat, body, promptCacheKey, defaultMappedModel)
		}
		if profile.Protocol == protocol.ProtocolOpenAIResponses {
			if eligible, reason := gatewayprovider.GrokBodyCodec().GrokChatResponsesBridgeEligibility(body); !eligible {
				return nil, fmt.Errorf("configured Grok Responses conversion cannot preserve request: %s", reason)
			}
			return p.DispatchChat(ctx, DispatchGrok, body, promptCacheKey, defaultMappedModel)
		}
		if profile.GrokOAuth {
			eligible, reason := gatewayprovider.GrokBodyCodec().GrokChatResponsesBridgeEligibility(body)
			if eligible {
				return p.DispatchChat(ctx, DispatchGrok, body, promptCacheKey, defaultMappedModel)
			}
			logging.L().Debug("grok chat_completions: using raw fallback",
				zap.Int64("provider_id", profile.ID),
				zap.String("reason", reason),
			)
		}
		return p.DispatchChat(ctx, DispatchRawChat, body, promptCacheKey, defaultMappedModel)
	}
	if err := requeststate.ValidateOpenAIReasoningEffort(body, gjson.GetBytes(body, "model").String()); err != nil {
		p.ChatError(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}

	// Detect Responses-shaped bodies before adaptive dispatch so input is converted
	// before reaching an upstream that expects messages.
	isResponsesShape := !gjson.GetBytes(body, "messages").Exists() && gjson.GetBytes(body, "input").Exists()

	// Adaptive providers send standard Chat bodies to Chat endpoints. Responses-shaped
	// bodies use native Responses on DeepSeek/Kimi and convert to Chat on Zhipu.
	if profile.Adaptive {
		if !isResponsesShape {
			return p.DispatchChat(ctx, DispatchRawChat, body, promptCacheKey, defaultMappedModel)
		}
		if !profile.NativeResponsesSupported {
			var responsesReq protocolopenai.ResponsesRequest
			if err := json.Unmarshal(body, &responsesReq); err != nil {
				return nil, fmt.Errorf("parse responses-shaped chat completions request: %w", err)
			}
			chatReq, err := p.ResponsesToChat(&responsesReq)
			if err != nil {
				return nil, fmt.Errorf("convert responses-shaped chat completions request: %w", err)
			}
			chatBody, err := json.Marshal(chatReq)
			if err != nil {
				return nil, fmt.Errorf("marshal converted chat completions request: %w", err)
			}
			return p.DispatchChat(ctx, DispatchRawChat, chatBody, promptCacheKey, defaultMappedModel)
		}
		// Kimi and DeepSeek Responses requests continue to the
		// Responses-to-Chat output conversion below.
	}

	if profile.Anthropic {
		return p.DispatchChat(ctx, DispatchAnthropic, body, promptCacheKey, defaultMappedModel)
	}

	// RawChat includes Kimi, Zhipu, and DeepSeek providers fixed to Chat and
	// API key providers whose resolved probe or administrator policy selects
	// Chat.
	if profile.RawChat {
		return p.DispatchChat(ctx, DispatchRawChat, body, promptCacheKey, defaultMappedModel)
	}
	if !profile.ProtocolSlotDefaults && p.DefaultChat() {
		return p.DispatchChat(ctx, DispatchRawChat, body, promptCacheKey, defaultMappedModel)
	}

	startTime := time.Now()

	var chatReq protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := chatReq.Model
	clientStream := chatReq.Stream

	// Resolve billing and vendor models before deriving a compatibility seed so
	// cache identity follows the selected upstream model while the caller model
	// remains available for output.
	billingModel := p.BillingModel(originalModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	chatReq.Model = upstreamModel

	promptCacheKey = strings.TrimSpace(promptCacheKey)
	compatPromptCacheInjected := false
	// Derive a missing Chat cache seed only for eligible model/provider pairs;
	// API Key requests namespace that seed and record its isolation so later
	// session construction uses the already-isolated identity.
	if promptCacheKey == "" && !isResponsesShape && (profile.UsesCodex || profile.OpenAIAPIKey) && gatewayprovider.ShouldAutoInjectPromptCacheKeyForCompat(upstreamModel) {
		promptCacheKey = gatewayprovider.DeriveCompatPromptCacheKey(&chatReq, upstreamModel)
		compatPromptCacheInjected = promptCacheKey != ""
		if compatPromptCacheInjected && profile.OpenAIAPIKey {
			promptCacheKey = p.IsolateCacheKey(promptCacheKey)
			compatPromptCacheTenantIsolated = true
		}
	}

	// Cursor can send a Responses input to the Chat URL. Decoding it through a Chat
	// request would discard input, so patch the raw body before OAuth normalization.
	var (
		responsesReq  *protocolopenai.ResponsesRequest
		responsesBody []byte
	)
	if isResponsesShape {
		responsesBody, err = sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("rewrite model in responses-shape body: %w", err)
		}
		// Raw model patching keeps Responses input and extension fields intact;
		// remove the listed incompatible extensions before tier and OAuth preparation
		// consume those bytes.
		// A failed optional field deletion leaves the current bytes available for the
		// remaining removals; later parsing still determines whether the resulting
		// body is valid.
		for _, field := range CursorResponsesUnsupportedFields {
			if stripped, derr := sjson.DeleteBytes(responsesBody, field); derr == nil {
				responsesBody = stripped
			}
		}
		var normalizedServiceTier string
		responsesBody, normalizedServiceTier, err = p.NormalizeBodyTier(responsesBody)
		if err != nil {
			return nil, fmt.Errorf("normalize service_tier in responses-shape body: %w", err)
		}
		// Save the normalized tier for completion processing.
		responsesReq = &protocolopenai.ResponsesRequest{
			Model:       upstreamModel,
			ServiceTier: normalizedServiceTier,
		}
	} else {
		// The Chat conversion requests a streaming Responses exchange so either
		// client output mode can be produced by the downstream Chat reader.
		responsesReq, err = p.ChatToResponses(&chatReq)
		if err != nil {
			return nil, fmt.Errorf("convert chat completions to responses: %w", err)
		}
		responsesReq.Model = upstreamModel
		p.NormalizeRequestTier(responsesReq)
		responsesBody, err = json.Marshal(responsesReq)
		if err != nil {
			return nil, fmt.Errorf("marshal responses request: %w", err)
		}
	}

	logFields := []zap.Field{
		zap.Int64("provider_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
		zap.Bool("responses_shape", isResponsesShape),
	}
	if compatPromptCacheInjected {
		logFields = append(logFields,
			zap.Bool("compat_prompt_cache_key_injected", true),
			zap.String("compat_prompt_cache_key_sha256", upstream.HashSensitiveValueForLog(promptCacheKey)),
		)
	}
	logging.L().Debug("openai chat_completions: model mapping applied", logFields...)

	// Codex preparation runs on a writable Responses map, retaining the reverse-
	// tool names and normalized cache/model values needed by the later output and
	// header stages.
	if profile.UsesCodex {
		var reqBody map[string]any
		if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
			return nil, fmt.Errorf("unmarshal for codex transform: %w", err)
		}
		isJSONObjectFormat := strings.EqualFold(strings.TrimSpace(gjson.GetBytes(responsesBody, "text.format.type").String()), "json_object")
		codexResult := gatewayprovider.ApplyCodexOAuthTransformWithOptions(reqBody, openai.CodexOAuthTransformOptions{
			SkipDefaultInstructions:             !isResponsesShape,
			OmitPromotedSystemMessagesFromInput: !isResponsesShape && !isJSONObjectFormat,
		})
		if codexResult.Error != nil {
			p.Reject(400, "invalid_request_error", codexResult.Error.Error())
			return nil, codexResult.Error
		}
		p.ToolNameReverse(codexResult.ToolNameReverse)
		if !isResponsesShape {
			protocolopenai.EnsureCodexInstructionsField(reqBody)
		}
		if codexResult.NormalizedModel != "" {
			upstreamModel = codexResult.NormalizedModel
		}
		if codexResult.PromptCacheKey != "" {
			promptCacheKey = codexResult.PromptCacheKey
		} else if promptCacheKey != "" {
			reqBody["prompt_cache_key"] = promptCacheKey
		}
		p.ProviderIdentity(reqBody, p.APIKeyID())
		responsesBody, err = json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("remarshal after codex transform: %w", err)
		}
	}
	if profile.Type == "apikey" {
		if trimmedKey := strings.TrimSpace(promptCacheKey); trimmedKey != "" {
			var reqBody map[string]any
			if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
				return nil, fmt.Errorf("unmarshal for prompt cache key injection: %w", err)
			}
			// API key conversion skips the Codex transform, so restore the ingress cache key
			// in the outgoing body when no key is already present.
			if existing, ok := reqBody["prompt_cache_key"].(string); !ok || strings.TrimSpace(existing) == "" {
				reqBody["prompt_cache_key"] = trimmedKey
				responsesBody, err = json.Marshal(reqBody)
				if err != nil {
					return nil, fmt.Errorf("remarshal after prompt cache key injection: %w", err)
				}
			}
		}
	}

	// Resolve fast-tier policy before capturing forwarded billing controls, so an
	// incoming tier removed or forced by policy is attributed from the outgoing
	// body.
	updatedBody, policyErr := p.ApplyChatFast(ctx, upstreamModel, responsesBody)
	if policyErr != nil {
		return nil, policyErr
	}
	responsesBody = updatedBody
	// Record the outgoing effort so fields discarded during conversion are not
	// reported as forwarded controls in usage records.
	reasoningEffort := requeststate.ExtractOpenAIReasoningEffortFromBody(responsesBody)
	reasoningEffort = gatewayprovider.ApplyThinkingEnabledFallback(reasoningEffort, responsesBody, billingModel)
	if serviceTier := requeststate.ExtractOpenAIServiceTierFromBody(responsesBody); serviceTier != nil {
		responsesReq.ServiceTier = *serviceTier
	} else {
		responsesReq.ServiceTier = ""
	}

	token, err := p.AccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	upstreamCtx, releaseUpstreamCtx := gatewayprovider.DetachUpstreamContext(ctx)
	upstreamReq, err := p.BuildChat(upstreamCtx, responsesBody, token, promptCacheKey)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	if promptCacheKey != "" {
		apiKeyID := p.APIKeyID()
		sessionKey := promptCacheKey
		if !compatPromptCacheTenantIsolated {
			sessionKey = p.UpstreamSessionKey(apiKeyID, promptCacheKey)
		}
		upstreamReq.Header.Set("session_id", upstream.GenerateSessionUUID(sessionKey))
	}

	// Prepare proxy state for this selected exchange; recursive agent recovery
	// enters RunChat again and repeats preparation for its newly built request.
	p.PrepareTransport()
	resp, err := p.Send(upstreamReq)
	if err != nil {
		return nil, p.TransportError(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := p.ReadUpstreamError(resp)
		// Spend the request-context agent recovery guard before recursion, allowing a
		// rebuilt attempt while repeated task rejection proceeds to ordinary failure
		// handling.
		if !requeststate.AgentTaskRecoveryTried(ctx) && p.IsAgentIdentity(ctx) && openai.IsAgentTaskInvalidHTTPResponse(resp.StatusCode, respBody) {
			if err := p.RecoverAgentTask(ctx); err != nil {
				return nil, fmt.Errorf("agent identity task recovery failed: %w", err)
			}
			return RunChat(requeststate.WithAgentTaskRecovery(ctx), body, promptCacheKey, defaultMappedModel, compatPromptCacheTenantIsolated, p)
		}
		respBody = p.RedactErrorBody(ctx, respBody)
		// ReadUpstreamError already closed the network body; replace its replay copy
		// with credential-redacted bytes before health classification and final Chat
		// presentation.
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		return p.ChatErrorResponse(resp, billingModel)
	}

	var result *Result
	var handleErr error
	var nativeResult *openai.CompatResponseResult
	output := upstream.NewDeferredOutputContext(p.Sink())
	options := p.ChatResponseOptions(resp, originalModel, billingModel, upstreamModel)
	if clientStream {
		nativeResult, handleErr = openai.ReadChatStreaming(resp, output, options, originalModel, upstreamModel, startTime, len(body))
	} else {
		nativeResult, handleErr = openai.ReadChatBuffered(resp, output, options, originalModel, upstreamModel, startTime)
	}
	result = FromCompatResult(nativeResult, billingModel)
	// A cyber-policy observation prevents returning successful completion data
	// even if the response reader finished; retain its existing error or supply
	// the warning failure.
	if p.CyberPolicy() {
		if handleErr == nil {
			handleErr = forwardcore.ErrCyberPolicyForwarded
		}
		return nil, handleErr
	}

	// Attach the final effort and billing tier to successful results.
	// Prefer the upstream's echoed tier, then the body after fast-policy processing.
	// A filtered tier must not reappear from the original client request.
	if handleErr == nil && result != nil {
		if tier := p.ResolvedServiceTier(requeststate.ExtractOpenAIServiceTierFromBody(responsesBody)); tier != nil {
			result.ServiceTier = tier
		}
		result.ReasoningEffort = reasoningEffort
	}

	// Update Codex usage from response headers for eligible credentials.
	// Spark shadows obtain their codex_* snapshot through QueryUsage's bengalfox query.
	if handleErr == nil && profile.UsesCodex && !profile.Shadow {
		p.UpdateCodexUsage(ctx, resp.Header)
	}

	return result, handleErr
}
