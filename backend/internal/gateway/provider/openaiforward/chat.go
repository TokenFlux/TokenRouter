// Chat 编排保留 Responses 形状短路、平台分流和同账号恢复，不另开账号循环。
package openaiforward

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
	"io"
	"net/http"
	"strings"
	"time"
)

// CursorResponsesUnsupportedFields 保留原 Responses 形状的专属过滤字段。
var CursorResponsesUnsupportedFields = []string{"prompt_cache_retention", "safety_identifier", "metadata", "stream_options"}

func RunChat(ctx context.Context, body []byte, promptCacheKey, defaultMappedModel string, compatPromptCacheTenantIsolated bool, p ChatPorts) (*Result, error) {
	profile, err := p.PrepareChat(ctx)
	if err != nil {
		return nil, err
	}
	if !p.ClientAllowed(ctx, body) {
		p.PolicyDenied()
		p.Reject(403, "forbidden_error", "This account only allows Codex official clients")
		return nil, errors.New("codex_cli_only restriction: only codex official clients are allowed")
	}
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
	if profile.Platform == "grok" {
		if profile.Protocol == protocol.ProtocolOpenAIChatCompletions {
			return p.DispatchChat(ctx, DispatchRawChat, body, promptCacheKey, defaultMappedModel)
		}
		if profile.Protocol == protocol.ProtocolOpenAIResponses {
			if eligible, reason := p.GrokBridgeEligible(body); !eligible {
				return nil, fmt.Errorf("configured Grok Responses conversion cannot preserve request: %s", reason)
			}
			return p.DispatchChat(ctx, DispatchGrok, body, promptCacheKey, defaultMappedModel)
		}
		if profile.GrokOAuth {
			if eligible, reason := p.GrokBridgeEligible(body); eligible {
				return p.DispatchChat(ctx, DispatchGrok, body, promptCacheKey, defaultMappedModel)
			} else {
				p.Debug("grok chat_completions: using raw fallback",
					zap.Int64("account_id", profile.ID),
					zap.String("reason", reason),
				)
			}
		}
		return p.DispatchChat(ctx, DispatchRawChat, body, promptCacheKey, defaultMappedModel)
	}
	if err := p.ValidateEffort(body, gjson.GetBytes(body, "model").String()); err != nil {
		p.ChatError(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}

	// 某些客户端会把 Responses 形状请求发送到 Chat Completions URL；必须先于
	// 自适应协议分流识别，否则会把 input 原样发给只接受 messages 的上游。
	isResponsesShape := !gjson.GetBytes(body, "messages").Exists() && gjson.GetBytes(body, "input").Exists()

	// 自适应账号的标准 Chat 入站使用供应商原生 CC 端点；Responses 形状下，
	// DeepSeek / Kimi 保留原生 Responses，智谱先转换为 Chat。
	if profile.Adaptive {
		if !isResponsesShape {
			return p.DispatchChat(ctx, DispatchRawChat, body, promptCacheKey, defaultMappedModel)
		}
		if !profile.SupportsNativeCN {
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
		// DeepSeek / Kimi 原生 Responses 请求继续走下方 Responses→Chat 回程转换。
	}

	// 固定 Anthropic 协议走原生 Anthropic 端点。
	if profile.Anthropic {
		return p.DispatchChat(ctx, DispatchAnthropic, body, promptCacheKey, defaultMappedModel)
	}

	// 固定 Chat 协议的 CN 账号，以及其他 APIKey 账号在探测/管理员策略要求
	// Chat 时，均走 CC 直转。
	if profile.RawChat {
		return p.DispatchChat(ctx, DispatchRawChat, body, promptCacheKey, defaultMappedModel)
	}
	if !profile.CNProvider && p.DefaultChat() {
		return p.DispatchChat(ctx, DispatchRawChat, body, promptCacheKey, defaultMappedModel)
	}

	startTime := time.Now()

	// 1. Parse Chat Completions request
	var chatReq protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := chatReq.Model
	clientStream := chatReq.Stream

	// 2. 先解析最终模型，供兼容缓存键按上游模型族派生稳定种子。
	billingModel := p.BillingModel(originalModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)

	promptCacheKey = strings.TrimSpace(promptCacheKey)
	compatPromptCacheInjected := false
	if promptCacheKey == "" && !isResponsesShape && (profile.UsesCodex || profile.OpenAIAPIKey) && p.AutoCacheKey(upstreamModel) {
		promptCacheKey = p.DeriveCacheKey(&chatReq, upstreamModel)
		compatPromptCacheInjected = promptCacheKey != ""
		if compatPromptCacheInjected && profile.OpenAIAPIKey {
			promptCacheKey = p.IsolateCacheKey(promptCacheKey)
			compatPromptCacheTenantIsolated = true
		}
	}

	// 3. 构建 Responses 上游报文。Cursor 可能向 Chat 端点发送只有 input 的
	// Responses 报文；套用 Chat 结构会丢失 input 并被上游拒绝。
	// 此形状保留原文，只改写模型，后续 OAuth 转换仍处理 store/stream/instructions。
	var (
		responsesReq  *protocolopenai.ResponsesRequest
		responsesBody []byte
	)
	if isResponsesShape {
		responsesBody, err = sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("rewrite model in responses-shape body: %w", err)
		}
		// 原文直转不会像结构体重建那样丢弃未知字段，因此显式移除 Codex 不支持的参数。
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
		// 保留原文中的档位，供后续完成结果使用。
		responsesReq = &protocolopenai.ResponsesRequest{
			Model:       upstreamModel,
			ServiceTier: normalizedServiceTier,
		}
	} else {
		// 普通路径转换 Chat→Responses，转换器固定上游 stream=true。
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
		zap.Int64("account_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
		zap.Bool("responses_shape", isResponsesShape),
	}
	if compatPromptCacheInjected {
		logFields = append(logFields,
			zap.Bool("compat_prompt_cache_key_injected", true),
			zap.String("compat_prompt_cache_key_sha256", p.HashForLog(promptCacheKey)),
		)
	}
	p.Debug("openai chat_completions: model mapping applied", logFields...)

	if profile.UsesCodex {
		var reqBody map[string]any
		if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
			return nil, fmt.Errorf("unmarshal for codex transform: %w", err)
		}
		isJSONObjectFormat := strings.EqualFold(strings.TrimSpace(gjson.GetBytes(responsesBody, "text.format.type").String()), "json_object")
		codexResult := p.CodexTransform(reqBody, native.CodexOAuthTransformOptions{
			SkipDefaultInstructions:             !isResponsesShape,
			OmitPromotedSystemMessagesFromInput: !isResponsesShape && !isJSONObjectFormat,
		})
		if codexResult.Error != nil {
			p.Reject(400, "invalid_request_error", codexResult.Error.Error())
			return nil, codexResult.Error
		}
		p.ToolNameReverse(codexResult.ToolNameReverse)
		if !isResponsesShape {
			p.EnsureInstructions(reqBody)
		}
		if codexResult.NormalizedModel != "" {
			upstreamModel = codexResult.NormalizedModel
		}
		if codexResult.PromptCacheKey != "" {
			promptCacheKey = codexResult.PromptCacheKey
		} else if promptCacheKey != "" {
			reqBody["prompt_cache_key"] = promptCacheKey
		}
		p.AccountIdentity(reqBody, p.APIKeyID())
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
			// API Key 账号的 Chat Completions 转 Responses 路径不会经过 Codex transform，
			// 需要在这里把入口解析出的 prompt_cache_key 补回上游请求体。
			if existing, ok := reqBody["prompt_cache_key"].(string); !ok || strings.TrimSpace(existing) == "" {
				reqBody["prompt_cache_key"] = trimmedKey
				responsesBody, err = json.Marshal(reqBody)
				if err != nil {
					return nil, fmt.Errorf("remarshal after prompt cache key injection: %w", err)
				}
			}
		}
	}

	// 4b. Apply OpenAI fast policy (may filter service_tier or block the request).
	updatedBody, policyErr := p.ApplyChatFast(ctx, upstreamModel, responsesBody)
	if policyErr != nil {

		return nil, policyErr
	}
	responsesBody = updatedBody
	// Usage Log 记录最终实际发送给上游的 effort，避免把转换过程中被丢弃的
	// Chat Completions 非标准字段误记为已转发。
	reasoningEffort := p.EffectiveEffort(responsesBody, body, upstreamModel, billingModel, originalModel)
	reasoningEffort = p.ThinkingFallback(reasoningEffort, responsesBody, billingModel)
	if serviceTier := p.ServiceTier(responsesBody); serviceTier != nil {
		responsesReq.ServiceTier = *serviceTier
	} else {
		responsesReq.ServiceTier = ""
	}

	// 5. Get access token
	token, err := p.AccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	// 6. Build upstream request
	upstreamCtx, releaseUpstreamCtx := p.UpstreamContext(ctx)
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
		upstreamReq.Header.Set("session_id", p.SessionUUID(sessionKey))
	}

	// 发送前固定本次代理投影，保留重试复用的范围。
	p.PrepareTransport()
	resp, err := p.Send(upstreamReq)
	if err != nil {
		return nil, p.TransportError(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 8. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, _ := p.ReadUpstreamError(resp)
		if !p.AgentRecoveryTried(ctx) && p.IsAgentIdentity(ctx) && p.InvalidAgentTask(resp.StatusCode, respBody) {
			if err := p.RecoverAgentTask(ctx); err != nil {
				return nil, fmt.Errorf("agent identity task recovery failed: %w", err)
			}
			return RunChat(p.MarkAgentRecovery(ctx), body, promptCacheKey, defaultMappedModel, compatPromptCacheTenantIsolated, p)
		}
		respBody = p.RedactErrorBody(ctx, respBody)
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := p.ErrorMessage(respBody)
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		return p.ChatErrorResponse(resp, billingModel)
	}

	// 9. Handle normal response
	var result *Result
	var handleErr error
	var nativeResult *native.CompatResponseResult
	output := upstream.NewDeferredOutputContext(p.Sink())
	options := p.ChatResponseOptions(resp, originalModel, billingModel, upstreamModel)
	if clientStream {
		nativeResult, handleErr = native.ReadChatStreaming(resp, output, options, originalModel, upstreamModel, startTime, len(body))
	} else {
		nativeResult, handleErr = native.ReadChatBuffered(resp, output, options, originalModel, upstreamModel, startTime)
	}
	result = FromCompatResult(nativeResult, billingModel)
	if p.CyberPolicy() {
		if handleErr == nil {
			handleErr = p.CyberError()
		}
		return nil, handleErr
	}

	// 将档位和最终 effort 传入完成结果供计费使用.
	// 计费 tier 优先采用上游回显值；上游未回显时回退到最终出站 body（经过
	// fast policy filter/force 之后）里的 tier，policy filter 删掉字段后不再
	// 按原请求 Fast 计费。
	if handleErr == nil && result != nil {
		if tier := p.ResolvedServiceTier(p.ServiceTier(responsesBody)); tier != nil {
			result.ServiceTier = tier
		}
		result.ReasoningEffort = reasoningEffort
	}

	// OAuth 账号从响应头提取并保存 Codex 用量快照。
	// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
	if handleErr == nil && profile.UsesCodex && !profile.Shadow {
		p.UpdateCodexUsage(ctx, resp.Header)
	}

	return result, handleErr
}
