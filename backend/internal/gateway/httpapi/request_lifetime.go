package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequestLifetime 引用应用的请求活动屏障。
type RequestLifetime struct{ enter func() (func(), error) }

// BindRequestActivity 只在构造图完成、开放 HTTP 之前调用。
func (r *RequestLifetime) BindRequestActivity(enter func() (func(), error)) { r.enter = enter }

// BeginRequest 登记请求活动，应用停止后拒绝请求。
func (r *RequestLifetime) BeginRequest(c *gin.Context, format string) (func(), bool) {
	if r.enter == nil {
		return func() {}, true
	}
	release, err := r.enter()
	if err == nil {
		return release, true
	}
	const message = "Service is shutting down"
	switch format {
	case "systemone":
		WriteSystemOneError(c, http.StatusServiceUnavailable, "api_error", "", "", message)
	case "google":
		WriteGoogleError(c, http.StatusServiceUnavailable, message)
	case "anthropic":
		WriteAnthropicError(c, http.StatusServiceUnavailable, "api_error", "", message)
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": message}})
	}
	return nil, false
}
