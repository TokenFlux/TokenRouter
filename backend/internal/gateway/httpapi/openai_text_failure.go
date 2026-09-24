package httpapi

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"context"
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
)

// httpFailover 对 >=400 的上游响应做 failover 判定：命中时
// 记录 ops 事件、执行账号级错误处置并返回 *UpstreamFailoverError；未命中返回
// nil，调用方继续走各自端点格式的非 failover 错误处理链。
func (s *OpenAITextExecutor) httpFailover(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	resp *http.Response,
	respBody []byte,
	upstreamMsg string,
	upstreamModel string,
) *forwardcore.UpstreamFailoverError {
	shouldFailover := gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, respBody)
	if account != nil && account.Record.Platform == capability.PlatformGrok {
		shouldFailover = gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, respBody)
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
		decision = gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Grok.Health, account, resp.StatusCode, resp.Header, respBody, "", upstreamModel)
	} else {
		decision = gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, account, resp.StatusCode, resp.Header, respBody, false, upstreamModel)
	}
	if decision.ShouldReturnGenericError() || !decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, shouldFailover) {
		return nil
	}
	upstreamDetail := ""
	if s.Output.Options.LogUpstreamErrorBody {
		maxBytes := s.Output.Options.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(respBody), maxBytes)
	}
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               "failover",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	return gatewayprovider.NewOpenAIUpstreamFailure(
		resp.StatusCode,
		resp.Header,
		respBody,
		upstreamMsg,
		decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
	)
}
