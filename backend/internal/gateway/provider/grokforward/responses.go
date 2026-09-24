package grokforward

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// Forward 拥有 Grok 请求准备和响应决定，网络恢复只复用原生 ResponsesExecutor。
func Forward(ctx context.Context, p Ports, o Options, in Input) (*forwardcore.OpenAIResult, error) {
	body, originalModel, reqStream, startTime := in.Body, in.OriginalModel, in.Stream, in.StartedAt

	if in.AccountType != "oauth" && in.AccountType != "apikey" {
		return nil, fmt.Errorf("grok account type %s is not supported by Responses forwarding", in.AccountType)
	}
	billingModel := p.BillingModel(originalModel)
	upstreamModel := p.UpstreamModel(billingModel)
	if p.ImageModel(upstreamModel) {
		err := fmt.Errorf("model %s is an image model and is not available on the Responses endpoint; use /v1/images/generations instead", upstreamModel)
		// 这是客户端点选择错误，直接返回 400，避免 handler 将普通错误改写为通用 502。
		if in.HTTPPresent {
			p.InvalidRequest(err.Error(), "model")
		}
		return nil, err
	}
	patchedBody, clientToolMapping, err := o.Codec.PatchGrokResponsesBodyWithClientTools(body, upstreamModel)
	if err != nil {
		p.SetError(http.StatusBadRequest, err.Error(), "")
		p.InvalidRequest(err.Error(), "tools")
		return nil, err
	}
	p.ClientTools(clientToolMapping)
	// xAI 没有原生 /responses/compact；改成普通 Responses 摘要轮次，响应阶段再封装为 compaction 条目。
	if in.Compact {
		patchedBody, err = o.Codec.BuildGrokCompactRequestBody(patchedBody)
		if err != nil {
			return nil, err
		}
	}
	// 从 xAI 实际接收的请求派生身份，使 Codex Responses Lite 的 additional_tools
	// 成为稳定工具前缀的一部分。若 Claude Code session 只存在于 metadata.user_id，
	// 则在 metadata 被剥离前使用原始请求保留该身份。
	cacheIdentityBody := patchedBody
	if grok.ExtractClaudeCodeSessionIDFromPayload(body) != "" {
		cacheIdentityBody = body
	}
	cacheIdentity := p.CacheIdentity(cacheIdentityBody, upstreamModel)
	mixedCacheIntentBody := append([]byte(nil), patchedBody...)
	patchedBody, err = grok.ApplyGrokResponsesCacheIdentity(patchedBody, body, cacheIdentity, in.OAuth)
	if err != nil {
		return nil, fmt.Errorf("apply grok prompt cache identity: %w", err)
	}
	// Free OAuth 携带客户端函数工具时复用混合工具缓存路由，补齐 web_search/x_search，
	// 避免 xAI 强制落到不可缓存的 build-free 层级。
	patchedBody, err = p.FreeCacheRoute(patchedBody, mixedCacheIntentBody, cacheIdentity)
	if err != nil {
		return nil, fmt.Errorf("apply grok Free function-tool cache route: %w", err)
	}
	err = p.Credential(ctx)
	if err != nil {
		return nil, err
	}
	upstreamCtx, releaseUpstreamCtx := p.Detach(ctx)
	defer releaseUpstreamCtx()
	p.ResolveProxy()
	upstreamStart := time.Now()
	var handled bool
	var handledResult *forwardcore.OpenAIResult
	var handleErr error
	target := &grok.ResponsesTarget{
		AccountID: in.AccountID,
		Model:     upstreamModel,
		Enter:     o.Enter,
		Exchange: grok.ResponsesExchange{
			Build: func(body []byte) (*http.Request, error) {
				return p.Build(upstreamCtx, body, cacheIdentity, true)
			},
			Do: func(req *http.Request) (*http.Response, error) {
				return p.Do(req)
			},
			ReadError: p.ReadError,
			AfterExchange: func(err error) error {
				p.Latency(time.Since(upstreamStart).Milliseconds())
				if err != nil {
					return p.TransportError(ctx, err)
				}
				return nil
			},
			OnReplay: func() {
				p.ReplayNotice(cacheIdentity != "")
			},
		},
		BeforeResponse: func(resp *http.Response, body []byte) (bool, error) {
			patchedBody = body
			if resp.StatusCode >= 400 {
				handled = true
				respBody := p.ReadError(resp)
				resp.Body = io.NopCloser(bytes.NewReader(respBody))
				upstreamMsg := p.ErrorMessage(respBody)
				if upstreamMsg == "" {
					upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
				}
				decision := p.Health(ctx, resp.StatusCode, resp.Header, respBody, upstreamModel, true)
				kind := "http_error"
				if decision.Failover {
					kind = "failover"
				}
				p.Observe(Notice{
					Platform:           in.Platform,
					AccountID:          in.AccountID,
					AccountName:        in.AccountName,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
					Kind:               kind,
					Message:            upstreamMsg,
				})
				if decision.Generic {
					handledResult, handleErr = p.HandleError(ctx, resp, patchedBody, upstreamModel)
					return true, handleErr
				}
				// 配额/限流响应写入团队模型覆盖层；容量属于请求压力，不应隐藏健康账号。
				if p.ShouldMarkTeam(resp.StatusCode, respBody) {
					p.MarkTeam(upstreamModel)
				}
				if kind == "failover" {
					retry := p.RetryMetadata(resp.StatusCode, respBody)
					return true, p.Failure(Failure{
						StatusCode:               resp.StatusCode,
						ResponseBody:             respBody,
						ResponseHeaders:          resp.Header.Clone(),
						RetryableOnSameAccount:   retry.Retryable || decision.RetrySameAccount,
						RequestScopedTransient:   retry.Retryable && resp.StatusCode == http.StatusTooManyRequests,
						SameAccountRetryDelay:    retry.Delay,
						SameAccountRetryDeadline: retry.Deadline,
						SameAccountRetryMax:      retry.Max,
					})
				}
				handledResult, handleErr = p.HandleError(ctx, resp, patchedBody, upstreamModel)
				return true, handleErr
			}
			p.ObserveSuccess(ctx, resp.Header, resp.StatusCode, upstreamModel)
			return false, nil
		},
		MaxLineSize: o.MaxLineSize,
		ClientTools: clientToolMapping,
		ReadResponse: func(resp *http.Response, input upstream.AttemptInput, _ upstream.OutputSink) (upstream.ResponsesObservation, error) {
			if input.Stream {
				return p.ReadStream(ctx, resp, startTime, originalModel, upstreamModel)
			}
			return p.ReadNonStream(ctx, resp, originalModel, upstreamModel)
		},
	}
	sink := p.Sink()
	nativeResult, err := (grok.ResponsesExecutor{}).Execute(upstreamCtx, upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIResponses,
		Body:          patchedBody,
		ResponseModel: originalModel,
		Stream:        reqStream,
		Target:        target,
	}, sink)
	if handled {
		return handledResult, err
	}
	if err != nil {
		return nil, err
	}
	usage := &protocolopenai.ForwardUsage{
		InputTokens:              nativeResult.Usage.InputTokens,
		ImageInputTokens:         nativeResult.ImageInputTokens,
		OutputTokens:             nativeResult.Usage.OutputTokens,
		CacheCreationInputTokens: nativeResult.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     nativeResult.Usage.CacheReadInputTokens,
		ImageOutputTokens:        nativeResult.Usage.ImageOutputTokens,
	}
	firstTokenMs := nativeResult.FirstTokenMs
	responseID := nativeResult.ResponseID
	searchCount := nativeResult.SearchCount
	imageCount := nativeResult.ObservedImages
	imageOutputSizes := nativeResult.ImageOutputSizes
	reasoningEffort := p.Effort(patchedBody, originalModel)
	result := &forwardcore.OpenAIResult{
		RequestID:       nativeResult.RequestID,
		UpstreamHeaders: nativeResult.UpstreamHeaders,
		ResponseID:      responseID,
		Usage:           *usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		Stream:          reqStream,
		OpenAIWSMode:    false,
		ResponseHeaders: nativeResult.UpstreamHeaders.Clone(),
		Duration:        time.Since(startTime),
		FirstTokenMs:    firstTokenMs,
	}
	// 从共享 Responses 处理器传递搜索与图片计数；否则流式或 JSON 统计虽会运行，
	// 但 search_price_per_1k 与图片费用不会生效。
	if searchCount > 0 {
		result.SearchCount = searchCount
	}
	if imageCount > 0 {
		result.ImageCount = imageCount
		result.ImageOutputSizes = imageOutputSizes
	}
	return result, nil

}
