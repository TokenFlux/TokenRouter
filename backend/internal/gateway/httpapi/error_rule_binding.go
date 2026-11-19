package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// error_rule_binding.go binds administrator error rules to requests and
// applies matched status, message, and monitoring choices.
// Administrator error rules are resolved through a service bound to the HTTP
// request. An unmatched request retains its caller's default response tuple;
// a match chooses upstream or configured values and can mark ordinary Ops
// observation for suppression while leaving classification to earlier execution stages.
//
// Matched rules can retain upstream status or supply a configured code, and
// choose extracted upstream text or a configured message. Missing replacements
// leave their upstream source in use. Matching always returns upstream_error,
// while a skip-monitoring action remains in request context for later ordinary
// error observation, including other matches that do not clear that marker.

// errorPassthroughServiceContextKey stores the shared matcher used by output
// adapters when applying administrator response rules to this HTTP request.
const errorPassthroughServiceContextKey = "error_passthrough_service"

// BindErrorPassthroughService binds the shared error-rule service to this HTTP request.
// Nil context or service is ignored, leaving any previous binding intact.
// The pointer is stored directly, so request output uses the supplied service's
// current matching behavior rather than a copied rule set.
func BindErrorPassthroughService(c *gin.Context, svc *errorpolicy.ErrorPassthroughService) {
	if c == nil || svc == nil {
		return
	}
	c.Set(errorPassthroughServiceContextKey, svc)
}

// BoundErrorPassthroughService reads the error-rule service bound to this request.
// Nil contexts, missing keys, and other dynamic types return nil. The stored
// pointer is returned directly, including a typed nil pointer that leaves
// ApplyErrorPassthroughRule using its caller's defaults.
func BoundErrorPassthroughService(c *gin.Context) *errorpolicy.ErrorPassthroughService {
	if c == nil {
		return nil
	}
	v, ok := c.Get(errorPassthroughServiceContextKey)
	if !ok {
		return nil
	}
	svc, ok := v.(*errorpolicy.ErrorPassthroughService)
	if !ok {
		return nil
	}
	return svc
}

// ApplyErrorPassthroughRule chooses the final response tuple from a bound administrator rule.
// Missing service or no match returns the supplied defaults and false. A match
// returns upstream_error type and true, selecting status and message from
// upstream data unless the rule supplies a configured replacement.
//
// The match decision is separate from provider failure classification. This
// adapter updates only response selection and the request's skip-monitoring flag;
// it does not dispatch another provider attempt. An enabled skip flag is set
// when matched, while a later rule without it leaves any existing flag intact.
// Callers provide already captured upstream bytes and their chosen safe defaults.
func ApplyErrorPassthroughRule(
	c *gin.Context,
	platform string,
	upstreamStatus int,
	responseBody []byte,
	defaultStatus int,
	defaultErrType string,
	defaultErrMsg string,
) (status int, errType string, errMsg string, matched bool) {
	status = defaultStatus
	errType = defaultErrType
	errMsg = defaultErrMsg

	svc := BoundErrorPassthroughService(c)
	if svc == nil {
		return status, errType, errMsg, false
	}

	rule := svc.MatchRule(platform, upstreamStatus, responseBody)
	if rule == nil {
		return status, errType, errMsg, false
	}

	status = upstreamStatus
	if !rule.PassthroughCode && rule.ResponseCode != nil {
		status = *rule.ResponseCode
	}

	errMsg = upstream.ExtractErrorMessage(responseBody)
	if !rule.PassthroughBody && rule.CustomMessage != nil {
		errMsg = *rule.CustomMessage
	}

	// A matching skip action applies to the whole request's ordinary Ops error
	// observation. Set it here before returning the selected client response values.
	if rule.SkipMonitoring {
		MarkOpsSkipMonitoring(c)
	}

	// Matched rules use the upstream_error type, as in failover error responses.
	errType = "upstream_error"
	return status, errType, errMsg, true
}
