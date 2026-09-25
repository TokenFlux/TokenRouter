package forward

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/tidwall/gjson"
)

// AsResponses 拥有请求转换、获取凭据和响应推进的原有顺序。
func AsResponses(ctx context.Context, p ConversionPorts, in ConversionInput, body []byte) (*Result, error) {
	startTime := time.Now()

	normalizedBody, normalized, err := p.NormalizeResponses(body)
	if err != nil {
		return nil, err
	}
	if normalized {
		body = normalizedBody
	}

	adaptedBody, clientToolMapping, err := AdaptResponsesClientToolsForAnthropic(body)
	if err != nil {
		return nil, fmt.Errorf("adapt responses client tools: %w", err)
	}

	var responsesReq protocolopenai.ResponsesRequest
	if err := json.Unmarshal(adaptedBody, &responsesReq); err != nil {
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := responsesReq.Model
	clientStream := responsesReq.Stream

	anthropicReq, err := bridge.ResponsesToAnthropicRequest(&responsesReq)
	if err != nil {
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}

	anthropicReq.Stream = true
	reqStream := true

	// 4. 模型映射：渠道映射已由 handler 写入 body，此处继续执行账号映射和平台规范化。
	mappedModel := p.ResolveModel(ctx, originalModel)
	if mappedModel == "" {
		mappedModel = originalModel
	}
	reasoningEffort := p.Effort(body, false, mappedModel, originalModel)
	// 按 Anthropic 出站档位记录，不能用 OpenAI 模型能力过滤掉 Claude 的 max。
	if anthropicReq.OutputConfig != nil {
		reasoningEffort = protocolcore.NormalizeClaudeOutputEffort(anthropicReq.OutputConfig.Effort)
	}
	// 国产模型没有显式 effort 档位时，thinking 启用后补默认展示值。
	reasoningEffort = p.ThinkingFallback(reasoningEffort, body, mappedModel)
	anthropicReq.Model = mappedModel

	p.ModelNotice("gateway forward_as_responses: model mapping applied", originalModel, mappedModel, clientStream)

	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	// OpenAI Responses 协议进来的请求永远不是 Claude Code 客户端，所以对 OAuth 账号
	// 必须完整执行 /v1/messages 主路径上的伪装链路（system 重写 + normalize + metadata 注入），
	// 否则会被 Anthropic 判为第三方应用并扣 extra usage。
	// 见 applyClaudeCodeOAuthMimicryToBody 的 godoc。
	shouldMimicClaudeCode := in.OAuth

	if shouldMimicClaudeCode {
		anthropicBody = p.Mimic(ctx, anthropicBody, anthropicReq.System, mappedModel)
	}

	anthropicBody = p.CacheLimit(anthropicBody)

	if err := p.Credential(ctx); err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}
	wireBody, err := p.Build(ctx, anthropicBody, mappedModel, reqStream, shouldMimicClaudeCode)
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	resp, err := p.Send(ctx)
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := p.ReadErrorBody()
		upstreamMsg := p.ErrorMessage(respBody)
		decision := p.Health(ctx, resp.StatusCode, respBody, mappedModel)

		if decision.Generic {
			p.Output().Error(500, "server_error", "Upstream gateway error")
			return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}

		if decision.Failover {
			p.FailoverNotice(resp.StatusCode, upstreamMsg)
			return nil, p.FailoverError(resp.StatusCode, respBody, decision.RetrySameAccount)
		}

		p.Output().Error(MapStatus(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	var result *Result
	var handleErr error
	if clientStream {
		result, handleErr = ResponsesStreaming(resp, p.Output(), originalModel, mappedModel, reasoningEffort, startTime, clientToolMapping)
	} else {
		result, handleErr = ResponsesBuffered(resp, p.Output(), originalModel, mappedModel, reasoningEffort, startTime, clientToolMapping)
	}

	if result != nil && strings.TrimSpace(result.Usage.Speed) == "" &&
		strings.EqualFold(strings.TrimSpace(gjson.GetBytes(wireBody, "speed").String()), "fast") {
		result.Usage.Speed = "fast"
	}
	return result, handleErr
}

// AsChat 拥有请求转换、获取凭据和响应推进的原有顺序。
func AsChat(ctx context.Context, p ConversionPorts, in ConversionInput, body []byte) (*Result, error) {
	startTime := time.Now()

	var ccReq protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &ccReq); err != nil {
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := ccReq.Model
	clientStream := ccReq.Stream
	includeUsage := ccReq.StreamOptions != nil && ccReq.StreamOptions.IncludeUsage

	responsesReq, err := bridge.ChatCompletionsToResponses(&ccReq, bridge.RequestOptions{DropSampling: capability.ResponsesBridgeDropsSampling(ccReq.Model), SupportsMaxEffort: capability.ResponsesBridgeSupportsMaxEffort(ccReq.Model)})
	if err != nil {
		return nil, fmt.Errorf("convert chat completions to responses: %w", err)
	}

	anthropicReq, err := bridge.ResponsesToAnthropicRequest(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}

	anthropicReq.Stream = true
	reqStream := true

	// 4. 模型映射：渠道映射已由 handler 写入 body，此处继续执行账号映射和平台规范化。
	mappedModel := p.ResolveModel(ctx, originalModel)
	if mappedModel == "" {
		mappedModel = originalModel
	}
	anthropicReq.Model = mappedModel

	p.ModelNotice("gateway forward_as_chat_completions: model mapping applied", originalModel, mappedModel, clientStream)

	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	// Chat Completions 协议进来的请求永远不是 Claude Code 客户端，所以对 OAuth 账号
	// 必须完整执行 /v1/messages 主路径上的伪装链路（system 重写 + normalize + metadata 注入），
	// 否则会被 Anthropic 判为第三方应用并扣 extra usage。
	// 见 applyClaudeCodeOAuthMimicryToBody 的 godoc。
	shouldMimicClaudeCode := in.OAuth

	if shouldMimicClaudeCode {
		anthropicBody = p.Mimic(ctx, anthropicBody, anthropicReq.System, mappedModel)
	}

	anthropicBody = p.CacheLimit(anthropicBody)

	if err := p.Credential(ctx); err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}
	wireBody, err := p.Build(ctx, anthropicBody, mappedModel, reqStream, shouldMimicClaudeCode)
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	resp, err := p.Send(ctx)
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := p.ReadErrorBody()
		upstreamMsg := p.ErrorMessage(respBody)
		decision := p.Health(ctx, resp.StatusCode, respBody, mappedModel)

		if decision.Generic {
			p.Output().Error(500, "server_error", "Upstream gateway error")
			return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}

		if decision.Failover {
			p.FailoverNotice(resp.StatusCode, upstreamMsg)
			return nil, p.FailoverError(resp.StatusCode, respBody, decision.RetrySameAccount)
		}

		p.Output().Error(MapStatus(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	reasoningEffort := p.Effort(body, true, mappedModel, originalModel)
	// 按 Anthropic 出站档位记录，不能用 OpenAI 模型能力过滤掉 Claude 的 max。
	if anthropicReq.OutputConfig != nil {
		reasoningEffort = protocolcore.NormalizeClaudeOutputEffort(anthropicReq.OutputConfig.Effort)
	}
	// 国产模型没有显式 effort 档位时，thinking 启用后补默认展示值。
	reasoningEffort = p.ThinkingFallback(reasoningEffort, body, mappedModel)

	var result *Result
	var handleErr error
	if clientStream {
		result, handleErr = ChatStreaming(resp, p.Output(), originalModel, mappedModel, reasoningEffort, startTime, includeUsage)
	} else {
		result, handleErr = ChatBuffered(resp, p.Output(), originalModel, mappedModel, reasoningEffort, startTime)
	}

	if result != nil && strings.TrimSpace(result.Usage.Speed) == "" &&
		strings.EqualFold(strings.TrimSpace(gjson.GetBytes(wireBody, "speed").String()), "fast") {
		result.Usage.Speed = "fast"
	}
	return result, handleErr
}
func AdaptResponsesClientToolsForAnthropic(body []byte) ([]byte, bridge.ResponsesClientToolMapping, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var requestBody map[string]any
	if err := decoder.Decode(&requestBody); err != nil {
		return body, bridge.ResponsesClientToolMapping{}, err
	}

	additionalToolsChanged, err := LiftResponsesAdditionalTools(requestBody)
	if err != nil {
		return body, bridge.ResponsesClientToolMapping{}, err
	}
	mapping, changed, err := bridge.AdaptResponsesClientTools(requestBody)
	if err != nil {
		return body, bridge.ResponsesClientToolMapping{}, err
	}
	changed = changed || additionalToolsChanged
	if !changed {
		return body, mapping, nil
	}

	rebuilt, err := json.Marshal(requestBody)
	if err != nil {
		return body, bridge.ResponsesClientToolMapping{}, err
	}
	return rebuilt, mapping, nil
}
func LiftResponsesAdditionalTools(requestBody map[string]any) (bool, error) {
	input, ok := requestBody["input"].([]any)
	if !ok {
		return false, nil
	}

	tools, _ := requestBody["tools"].([]any)
	kept := make([]any, 0, len(input))
	changed := false
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || strings.TrimSpace(fmt.Sprint(item["type"])) != "additional_tools" {
			kept = append(kept, raw)
			continue
		}
		additional, ok := item["tools"].([]any)
		if !ok {
			return false, fmt.Errorf("additional_tools.tools must be an array")
		}
		tools = append(tools, additional...)
		changed = true
	}
	if !changed {
		return false, nil
	}
	requestBody["tools"] = tools
	requestBody["input"] = kept
	return true, nil
}
