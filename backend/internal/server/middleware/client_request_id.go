package middleware

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	clientRequestIDHeader   = "X-Client-Request-ID"
	internalRequestIDHeader = "X-Sub2API-Request-ID"
)

// ClientRequestID 为请求生成内部关联 ID，并把调用方 ID 单独保存为 parent_client_request_id。
// 外部 ID 只用于跨服务排障，不能影响结算幂等或内部身份判断。
func ClientRequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil {
			c.Next()
			return
		}

		// 入口时间必须在读取请求体和账号调度之前记录，便于拆分请求体上传与应用内耗时。
		ctx := c.Request.Context()
		if _, ok := ctx.Value(telemetry.RequestStartedAt).(time.Time); !ok {
			ctx = context.WithValue(ctx, telemetry.RequestStartedAt, time.Now())
		}
		ctx = timing.WithHTTPTrace(ctx)

		// 已存在的 context 值只可能来自受信任的内部调用；HTTP Header 永远不能覆盖它。
		internalID, valid := normalizeCorrelationIDFromContext(ctx, telemetry.ClientRequestID)
		if !valid {
			internalID = uuid.NewString()
		}
		parentID, _ := normalizeCorrelationID(c.GetHeader(clientRequestIDHeader))
		ctx = context.WithValue(ctx, telemetry.ClientRequestID, internalID)
		if parentID != "" {
			ctx = context.WithValue(ctx, telemetry.ParentClientRequestID, parentID)
		}
		requestLogger := logging.FromContext(ctx).With(zap.String("client_request_id", internalID))
		if parentID != "" {
			requestLogger = requestLogger.With(zap.String("parent_client_request_id", parentID))
		}
		ctx = logging.IntoContext(ctx, requestLogger)
		c.Request = c.Request.WithContext(ctx)
		// 内部关联头由服务独占；清除调用方伪造值，避免被其它转发路径带到上游。
		c.Request.Header.Del(internalRequestIDHeader)
		// 只将关联 ID 写入响应；不把服务生成的内部 ID 加入上游请求。
		if parentID != "" {
			c.Header(clientRequestIDHeader, parentID)
		} else {
			c.Header(clientRequestIDHeader, internalID)
		}
		// 专用内部头只用于下游诊断响应，不加入出站上游请求。
		c.Header(internalRequestIDHeader, internalID)
		c.Next()
	}
}

func normalizeCorrelationIDFromContext(ctx context.Context, key telemetry.ContextKey) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v, _ := ctx.Value(key).(string)
	return normalizeCorrelationID(v)
}
