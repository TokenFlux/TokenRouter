// 原生 Anthropic 的三类入站保留各自转换、资金观察时点及取消策略。
package openaiforward

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
	"net/http"
	"strings"
	"time"
)

type NativeAnthropicKind uint8

const (
	NativeMessages NativeAnthropicKind = iota
	NativeResponses
	NativeChat
)

func ForwardNativeMessages(ctx context.Context, body []byte, defaultMappedModel string, p NativeAnthropicPorts) (*Result, error) {
	profile := p.Profile()
	startTime := time.Now()

	originalModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if originalModel == "" {
		p.Error(http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	clientStream := gjson.GetBytes(body, "stream").Bool()

	billingModel := p.BillingModel(originalModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	if upstreamModel != originalModel {
		rewritten, err := sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("rewrite model: %w", err)
		}
		body = rewritten
	}
	if normalized, changed := p.NormalizeThinking(body, upstreamModel); changed {
		body = normalized
	}

	// 记录客户端请求的推理强度：优先 Claude 协议的 output_config.effort；
	// 缺失且 thinking 已启用时，按国产 passback-required 模型兜底为 high
	// （对齐 Anthropic 网关 gateway_handler 的记录语义，避免该路径长期落 NULL）。
	requestedReasoningEffort := protocol.NormalizeClaudeOutputEffort(gjson.GetBytes(body, "output_config.effort").String())
	reasoningEffort := p.ThinkingFallback(
		requestedReasoningEffort,
		body,
		billingModel,
	)

	// 与 Anthropic 平台 passthrough 相同的 pre-filter：剥离空文本块与上游
	// 无法接受的 web-search 历史块（GLM/Kimi/DeepSeek 对 server_tool_use 400）。
	body = p.StripEmpty(body)
	body = p.FilterSearch(body, upstreamModel)

	p.Log("[CN Anthropic 直通] account=%d(%s) platform=%s model=%s upstream=%s stream=%v",
		profile.ID, profile.Name, profile.Platform, originalModel, upstreamModel, clientStream)

	apiKey := strings.TrimSpace(p.ProtocolAPIKey())
	if apiKey == "" {
		return nil, fmt.Errorf("account %d missing api_key", profile.ID)
	}
	targetURL, err := p.TargetURL()
	if err != nil {
		return nil, err
	}

	p.PrepareTransport()

	upstreamCtx, releaseUpstreamCtx := p.StreamContext(ctx, clientStream)
	upstreamReq, err := p.BuildNative(upstreamCtx, body, apiKey, targetURL)
	releaseUpstreamCtx()
	if err != nil {
		return nil, err
	}

	resp, err := p.SendNative(upstreamReq)
	if err != nil {
		return nil, p.TransportErrorNative(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := p.ReadUpstreamError(resp)
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		// 非 failover 错误：经共享 compat handler 以 Anthropic 格式回写
		// （透传规则、ops 记录、cyber_policy 与 CC 回退路径一致）。
		return p.ErrorResponse(resp, billingModel)
	}

	if clientStream {
		result, err := NativeAnthropicStreaming(ctx, resp, upstream.NewDeferredOutputContext(p.Sink()), p.DirectOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime)
		if result != nil {
			result.RequestedReasoningEffort = requestedReasoningEffort
		}
		return result, err
	}
	result, err := NativeAnthropicBuffered(ctx, resp, upstream.NewDeferredOutputContext(p.Sink()), p.DirectOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime)
	if result != nil {
		result.RequestedReasoningEffort = requestedReasoningEffort
	}
	return result, err
}

func ForwardNativeResponses(ctx context.Context, body []byte, defaultMappedModel string, p NativeAnthropicPorts) (*Result, error) {
	profile := p.Profile()
	startTime := time.Now()

	// 1. 把 Codex 客户端工具降级为 Anthropic 可识别的函数工具。
	adaptedBody, clientToolMapping, err := p.AdaptResponsesTools(body)
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to adapt request tools")
		return nil, fmt.Errorf("adapt responses client tools: %w", err)
	}

	// 2. 解析 Responses 请求。
	var responsesReq protocolopenai.ResponsesRequest
	if err := json.Unmarshal(adaptedBody, &responsesReq); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := responsesReq.Model
	if strings.TrimSpace(originalModel) == "" {
		p.Error(http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	clientStream := responsesReq.Stream

	// 3. 把 Responses 请求转换为 Anthropic 请求。
	anthropicReq, err := p.ResponsesToAnthropic(&responsesReq)
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to convert request")
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}

	// 4. Model mapping（OpenAI 网关统一入口的映射语义）
	billingModel := p.BillingModel(originalModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	anthropicReq.Model = upstreamModel

	reasoningEffort := p.ResponsesEffort(body, upstreamModel, billingModel, originalModel)
	reasoningEffort = p.ThinkingFallback(reasoningEffort, body, billingModel)

	// 5. Force upstream streaming（客户端原始终决定响应格式；
	// 上游恒为流式，非流式由缓冲路径组装）。
	anthropicReq.Stream = true
	reqStream := true

	p.Debug("openai responses: forwarding via native anthropic endpoint",
		zap.Int64("account_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("client_stream", clientStream),
	)

	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	// 与 /v1/messages 直通路径相同的 pre-filter。
	anthropicBody = p.StripEmpty(anthropicBody)
	anthropicBody = p.FilterSearch(anthropicBody, upstreamModel)
	anthropicBody = p.CacheLimit(anthropicBody)

	apiKey := strings.TrimSpace(p.ProtocolAPIKey())
	if apiKey == "" {
		return nil, fmt.Errorf("account %d missing api_key", profile.ID)
	}
	targetURL, err := p.TargetURL()
	if err != nil {
		return nil, err
	}

	p.PrepareTransport()

	upstreamCtx, releaseUpstreamCtx := p.StreamContext(ctx, reqStream)
	upstreamReq, err := p.BuildNative(upstreamCtx, anthropicBody, apiKey, targetURL)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	resp, err := p.SendNative(upstreamReq)
	if err != nil {
		return nil, p.TransportErrorNative(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := p.ReadUpstreamError(resp)
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		p.Error(p.MapStatus(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	if clientStream {
		return ResponsesFromAnthropicStreaming(resp, upstream.NewDeferredOutputContext(p.Sink()), p.OutputOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, clientToolMapping)
	}
	return ResponsesFromAnthropicBuffered(resp, upstream.NewDeferredOutputContext(p.Sink()), p.OutputOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, clientToolMapping)
}

func ForwardNativeChat(ctx context.Context, body []byte, defaultMappedModel string, p NativeAnthropicPorts) (*Result, error) {
	profile := p.Profile()
	startTime := time.Now()

	// 1. 解析 Chat Completions 请求。
	var ccReq protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &ccReq); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := ccReq.Model
	if strings.TrimSpace(originalModel) == "" {
		p.Error(http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	clientStream := ccReq.Stream
	includeUsage := ccReq.StreamOptions != nil && ccReq.StreamOptions.IncludeUsage

	// 2. 按 CC → Responses → Anthropic 做链式转换。
	responsesReq, err := p.ChatToResponses(&ccReq)
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to convert request")
		return nil, fmt.Errorf("convert chat completions to responses: %w", err)
	}
	anthropicReq, err := p.ResponsesToAnthropic(responsesReq)
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to convert request")
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}

	// 3. Model mapping（OpenAI 网关统一入口的映射语义）
	billingModel := p.BillingModel(originalModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	anthropicReq.Model = upstreamModel

	// 4. Force upstream streaming（客户端原始终决定响应格式；
	// 上游恒为流式，非流式由缓冲路径组装）。
	anthropicReq.Stream = true
	reqStream := true

	p.Debug("openai chat_completions: forwarding via native anthropic endpoint",
		zap.Int64("account_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("client_stream", clientStream),
	)

	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	// 与 /v1/messages 直通路径相同的 pre-filter。
	anthropicBody = p.StripEmpty(anthropicBody)
	anthropicBody = p.FilterSearch(anthropicBody, upstreamModel)
	anthropicBody = p.CacheLimit(anthropicBody)

	apiKey := strings.TrimSpace(p.ProtocolAPIKey())
	if apiKey == "" {
		return nil, fmt.Errorf("account %d missing api_key", profile.ID)
	}
	targetURL, err := p.TargetURL()
	if err != nil {
		return nil, err
	}

	p.PrepareTransport()

	upstreamCtx, releaseUpstreamCtx := p.StreamContext(ctx, reqStream)
	upstreamReq, err := p.BuildNative(upstreamCtx, anthropicBody, apiKey, targetURL)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	resp, err := p.SendNative(upstreamReq)
	if err != nil {
		return nil, p.TransportErrorNative(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := p.ReadUpstreamError(resp)
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		p.Error(p.MapStatus(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	reasoningEffort := p.ChatEffort(body, upstreamModel, billingModel, originalModel)
	reasoningEffort = p.ThinkingFallback(reasoningEffort, body, billingModel)

	if clientStream {
		return ChatFromAnthropicStreaming(resp, upstream.NewDeferredOutputContext(p.Sink()), p.OutputOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, includeUsage)
	}
	return ChatFromAnthropicBuffered(resp, upstream.NewDeferredOutputContext(p.Sink()), p.OutputOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime)
}
