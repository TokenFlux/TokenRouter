package httpx

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// ToHTTP converts an error into an HTTP status code and a JSON-serializable body.
//
// The returned body matches the project's apperror.Status shape:
// { code, reason, message, metadata }.
// @project-doc docs/interfaces/http_api.md#response_errors
func ToHTTP(err error) (statusCode int, body apperror.Status) {
	if err == nil {
		return http.StatusOK, apperror.Status{Code: int32(http.StatusOK)}
	}

	appErr := apperror.FromError(err)
	if appErr == nil {
		return http.StatusOK, apperror.Status{Code: int32(http.StatusOK)}
	}

	body = apperror.Status{
		Code:    appErr.Code,
		Reason:  appErr.Reason,
		Message: appErr.Message,
	}
	if appErr.Metadata != nil {
		body.Metadata = make(map[string]string, len(appErr.Metadata))
		for k, v := range appErr.Metadata {
			body.Metadata[k] = v
		}
	}
	return int(appErr.Code), body
}

// ErrorCode 在 HTTP 适配层解释应用类别，保留 nil 和自定义旧状态码。
func ErrorCode(err error) int {
	return int(apperror.CategoryOf(err))
}
