// 计数过渡适配仅投影账号与既有执行器，重试由 gateway/text 持有。
package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type countTokensHTTPBackend struct{ messagesHTTPBackend }

func (h *GatewayHandler) NewCountTokensHTTPHandler() *gatewayhttp.CountTokensHandler {
	return gatewayhttp.NewCountTokensHandler(gatewayMaxBodySize(h.cfg), h.maxAccountSwitches, countTokensHTTPBackend{messagesHTTPBackend{h}}, h.gatewayService)
}
func (p countTokensHTTPBackend) Execution(c *gin.Context, key *apikey.APIKey, parsed *requeststate.ParsedRequest, hash string, log *zap.Logger) textflow.CountPorts {
	return &countTokensAttemptBridge{h: p.h, c: c, key: service.APIKeyFromView(key), parsed: parsed, hash: hash, log: log}
}

// 每请求只保留当前账号及当次副本，不存储排除集合或第二份重试计数。
type countTokensAttemptBridge struct {
	h               *GatewayHandler
	c               *gin.Context
	key             *service.APIKey
	parsed, attempt *requeststate.ParsedRequest
	hash            string
	log             *zap.Logger
	account         *service.Account
}

func (b *countTokensAttemptBridge) Context() context.Context { return b.c.Request.Context() }
func (b *countTokensAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	account, err := b.h.gatewayService.SelectAccountForModelWithExclusions(b.Context(), b.key.GroupID, b.hash, b.parsed.Model, excluded)
	if err != nil {
		return textflow.Selection{}, err
	}
	b.account = account
	setOpsSelectedAccount(b.c, account.ID, account.Platform)
	return textflow.Selection{Account: service.AccountSnapshotView(account), RetryLimit: account.GetPoolModeRetryCount()}, nil
}
func (b *countTokensAttemptBridge) SelectionFailed(err error, last *textflow.AttemptFailure) {
	b.log.Warn("gateway.count_tokens_select_account_failed", zap.Error(err))
	if last != nil {
		b.exhausted(last, service.PlatformAnthropic)
		return
	}
	if handleGroupSelectionBusinessError(b.c, err, false, func(status int, kind, message string, _ bool) { b.h.errorResponse(b.c, status, kind, message) }) {
		return
	}
	cls := classifyNoAccountErrorFromGin(b.c, b.h.gatewayService, b.key, b.parsed.Model, b.parsed.Model, service.PlatformAnthropic)
	if !cls.ModelNotFound {
		markOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	b.h.errorResponse(b.c, cls.Status, cls.ErrType, cls.Message)
}
func (b *countTokensAttemptBridge) Prepare(_ textflow.Selection) bool {
	var err error
	b.attempt, _, err = b.h.prepareGatewayAttemptRequest(b.Context(), b.parsed, b.parsed.Body.Bytes(), b.key, b.parsed.Model)
	if err != nil {
		b.h.errorResponse(b.c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return false
	}
	return true
}
func (b *countTokensAttemptBridge) Forward(_ textflow.Selection) *textflow.AttemptFailure {
	err := b.h.gatewayService.ForwardCountTokens(b.Context(), b.c, b.account, b.attempt)
	if err == nil {
		return nil
	}
	var original *service.UpstreamFailoverError
	if errors.As(err, &original) {
		return &textflow.AttemptFailure{Cause: err, Policy: original.RetryFailure()}
	}
	return &textflow.AttemptFailure{Cause: err}
}
func (b *countTokensAttemptBridge) ForwardFailed(selected textflow.Selection, err error) {
	b.log.Error("gateway.count_tokens_forward_failed", zap.Int64("account_id", selected.Account.ID), zap.Error(err))
}
func (b *countTokensAttemptBridge) ReleaseSession(_ textflow.Selection) {
	b.h.gatewayService.ReleaseAccountSession(context.Background(), b.account, b.hash)
}
func (b *countTokensAttemptBridge) Canceled() { failoverClientGone(b.c) }
func (b *countTokensAttemptBridge) Exhausted(selected textflow.Selection, last *textflow.AttemptFailure) {
	b.exhausted(last, selected.Account.Platform)
}
func (b *countTokensAttemptBridge) exhausted(last *textflow.AttemptFailure, platform string) {
	var original *service.UpstreamFailoverError
	if last != nil {
		errors.As(last.Cause, &original)
	}
	b.h.handleFailoverExhausted(b.c, original, platform, false)
}
func (b *countTokensAttemptBridge) TempUnscheduleRetryableError(ctx context.Context, id int64, failure *textflow.AttemptFailure) {
	var original *service.UpstreamFailoverError
	if errors.As(failure.Cause, &original) {
		b.h.gatewayService.TempUnscheduleRetryableError(ctx, id, original)
	}
}
