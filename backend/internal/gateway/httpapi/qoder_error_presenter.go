package httpapi

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeldisplay"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// qoder_error_presenter.go translates Qoder execution failures and applies
// configured client error rules.
// Qoder execution failures carry a stage as well as any recognized upstream
// details. HTTP presentation preserves an existing HTTPFailure, otherwise
// translates billing, queue, slot, selection and refresh-pending stages into
// their client response and retry guidance. Upstream exhaustion can retain the
// last recognized failure.
//
// Configured Qoder rules match the original upstream status and body after
// classification. A match can replace client status and message while
// retaining the original source for Ops, and can suppress monitoring on the
// active request. Unrecognized errors keep generic presentation rather than
// being promoted into a rule-derived upstream failure.

// QoderErrorPresenter maps execution failure stages and classified upstream
// response rules.
type QoderErrorPresenter struct {
	// Rules is optional; Details applies matches only to recognized failures with
	// a positive source status, leaving other classifications at their existing defaults.
	Rules ErrorRuleMatcher
	// Describe is required for Details and upstream/exhausted Failure stages; its
	// source status remains available for diagnostics after a client rule override.
	Describe   func(error) forward.QoderErrorView
	ReadAccess func(*gin.Context) (*apikey.APIKey, bool) // selection business-error authentication reader
	Catalogue  modeldisplay.Catalog                      // selection model-display lookup service
}

// Details returns classified client error details and their recognized flag,
// applying optional Qoder response rules. Callers provide the Describe
// callback; absent rules, unrecognized classification or nonpositive source
// status retain the original classification.
//
// A matched rule begins from upstream source status and category
// upstream_error, then applies configured code and message overrides.
// Passthrough message extraction can replace the classified message when the
// body supplies one; otherwise that message remains. Monitoring suppression
// updates Gin only when a context exists, keeping rule-selected client output
// distinct from the observed upstream status recorded here.
func (p QoderErrorPresenter) Details(c *gin.Context, err error) (int, string, string, bool) {
	value := p.Describe(err)
	status, errType, message, ok := value.Status, value.Kind, value.Message, value.Recognized
	if ok && value.SourceStatus > 0 {
		// Rules can replace the diagnostic below. Record the final message only
		// when an upstream actually supplied the status used for this presentation.
		defer func() { SetOpsUpstreamError(c, value.SourceStatus, message, "") }()
	}
	if !ok || p.Rules == nil || value.SourceStatus <= 0 {
		return status, errType, message, ok
	}

	rule := p.Rules.MatchRule(capability.PlatformQoder, value.SourceStatus, []byte(value.Body))
	if rule == nil {
		return status, errType, message, ok
	}

	// Match against upstream evidence before changing client details; a rule’s
	// source status remains available to callers even when its response code differs.
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

// Failure returns client presentation for a Qoder execution error and its
// stage. An existing HTTPFailure is returned by identity, so callers copy it
// before request-specific mutation; unknown errors receive generic 502
// upstream failure.
//
// Billing and queue/slot stages preserve their dedicated admission messages,
// while refresh-pending supplies retry guidance. Selection stages consult
// business/model errors before generic capacity presentation. Upstream and
// exhausted stages delegate rule-aware classification and record its source
// status for Ops; unrecognized exhaustion uses the providers-exhausted
// message, and the caller still decides JSON versus stream output.
func (p QoderErrorPresenter) Failure(c *gin.Context, err error) *HTTPFailure {
	if direct, ok := errors.AsType[*HTTPFailure](err); ok {
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
	case gateway.FailureUserSlot, gateway.FailureProviderSlot:
		if failure.Stage == gateway.FailureProviderSlot && failure.Cause != nil && failure.Cause.Error() == "no available providers" {
			MarkOpsRoutingCapacityLimited(c)
			result.Status = 503
			result.Type = "api_error"
			result.Message = "No available providers"
			return result
		}
		slot := "user"
		if failure.Stage == gateway.FailureProviderSlot {
			slot = "provider"
		}
		result.Status, result.Type, _, result.Message = ConcurrencyErrorResponse(failure.Cause, slot)
	case gateway.FailureRefreshPending:
		result.Status = 503
		result.Message = "Qoder provider refresh is still in progress, please retry shortly"
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
			result.Message = "No available providers: " + failure.Cause.Error()
		}
	case gateway.FailureUpstream, gateway.FailureExhausted:
		status, kind, message, ok := p.Details(c, failure.Cause)
		if ok {
			result.Status = status
			result.Type = kind
			result.Message = message
		} else if failure.Stage == gateway.FailureExhausted {
			result.Message = "All available providers exhausted"
		}
	}
	return result
}
