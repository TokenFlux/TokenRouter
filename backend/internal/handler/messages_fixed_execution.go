// 固定 Messages 执行绑定只构造一次依赖；Open 每次仅分配请求/attempt 状态。
package handler

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type fixedMessagesRuntime struct{ dependencies *messageExecutionDependencies }

func (r *fixedMessagesRuntime) Open(ctx context.Context, in execution.Request, sink upstream.OutputSink) (textflow.MessagePorts, error) {
	output, ok := sink.(*gatewayhttp.MessagesOutput)
	if !ok || output.HTTP == nil {
		return nil, errors.New("messages execution requires its HTTP output adapter")
	}
	// 兼容桥接仍提供一次执行原语；完整旧 handler 不进入新执行会话。
	base := messageAttemptBridge{
		fixed: r.dependencies, c: output.HTTP,
		apiKey: apikey.CopyAPIKey(in.Funding.Key), subject: authctx.AuthSubject{UserID: in.UserID, Concurrency: in.Concurrency},
		subscription: in.Funding.Subscription, parsedReq: in.Text.Parsed, body: in.Body, reqModel: in.Model, reqStream: in.Stream,
		isClaudeCodeClient: in.Metadata.ClaudeCode, platform: in.Text.Platform, hasBoundSession: in.Text.HasBoundSession,
		sessionKey: in.SessionHash, sessionBoundAccountID: in.Text.BoundAccountID, streamStarted: output.StreamStarted, reqLog: output.Log,
	}
	// 前置步骤已完成 context 绑定，执行采用调用者传入的同一请求 context。
	output.HTTP.Request = output.HTTP.Request.WithContext(ctx)
	switch in.Text.Kind {
	case execution.TextGeminiMessages:
		return &geminiMessageAttemptBridge{messageAttemptBridge: base, forwardModel: in.Text.GeminiModel, forwardBody: in.Text.GeminiBody, channelMapping: routing.ChannelMappingResult(in.Route.Mapping())}, nil
	case execution.TextGenericResponses:
		return &genericResponsesAttemptBridge{messageAttemptBridge: base, requestCtx: in.Text.SelectionContext, forwardBody: in.AttemptBody, channelMapping: routing.ChannelMappingResult(in.Text.Mapping)}, nil
	case execution.TextGenericChat:
		return &genericChatAttemptBridge{messageAttemptBridge: base, requestCtx: in.Text.SelectionContext, groupPlatform: in.Text.Platform, selectionSessionHash: in.Text.SelectionSessionHash, channelMapping: routing.ChannelMappingResult(in.Text.Mapping)}, nil
	case execution.TextNativeGemini:
		return &nativeGeminiAttemptBridge{
			messageAttemptBridge: base,
			modelName:            in.Text.GeminiModel,
			action:               in.Text.Action,
			stream:               in.Stream,
			geminiConcurrency:    output.Concurrency,
			useDigestFallback:    in.Text.UseDigestFallback,
			geminiDigestChain:    in.Text.DigestChain,
			geminiPrefixHash:     in.Text.PrefixHash,
			geminiSessionUUID:    in.Text.SessionUUID,
			matchedDigestChain:   in.Text.MatchedDigestChain,
			channelMapping:       routing.ChannelMappingResult(in.Text.Mapping),
			signatureState:       in.Text.SignatureState,
		}, nil
	}
	return &base, nil
}

// messageObservedAttempt 只投影已观测结果；失败结果与错误可以同时返回，不创建额外完成任务。
func messageObservedAttempt(result *forwardcore.MessagesResult, err error) upstream.AttemptResult {
	if result == nil {
		return upstream.AttemptResult{Cancelled: errors.Is(err, context.Canceled)}
	}
	out := upstream.AttemptResult{
		RequestID: result.RequestID, Model: result.Model, UpstreamModel: result.UpstreamModel, Usage: result.Usage,
		Stream: result.Stream, Duration: result.Duration, ClientDisconnect: result.ClientDisconnect,
		UpstreamHeaders: http.Header(result.UpstreamHeaders).Clone(), ImageOutputSizes: slices.Clone(result.ImageOutputSizes),
		ObservedImages: result.ImageCount, SearchCount: result.SearchCount, Cancelled: errors.Is(err, context.Canceled),
	}
	// 复用协议计量判断，只投影已观测产物，不改变 RunMessages 的完成资格。
	out.HasUsage = result.Usage.HasObservedTokens() || result.ImageCount > 0 || result.AudioUsage != nil || result.SearchCount > 0
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

// capturedTextSelection 在候选返回时立即取得实际计划，结束后不再查看可变候选状态。
func capturedTextSelection(account *gatewayprovider.ExecutionAccount) textflow.Selection {
	plan, provided := gatewayprovider.ExecutionCandidatePlan(account)
	return textflow.Selection{Account: gatewayprovider.ExecutionSnapshot(account), RetryLimit: account.View().GetPoolModeRetryCount(), Plan: plan, PlanProvided: provided}
}
