// 计数专用 Adapter 只执行原单次选择与上游桥接，不取得槽位或提交用量。
package httpapi

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type openAICountAttempt struct {
	ports   OpenAITokenPorts
	c       *gin.Context
	call    OpenAICountCall
	key     *apikey.APIKey
	account OpenAICountTarget
}

func (p OpenAITokenPorts) CountExecution(c *gin.Context, call OpenAICountCall) textflow.SingleCountPorts {
	return &openAICountAttempt{ports: p, c: c, call: call, key: apikey.CopyAPIKey(call.Key)}
}
func (p *openAICountAttempt) Select() (bool, error) {
	// 专用入口显式豁免利润门，不能替换为普通带槽选择。
	account, err := p.ports.Execution.SelectCount(p.c.Request.Context(), p.key.GroupID, p.call.SessionHash, p.call.AccountLayerModel, p.call.Platform)
	p.account = account
	return account != nil, err
}
func (p *openAICountAttempt) Selected() {
	SetOpsLatencyMs(p.c, OpsAuthLatencyMsKey, time.Since(p.call.StartedAt).Milliseconds())
}
func (p *openAICountAttempt) SelectionFailed(err error) {
	if err != nil {
		p.call.Log.Warn("openai_count_tokens.account_select_failed", zap.Error(OpenAICompatibleSelectionErrorForLog(err, p.call.Platform)))
	}
	cls := tokenSelectionError(p.c, p.ports.Diagnoser, p.key, p.call.AccountLayerModel, p.call.Model)
	if !cls.ModelNotFound {
		if err != nil {
			MarkOpsRoutingCapacityLimitedIfNoAvailable(p.c, err)
		} else {
			MarkOpsRoutingCapacityLimited(p.c)
		}
	}
	WriteAnthropicError(p.c, cls.Status, cls.ErrType, "", cls.Message)
}
func (p *openAICountAttempt) Forward() error {
	selected := p.account.Snapshot()
	SetOpsSelectedAccount(p.c, selected.ID, selected.Platform)
	body := p.call.MappedBody(p.call.Mapping.Mapped, p.call.Mapping.MappedModel)
	return p.account.ForwardCount(p.c.Request.Context(), p.c, body, p.call.AccountLayerModel)
}
func (p *openAICountAttempt) ForwardFailed(err error) {
	p.call.Log.Error("openai_count_tokens.forward_failed", zap.Int64("account_id", p.account.Snapshot().ID), zap.Error(err))
}
