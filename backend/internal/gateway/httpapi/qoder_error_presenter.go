package httpapi

import (
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeldisplay"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/gin-gonic/gin"
)

// QoderErrorPresenter 共用原生错误展示；供应商识别和目录读取由固定端口注入。
type QoderErrorPresenter struct {
	Rules      ErrorRuleMatcher
	Describe   func(error) forward.QoderErrorView
	ReadAccess func(*gin.Context) (*apikey.APIKey, bool)
	Catalogue  modeldisplay.Catalog
}

// Details 保留规则覆盖与监控跳过时机，不改变健康和重试资格。
func (p QoderErrorPresenter) Details(c *gin.Context, err error) (int, string, string, bool) {
	value := p.Describe(err)
	status, errType, message, ok := value.Status, value.Kind, value.Message, value.Recognized
	if !ok || p.Rules == nil || value.SourceStatus <= 0 {
		return status, errType, message, ok
	}

	rule := p.Rules.MatchRule(capability.PlatformQoder, value.SourceStatus, []byte(value.Body))
	if rule == nil {
		return status, errType, message, ok
	}

	status = value.SourceStatus
	if !rule.PassthroughCode && rule.ResponseCode != nil {
		status = *rule.ResponseCode
	}
	errType = "upstream_error"
	if !rule.PassthroughBody && rule.CustomMessage != nil {
		message = *rule.CustomMessage
	} else if extracted := upstream.ExtractErrorMessage([]byte(value.Body)); extracted != "" {
		message = extracted
	}
	if rule.SkipMonitoring && c != nil {
		c.Set(OpsSkipPassthroughKey, true)
	}
	return status, errType, message, true
}

// Failure 映射原 Chat 完整链的失败阶段与 Retry-After。
func (p QoderErrorPresenter) Failure(c *gin.Context, err error) *HTTPFailure {
	var direct *HTTPFailure
	if errors.As(err, &direct) {
		return direct
	}
	result := &HTTPFailure{Status: 502, Type: "upstream_error", Message: "Upstream request failed"}
	var failure *gateway.Failure
	if !errors.As(err, &failure) {
		return result
	}
	switch failure.Stage {
	case gateway.FailureBilling:
		result.Status, result.Type, result.Message, result.RetryAfter = BillingErrorDetails(failure.Cause)
	case gateway.FailureUserQueue:
		result.Status = 429
		result.Type = "rate_limit_error"
		result.Message = "Too many pending requests, please retry later"
	case gateway.FailureUserSlot, gateway.FailureAccountSlot:
		if failure.Stage == gateway.FailureAccountSlot && failure.Cause != nil && failure.Cause.Error() == "no available accounts" {
			MarkOpsRoutingCapacityLimited(c)
			result.Status = 503
			result.Type = "api_error"
			result.Message = "No available accounts"
			return result
		}
		slot := "user"
		if failure.Stage == gateway.FailureAccountSlot {
			slot = "account"
		}
		result.Status, result.Type, _, result.Message = ConcurrencyErrorResponse(failure.Cause, slot)
	case gateway.FailureRefreshPending:
		result.Status = 503
		result.Message = "Qoder account refresh is still in progress, please retry shortly"
		result.RetryAfter = 1
	case gateway.FailureSelection:
		MarkOpsRoutingCapacityLimitedIfNoAvailable(c, failure.Cause)
		handled := WriteGroupSelectionBusinessError(c, failure.Cause, c.Writer.Written(), p.ReadAccess, p.Catalogue, func(status int, kind, message string, _ bool) {
			result.Status = status
			result.Type = kind
			result.Message = message
		})
		if !handled {
			result.Status = 503
			result.Type = "api_error"
			result.Message = "No available accounts: " + failure.Cause.Error()
		}
	case gateway.FailureUpstream, gateway.FailureExhausted:
		status, kind, message, ok := p.Details(c, failure.Cause)
		if ok {
			result.Status = status
			result.Type = kind
			result.Message = message
			SetOpsUpstreamError(c, p.Describe(failure.Cause).SourceStatus, message, "")
		} else if failure.Stage == gateway.FailureExhausted {
			result.Message = "All available accounts exhausted"
		}
	}
	return result
}
