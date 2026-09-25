// Qoder HTTP 只连接受控目标与输出，账号尝试循环由 gateway/text 唯一拥有。
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type qoderCompatibleAttemptBridge struct {
	h             *QoderCompatibleRuntime
	c             *gin.Context
	endpoint      QoderEndpoint
	key           *apikey.APIKey
	subjectID     int64
	hash, model   string
	stream        bool
	streamStarted *bool
	body          []byte
	log           *zap.Logger
	record        func(QoderCompatibleTarget, *forwardcore.MessagesResult)
	partial       func(QoderCompatibleTarget, *forwardcore.MessagesResult, error) bool
	selection     QoderCompatibleSelection
	account       QoderCompatibleTarget
	result        *forwardcore.MessagesResult
	release       func()
	writerSize    int
}

func (b *qoderCompatibleAttemptBridge) Context() context.Context { return b.c.Request.Context() }
func (b *qoderCompatibleAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	selected, err := b.h.options.Execution.Select(b.Context(), b.key.GroupID, b.hash, b.model, excluded, b.subjectID)
	if err != nil {
		return textflow.Selection{}, err
	}
	b.selection = selected
	b.account = selected.Target()
	SetOpsSelectedAccount(b.c, b.account.Snapshot().ID, b.account.Snapshot().Platform)
	return textflow.Selection{Account: b.account.Snapshot()}, nil
}
func (b *qoderCompatibleAttemptBridge) SelectionFailed(err error, pending, first bool, last error) {
	if pending {
		b.RefreshPending(*b.streamStarted)
		return
	}
	if first {
		MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
		if WriteGroupSelectionBusinessError(b.c, err, *b.streamStarted, b.h.options.ReadAccess, b.h.options.Errors.Catalogue, func(status int, kind, message string, started bool) {
			b.h.streamingAwareError(b.c, status, kind, message, started, b.endpoint)
		}) {
			return
		}
		b.h.errorResponse(b.c, http.StatusServiceUnavailable, "api_error", "No available accounts: "+err.Error(), b.endpoint)
		return
	}
	// 原 Qoder 状态不写 LastFailoverErr，因此没有 503 单账号退避分支。
	if b.Context().Err() != nil {
		return
	}
	if b.h.writeQoderFailoverExhaustedError(b.c, b.endpoint, *b.streamStarted, last) {
		return
	}
	b.h.errorResponse(b.c, http.StatusBadGateway, "upstream_error", "All available accounts exhausted", b.endpoint)
}
func (b *qoderCompatibleAttemptBridge) Acquire(retry bool) bool {
	var err error
	if retry {
		// 重试保持原时点：等待期间心跳同样改变当前尝试的字节边界。
		b.writerSize = b.c.Writer.Size()
		b.release, err = b.h.acquireQoderRetryAccountSlot(b.c, qoderTargetSnapshot(b.account), b.selection, b.stream, b.streamStarted)
	} else {
		b.release = b.selection.ReleaseFunc()
		if !b.selection.Acquired() {
			if b.selection.WaitPlan() == nil {
				MarkOpsRoutingCapacityLimited(b.c)
				b.h.errorResponse(b.c, http.StatusServiceUnavailable, "api_error", "No available accounts", b.endpoint)
				return false
			}
			b.release, err = b.h.acquireQoderAccountSlotWithWait(b.c, qoderTargetSnapshot(b.account), b.selection.WaitPlan(), b.stream, b.streamStarted, b.log)
		}
	}
	if err != nil {
		event := "qoder.account_slot_acquire_failed"
		if retry {
			event = "qoder.account_retry_slot_acquire_failed"
		}
		b.log.Warn(event, zap.Int64("account_id", b.account.Snapshot().ID), zap.Error(err))
		b.h.handleConcurrencyError(b.c, err, "account", *b.streamStarted, b.endpoint)
		return false
	}
	b.release = wrapQoderReleaseOnDone(b.Context(), b.release, b.stream)
	if !retry {
		b.writerSize = b.c.Writer.Size()
	}
	return true
}
func (b *qoderCompatibleAttemptBridge) Forward() textflow.QoderCompatibleOutcome {
	var err error
	switch b.endpoint {
	case QoderChat:
		b.result, err = b.account.Forward(b.Context(), b.c, b.body, protocol.ProtocolOpenAIChatCompletions, b.model)
	case QoderResponses:
		b.result, err = b.account.Forward(b.Context(), b.c, b.body, protocol.ProtocolOpenAIResponses, b.model)
	default:
		b.result, err = b.account.Forward(b.Context(), b.c, b.body, protocol.ProtocolAnthropicMessages, b.model)
	}
	if b.release != nil {
		b.release()
		b.release = nil
	}
	b.selection.Report(b.account.Snapshot().ID, err == nil, b.result)
	return textflow.QoderCompatibleOutcome{Err: err, OutputChanged: b.c.Writer.Size() != b.writerSize, Partial: err != nil && b.result != nil, Canceled: err != nil && qoderRequestCanceled(b.Context(), err), CanRefresh: b.h.shouldRefreshQoderAccount(err, false), CanFailover: b.h.options.MaySwitch(err)}
}
func (b *qoderCompatibleAttemptBridge) Refresh() textflow.QoderRefreshResult {
	account, err := b.h.refreshQoderAccount(b.Context(), b.account)
	if err == nil && account != nil {
		b.log.Info("qoder.account_refreshed_after_auth_error", zap.Int64("account_id", b.account.Snapshot().ID))
		b.account = account
		SetOpsSelectedAccount(b.c, account.Snapshot().ID, account.Snapshot().Platform)
		return textflow.QoderRefreshResult{Ready: true}
	}
	if errors.Is(err, accountcore.ErrQoderRefreshInProgress) {
		b.log.Info("qoder.account_refresh_after_auth_error_in_progress", zap.Int64("account_id", b.account.Snapshot().ID))
		return textflow.QoderRefreshResult{Pending: true}
	}
	if err != nil {
		b.log.Warn("qoder.account_refresh_after_auth_error_failed", zap.Int64("account_id", b.account.Snapshot().ID), zap.Error(err))
	}
	return textflow.QoderRefreshResult{}
}
func (b *qoderCompatibleAttemptBridge) RefreshPending(started bool) {
	b.c.Header("Retry-After", "1")
	b.h.streamingAwareError(b.c, http.StatusServiceUnavailable, "upstream_error", "Qoder account refresh is still in progress, please retry shortly", started, b.endpoint)
}
func (b *qoderCompatibleAttemptBridge) Partial(out textflow.QoderCompatibleOutcome) {
	b.partial(b.account, b.result, out.Err)
}
func (b *qoderCompatibleAttemptBridge) Canceled(retry bool, err error) {
	event := "qoder.forward_canceled"
	if retry {
		event = "qoder.retry_forward_canceled"
	}
	b.log.Info(event, zap.Int64("account_id", b.account.Snapshot().ID), zap.Error(err))
}
func (b *qoderCompatibleAttemptBridge) Failure(out textflow.QoderCompatibleOutcome) bool {
	if status, kind, message, ok := b.h.qoderGatewayErrorDetails(b.c, out.Err); ok {
		SetOpsUpstreamError(b.c, b.h.options.Errors.Describe(out.Err).SourceStatus, message, "")
		b.h.streamingAwareError(b.c, status, kind, message, out.OutputChanged, b.endpoint)
		return true
	}
	if out.OutputChanged {
		b.h.streamingAwareError(b.c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, b.endpoint)
		return true
	}
	return false
}
func (b *qoderCompatibleAttemptBridge) Exhausted(err error) {
	b.log.Error("qoder.forward_failed", zap.Int64("account_id", b.account.Snapshot().ID), zap.Error(err))
	b.h.errorResponse(b.c, http.StatusBadGateway, "upstream_error", "Upstream request failed", b.endpoint)
}
func (b *qoderCompatibleAttemptBridge) Success() {
	b.h.bindQoderStickySessions(b.Context(), b.key.GroupID, b.hash, b.account.Snapshot().ID, b.endpoint, b.result, b.log)
	b.record(b.account, b.result)
}
func (b *qoderCompatibleAttemptBridge) Switched() {
	b.selection.Switched()
}

// qoderTargetSnapshot 不向等待逻辑暴露凭据，保留 nil 目标的原拒绝语义。
func qoderTargetSnapshot(target QoderCompatibleTarget) *accountcore.AccountSnapshot {
	if target == nil {
		return nil
	}
	value := target.Snapshot()
	return &value
}
