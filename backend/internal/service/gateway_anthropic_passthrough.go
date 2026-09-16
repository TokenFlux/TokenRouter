package service

// 本文件由 gateway_service.go 纯移动拆分而来：Anthropic APIKey 直通
// （passthrough）转发路径及其流式/非流式响应与 usage 解析。仅做代码搬迁，
// 无任何行为变更。

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"
	"github.com/tidwall/gjson"

	"github.com/gin-gonic/gin"
)

type anthropicPassthroughForwardInput struct {
	Body          []byte
	Parsed        *ParsedRequest
	RequestModel  string
	OriginalModel string
	RequestStream bool
	StartTime     time.Time
}

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
	if s.nativeAttemptActivity != nil {
		done, err := s.nativeAttemptActivity()
		if err != nil {
			return nil, err
		}
		defer done()
	}
	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	if tokenType != "apikey" {
		return nil, fmt.Errorf("anthropic api key passthrough requires apikey token, got: %s", tokenType)
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	logger.LegacyPrintf("service.gateway", "[Anthropic 自动透传] 命中 API Key 透传分支: account=%d name=%s model=%s stream=%v",
		account.ID, account.Name, input.RequestModel, input.RequestStream)

	if c != nil {
		c.Set("anthropic_passthrough", true)
	}
	// Pre-filter: strip empty text blocks (including nested in tool_result) to prevent upstream 400.
	input.Body = StripEmptyTextBlocks(input.Body)
	// Pre-filter: strip web-search history blocks the upstream cannot accept
	// (emulation-synthesized ones always; genuine ones additionally for
	// passback-required third-party upstreams such as GLM/Kimi/DeepSeek,
	// which reject server_tool_use with 400). input.RequestModel 已是映射后的模型 ID。
	input.Body = FilterWebSearchHistoryBlocks(input.Body, input.RequestModel)
	if input.Parsed != nil {
		// 透传分支也会改写实际 wire body，成功 usage hash 依赖这里同步当前 body。
		if err := input.Parsed.ReplaceBody(input.Body); err != nil {
			return nil, err
		}
	}

	options := s.anthropicPassthroughExchangeOptions(ctx, c, account, token, proxyURL, &input)
	var resp *http.Response
	lastWireBody := input.Body
	var earlyResult *ForwardResult
	stopped := false
	var streamResult *streamingResult
	before := func(ctx context.Context, response *http.Response, wire []byte) (bool, error) {
		resp = response
		lastWireBody = wire
		early := func(result *ForwardResult, err error) (bool, error) { earlyResult = result; return true, err }
		if resp.StatusCode >= 400 && s.shouldRetryUpstreamError(account, resp.StatusCode) {
			if s.shouldFailoverUpstreamError(resp.StatusCode) {
				respBody, _ := s.readUpstreamErrorBody(resp)
				_ = resp.Body.Close()
				resp.Body = io.NopCloser(bytes.NewReader(respBody))

				logger.LegacyPrintf("service.gateway", "[Anthropic Passthrough] Upstream error (retry exhausted, failover): Account=%d(%s) Status=%d RequestID=%s Body=%s",
					account.ID, account.Name, resp.StatusCode, resp.Header.Get("x-request-id"), truncateString(string(respBody), 1000))

				decision := s.handleRetryExhaustedSideEffects(ctx, resp, account, input.RequestModel)
				if decision.ShouldReturnGenericError() {
					return early(s.handleErrorResponse(ctx, resp, c, account, input.RequestModel))
				}
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get("x-request-id"),
					Passthrough:        true,
					Kind:               "retry_exhausted_failover",
					Message:            extractUpstreamErrorMessage(respBody),
					Detail: func() string {
						if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
							return truncateString(string(respBody), s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
						}
						return ""
					}(),
				})
				return true, &UpstreamFailoverError{
					StatusCode:             resp.StatusCode,
					ResponseBody:           respBody,
					RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
				}
			}
			return early(s.handleRetryExhaustedError(ctx, resp, c, account, input.RequestModel))
		}

		if resp.StatusCode >= 400 && s.shouldFailoverUpstreamError(resp.StatusCode) {
			respBody, _ := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(respBody))

			logger.LegacyPrintf("service.gateway", "[Anthropic Passthrough] Upstream error (failover): Account=%d(%s) Status=%d RequestID=%s Body=%s",
				account.ID, account.Name, resp.StatusCode, resp.Header.Get("x-request-id"), truncateString(string(respBody), 1000))

			decision := s.handleFailoverSideEffects(ctx, resp, account, input.RequestModel)
			if decision.ShouldReturnGenericError() {
				return early(s.handleErrorResponse(ctx, resp, c, account, input.RequestModel))
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Passthrough:        true,
				Kind:               "failover",
				Message:            extractUpstreamErrorMessage(respBody),
				Detail: func() string {
					if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
						return truncateString(string(respBody), s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
					}
					return ""
				}(),
			})
			return true, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
			}
		}

		if resp.StatusCode >= 400 {
			return early(s.handleErrorResponse(ctx, resp, c, account, input.RequestModel))
		}

		return false, nil
	}
	target := &claude.Target{AccountID: account.ID, Model: input.RequestModel, Passthrough: true, Exchange: options, Response: s.anthropicResponseOptions(ctx, c, account, input.RequestModel, true), StartedAt: input.StartTime, BeforeResponse: func(ctx context.Context, resp *http.Response, wire []byte) (bool, error) {
		var err error
		stopped, err = before(ctx, resp, wire)
		return stopped, err
	}, OnWireBody: func(wire []byte) { lastWireBody = wire }, OnStream: func(result *claude.StreamResult, _ error) {
		if result != nil {
			streamResult = &streamingResult{usage: result.Usage, firstTokenMs: result.FirstTokenMs, clientDisconnect: result.ClientDisconnect}
		}
	}}
	attempt, err := (claude.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocolcore.ProtocolAnthropicMessages, Body: input.Body, ResponseModel: input.OriginalModel, Stream: input.RequestStream, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if stopped {
		return earlyResult, err
	}
	if err != nil {
		if input.RequestStream {
			requestSpeed := gjson.GetBytes(lastWireBody, "speed").String()
			if partial := partialStreamUsageResult(resp, streamResult, input.OriginalModel, input.RequestModel, input.StartTime, requestSpeed, err); partial != nil {
				return partial, err
			}
		}
		return nil, err
	}
	usage := &attempt.Usage
	firstTokenMs := attempt.FirstTokenMs
	clientDisconnect := attempt.ClientDisconnect
	return &ForwardResult{
		RequestID:                   resp.Header.Get("x-request-id"),
		UpstreamHeaders:             resp.Header,
		Usage:                       *usage,
		Model:                       input.OriginalModel,
		UpstreamModel:               input.RequestModel,
		UpstreamResponseServiceTier: observedUpstreamResponseServiceTier(c),
		Stream:                      input.RequestStream,
		Duration:                    time.Since(input.StartTime),
		FirstTokenMs:                firstTokenMs,
		ClientDisconnect:            clientDisconnect,
	}, nil
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
	const statusCode = http.StatusBadGateway

	accountID := int64(0)
	accountName := ""
	if account != nil {
		accountID = account.ID
		accountName = account.Name
	}

	logger.LegacyPrintf(
		"service.gateway",
		"Account %d(%s): upstream returned non-JSON 2xx response, attempting failover: status=%d request_id=%s error=%v",
		accountID,
		accountName,
		resp.StatusCode,
		resp.Header.Get("x-request-id"),
		parseErr,
	)

	decision := upstreamErrorDecisionWithoutPersistence(account, statusCode)
	if rateLimitService != nil && account != nil {
		if len(requestedModel) > 0 {
			decision = rateLimitService.ApplyUpstreamError(ctx, account, statusCode, resp.Header, body, requestedModel[0])
		} else {
			decision = rateLimitService.ApplyUpstreamError(ctx, account, statusCode, resp.Header, body)
		}
	}
	if decision.ShouldReturnGenericError() {
		return fmt.Errorf("upstream returned invalid JSON (not in custom error codes): %w", parseErr)
	}

	return &UpstreamFailoverError{
		StatusCode:             statusCode,
		ResponseBody:           body,
		ResponseHeaders:        resp.Header,
		RetryableOnSameAccount: decision.RetryableOnSameAccount(account, statusCode),
	}
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
	if dst == nil || src == nil {
		return
	}
	if filter != nil {
		responseheaders.WriteFilteredHeaders(dst, src, filter)
		return
	}
	if v := strings.TrimSpace(src.Get("Content-Type")); v != "" {
		dst.Set("Content-Type", v)
	}
	if v := strings.TrimSpace(src.Get("x-request-id")); v != "" {
		dst.Set("x-request-id", v)
	}
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
