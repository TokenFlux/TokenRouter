package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/servertiming"
	"github.com/gin-gonic/gin"
)

const (
	snapshotCacheHeader = "X-Snapshot-Cache"
	usageCacheHeader    = "X-Usage-Stats-Cache"
)

type serverTimingResponseWriter struct {
	gin.ResponseWriter
	context *gin.Context
	once    sync.Once
}

func (w *serverTimingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// ServerTiming 在启用后采集管理端与用户端 Web 页面发出的请求耗时。
func ServerTiming(enabled bool) gin.HandlerFunc {
	if !enabled {
		return func(c *gin.Context) {
			c.Next()
		}
	}
	return func(c *gin.Context) {
		if !shouldCollectServerTiming(c) || c.Request == nil {
			c.Next()
			return
		}

		collector := servertiming.New(time.Now())
		c.Request = c.Request.WithContext(servertiming.WithCollector(c.Request.Context(), collector))
		writer := &serverTimingResponseWriter{
			ResponseWriter: c.Writer,
			context:        c,
		}
		c.Writer = writer
		c.Next()
		writer.finalize()
	}
}

func (w *serverTimingResponseWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *serverTimingResponseWriter) WriteHeaderNow() {
	w.finalize()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *serverTimingResponseWriter) Write(data []byte) (int, error) {
	w.finalize()
	return w.ResponseWriter.Write(data)
}

func (w *serverTimingResponseWriter) WriteString(data string) (int, error) {
	w.finalize()
	return w.ResponseWriter.WriteString(data)
}

func (w *serverTimingResponseWriter) Flush() {
	w.finalize()
	w.ResponseWriter.Flush()
}

func (w *serverTimingResponseWriter) finalize() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		if value := ServerTimingHeaderValue(w.context); value != "" {
			w.ResponseWriter.Header().Set(servertiming.HeaderName, value)
		}
	})
}

// ServerTimingHeaderValue 仅为已授权的 UI 范围返回耗时信息。
// 管理员可以接收已采集的管理端或用户端请求耗时；普通认证用户只能接收 allowlist 内的用户 API 耗时。
// X-User-UI-Request 只标识采集范围，绝不作为授权凭据。
func ServerTimingHeaderValue(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	role, ok := GetUserRoleFromContext(c)
	if !ok || role == "" {
		return ""
	}
	if role != "admin" && !isUserTimingPath(c.Request.URL.Path) {
		return ""
	}
	return servertiming.HeaderValue(c.Request.Context(), time.Now(), responseCacheStatus(c.Writer.Header()))
}

// ServerTimingResponseHeader 构造 WebSocket 升级所需的额外响应头。
func ServerTimingResponseHeader(c *gin.Context) http.Header {
	value := ServerTimingHeaderValue(c)
	if value == "" {
		return nil
	}
	return http.Header{servertiming.HeaderName: []string{value}}
}

func shouldCollectServerTiming(c *gin.Context) bool {
	return isAdminUIRequest(c) || isUserUIRequest(c)
}

func isAdminUIRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	if strings.TrimSpace(c.GetHeader(servertiming.AdminUIHeader)) == "1" {
		return true
	}
	path := strings.TrimSpace(c.Request.URL.Path)
	return path == "/api/v1/admin" || strings.HasPrefix(path, "/api/v1/admin/")
}

func isUserUIRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	if strings.TrimSpace(c.GetHeader(servertiming.UserUIHeader)) == "1" {
		return true
	}
	return isUserTimingPath(c.Request.URL.Path)
}

// isUserTimingPath 判断路径是否为可向认证调用方返回 Server-Timing 的用户 Web API。
func isUserTimingPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	const prefix = "/api/v1"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return false
	}
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}

	switch {
	case rest == "/auth/me",
		rest == "/auth/revoke-all-sessions",
		rest == "/auth/oauth/bind-token":
		return true
	case rest == "/user", strings.HasPrefix(rest, "/user/"):
		return true
	case rest == "/keys", strings.HasPrefix(rest, "/keys/"):
		return true
	case rest == "/data-sharing", strings.HasPrefix(rest, "/data-sharing/"):
		// 公开下载仅依赖签名票据，不属于认证用户 API。
		return rest != "/data-sharing/export/download"
	case rest == "/groups/available", rest == "/groups/rates":
		return true
	case rest == "/channels/available":
		return true
	case rest == "/usage", strings.HasPrefix(rest, "/usage/"):
		return true
	case rest == "/announcements", strings.HasPrefix(rest, "/announcements/"):
		return true
	case rest == "/redeem", strings.HasPrefix(rest, "/redeem/"):
		return true
	case rest == "/subscriptions", strings.HasPrefix(rest, "/subscriptions/"):
		return true
	case rest == "/channel-monitors", strings.HasPrefix(rest, "/channel-monitors/"):
		return true
	case strings.HasPrefix(rest, "/payment/"):
		// 排除公开支付与 webhook 接口。
		if strings.HasPrefix(rest, "/payment/public") || strings.HasPrefix(rest, "/payment/webhook") {
			return false
		}
		return true
	default:
		return false
	}
}

func responseCacheStatus(header http.Header) string {
	for _, name := range []string{snapshotCacheHeader, usageCacheHeader} {
		switch strings.ToLower(strings.TrimSpace(header.Get(name))) {
		case "hit":
			return "hit"
		case "miss":
			return "miss"
		case "bypass":
			return "bypass"
		}
	}
	return "bypass"
}
