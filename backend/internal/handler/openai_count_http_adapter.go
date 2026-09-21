// 计数专用 Adapter 只执行原单次选择与上游桥接，不取得槽位或提交用量。
package handler

import (
	"time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type openAICountAttempt struct {
	h       *OpenAIGatewayHandler
	c       *gin.Context
	call    gatewayhttp.OpenAICountCall
	key     *apikey.APIKey
	account *service.Account
}

func (p openAITextHTTPBackend) CountExecution(c *gin.Context, call gatewayhttp.OpenAICountCall) textflow.SingleCountPorts {
	return &openAICountAttempt{h: p.h, c: c, call: call, key: apikey.CopyAPIKey(call.Key)}
}
func (p *openAICountAttempt) Select() (bool, error) {
	// 专用入口显式豁免利润门，不能替换为普通带槽选择。
	account, err := p.h.gatewayService.SelectAccountForTokenCount(p.c.Request.Context(), p.key.GroupID, p.call.SessionHash, p.call.AccountLayerModel, accountcore.OpenAIEndpointCapabilityTextGeneration, p.call.Platform)
	p.account = account
	return account != nil, err
}
func (p *openAICountAttempt) Selected() {
	gatewayhttp.SetOpsLatencyMs(p.c, gatewayhttp.OpsAuthLatencyMsKey, time.Since(p.call.StartedAt).Milliseconds())
}
func (p *openAICountAttempt) SelectionFailed(err error) {
	if err != nil {
		p.call.Log.Warn("openai_count_tokens.account_select_failed", zap.Error(openAICompatibleSelectionErrorForLog(err, p.call.Platform)))
	}
	cls := classifyOpenAICompatibleNoAccountErrorFromGin(p.c, p.h.gatewayService, p.key, p.call.AccountLayerModel, p.call.Model)
	if !cls.ModelNotFound {
		if err != nil {
			gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(p.c, err)
		} else {
			gatewayhttp.MarkOpsRoutingCapacityLimited(p.c)
		}
	}
	gatewayhttp.WriteAnthropicError(p.c, cls.Status, cls.ErrType, "", cls.Message)
}
func (p *openAICountAttempt) Forward() error {
	gatewayhttp.SetOpsSelectedAccount(p.c, p.account.ID, p.account.Platform)
	body := p.call.MappedBody(p.call.Mapping.Mapped, p.call.Mapping.MappedModel)
	return p.h.gatewayService.ForwardCountTokensAsAnthropic(p.c.Request.Context(), p.c, p.account, body, p.call.AccountLayerModel)
}
func (p *openAICountAttempt) ForwardFailed(err error) {
	p.call.Log.Error("openai_count_tokens.forward_failed", zap.Int64("account_id", p.account.ID), zap.Error(err))
}
