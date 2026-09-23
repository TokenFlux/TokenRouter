package provider

import (
	"errors"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// DescribeQoderError 仅投影供应商错误，保留原权限、速率与 agent limit 区别。
func DescribeQoderError(err error) forward.QoderErrorView {
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
	if apiErr.IsAgentLimit() {
		status = http.StatusTooManyRequests
		errType = "rate_limit_error"
	}
	return forward.QoderErrorView{Recognized: true, Status: status, SourceStatus: apiErr.StatusCode, Kind: errType, Message: apiErr.Error(), Body: apiErr.Body}
}
