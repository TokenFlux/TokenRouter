package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"

	// 本文件由 gateway_service.go 纯移动拆分而来：Bedrock 上游转发（CC 兼容转换、
	// 请求构建、错误处理与非流式响应）。仅做代码搬迁，无任何行为变更。

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
	"github.com/gin-gonic/gin"
)

// ApplyBedrockCCCompat 应用 Bedrock CC 兼容转换（渠道级模型映射后调用）
// 清理 body 中 Anthropic API 专有字段、修复 thinking/tool_use ID、过滤 beta token，
// 同时过滤 HTTP header 中的 anthropic-beta（防止 Passthrough 路径透传不支持的 token）。
func (s *GatewayService) ApplyBedrockCCCompat(c *gin.Context, body []byte, model string, account *gatewayprovider.ExecutionAccount, groupID *int64) []byte {
	if !s.isBedrockCCCompatEnabled(c.Request.Context(), account, groupID) {
		return body
	}
	body = bedrock.SanitizeBedrockCCFields(body)
	body = bedrock.SanitizeBedrockThinking(body, model)
	body = bedrock.SanitizeBedrockToolUseIDs(body)
	body = bedrock.SanitizeBedrockCCBetaTokens(body, model)
	// 过滤 HTTP header 中的 anthropic-beta，只保留 Bedrock 支持的 token
	if betaHeader := c.GetHeader("anthropic-beta"); betaHeader != "" {
		if filtered := bedrock.ResolveBedrockBetaTokens(betaHeader, body, model); len(filtered) > 0 {
			c.Request.Header.Set("anthropic-beta", strings.Join(filtered, ", "))
		} else {
			c.Request.Header.Del("anthropic-beta")
		}
	}
	return body
}

// isBedrockCCCompatEnabled 检查渠道是否启用了 Bedrock CC 兼容模式
func (s *GatewayService) isBedrockCCCompatEnabled(ctx context.Context, account *gatewayprovider.ExecutionAccount, groupID *int64) bool {
	if groupID == nil || s.channelService == nil {
		return false
	}
	ch, err := s.channelService.GetChannelForGroup(ctx, *groupID)
	if err != nil || ch == nil {
		return false
	}
	return ch.IsBedrockCCCompatEnabled(account.Record.Platform)
}

// forwardBedrock 转发请求到 AWS Bedrock
func (s *GatewayService) forwardBedrock(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	parsed *requeststate.ParsedRequest,
	startTime time.Time,
) (*forward.MessagesResult, error) {
	reqModel := parsed.Model
	reqStream := parsed.Stream
	body := parsed.Body.Bytes()

	route, err := gatewayprovider.ExecutionModelPolicy(account).BedrockRoute(reqModel)
	if err != nil {
		logging.LegacyPrintf("service.gateway", "[Bedrock] %s", bedrock.BedrockRoutingDiagnostic(err))
		return nil, err
	}
	region, mappedModel := route.SourceRegion, route.ModelID
	if mappedModel != reqModel {
		logging.LegacyPrintf("service.gateway", "[Bedrock] Model mapping: %s -> %s (account: %s)", reqModel, mappedModel, account.Record.Name)
	}

	betaHeader := ""
	if c != nil && c.Request != nil {
		betaHeader = c.GetHeader("anthropic-beta")
	}

	// 准备请求体（注入 anthropic_version/anthropic_beta，移除 Bedrock 不支持的字段，清理 cache_control）
	betaTokens, err := s.resolveBedrockBetaTokensForRequest(ctx, account, betaHeader, body, mappedModel)
	if err != nil {
		return nil, err
	}

	bedrockBody, err := bedrock.PrepareBedrockRequestBodyWithTokens(body, mappedModel, betaTokens, false)
	if err != nil {
		return nil, fmt.Errorf("prepare bedrock request body: %w", err)
	}

	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}

	logging.LegacyPrintf("service.gateway", "[Bedrock] 命中 Bedrock 分支: account=%d name=%s model=%s->%s stream=%v",
		account.Record.ID, account.Record.Name, reqModel, mappedModel, reqStream)

	// 根据账号类型选择认证方式
	var signer *bedrock.BedrockSigner
	var bedrockAPIKey string
	if account.View().IsBedrockAPIKey() {
		bedrockAPIKey = account.View().GetCredential("api_key")
		if bedrockAPIKey == "" {
			return nil, fmt.Errorf("api_key not found in bedrock credentials")
		}
	} else {
		signer, err = accountprovider.NewBedrockSignerFromAccount(gatewayprovider.ExecutionRecord(account))
		if err != nil {
			return nil, fmt.Errorf("create bedrock signer: %w", err)
		}
	}

	options, policy := s.bedrockRequestOptions(ctx, c, account, mappedModel, region, reqStream, signer, bedrockAPIKey, proxyURL)
	streamOptions := bedrock.StreamOptions{AccountID: account.Record.ID}
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamOptions.Interval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	if s.healthObserver != nil {
		streamOptions.OnTimeout = func(ctx context.Context, model string) {
			s.healthObserver.Core.HandleStreamTimeout(ctx, gatewayprovider.ExecutionRecord(account), model)

		}
	}
	hadHTTPError := false
	var errorResult *forward.MessagesResult
	target := &bedrock.Target{AccountID: account.Record.ID, Request: options, Retry: policy, Stream: streamOptions, StartedAt: startTime, Enter: s.nativeAttemptActivity, Accepted: parsed.OnUpstreamAccepted, ReadBody: func(r io.Reader) ([]byte, error) {
		return gatewayhttp.ReadUpstreamResponseBody(r, resolveUpstreamResponseReadLimit(s.cfg), c, gatewayhttp.AnthropicResponseTooLarge)
	}, HTTPError: func(ctx context.Context, resp *http.Response) (upstream.AttemptResult, error) {
		hadHTTPError = true
		var err error
		errorResult, err = s.handleBedrockUpstreamErrors(ctx, resp, c, account, mappedModel)
		return upstream.AttemptResult{}, err
	}}
	result, err := (bedrock.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolAnthropicMessages, Body: bedrockBody, Stream: reqStream, ResponseModel: reqModel, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if hadHTTPError {
		return errorResult, err
	}
	if err != nil {
		return nil, err
	}
	converted := forward.MessagesFromAttempt(result)
	converted.UpstreamHeaders = result.UpstreamHeaders
	return converted, nil
}

// handleBedrockUpstreamErrors 处理 Bedrock 上游 4xx/5xx 错误（failover + 错误响应）
func (s *GatewayService) handleBedrockUpstreamErrors(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	mappedModel string,
) (*forward.MessagesResult, error) {
	// retry exhausted + failover
	if s.shouldRetryUpstreamError(account, resp.StatusCode) {
		if s.shouldFailoverUpstreamError(resp.StatusCode) {
			respBody, _ := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(respBody))

			logging.LegacyPrintf("service.gateway", "[Bedrock] Upstream error (retry exhausted, failover): Account=%d(%s) Status=%d Body=%s",
				account.Record.ID, account.Record.Name, resp.StatusCode, logredact.TruncateUTF8(string(respBody), 1000))

			decision := s.handleRetryExhaustedSideEffects(ctx, resp, account, mappedModel)
			if decision.ShouldReturnGenericError() {
				return s.handleErrorResponse(ctx, resp, c, account, mappedModel)
			}
			gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
				Platform:           account.Record.Platform,
				AccountID:          account.Record.ID,
				AccountName:        account.Record.Name,
				UpstreamStatusCode: resp.StatusCode,
				Kind:               "retry_exhausted_failover",
				Message:            upstream.ExtractErrorMessage(respBody),
			})
			return nil, &forward.UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
			}
		}
		return s.handleRetryExhaustedError(ctx, resp, c, account, mappedModel)
	}

	// non-retryable failover
	if s.shouldFailoverUpstreamError(resp.StatusCode) {
		respBody, _ := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		decision := s.handleFailoverSideEffects(ctx, resp, account, mappedModel)
		if decision.ShouldReturnGenericError() {
			return s.handleErrorResponse(ctx, resp, c, account, mappedModel)
		}
		gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			Kind:               "failover",
			Message:            upstream.ExtractErrorMessage(respBody),
		})
		return nil, &forward.UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           respBody,
			RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
		}
	}

	// other errors
	return s.handleErrorResponse(ctx, resp, c, account, mappedModel)
}

func (s *GatewayService) bedrockRequestOptions(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, modelID, region string, stream bool, signer *bedrock.BedrockSigner, apiKey, proxyURL string) (bedrock.RequestOptions, bedrock.RetryPolicy) {
	options := bedrock.RequestOptions{ModelID: modelID, Region: region, Stream: stream, Signer: signer, APIKey: apiKey, APIKeyMode: account.View().IsBedrockAPIKey(), Do: func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.DoWithTLS(req, proxyURL, account.Record.ID, account.Record.Concurrency, nil)
	}}
	policy := bedrock.RetryPolicy{MaxAttempts: maxRetryAttempts, MaxElapsed: maxRetryElapsed, Delay: forward.RetryDelay, ShouldRetry: func(status int) bool { return s.shouldRetryUpstreamError(account, status) }, ReadErrorBody: s.readUpstreamErrorBody, TransportError: func(err error, url string) error {
		return s.handleUpstreamTransportError(ctx, c, account, err, ops.OpsUpstreamErrorEvent{UpstreamURL: logredact.SafeUpstreamURL(url)})
	}, ObserveRetry: func(resp *http.Response, body []byte, url string, attempt int, delay time.Duration) {
		detail := ""
		if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
			detail = logredact.TruncateUTF8(string(body), s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
		}
		gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{Platform: account.Record.Platform, AccountID: account.Record.ID, AccountName: account.Record.Name, UpstreamStatusCode: resp.StatusCode, UpstreamURL: logredact.SafeUpstreamURL(url), Kind: "retry", Message: upstream.ExtractErrorMessage(body), Detail: detail})
		logging.LegacyPrintf("service.gateway", "[Bedrock] account %d: upstream error %d, retry %d/%d after %v", account.Record.ID, resp.StatusCode, attempt, maxRetryAttempts, delay)
	}}
	return options, policy
}
