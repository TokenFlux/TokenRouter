package provider

import (
	"errors"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoder_error.go translates Qoder API failures into gateway status, error
// type, and source evidence.
// Qoder error codes distinguish entitlement denial from authentication
// rejection even when both arrive as HTTP 403. Gateway classification retains
// the source status and message, translates ordinary credential failures to
// unauthorized, and lets the agent-limit signal override the transport status
// with rate limiting.
//
// Unrecognized errors return an empty view so another classifier can handle
// them. Recognized API errors retain the vendor body and source status even
// when the client status changes. Agent-limit detection runs after the status
// switch and selects rate_limit_error, including when entitlement or
// authentication handling would otherwise produce a different client response.

// DescribeQoderError extracts a Qoder APIError from the error chain and
// produces gateway status, kind, and source evidence. Unrecognized errors
// return an empty view; entitlement 403 stays forbidden, and agent-limit
// classification overrides status to HTTP 429.
func DescribeQoderError(err error) forward.QoderErrorView {
	if local, ok := errors.AsType[*qoder.RequestError](err); ok {
		return forward.QoderErrorView{Recognized: true, Status: http.StatusBadRequest, Kind: "invalid_request_error", Message: local.Message}
	}
	var apiErr *qoder.APIError
	if !errors.As(err, &apiErr) {
		return forward.QoderErrorView{}
	}

	status := http.StatusBadGateway
	errType := "upstream_error"
	switch apiErr.StatusCode {
	case http.StatusUnauthorized:
		status = http.StatusUnauthorized
	case http.StatusForbidden:
		// Qoder distinguishes model entitlement from other 403 failures by its API
		// error code; retain forbidden for entitlement and use unauthorized otherwise.
		if apiErr.IsEntitlementDenied() {
			status = http.StatusForbidden
		} else {
			status = http.StatusUnauthorized
		}
	case http.StatusTooManyRequests:
		status = http.StatusTooManyRequests
		errType = "rate_limit_error"
	case http.StatusServiceUnavailable:
		status = http.StatusServiceUnavailable
	default:
		if apiErr.StatusCode >= http.StatusInternalServerError {
			status = http.StatusBadGateway
		}
	}
	// Agent-limit codes describe quota even when the source transport status is
	// different, so the gateway reports a rate-limit response for that condition.
	if apiErr.IsAgentLimit() {
		status = http.StatusTooManyRequests
		errType = "rate_limit_error"
	}
	return forward.QoderErrorView{Recognized: true, Status: status, SourceStatus: apiErr.StatusCode, Kind: errType, Message: apiErr.Error(), Body: apiErr.Body}
}

// IsLocalQoderRejection identifies request or capability validation that failed
// before inference. Native Chat and compatible entry points must release their
// capacity but leave the selected provider's scheduling and health samples untouched.
func IsLocalQoderRejection(err error) bool {
	return qoder.IsRequestError(err) || routing.IsModelAttributeError(err)
}
