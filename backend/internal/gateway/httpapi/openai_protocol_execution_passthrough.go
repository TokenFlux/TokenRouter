package httpapi

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

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

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// 旧透传入口仅保持签名，当前账号的请求准备和恢复由目标执行器唯一实现。
func (s *OpenAITextExecutor) Passthrough(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body, canonicalImageIntentBody []byte, reqModel string, attemptImageIntentInvalidated bool, reasoningEffort *string, reqStream bool, startTime time.Time, tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult) (*forwardcore.OpenAIResult, error) {
	input := openaiexecution.PassthroughInput{Body: body, CanonicalImageIntentBody: canonicalImageIntentBody, Model: reqModel, ImageIntentInvalidated: attemptImageIntentInvalidated, ReasoningEffort: reasoningEffort, Stream: reqStream, StartedAt: startTime}
	p := &openAIPassthroughExecutionAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch}}
	result, err := openaiexecution.RunPassthrough(ctx, input, p)
	return openaiexecution.ToForwardResult(result), err
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
	fields = AppendCodexRejectedRequestFields(fields, c, body)
	logging.FromContext(ctx).With(fields...).Warn("OpenAI passthrough 本地拦截：Codex 请求缺少有效 instructions")
}

// shouldFailoverOpenAIPassthroughResponse 只投影账号类别与平台错误分类。
func shouldFailoverOpenAIPassthroughResponse(account *gatewayprovider.ExecutionAccount, status int, body []byte) bool {
	return openaiexecution.ShouldFailoverPassthrough(status, body, openaiexecution.PassthroughFailureOptions{
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
