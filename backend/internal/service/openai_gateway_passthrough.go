package service

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"context"

	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	// 本文件承载 /v1/responses 透传转发及其流式、非流式响应与错误处理。

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// 旧透传入口仅保持签名，当前账号的请求准备和恢复由目标执行器唯一实现。
func (s *OpenAIGatewayService) forwardOpenAIPassthrough(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body, canonicalImageIntentBody []byte, reqModel string, attemptImageIntentInvalidated bool, reasoningEffort *string, reqStream bool, startTime time.Time, tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult) (*forwardcore.OpenAIResult, error) {
	input := forward.PassthroughInput{Body: body, CanonicalImageIntentBody: canonicalImageIntentBody, Model: reqModel, ImageIntentInvalidated: attemptImageIntentInvalidated, ReasoningEffort: reasoningEffort, Stream: reqStream, StartedAt: startTime}
	p := &openAIPassthroughExecutionAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch}}
	result, err := forward.RunPassthrough(ctx, input, p)
	return openAIForwardResultFromHTTP(result), err
}

func logOpenAIPassthroughInstructionsRejected(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	reqModel string,
	rejectReason string,
	body []byte,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	accountID := int64(0)
	accountName := ""
	accountType := ""
	if account != nil {
		accountID = account.Record.ID
		accountName = strings.TrimSpace(account.Record.Name)
		accountType = strings.TrimSpace(string(account.Record.Type))
	}
	fields := []zap.Field{
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", accountID),
		zap.String("account_name", accountName),
		zap.String("account_type", accountType),
		zap.String("request_model", strings.TrimSpace(reqModel)),
		zap.String("reject_reason", strings.TrimSpace(rejectReason)),
	}
	fields = gatewayhttp.AppendCodexRejectedRequestFields(fields, c, body)
	logging.FromContext(ctx).With(fields...).Warn("OpenAI passthrough 本地拦截：Codex 请求缺少有效 instructions")
}

func (s *OpenAIGatewayService) buildUpstreamRequestOpenAIPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	token string,
	routerMatch ...egress.TLSFingerprintRouterMatchResult,
) (*http.Request, error) {
	return forward.BuildPassthroughRequest(ctx, body, s.openAIRequestTarget(c, account, true), func(b []byte) []byte {
		return forward.NormalizeCNResponsesBody(account != nil && gatewayprovider.ExecutionProtocolTarget(account).UsesNativeCNResponses(), b)
	}, func(target string) openai.PassthroughRequestOptions {
		options := s.nativeResponsesRequestOptions(ctx, c, account, token, target, false, routerMatch...)
		options.ForwardHeaders = func() http.Header {
			if c == nil || c.Request == nil {
				return nil
			}
			return c.Request.Header
		}
		options.ApplyUserAgent = func(req *http.Request) { s.applyOpenAIUpstreamUserAgent(ctx, c, account, req, true, routerMatch...) }
		options.Diagnostics = func(headers http.Header, body []byte) {
			logOpenAIRoutingDiagnosticsFromBody(ctx, account, "http_passthrough", headers, body, "not_applicable")
		}
		return openai.PassthroughRequestOptions{
			ResponsesRequestOptions: options,
			AllowTimeoutHeaders:     s.isOpenAIPassthroughTimeoutHeadersAllowed,
			AllowPassthroughHeader:  isOpenAIPassthroughAllowedRequestHeader,
			MatchedOriginator: func() string {
				if len(routerMatch) > 0 && routerMatch[0].Matched {
					return strings.TrimSpace(routerMatch[0].UpstreamOriginator)
				}
				return ""
			},
		}
	})
}

// shouldFailoverOpenAIPassthroughResponse 只投影账号类别与平台错误分类。
func shouldFailoverOpenAIPassthroughResponse(account *gatewayprovider.ExecutionAccount, status int, body []byte) bool {
	return forward.ShouldFailoverPassthrough(status, body, forward.PassthroughFailureOptions{
		APIKey:        account != nil && account.Record.Type == capability.AccountTypeAPIKey,
		Cyber:         func(b []byte) bool { hit, _, _ := openai.DetectOpenAICyberPolicy(b); return hit },
		ContextWindow: func(b []byte) bool { return openai.IsOpenAIContextWindowError("", b) },
		AccessState:   func(s int, b []byte) bool { return gatewayprovider.IsOpenAIHTTPUpstreamAccessStateError(s, "", b) },
		BodyTooLarge:  func(s int, b []byte) bool { return gatewayprovider.IsOpenAIRequestBodyTooLargeError(s, "", b) },
		PoolRetryable: func(s int) bool {
			return account != nil && account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(s)
		},
	})
}

func isOpenAIPassthroughAllowedRequestHeader(lowerKey string, allowTimeoutHeaders bool) bool {
	if lowerKey == "" {
		return false
	}
	if isOpenAIPassthroughTimeoutHeader(lowerKey) {
		return allowTimeoutHeaders
	}
	return openaiPassthroughAllowedHeaders[lowerKey]
}

func isOpenAIPassthroughTimeoutHeader(lowerKey string) bool {
	switch lowerKey {
	case "x-stainless-timeout", "x-stainless-read-timeout", "x-stainless-connect-timeout", "x-request-timeout", "request-timeout", "grpc-timeout":
		return true
	default:
		return false
	}
}

func (s *OpenAIGatewayService) isOpenAIPassthroughTimeoutHeadersAllowed() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIPassthroughAllowTimeoutHeaders
}

func collectOpenAIPassthroughTimeoutHeaders(h http.Header) []string {
	if h == nil {
		return nil
	}
	var matched []string
	for key, values := range h {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if isOpenAIPassthroughTimeoutHeader(lowerKey) {
			entry := lowerKey
			if len(values) > 0 {
				entry = fmt.Sprintf("%s=%s", lowerKey, strings.Join(values, "|"))
			}
			matched = append(matched, entry)
		}
	}
	sort.Strings(matched)
	return matched
}
