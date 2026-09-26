// OpenAI 固定执行适配只创建状态，不按请求装配选择、刷新、计费或完成回调。
package openaiattempt

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type Runtime struct{ dependencies *openAIExecutionDependencies }

func (r *Runtime) Open(ctx context.Context, in execution.Request, sink upstream.OutputSink) (textflow.ResponsePorts, error) {
	output, ok := sink.(*gatewayhttp.MessagesOutput)
	if !ok || output.HTTP == nil {
		return nil, errors.New("openai text execution requires its HTTP output adapter")
	}
	output.HTTP.Request = output.HTTP.Request.WithContext(ctx)
	base := responsesAttemptBridge{
		fixed: r.dependencies, c: output.HTTP, apiKey: apikey.CopyAPIKey(in.Funding.Key), subject: authctx.AuthSubject{UserID: in.UserID, Concurrency: in.Concurrency}, subscription: in.Funding.Subscription, reqLog: output.Log,
		body: in.Body, forwardBody: in.AttemptBody, sessionHashBody: in.Text.SessionHashBody, reqModel: in.Model, forwardModel: in.Text.ForwardModel, sessionHash: in.SessionHash, previousResponseID: in.Text.PreviousResponseID, requestPlatform: in.Text.Platform, reqStream: in.Stream,
		nativeCompactionV2: in.Text.NativeCompactionV2, legacyCompact: in.Text.LegacyCompact, requireCompact: in.Text.RequireCompact, streamStarted: output.StreamStarted, selectionCtx: in.Text.SelectionContext, groupMapping: routing.GroupMappingResult(in.Text.Mapping), routingStart: in.Text.RoutingStart, requiredCapability: in.Text.RequiredCapability,
	}
	switch in.Text.Kind {
	case execution.TextOpenAIChat:
		return &openAIChatAttemptBridge{responsesAttemptBridge: base, promptCacheKey: in.Text.PromptCacheKey}, nil
	case execution.TextOpenAIMessages:
		// 该请求私有缓存只在首次调用时改写，原前置创建阶段没有 I/O 或诊断。
		mapped := requeststate.NewModelMappedBodyCache(in.Body, r.dependencies.replaceModelInBody)
		return &openAIMessageAttemptBridge{
			responsesAttemptBridge: base,
			accountLayerModel:      in.Text.AccountLayerModel,
			currentRoutingModel:    in.Text.AccountLayerModel,
			promptCacheKey:         in.Text.PromptCacheKey,
			groupMappingMsg:        routing.GroupMappingResult(in.Text.Mapping),
			mappedBodyForMessages:  mapped,
		}, nil
	default:
		return &base, nil
	}
}

func openAIObservedAttempt(result *forwardcore.OpenAIResult, err error) upstream.AttemptResult {
	if result == nil {
		return upstream.AttemptResult{Cancelled: errors.Is(err, context.Canceled)}
	}
	out := upstream.AttemptResult{
		RequestID:        result.RequestID,
		ResponseID:       result.ResponseID,
		Model:            result.Model,
		UpstreamModel:    result.UpstreamModel,
		Stream:           result.Stream,
		Duration:         result.Duration,
		ClientDisconnect: result.ClientDisconnect,
		UpstreamHeaders:  http.Header(result.UpstreamHeaders).Clone(),
		ObservedImages:   result.ImageCount,
		ImageOutputSizes: slices.Clone(result.ImageOutputSizes),
		SearchCount:      result.SearchCount,
		Cancelled: errors.Is(err,
			context.Canceled),
	}
	out.Usage = upstream.TokenUsage{InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens, CacheCreationInputTokens: result.Usage.CacheCreationInputTokens, CacheReadInputTokens: result.Usage.CacheReadInputTokens, ImageOutputTokens: result.Usage.ImageOutputTokens}
	out.ImageInputTokens = result.Usage.ImageInputTokens
	out.HasUsage = out.Usage.HasObservedTokens() || result.ImageCount > 0 || result.SearchCount > 0 || result.WebSearchCalls > 0 || result.AudioUsage != nil || result.Usage.ImageInputTokens > 0
	out.Served = err == nil || out.HasUsage
	if result.FirstTokenMs != nil {
		v := *result.FirstTokenMs
		out.FirstTokenMs = &v
	}
	if result.ServiceTier != nil {
		out.ServiceTier = *result.ServiceTier
	}
	if result.ReasoningEffort != nil {
		out.ReasoningEffort = *result.ReasoningEffort
	}
	if result.AudioUsage != nil {
		v := *result.AudioUsage
		out.AudioUsage = &v
	}
	return out
}
