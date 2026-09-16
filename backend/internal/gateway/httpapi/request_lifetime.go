package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// requestLifetime 只引用组合根的统一屏障，不创建第二套计数或后台工作。
type requestLifetime struct{ enter func() (func(), error) }

// BindRequestActivity 只在构造图完成、开放 HTTP 之前调用。
func (r *requestLifetime) BindRequestActivity(enter func() (func(), error)) { r.enter = enter }

// beginRequest 保留正常请求顺序；停止后的入口不再进入账号尝试或提交完成任务。
func (r *requestLifetime) beginRequest(c *gin.Context, format string) (func(), bool) {
	if r.enter == nil {
		return func() {}, true
	}
	release, err := r.enter()
	if err == nil {
		return release, true
	}
	const message = "Service is shutting down"
	switch format {
	case "google":
		WriteGoogleError(c, http.StatusServiceUnavailable, message)
	case "anthropic":
		WriteAnthropicError(c, http.StatusServiceUnavailable, "api_error", "", message)
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": message}})
	}
	return nil, false
}
