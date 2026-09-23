package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
)

// 本文件收敛三个 CC（Chat Completions）forwarder 之间重复的 HTTP 管线与 SSE
// 循环骨架（PR #3802 遗留项）：
//
//   - forwardAsRawChatCompletions          （原生 CC 直转）
//   - forwardResponsesViaRawChatCompletions（/v1/responses → CC 回退）
//   - forwardAnthropicViaRawChatCompletions（/v1/messages → CC 回退）
//
// 以及 messages / chat_completions 两条 Responses 主路径中逐字相同的错误处理块。
// 所有 helper 都是对既有内联代码的等价提取，不改变任何行为；各路径的差异
// （GLM effort 归一化、fast policy、Grok 分支、ClientDisconnect 语义等）仍留在
// 调用方，属于有意保留的行为差异，不在此强行统一。

func (s *OpenAIGatewayService) newUpstreamSSEScanner(r io.Reader) *bufio.Scanner {
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	return openai.NewCompatSSEScanner(r, maxLineSize)
}

// readOpenAIUpstreamError 读取上游错误体并把 resp.Body 回卷为可重读的副本
// （下游 handleXxxErrorResponse 需要再次读取），返回原始错误体与脱敏后的
// 上游错误消息。
func (s *OpenAIGatewayService) readOpenAIUpstreamError(resp *http.Response) ([]byte, string) {
	respBody := s.readUpstreamErrorBody(resp)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(respBody))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	return respBody, upstreamMsg
}

// failoverOpenAIUpstreamHTTPError 对 >=400 的上游响应做 failover 判定：命中时
// 记录 ops 事件、执行账号级错误处置并返回 *UpstreamFailoverError；未命中返回
// nil，调用方继续走各自端点格式的非 failover 错误处理链。
func (s *OpenAIGatewayService) failoverOpenAIUpstreamHTTPError(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	resp *http.Response,
	respBody []byte,
	upstreamMsg string,
	upstreamModel string,
) *forwardcore.UpstreamFailoverError {
	shouldFailover := s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody)
	if account != nil && account.Record.Platform == capability.PlatformGrok {
		shouldFailover = s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)
	}
	// 请求级拒绝不能触发账号策略或池模式重试。
	if detectHit, _, _ := openai.DetectOpenAICyberPolicy(respBody); detectHit || gatewayprovider.IsOpenAICyberWarningPayload(respBody, upstreamMsg) ||
		openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, respBody) ||
		openai.IsOpenAIContextWindowError(upstreamMsg, respBody) ||
		(account != nil && account.Record.Platform == capability.PlatformGrok && grok.IsGrokContentPolicyRejection(resp.StatusCode, respBody)) {
		return nil
	}
	// 没有 gin 上下文时无法安全评估请求级临时规则；保持上游语义，
	// 仅让默认已判定为可故障转移的错误继续进入账号策略管线。
	if c == nil && !shouldFailover && (account == nil || account.Record.Platform != capability.PlatformGrok) {
		return nil
	}
	var decision accountcore.UpstreamErrorDecision
	if account != nil && account.Record.Platform == capability.PlatformGrok {
		decision = s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
	} else {
		decision = s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
	}
	if decision.ShouldReturnGenericError() || !decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, shouldFailover) {
		return nil
	}
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(respBody), maxBytes)
	}
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               "failover",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	return newOpenAIUpstreamFailoverError(
		resp.StatusCode,
		resp.Header,
		respBody,
		upstreamMsg,
		decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
	)
}

// openAIChatCompletionsTargetURL 解析账号的（非 Grok）Chat Completions 上游端点。
func (s *OpenAIGatewayService) openAIChatCompletionsTargetURL(account *gatewayprovider.ExecutionAccount) (string, error) {
	baseURL := gatewayprovider.ExecutionProtocolTarget(account).GetOpenAIBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return buildOpenAIChatCompletionsURL(validatedURL), nil
}

// resolveCCFallbackTarget 解析两条 CC 回退路径共用的账号凭证与上游端点
// Grok 沿用自己的 OAuth/API Key 认证和 CLI 端点。
func (s *OpenAIGatewayService) resolveCCFallbackTarget(ctx context.Context, account *gatewayprovider.ExecutionAccount) (apiKey string, targetURL string, err error) {
	if account.View().IsGrok() {
		apiKey, _, err = s.executionCredentials.Resolve(ctx, gatewayprovider.ExecutionRecord(account))
		if err != nil {
			return "", "", err
		}
		targetURL, err = s.rawChatCompletionsURL(account)
		return apiKey, targetURL, err
	}
	apiKey = strings.TrimSpace(account.View().GetOpenAIProtocolAPIKey())
	if apiKey == "" {
		return "", "", fmt.Errorf("account %d missing api_key", account.Record.ID)
	}
	targetURL, err = s.openAIChatCompletionsTargetURL(account)
	if err != nil {
		return "", "", err
	}
	return apiKey, targetURL, nil
}

func (s *OpenAIGatewayService) sendCCUpstreamRequest(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	targetURL string,
	body []byte,
	stream bool,
	bearerToken string,
	userAgent string,
	grokCacheIdentity string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*http.Response, error) {
	return openai.SendChatRequest(ctx, body, openai.CCRequestOptions{
		URL: targetURL, Token: bearerToken, Stream: stream, Headers: c.Request.Header,
		RequestContext:  detachUpstreamContext,
		ObserveEndpoint: func() { gatewayhttp.SetActualOpenAIUpstreamEndpoint(c, "/v1/chat/completions") },
		AllowHeader:     func(name string) bool { return openaiCCRawAllowedHeaders[name] },
		PrepareTransport: func(upstreamReq *http.Request) {
			if len(tlsRouterMatch) == 0 {
				tlsRouterMatch = []egress.TLSFingerprintRouterMatchResult{s.matchTLSFingerprintRouter(c, account)}
			}
			if account.Record.Platform == capability.PlatformGrok && userAgent != "" {
				upstreamReq.Header.Set("user-agent", userAgent)
			} else if account.Record.Platform != capability.PlatformGrok {
				s.applyOpenAIUpstreamUserAgent(c.Request.Context(), c, account, upstreamReq, false, tlsRouterMatch[0])
			}

			if account.Record.Platform == capability.PlatformGrok {
				if account.View().IsGrokOAuth() {
					grok.ApplyCLIHeaders(upstreamReq.Header)
				}
				grok.ApplyGrokCacheHeaders(upstreamReq.Header, grokCacheIdentity)
			}
		},
		FinalizeHeaders: func(headers http.Header) {
			accountprovider.ApplyAccountHeaderOverrides(gatewayprovider.ExecutionProtocolRecord(account), headers)
			applyOpenCodeSessionHeader(c, account, targetURL, headers)
		},
		Do: func(req *http.Request) (*http.Response, error) {
			proxyURL := ""
			if account.Record.Proxy != nil {
				proxyURL = account.Record.Proxy.URL()
			}
			return s.httpUpstream.DoWithTLS(req, proxyURL, account.Record.ID, account.Record.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch...))
		},
		TransportError: func(err error) error { return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false) },
	})
}
