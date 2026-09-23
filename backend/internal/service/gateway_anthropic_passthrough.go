package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/ops"

	// 本文件保留 API Key 直通的兼容入口和原生平台参数装配。
	// 网关响应与恢复解释由 gateway/forward 唯一执行；平台流读取和交换仍复用 upstream。

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func (s *GatewayService) forwardAnthropicAPIKeyPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	reqModel string,
	originalModel string,
	reqStream bool,
	startTime time.Time,
) (*forwardcore.MessagesResult, error) {
	return s.forwardAnthropicAPIKeyPassthroughWithInput(ctx, c, account, forwardcore.APIKeyInput{
		Body:          body,
		RequestModel:  reqModel,
		OriginalModel: originalModel,
		RequestStream: reqStream,
		StartTime:     startTime,
	})
}

func (s *GatewayService) forwardAnthropicAPIKeyPassthroughWithInput(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	input forwardcore.APIKeyInput,
) (*forwardcore.MessagesResult, error) {
	adapter := &anthropicPassthroughAdapter{messageExecutionAdapter: newMessageExecutionAdapter(s, c, account)}
	result, err := forwardcore.APIKeyPassthrough(ctx, adapter, adapter.input(), input)
	return legacyForwardExecutionResult(result), err
}

func (s *GatewayService) buildUpstreamRequestAnthropicAPIKeyPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	token string,
) (*http.Request, []byte, error) {
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	o := s.anthropicRequestOptions(ctx, c, account, model, "apikey", false)
	o.URL = func() (string, error) {
		if base := account.View().GetBaseURL(); base != "" {
			url, err := s.validateUpstreamBaseURL(base)
			if err != nil {
				return "", err
			}
			return url + "/v1/messages?beta=true", nil
		}
		return claude.ClaudeAPIURL, nil
	}
	o.OriginalPolicy = func(ctx context.Context, header, model string) (map[string]struct{}, error) {
		policy := s.evaluateBetaPolicy(ctx, header, account, model)
		if policy.blockErr != nil {
			return nil, policy.blockErr
		}
		return policy.filterSet, nil
	}
	return claude.BuildRequestPassthrough(ctx, body, token, o)
}

func (s *GatewayService) handleStreamingResponseAnthropicAPIKeyPassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	startTime time.Time,
	model string,
) (*streamingResult, error) {
	options := s.anthropicStreamOptions(c, account)
	if s.rateLimitService == nil {
		options.UpdateWindow = nil
	}
	options.WriteHeaders = func(dst, src http.Header) {
		gatewayhttp.WriteAnthropicPassthroughHeaders(dst, src, s.responseHeaderFilter)
	}
	result, err := claude.StreamResponsePassthrough(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), options, startTime, model)
	if result == nil {
		return nil, err
	}
	return &streamingResult{usage: result.Usage, firstTokenMs: result.FirstTokenMs, clientDisconnect: result.ClientDisconnect}, err
}

// invalidNonStreamingJSONFailoverError 把"上游 2xx 返回非 JSON body"归一为
// failover 错误（包级函数：Anthropic 平台 passthrough 与国产供应商原生
// Anthropic 直通共用）。
func invalidNonStreamingJSONFailoverError(
	ctx context.Context,
	rateLimitService *RateLimitService,
	resp *http.Response,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	parseErr error,
	requestedModel ...string,
) error {
	input := forwardcore.InvalidJSONInput{UpstreamStatus: resp.StatusCode, RequestID: resp.Header.Get("x-request-id"), Headers: resp.Header, Body: body, ParseError: parseErr, RequestedModels: requestedModel}
	if account != nil {
		input.AccountID = account.Record.ID
		input.AccountName = account.Record.Name
	}
	return forwardcore.InvalidJSON(ctx, invalidJSONAdapter{rateLimit: rateLimitService, account: account, headers: resp.Header}, input)
}

func (s *GatewayService) handleNonStreamingResponseAnthropicAPIKeyPassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
) (*upstream.TokenUsage, error) {
	options := s.anthropicResponseOptions(ctx, c, account, "", true)
	return claude.NonStreamResponsePassthrough(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), options)
}

func (s *GatewayService) anthropicPassthroughExchangeOptions(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, token, proxyURL string, input *forwardcore.APIKeyInput) claude.ExchangeOptions {
	options := s.anthropicExchangeOptions(ctx, c, account, token, "apikey", input.RequestModel, input.RequestStream, false, proxyURL, nil, func(body []byte) error {
		if input.Parsed != nil {
			if err := input.Parsed.ReplaceBody(body); err != nil {
				return err
			}
			input.Body = input.Parsed.Body.Bytes()
		}
		return nil
	})
	options.SynchronizeBody = input.Parsed != nil
	options.Build = func(ctx context.Context, body []byte) (*http.Request, []byte, error) {
		return s.buildUpstreamRequestAnthropicAPIKeyPassthrough(ctx, c, account, body, token)
	}
	options.Do = func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.DoWithTLS(req, proxyURL, account.Record.ID, account.Record.Concurrency, s.tlsFPProfileService.ResolveRequestTLS(accountTLSSelection(account, nil)))
	}
	options.TransportError = func(ctx context.Context, err error, url string) error {
		return s.handleUpstreamTransportError(ctx, c, account, err, ops.OpsUpstreamErrorEvent{UpstreamURL: logredact.SafeUpstreamURL(url), Passthrough: true})
	}
	options.Observe = func(e claude.ExchangeNotice) {
		gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{Platform: e.Platform, AccountID: e.AccountID, AccountName: e.AccountName, UpstreamStatusCode: e.UpstreamStatusCode, UpstreamRequestID: e.UpstreamRequestID, UpstreamURL: e.UpstreamURL, Kind: e.Kind, Message: e.Message, Detail: e.Detail, Passthrough: e.Passthrough})
	}
	return options
}
