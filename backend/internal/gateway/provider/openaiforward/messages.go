// Messages 的请求转换、会话恢复和流/非流消费保持独立，账号切换由外层拥有。
package openaiforward

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"io"
	"net/http"
	"strings"
	"time"
)

func RunMessages(ctx context.Context, body []byte, promptCacheKey, defaultMappedModel string, p MessagesPorts) (*Result, error) {
	profile, route, err := p.Prepare(ctx)
	if err != nil {
		return nil, err
	}
	if route != DispatchResponses {
		return p.Dispatch(ctx, route, body, promptCacheKey, defaultMappedModel)
	}
	startTime := time.Now()

	// 1. Parse Anthropic request
	var anthropicReq protocolanthropic.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}
	if err := p.ValidateEffort(body, anthropicReq.Model); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	anthropicDigestReq := p.CloneDigest(&anthropicReq)
	originalModel := anthropicReq.Model
	p.NormalizeModel(&anthropicReq)
	normalizedModel := anthropicReq.Model
	clientStream := anthropicReq.Stream // client's original stream preference

	// 2. Model mapping
	billingModel := p.BillingModel(normalizedModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	apiKeyID := p.APIKeyID()
	anthropicDigestChain := ""
	anthropicMatchedDigestChain := ""
	compatPromptCacheInjected := false
	// Grok 不经过 gpt-5/codex 兼容注入器，但 Claude Code 仍携带稳定的会话标识。
	// 优先将它作为 Grok 提示缓存种子，使多轮 /v1/messages 流量可以命中 xAI
	// 服务端缓存。
	if promptCacheKey == "" && profile.Platform == "grok" {
		if sessionSeed := p.ClaudeSession(body); sessionSeed != "" {
			promptCacheKey = sessionSeed
			compatPromptCacheInjected = true
		} else if sessionSeed := p.MetadataSession(&anthropicReq); sessionSeed != "" {
			promptCacheKey = sessionSeed
			compatPromptCacheInjected = true
		}
	}
	if promptCacheKey == "" && p.AutoCacheKey(upstreamModel) {
		promptCacheKey = p.MetadataSession(&anthropicReq)
		if promptCacheKey == "" {
			promptCacheKey = p.CacheControlKey(&anthropicReq)
		}
		if promptCacheKey == "" {
			anthropicDigestChain = p.DigestChain(anthropicDigestReq)
			if reusedKey, matchedChain := p.FindDigestKey(apiKeyID, anthropicDigestChain); reusedKey != "" {
				promptCacheKey = reusedKey
				anthropicMatchedDigestChain = matchedChain
			} else {
				promptCacheKey = p.DigestKey(anthropicDigestChain)
			}
		}
		compatPromptCacheInjected = promptCacheKey != ""
	}
	compatReplayTrimmed := false
	compatReplayGuardEnabled := p.AutoCacheKey(upstreamModel)
	compatContinuationEnabled := p.ContinuationEnabled(upstreamModel)
	previousResponseID := ""
	if compatContinuationEnabled {
		previousResponseID = p.ResponseID(ctx, promptCacheKey)
	}
	compatContinuationDisabled := compatContinuationEnabled &&
		p.ContinuationDisabled(ctx, promptCacheKey)
	compatTurnState := ""
	// ChatGPT/Codex 凭据依赖 session_id 与 x-codex-turn-state；截成 12 条滑动窗口
	// 会让缓存前缀停留在 system/tools，因此保留完整重放供上游逐轮扩展缓存。
	if compatReplayGuardEnabled && !profile.UsesCodex && previousResponseID == "" && !compatContinuationDisabled {
		compatReplayTrimmed = p.ReplayGuard(&anthropicReq)
	}

	// 3. Convert Anthropic → Responses after compatibility-only replay guard.
	responsesReq, err := p.Convert(&anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("convert anthropic to responses: %w", err)
	}

	// 上游保持流式，兼容不支持同步模式的端点；回程格式由客户端原始偏好决定。
	responsesReq.Stream = true
	isStream := true

	// 3b. Handle BetaFastMode → service_tier: "priority"
	if p.BetaFast() {
		responsesReq.ServiceTier = "priority"
	}

	responsesReq.Model = upstreamModel
	if responsesReq.Reasoning != nil {
		responsesReq.Reasoning.Effort = p.MessagesEffort(&anthropicReq, upstreamModel, responsesReq.Reasoning.Effort)
	}
	if previousResponseID != "" {
		responsesReq.PreviousResponseID = previousResponseID
		p.TrimLatestTurn(responsesReq)
	}
	if compatReplayGuardEnabled && !profile.UsesCodex {
		p.TodoGuard(responsesReq)
	}

	logFields := []zap.Field{
		zap.Int64("account_id", profile.ID),
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
			zap.String("compat_prompt_cache_key_sha256", p.HashForLog(promptCacheKey)),
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
			zap.String("compat_previous_response_id", p.Truncate(previousResponseID, p.LogIDLimit())),
		)
	}
	if compatTurnState != "" {
		logFields = append(logFields, zap.Bool("compat_turn_state_attached", true))
	}
	p.Debug("openai messages: model mapping applied", logFields...)

	// 4. Marshal Responses request body, then apply the ChatGPT/Codex transform.
	responsesBody, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("marshal responses request: %w", err)
	}

	if profile.UsesCodex && profile.Platform != "grok" {
		var reqBody map[string]any
		if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
			return nil, fmt.Errorf("unmarshal for codex transform: %w", err)
		}
		codexResult := p.CodexTransform(reqBody, native.CodexOAuthTransformOptions{
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
		if strings.TrimSpace(existingInstructions) == "" {
			existingInstructions = native.ExtractPromptLikeInstructionsFromInput(reqBody)
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
		p.EnsureInstructions(reqBody)
		if p.AutoCacheKey(upstreamModel) {
			p.TodoGuardBody(reqBody)
		}
		if codexResult.NormalizedModel != "" {
			upstreamModel = codexResult.NormalizedModel
		}
		if codexResult.PromptCacheKey != "" {
			promptCacheKey = codexResult.PromptCacheKey
		}
		p.AccountIdentity(reqBody, apiKeyID)
		delete(reqBody, "prompt_cache_key")
		if p.AutoCacheKey(upstreamModel) {
			compatTurnState = p.TurnState(ctx, promptCacheKey)
		}
		// OAuth Codex 转换固定上游 stream=true，读取始终使用流式处理器。
		isStream = true
		responsesBody, err = json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("remarshal after codex transform: %w", err)
		}
	}

	// API Key 账号也通过请求体传递 prompt_cache_key，供兼容 Responses 的上游
	// 推导稳定会话标识，保持 Messages 桥与原生 Responses 客户端的缓存契约。
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
	// Messages 桥只在客户端显式提供 output_config.effort 时绑定策略；此处
	// 在所有模型/提示词改写完成后统一执行，确保出站请求和计费结果一致。
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

	// 4c. Apply OpenAI fast policy (may filter service_tier or block the request).
	// 按请求体 service_tier 应用与 Claude fast-mode beta 对应的过滤语义。
	updatedBody, policyErr := p.ApplyFast(ctx, upstreamModel, responsesBody)
	if policyErr != nil {

		return nil, policyErr
	}
	responsesBody = updatedBody
	if serviceTier := p.ServiceTier(responsesBody); serviceTier != nil {
		responsesReq.ServiceTier = *serviceTier
	} else {
		responsesReq.ServiceTier = ""
	}
	grokCacheIdentity := ""
	if profile.Platform == "grok" {
		grokIntentBody := responsesBody
		grokCacheIdentity = p.GrokCacheIdentity(grokIntentBody, promptCacheKey, upstreamModel)
		patchedBody, patchErr := p.PatchGrokBody(grokIntentBody, upstreamModel)
		if patchErr != nil {
			return nil, patchErr
		}
		responsesBody, patchErr = p.ApplyGrokCache(patchedBody, grokIntentBody, grokCacheIdentity, profile.GrokOAuth)
		if patchErr != nil {
			return nil, fmt.Errorf("apply grok prompt cache identity: %w", patchErr)
		}
		responsesBody, patchErr = p.GrokFreeToolRoute(responsesBody, grokIntentBody, grokCacheIdentity)
		if patchErr != nil {
			return nil, fmt.Errorf("apply grok Free function-tool cache route: %w", patchErr)
		}
	}

	// 5. Get access token
	token, err := p.Credential(ctx)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	// 6. Build upstream request
	if profile.UsesCodex && profile.Platform != "grok" {
		// Messages 兼容桥即使 body 未带 todo-guard/prompt_cache_key 标记（如映射到非
		// gpt-5/codex 模型），也必须让 buildUpstreamRequest 走 bridge 分支，以保留
		// 既有 body/session/conversation 行为。身份头在 post-build 阶段统一恢复。
		p.BindMessagesBridge(true)
	}
	upstreamCtx, releaseUpstreamCtx := p.UpstreamContext(ctx)
	upstreamReq, err := p.Build(ctx, upstreamCtx, responsesBody, token, isStream, promptCacheKey, grokCacheIdentity)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	// 使用隔离会话键派生的确定性 UUID 覆盖 session_id，保持不同 Key 的会话隔离。
	if profile.Platform != "grok" && promptCacheKey != "" {
		isolatedSessionID := p.IsolatedSessionID(apiKeyID, promptCacheKey)
		upstreamReq.Header.Set("session_id", isolatedSessionID)
		if upstreamReq.Header.Get("conversation_id") != "" {
			upstreamReq.Header.Set("conversation_id", isolatedSessionID)
		}
	}
	if profile.UsesCodex && profile.Platform != "grok" {
		// buildUpstreamRequest 保留 Messages bridge 的 body/session 兼容行为，并会先
		// 清除身份头。真正发送前恢复完整 Codex 身份，避免 ChatGPT Codex 上游因缺失
		// originator/OpenAI-Beta 返回 404（issue #3901）。
		p.RestoreIdentity(upstreamReq.Header)
		p.Debug("openai messages: upstream identity restored",
			zap.Int64("account_id", profile.ID),
			zap.String("upstream_model", upstreamModel),
			zap.Bool("compat_identity_restored", true),
		)
	}
	if profile.UsesCodex && promptCacheKey != "" && strings.TrimSpace(p.Header("conversation_id")) == "" {
		upstreamReq.Header.Del("conversation_id")
	}
	if compatTurnState != "" && upstreamReq.Header.Get("x-codex-turn-state") == "" {
		upstreamReq.Header.Set("x-codex-turn-state", compatTurnState)
	}

	// 发送前固定本次代理投影，保留重试复用的范围。
	p.PrepareTransport()
	// Grok 可能拒绝在不同 OAuth 账号或缓存身份下回放的加密推理。与
	// forwardGrokResponses 保持一致：先剥离密文并重试一次，再将 400 作为硬失败
	// 或故障转移触发条件处理。
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if profile.Platform != "grok" {
				break
			}
			upstreamCtxRetry, releaseRetry := p.UpstreamContext(ctx)
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
		respBody := p.ReadErrorBody(resp)
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		// 优先识别明确的解密错误；若出站请求仍带有 reasoning.encrypted_content，
		// 任意 400 也允许剥离一次（账号切换经常只返回不透明的 "Upstream error: 400"）。
		shouldStrip := p.GrokInvalidEncrypted(resp.StatusCode, respBody) ||
			p.GrokHasEncrypted(responsesBody)
		if !shouldStrip {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			break
		}
		retryBody, changed, trimErr := p.TrimGrokEncrypted(responsesBody)
		if trimErr != nil {
			return nil, fmt.Errorf("prepare Grok invalid encrypted_content retry: %w", trimErr)
		}
		if !changed {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			break
		}
		responsesBody = retryBody
		p.Info("openai messages: retrying after stripping invalid Grok encrypted_content",
			zap.Int64("account_id", profile.ID),
			zap.Bool("cache_identity_present", strings.TrimSpace(grokCacheIdentity) != ""),
			zap.String("upstream_error_preview", p.Truncate(string(respBody), 240)),
		)
	}
	defer func() { _ = resp.Body.Close() }()

	// 8. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, _ := p.ReadUpstreamError(resp)
		if !p.AgentRecoveryTried(ctx) && p.IsAgentIdentity(ctx) && p.InvalidAgentTask(resp.StatusCode, respBody) {
			if err := p.RecoverAgentTask(ctx); err != nil {
				return nil, fmt.Errorf("agent identity task recovery failed: %w", err)
			}
			return RunMessages(p.MarkAgentRecovery(ctx), body, promptCacheKey, defaultMappedModel, p)
		}
		respBody = p.RedactErrorBody(ctx, respBody)
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := p.ErrorMessage(respBody)
		if previousResponseID != "" && (p.PreviousMissing(resp.StatusCode, upstreamMsg, respBody) || p.PreviousUnsupported(resp.StatusCode, upstreamMsg, respBody)) {
			if p.PreviousUnsupported(resp.StatusCode, upstreamMsg, respBody) {
				p.DisableContinuation(ctx, promptCacheKey)
			} else {
				p.DeleteResponseID(ctx, promptCacheKey)
			}
			p.Info("openai messages: previous_response_id unavailable, retrying without continuation",
				zap.Int64("account_id", profile.ID),
				zap.String("previous_response_id", p.Truncate(previousResponseID, p.LogIDLimit())),
				zap.String("upstream_model", upstreamModel),
				zap.Bool("compat_continuation_supported", profile.ContinuationSupported),
			)
			return RunMessages(ctx, body, promptCacheKey, defaultMappedModel, p)
		}
		// Grok 切换账号后的历史记录经常解密失败；在客户端请求体层剥离一次加密推理，
		// 让故障转移账号可以接收多轮工具续接，避免连续返回 400。
		if profile.Platform == "grok" &&
			p.GrokInvalidEncrypted(resp.StatusCode, respBody) &&
			!p.GrokStripRetried(ctx) {
			if strippedBody, ok := p.StripThinkingSignatures(body); ok {
				p.Info("openai messages: stripping thinking signatures for Grok failover retry",
					zap.Int64("account_id", profile.ID),
				)
				return RunMessages(p.MarkGrokStrip(ctx), strippedBody, promptCacheKey, defaultMappedModel, p)
			}
		}
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		// 非 failover 错误按 Anthropic 格式返回。
		return p.ErrorResponse(resp, billingModel)
	}
	if profile.Platform == "grok" && profile.Type == "oauth" && !profile.Shadow {
		p.UpdateGrokUsage(ctx, upstreamModel, resp.Header, resp.StatusCode)
	}

	if profile.UsesCodex && promptCacheKey != "" {
		if turnState := strings.TrimSpace(resp.Header.Get("x-codex-turn-state")); turnState != "" {
			p.BindTurnState(ctx, promptCacheKey, turnState)
		}
	}

	// 9. Handle normal response
	// 上游始终流式，回程按客户端偏好选择格式。
	var result *Result
	var handleErr error
	var nativeResult *native.CompatResponseResult
	output := upstream.NewDeferredOutputContext(p.Sink())
	responseOptions := p.ResponseOptions(resp, originalModel, billingModel, upstreamModel)
	if clientStream {
		nativeResult, handleErr = native.ReadMessagesStreaming(resp, output, responseOptions, originalModel, upstreamModel, startTime)
	} else {
		nativeResult, handleErr = native.ReadMessagesBuffered(resp, output, responseOptions, originalModel, upstreamModel, startTime)
	}
	result = FromCompatResult(nativeResult, billingModel)
	if p.CyberPolicy() {
		if handleErr == nil {
			handleErr = p.CyberError()
		}
		return nil, handleErr
	}

	// 将档位和最终 effort 传入完成结果供计费使用
	if handleErr == nil && result != nil {
		if compatContinuationEnabled && promptCacheKey != "" && result.ResponseID != "" {
			p.BindResponseID(ctx, promptCacheKey, result.ResponseID)
		}
		if promptCacheKey != "" && anthropicDigestChain != "" {
			p.BindDigestKey(apiKeyID, anthropicDigestChain, promptCacheKey, anthropicMatchedDigestChain)
		}
		// 计费 tier 优先采用上游回显值；上游未回显时回退到最终出站 body（经过
		// fast policy filter/force 之后）里的 tier。
		if tier := p.ResolvedServiceTier(p.ServiceTier(responsesBody)); tier != nil {
			result.ServiceTier = tier
		}
		if responsesReq.Reasoning != nil && responsesReq.Reasoning.Effort != "" {
			re := responsesReq.Reasoning.Effort
			result.ReasoningEffort = &re
		}
	}

	// OAuth 账号从响应头提取并保存 Codex 用量快照。
	// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
	if handleErr == nil && profile.Type == "oauth" && !profile.Shadow && profile.Platform != "grok" {
		p.UpdateCodexUsage(ctx, resp.Header)
	}

	return result, handleErr
}
