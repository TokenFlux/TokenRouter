// Chat-only 上游的两条转换链保留各自模型、策略和输出次序。
package openaiforward

import (
	"context"
	"encoding/json"
	"fmt"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"net/http"
	"strings"
	"time"
)

func MessagesViaRawChat(ctx context.Context, body []byte, defaultMappedModel string, p RawFallbackPorts) (*Result, error) {
	profile := p.Profile()
	startTime := time.Now()

	// 1. 解析 Anthropic 请求。
	var anthropicReq protocolanthropic.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}
	originalModel := anthropicReq.Model
	if strings.TrimSpace(originalModel) == "" {
		p.Error(http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	if err := p.ValidateEffort(body, originalModel); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	p.NormalizeModel(&anthropicReq)
	clientStream := anthropicReq.Stream

	// 2. 将 Anthropic 请求直接转换为 Chat Completions。
	chatReq, err := p.AnthropicToChat(&anthropicReq)
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert anthropic to chat completions: %w", err)
	}

	billingModel := p.BillingModel(anthropicReq.Model, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	chatReq.Model = upstreamModel
	chatReq.ReasoningEffort = p.MessagesEffort(&anthropicReq, upstreamModel, chatReq.ReasoningEffort)
	chatReq.Stream = clientStream
	if clientStream {
		chatReq.StreamOptions = &protocolopenai.ChatStreamOptions{IncludeUsage: true}
	}

	convertedEffort := chatReq.ReasoningEffort
	reasoningEffort := &convertedEffort
	reasoningEffort = p.ThinkingFallback(reasoningEffort, body, billingModel)
	serviceTier := p.ServiceTier(body)

	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions request: %w", err)
	}
	if normalizedBody, normalized := p.NormalizeGLM(chatBody, upstreamModel); normalized {
		chatBody = normalizedBody
	}
	// Messages 桥只对客户端显式指定的推理强度执行分组超限策略。
	if profile.OpenAI {
		policyBody, changed, policyErr := p.ApplyEffort(ctx, chatBody)
		if policyErr != nil {
			return nil, policyErr
		}
		if changed {
			chatBody = policyBody
			if effectiveEffort := strings.TrimSpace(gjson.GetBytes(chatBody, "reasoning_effort").String()); effectiveEffort != "" {
				reasoningEffort = &effectiveEffort
			}
		}
	}
	chatBody, err = p.FastFallback(ctx, upstreamModel, chatBody)
	if err != nil {
		return nil, err
	}
	if serviceTier == nil {
		serviceTier = p.ServiceTier(chatBody)
	}

	p.Debug("openai messages: forwarding via raw chat completions",
		zap.Int64("account_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)

	// 3. 通过共享 CC 管线构造并发送上游请求。
	apiKey, targetURL, err := p.Target(ctx)
	if err != nil {
		return nil, err
	}
	p.Endpoint("/v1/chat/completions")
	resp, err := p.SendCC(ctx, targetURL, chatBody, clientStream, apiKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// 4. 处理上游错误响应。
	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := p.ReadUpstreamError(resp)
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		// 非 failover 错误交给共享兼容处理器返回 Anthropic 格式，确保错误
		// 透传规则、ops 记录和 cyber_policy 检测保持一致。
		return p.AnthropicError(resp, billingModel)
	}

	// 5. 转换上游响应。
	output := upstream.NewDeferredOutputContext(p.Sink())
	options := p.RawOptions(resp, billingModel, upstreamModel, serviceTier)
	var result *native.CompatResponseResult
	if clientStream {
		result, err = native.ReadCCAsMessagesStreaming(output, resp, options, originalModel, upstreamModel, reasoningEffort, startTime)
	} else {
		result, err = native.ReadCCAsMessagesBuffered(output, resp, options, originalModel, upstreamModel, reasoningEffort, startTime)
	}
	return FromCompatResult(result, billingModel), err
}
func ResponsesViaRawChat(ctx context.Context, body []byte, p RawFallbackPorts) (*Result, error) {
	profile := p.Profile()
	startTime := time.Now()

	var responsesReq protocolopenai.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := strings.TrimSpace(responsesReq.Model)
	if originalModel == "" {
		p.Error(http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	clientStream := responsesReq.Stream
	serviceTier := p.ServiceTier(body)
	// custom 工具（如 codex 的 exec）降级为 function 工具转发，回程需按名字还原为
	// custom_tool_call 项，先记下名字集合；tool_search 工具同理，回程还原为
	// tool_search_call 项；namespace 子工具（如 MCP 工具）摊平转发，回程按映射还原
	// 为带 namespace 字段的 function_call 项。
	effectiveTools, err := bridge.EffectiveResponsesTools(&responsesReq)
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("resolve responses tools: %w", err)
	}
	customTools := bridge.CustomToolNames(effectiveTools)
	functionTools := bridge.FunctionToolNames(effectiveTools)
	toolSearch := bridge.HasToolSearchTool(effectiveTools)
	namespaceTools := bridge.NamespaceToolNames(effectiveTools)

	// 带明文 summary 的历史 reasoning 顺手刷新缓存，帮助 encrypted-only 副本自愈。
	p.RecacheInput(responsesReq.Input)
	chatReq, err := bridge.ResponsesToChatCompletionsRequestWithOptions(&responsesReq, &bridge.ResponsesToChatOptions{
		ReasoningContentByID: p.ReasoningContent,
	})
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert responses to chat completions: %w", err)
	}

	billingModel := p.BillingModel(originalModel, "")
	upstreamModel := p.UpstreamModel(billingModel)
	chatReq.Model = upstreamModel
	if clientStream {
		chatReq.StreamOptions = &protocolopenai.ChatStreamOptions{IncludeUsage: true}
	}

	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions fallback request: %w", err)
	}
	chatBody, err = p.FastFallback(ctx, upstreamModel, chatBody)
	if err != nil {
		return nil, err
	}
	// Usage Log 以 Responses→Chat 转换和策略处理后的最终上游请求为准。
	reasoningEffort := p.EffectiveEffort(chatBody, body, upstreamModel, billingModel, originalModel)
	// 国产模型没有显式 effort 档位时，thinking 启用后补默认展示值。
	reasoningEffort = p.ThinkingFallback(reasoningEffort, chatBody, billingModel)
	if serviceTier == nil {
		serviceTier = p.ServiceTier(chatBody)
	}

	p.Debug("openai responses: forwarding via raw chat completions",
		zap.Int64("account_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)
	p.ObserveModel(upstreamModel)

	// 通过共享 CC 管线构造并发送上游请求。
	apiKey, targetURL, err := p.Target(ctx)
	if err != nil {
		return nil, err
	}
	p.Endpoint("/v1/chat/completions")
	resp, err := p.SendCC(ctx, targetURL, chatBody, clientStream, apiKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := p.ReadUpstreamError(resp)
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		return p.ResponsesError(ctx, resp, chatBody, billingModel)
	}

	output := upstream.NewDeferredOutputContext(p.Sink())
	options := p.RawOptions(resp, billingModel, upstreamModel, serviceTier)
	var result *native.CompatResponseResult
	if clientStream {
		result, err = native.ReadCCAsResponsesStreaming(output, resp, options, originalModel, upstreamModel, reasoningEffort, startTime, customTools, functionTools, toolSearch, namespaceTools)
	} else {
		result, err = native.ReadCCAsResponsesBuffered(output, resp, options, originalModel, upstreamModel, reasoningEffort, startTime, customTools, functionTools, toolSearch, namespaceTools)
	}
	return FromCompatResult(result, billingModel), err
}
