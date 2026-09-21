// 预检只投影当前账号与单次上游调用，不持有额外重试或资金状态。
package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (p openAITextHTTPBackend) InputTokensExecution(c *gin.Context, call gatewayhttp.InputTokensCall) textflow.InputTokensPorts {
	return &inputTokensAttemptBridge{h: p.h, c: c, call: call, key: apikey.CopyAPIKey(call.Key)}
}

type inputTokensAttemptBridge struct {
	h         *OpenAIGatewayHandler
	c         *gin.Context
	call      gatewayhttp.InputTokensCall
	key       *apikey.APIKey
	selection *service.AccountSelectionResult
}

func (p *inputTokensAttemptBridge) Context() context.Context { return p.c.Request.Context() }
func (p *inputTokensAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, bool, error) {
	start := time.Now()
	selected, _, err := p.h.gatewayService.SelectAccountWithSchedulerForCapabilityAndRoutingModel(p.Context(), p.key.GroupID, "", p.call.SessionHash, p.call.Model, p.call.RoutingModel, excluded, egress.OpenAIUpstreamTransportAny, accountcore.OpenAIEndpointCapabilityTextGeneration, false, false, p.call.Platform)
	gatewayhttp.SetOpsLatencyMs(p.c, gatewayhttp.OpsRoutingLatencyMsKey, time.Since(start).Milliseconds())
	if err != nil {
		return textflow.Selection{}, false, err
	}
	if selected == nil || selected.Account == nil {
		return textflow.Selection{}, false, nil
	}
	p.selection = selected
	account := selected.Account
	gatewayhttp.SetOpsSelectedAccount(p.c, account.ID, account.Platform)
	return textflow.Selection{Account: service.AccountSnapshotView(account), RetryLimit: account.GetPoolModeRetryCount()}, true, nil
}
func (p *inputTokensAttemptBridge) SelectionFailed(err error, last *textflow.AttemptFailure, _ bool) {
	if err != nil {
		p.call.Log.Warn("openai_responses_input_tokens.account_select_failed", zap.Error(err))
	}
	if last != nil {
		p.Exhausted(last)
		return
	}
	if err == nil {
		gatewayhttp.MarkOpsRoutingCapacityLimited(p.c)
		p.h.errorResponse(p.c, http.StatusServiceUnavailable, "api_error", "No available accounts")
		return
	}
	if p.h.handleOpenAISelectionBusinessError(p.c, err, false) {
		return
	}
	cls := classifyOpenAICompatibleResolvedRoutingNoAccountErrorFromGin(p.c, p.h.gatewayService, p.key, p.call.RoutingModel, p.call.Model)
	if !cls.ModelNotFound {
		gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(p.c, err)
	}
	p.h.errorResponse(p.c, cls.Status, cls.ErrType, cls.Message)
}
func (p *inputTokensAttemptBridge) Forward(_ textflow.Selection) *textflow.AttemptFailure {
	start := time.Now()
	err := func() error {
		if p.selection.Acquired && p.selection.ReleaseFunc != nil {
			defer p.selection.ReleaseFunc()
		}
		return p.h.gatewayService.ForwardResponsesInputTokens(p.Context(), p.c, p.selection.Account, p.call.Body)
	}()
	gatewayhttp.SetOpsLatencyMs(p.c, gatewayhttp.OpsResponseLatencyMsKey, time.Since(start).Milliseconds())
	if err == nil {
		return nil
	}
	var original *forwardcore.UpstreamFailoverError
	if errors.As(err, &original) {
		return &textflow.AttemptFailure{Cause: err, Policy: original.RetryFailure()}
	}
	return &textflow.AttemptFailure{Cause: err}
}
func (p *inputTokensAttemptBridge) ForwardFailed(selected textflow.Selection, err error) {
	p.call.Log.Error("openai_responses_input_tokens.forward_failed", zap.Int64("account_id", selected.Account.ID), zap.Error(err))
}
func (p *inputTokensAttemptBridge) Exhausted(failure *textflow.AttemptFailure) {
	var original *forwardcore.UpstreamFailoverError
	errors.As(failure.Cause, &original)
	p.h.handleFailoverExhausted(p.c, original, false)
}
