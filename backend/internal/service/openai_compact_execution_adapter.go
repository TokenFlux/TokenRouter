package service

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/gateway/compact"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

// compactModelAdapter 按原时点读取旧账号/配置；不保存另一份模型目录。
type compactModelAdapter struct {
	s       *OpenAIGatewayService
	account *gatewayprovider.ExecutionAccount
}

func (p compactModelAdapter) AccountModel(model string) (string, bool) {
	if p.account == nil {
		return "", false
	}
	return p.account.View().ResolveCompactMappedModel(model)
}
func (p compactModelAdapter) GlobalModel() string {
	if p.s == nil || p.s.cfg == nil {
		return ""
	}
	return p.s.cfg.Gateway.OpenAICompactModel
}
func (p compactModelAdapter) ResolveGlobalModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(p.account).OpenAIUpstream(model, false, false)
}
func compactRecovery(s *OpenAIGatewayService, account *gatewayprovider.ExecutionAccount) compact.Recovery {
	return compact.Recovery{Models: compactModelAdapter{s: s, account: account}, ContextWindow: openai.IsOpenAIContextWindowError, RewriteModel: openaiprotocol.ReplaceModelInBody}
}

// compactRetryAdapter 只执行单步响应释放和观测投影，决策与次序由原生核心拥有。
type compactRetryAdapter struct {
	s        *OpenAIGatewayService
	c        *gin.Context
	account  *gatewayprovider.ExecutionAccount
	response *http.Response
}

func (p *compactRetryAdapter) ObserveRetry(payload []byte, message string) {
	p.observe(payload, message, true)
}
func (p *compactRetryAdapter) observe(payload []byte, message string, passthrough bool) {
	in := compact.RetryObservation{Status: http.StatusBadRequest, Passthrough: passthrough}
	if p.account != nil {
		in.AccountPresent = true
		in.AccountID = p.account.Record.ID
		in.AccountName = p.account.Record.Name
		in.Platform = p.account.Record.Platform
	}
	if p.response != nil {
		in.Status = p.response.StatusCode
		in.RequestID = p.response.Header.Get("x-request-id")
	}
	if p.s != nil && p.s.cfg != nil {
		in.LogBody = p.s.cfg.Gateway.LogUpstreamErrorBody
		in.LogBodyMaxBytes = p.s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
	}
	notice := compact.Notice(in, payload, message, logredact.TruncateUTF8)
	if notice == nil {
		return
	}
	gatewayhttp.AppendOpsUpstreamError(p.c, ops.OpsUpstreamErrorEvent{
		Platform: notice.Platform, AccountID: notice.AccountID, AccountName: notice.AccountName,
		UpstreamStatusCode: notice.Status, UpstreamRequestID: notice.RequestID, Passthrough: notice.Passthrough,
		Kind: notice.Kind, Reason: notice.Reason, Message: notice.Message, Detail: notice.Detail, UpstreamResponseBody: notice.Detail,
	})
}
func (p *compactRetryAdapter) CloseResponse() {
	if p.response != nil && p.response.Body != nil {
		_ = p.response.Body.Close()
	}
}
func (p *compactRetryAdapter) SetModel(model string) { gatewayhttp.SetOpsUpstreamModel(p.c, model) }
func (p *compactRetryAdapter) LogRetry(from, model, code string) {
	name := ""
	if p.account != nil {
		name = p.account.Record.Name
	}
	logging.LegacyPrintf("service.openai_gateway", "[OpenAI passthrough] Retrying explicit compact request once with fallback model (account: %s, from: %s, to: %s, upstream_code: %s)", name, from, model, code)
}
