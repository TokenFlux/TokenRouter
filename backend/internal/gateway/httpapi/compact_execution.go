package httpapi

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/gateway/compact"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/gin-gonic/gin"
)

// CompactExecutor 绑定固定模型配置与 HTTP 观测；一次恢复仍由 compact.Recovery 决定。
type CompactExecutor struct {
	Models          gatewayprovider.CompactModels
	LogBody         bool
	LogBodyMaxBytes int
}
type compactRetryEffects struct {
	options  *CompactExecutor
	c        *gin.Context
	account  *gatewayprovider.ExecutionAccount
	response *http.Response
}

func (p *compactRetryEffects) ObserveRetry(payload []byte, message string) {
	p.observe(payload, message, true)
}

func (p *compactRetryEffects) observe(payload []byte, message string, passthrough bool) {
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
	if p.options != nil {
		in.LogBody = p.options.LogBody
		in.LogBodyMaxBytes = p.options.LogBodyMaxBytes
	}
	notice := compact.Notice(in, payload, message, logredact.TruncateUTF8)
	if notice == nil {
		return
	}
	AppendOpsUpstreamError(p.c, ops.OpsUpstreamErrorEvent{
		Platform: notice.Platform, AccountID: notice.AccountID, AccountName: notice.AccountName,
		UpstreamStatusCode: notice.Status, UpstreamRequestID: notice.RequestID, Passthrough: notice.Passthrough,
		Kind: notice.Kind, Reason: notice.Reason, Message: notice.Message, Detail: notice.Detail, UpstreamResponseBody: notice.Detail,
	})
}

func (p *compactRetryEffects) CloseResponse() {
	if p.response != nil && p.response.Body != nil {
		_ = p.response.Body.Close()
	}
}

func (p *compactRetryEffects) SetModel(model string) { SetOpsUpstreamModel(p.c, model) }

func (p *compactRetryEffects) LogRetry(from, model, code string) {
	name := ""
	if p.account != nil {
		name = p.account.Record.Name
	}
	logging.LegacyPrintf("service.openai_gateway", "[OpenAI passthrough] Retrying explicit compact request once with fallback model (account: %s, from: %s, to: %s, upstream_code: %s)", name, from, model, code)
}
func IsExplicitOpenAICompactContext(c *gin.Context) bool {
	return IsOpenAIResponsesCompactPath(c) || IsOpenAINativeCompactionV2(c)
}
func IsExplicitOpenAICompactRequest(c *gin.Context, body []byte) bool {
	return IsOpenAIResponsesCompactPath(c) || openaiprotocol.HasCompactionTriggerInInput(body)
}
func NewOpenAICompactFailure(c *gin.Context, payload []byte, message string) error {
	signal := (gatewayprovider.CompactModels{}).Recovery(nil).NewFailure(IsExplicitOpenAICompactContext(c), payload, message)
	if signal == nil {
		return nil
	}
	return signal
}
func (p *CompactExecutor) ResolveModel(target *gatewayprovider.ExecutionAccount, model string) string {
	return p.Models.Recovery(target).ResolveModel(model)
}
func (p *CompactExecutor) Prepare(c *gin.Context, target *gatewayprovider.ExecutionAccount, requested string, body []byte, status int, message string, payload []byte, tried bool) ([]byte, string, bool) {
	return p.Models.Recovery(target).Prepare(compact.Request{Explicit: IsExplicitOpenAICompactRequest(c, body), AlreadyRetried: tried, RequestedModel: requested, Body: body}, status, message, payload)
}
func (p *CompactExecutor) Observe(c *gin.Context, target *gatewayprovider.ExecutionAccount, response *http.Response, payload []byte, message string, passthrough bool) {
	effects := compactRetryEffects{options: p, c: c, account: target, response: response}
	effects.observe(payload, message, passthrough)
}
func (p *CompactExecutor) ApplySignal(c *gin.Context, target *gatewayprovider.ExecutionAccount, requested string, body []byte, err error, tried bool, response *http.Response) ([]byte, string, bool) {
	signal, ok := compact.AsFailure(err)
	if !ok {
		return body, "", false
	}
	return p.Models.Recovery(target).ApplySignal(compact.Request{Explicit: IsExplicitOpenAICompactRequest(c, body), AlreadyRetried: tried, RequestedModel: requested, Body: body}, signal, &compactRetryEffects{options: p, c: c, account: target, response: response})
}
