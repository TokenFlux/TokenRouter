package middleware

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Logger 请求日志中间件
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 开始时间
		startTime := time.Now()

		// 请求路径
		path := c.Request.URL.Path

		// 处理请求
		c.Next()

		// 跳过健康检查等高频探针路径的日志
		if path == "/health" || path == "/setup/status" {
			return
		}

		endTime := time.Now()
		latency := endTime.Sub(startTime)

		method := c.Request.Method
		statusCode := c.Writer.Status()
		clientIP := clientip.GetClientIP(c)
		protocol := c.Request.Proto
		accountID, hasAccountID := c.Request.Context().Value(telemetry.AccountID).(int64)
		platform, _ := c.Request.Context().Value(telemetry.Platform).(string)
		model, _ := c.Request.Context().Value(telemetry.Model).(string)
		reason, rejected := GetIngressRejectReason(c)
		if rejected {
			recordIngressReject(c, reason)
			allowed, droppedSummary := globalIngressRejectAccessSampler.allow(endTime)
			if droppedSummary > 0 {
				logging.FromContext(c.Request.Context()).Info("ingress rejection access logs dropped",
					zap.String("component", "http.access"),
					zap.Uint64("dropped_count", droppedSummary),
					zap.Bool(logging.OpsSystemLogSkipField, true),
				)
			}
			if !allowed {
				return
			}
		}

		fields := []zap.Field{
			zap.String("component", "http.access"),
			zap.Int("status_code", statusCode),
			zap.Int64("latency_ms", latency.Milliseconds()),
			zap.String("client_ip", clientIP),
			zap.String("protocol", protocol),
			zap.String("method", method),
			zap.String("path", path),
		}
		if rejected {
			fields = append(fields,
				zap.String("ingress_reject_reason", string(reason)),
				zap.Bool(logging.OpsSystemLogSkipField, true),
			)
		}
		if hasAccountID && accountID > 0 {
			fields = append(fields, zap.Int64("account_id", accountID))
		}
		if platform != "" {
			fields = append(fields, zap.String("platform", platform))
		}
		if model != "" {
			fields = append(fields, zap.String("model", model))
		}
		if c.Request.ContentLength >= 0 {
			fields = append(fields, zap.Int64("request_content_length", c.Request.ContentLength))
		}
		fields = appendRequestStageFields(fields, c)

		l := logging.FromContext(c.Request.Context()).With(fields...)
		l.Info("http request completed", zap.Time("completed_at", endTime))

		if len(c.Errors) > 0 {
			l.Warn("http request contains gin errors", zap.String("errors", c.Errors.String()))
		}
	}
}

// appendRequestStageFields 将请求入口、账号槽位和出站 HTTP 阶段写入访问日志。
// 这些字段只记录时间与计数，不记录请求体、凭据或上游响应内容。
func appendRequestStageFields(fields []zap.Field, c *gin.Context) []zap.Field {
	if c == nil || c.Request == nil {
		return fields
	}
	ctx := c.Request.Context()
	startedAt, _ := ctx.Value(telemetry.RequestStartedAt).(time.Time)
	appendTimestamp := func(key telemetry.ContextKey, name string) {
		at, ok := ctx.Value(key).(time.Time)
		if !ok || startedAt.IsZero() || at.Before(startedAt) {
			return
		}
		fields = append(fields, zap.Int64(name, at.Sub(startedAt).Milliseconds()))
	}
	appendTimestamp(telemetry.AccountSlotAcquiredAt, "account_slot_acquired_ms")
	appendTimestamp(telemetry.FirstSSEDataAt, "upstream_first_sse_data_ms")
	appendTimestamp(telemetry.FirstVisibleOutputAt, "first_visible_output_ms")
	appendTimestamp(telemetry.FirstDownstreamFlushAt, "first_downstream_flush_ms")

	if snapshot, ok := timing.HTTPTraceSnapshotFromContext(ctx); ok {
		if elapsed, valid := timing.HTTPTraceElapsedMs(snapshot, snapshot.GetConnAt); valid {
			fields = append(fields, zap.Int64("upstream_get_conn_ms", elapsed))
		}
		if elapsed, valid := timing.HTTPTraceElapsedMs(snapshot, snapshot.GotConnAt); valid {
			fields = append(fields, zap.Int64("upstream_got_conn_ms", elapsed))
		}
		if elapsed, valid := timing.HTTPTraceElapsedMs(snapshot, snapshot.WroteRequestAt); valid {
			fields = append(fields, zap.Int64("upstream_wrote_request_ms", elapsed))
		}
		if elapsed, valid := timing.HTTPTraceElapsedMs(snapshot, snapshot.GotFirstResponseByteAt); valid {
			fields = append(fields, zap.Int64("upstream_first_response_byte_ms", elapsed))
		}
		if snapshot.GotConnCount > 0 {
			fields = append(fields, zap.Int("upstream_got_conn_count", snapshot.GotConnCount))
		}
		if snapshot.GetConnCount > 0 {
			fields = append(fields, zap.Int("upstream_get_conn_count", snapshot.GetConnCount))
		}
		if snapshot.WroteRequestCount > 0 {
			fields = append(fields, zap.Int("upstream_attempt_count", snapshot.WroteRequestCount))
		}
		if snapshot.FirstResponseByteCount > 0 {
			fields = append(fields, zap.Int("upstream_first_response_byte_count", snapshot.FirstResponseByteCount))
		}
		if snapshot.ConnectionReused {
			fields = append(fields, zap.Bool("upstream_connection_reused", true))
		}
		if snapshot.WroteRequestErrorObserved {
			fields = append(fields, zap.Bool("upstream_wrote_request_error", true))
		}
	}
	return fields
}
