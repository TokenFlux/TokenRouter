// 预检只投影当前账号与单次上游调用，不持有额外重试或资金状态。
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (p OpenAITokenPorts) InputTokensExecution(c *gin.Context, call InputTokensCall) textflow.InputTokensPorts {
	return &inputTokensAttemptBridge{ports: p, c: c, call: call, key: apikey.CopyAPIKey(call.Key)}
}

type inputTokensAttemptBridge struct {
	ports     OpenAITokenPorts
	c         *gin.Context
	call      InputTokensCall
	key       *apikey.APIKey
	selection InputTokensSelection
}

func (p *inputTokensAttemptBridge) Context() context.Context { return p.c.Request.Context() }
func (p *inputTokensAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, bool, error) {
	start := time.Now()
	selected, err := p.ports.Execution.SelectInputTokens(p.Context(), p.key.GroupID, p.call.SessionHash, p.call.Model, p.call.RoutingModel, excluded, p.call.Platform)
	SetOpsLatencyMs(p.c, OpsRoutingLatencyMsKey, time.Since(start).Milliseconds())
	if err != nil {
		return textflow.Selection{}, false, err
	}
	if selected.Target == nil {
		return textflow.Selection{}, false, nil
	}
	p.selection = selected
	account := selected.Target.Snapshot()
	SetOpsSelectedAccount(p.c, account.ID, account.Platform)
	return textflow.Selection{Account: account, RetryLimit: selected.Target.RetryLimit()}, true, nil
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
		MarkOpsRoutingCapacityLimited(p.c)
		writeOpenAITokenError(p.c, http.StatusServiceUnavailable, "api_error", "No available accounts")
		return
	}
	if WriteGroupSelectionBusinessError(p.c, err, false, keyhttp.GetAPIKeyFromContext, gatewayprovider.ModelDisplayCatalogue{}, func(status int, kind, message string, _ bool) { writeOpenAITokenError(p.c, status, kind, message) }) {
		return
	}
	cls := tokenSelectionError(p.c, p.ports.ResolvedDiagnoser, p.key, p.call.RoutingModel, p.call.Model)
	if !cls.ModelNotFound {
		MarkOpsRoutingCapacityLimitedIfNoAvailable(p.c, err)
	}
	writeOpenAITokenError(p.c, cls.Status, cls.ErrType, cls.Message)
}
func (p *inputTokensAttemptBridge) Forward(_ textflow.Selection) *textflow.AttemptFailure {
	start := time.Now()
	err := func() error {
		if p.selection.Release != nil {
			defer p.selection.Release()
		}
		return p.selection.Target.ForwardInputTokens(p.Context(), p.c, p.call.Body)
	}()
	SetOpsLatencyMs(p.c, OpsResponseLatencyMsKey, time.Since(start).Milliseconds())
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
	WriteOpenAIFailoverExhausted(p.c, ProjectOpenAIFailoverError(original), false, p.ports.Rules, FailoverErrorHooks{Upstream: func(c *gin.Context, status int, message string) { SetOpsUpstreamError(c, status, message, "") }, SkipMonitoring: func(c *gin.Context) { c.Set(OpsSkipPassthroughKey, true) }}, func(c *gin.Context, status int, kind, message string, _ bool) {
		writeOpenAITokenError(c, status, kind, message)
	})
}
