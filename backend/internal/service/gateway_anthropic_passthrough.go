package service

// 本文件保留 API Key 直通的兼容入口和原生平台参数装配。
// 网关响应与恢复解释由 gateway/forward 唯一执行；平台流读取和交换仍复用 upstream。

import (
	"context"
	"net/http"
	"strings"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"
	"github.com/tidwall/gjson"

	"github.com/gin-gonic/gin"
)

type anthropicPassthroughForwardInput = forwardcore.APIKeyInput

func (s *GatewayService) forwardAnthropicAPIKeyPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	reqModel string,
	originalModel string,
	reqStream bool,
	startTime time.Time,
) (*ForwardResult, error) {
	return s.forwardAnthropicAPIKeyPassthroughWithInput(ctx, c, account, anthropicPassthroughForwardInput{
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
	account *Account,
	input anthropicPassthroughForwardInput,
) (*ForwardResult, error) {
	adapter := &anthropicPassthroughAdapter{messageExecutionAdapter: newMessageExecutionAdapter(s, c, account)}
	result, err := forwardcore.APIKeyPassthrough(ctx, adapter, adapter.input(), input)
	return legacyForwardExecutionResult(result), err
}

func (s *GatewayService) buildUpstreamRequestAnthropicAPIKeyPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) (*http.Request, []byte, error) {
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	o := s.anthropicRequestOptions(ctx, c, account, model, "apikey", false)
	o.URL = func() (string, error) {
		if base := account.GetBaseURL(); base != "" {
			url, err := s.validateUpstreamBaseURL(base)
			if err != nil {
				return "", err
			}
			return url + "/v1/messages?beta=true", nil
		}
		return claudeAPIURL, nil
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
	account *Account,
	startTime time.Time,
	model string,
) (*streamingResult, error) {
	options := s.anthropicStreamOptions(c, account)
	if s.rateLimitService == nil {
		options.UpdateWindow = nil
	}
	options.WriteHeaders = func(dst, src http.Header) { writeAnthropicPassthroughResponseHeaders(dst, src, s.responseHeaderFilter) }
	result, err := claude.StreamResponsePassthrough(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), options, startTime, model)
	if result == nil {
		return nil, err
	}
	return &streamingResult{usage: result.Usage, firstTokenMs: result.FirstTokenMs, clientDisconnect: result.ClientDisconnect}, err
}

func extractAnthropicSSEDataLine(line string) (string, bool) { return claude.ExtractSSEDataLine(line) }

func parseSSEUsagePassthrough(data string, usage *ClaudeUsage) {
	protocolanthropic.ParseSSEUsagePassthrough(data, usage)
}

func parseClaudeUsageFromResponseBody(body []byte) *ClaudeUsage {
	return protocolanthropic.ParseClaudeUsageFromResponseBody(body)
}

// invalidNonStreamingJSONFailoverError 把"上游 2xx 返回非 JSON body"归一为
// failover 错误（包级函数：Anthropic 平台 passthrough 与国产供应商原生
// Anthropic 直通共用）。
func invalidNonStreamingJSONFailoverError(
	ctx context.Context,
	rateLimitService *RateLimitService,
	resp *http.Response,
	account *Account,
	body []byte,
	parseErr error,
	requestedModel ...string,
) error {
	input := forwardcore.InvalidJSONInput{UpstreamStatus: resp.StatusCode, RequestID: resp.Header.Get("x-request-id"), Headers: resp.Header, Body: body, ParseError: parseErr, RequestedModels: requestedModel}
	if account != nil {
		input.AccountID = account.ID
		input.AccountName = account.Name
	}
	return forwardcore.InvalidJSON(ctx, invalidJSONAdapter{rateLimit: rateLimitService, account: account, headers: resp.Header}, input)
}

func (s *GatewayService) handleNonStreamingResponseAnthropicAPIKeyPassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
) (*ClaudeUsage, error) {
	options := s.anthropicResponseOptions(ctx, c, account, "", true)
	return claude.NonStreamResponsePassthrough(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), options)
}

func classifyAnthropicResponseInputAsCacheRead(body []byte, usage *ClaudeUsage) ([]byte, error) {
	return claude.ClassifyResponseInputAsCacheRead(body, usage)
}

func writeAnthropicPassthroughResponseHeaders(dst http.Header, src http.Header, filter *responseheaders.CompiledHeaderFilter) {
	gatewayhttp.WriteAnthropicPassthroughHeaders(dst, src, filter)
}

func (s *GatewayService) anthropicPassthroughExchangeOptions(ctx context.Context, c *gin.Context, account *Account, token, proxyURL string, input *anthropicPassthroughForwardInput) claude.ExchangeOptions {
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
		return s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
	}
	options.TransportError = func(ctx context.Context, err error, url string) error {
		return s.handleUpstreamTransportError(ctx, c, account, err, OpsUpstreamErrorEvent{UpstreamURL: safeUpstreamURL(url), Passthrough: true})
	}
	options.Observe = func(e claude.ExchangeNotice) {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: e.Platform, AccountID: e.AccountID, AccountName: e.AccountName, UpstreamStatusCode: e.UpstreamStatusCode, UpstreamRequestID: e.UpstreamRequestID, UpstreamURL: e.UpstreamURL, Kind: e.Kind, Message: e.Message, Detail: e.Detail, Passthrough: e.Passthrough})
	}
	return options
}
