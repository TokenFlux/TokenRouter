// Package telemetry 将网关技术观测接到唯一日志后端，不持有业务状态。
package telemetry

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"go.uber.org/zap"
)

func Completion(event completion.Event) {
	fields := make([]zap.Field, 0, len(event.Fields))
	for k, v := range event.Fields {
		fields = append(fields, zap.Any(k, v))
	}
	log := logger.L().With(fields...)
	switch event.Level {
	case "error":
		log.Error(event.Message)
	case "warn":
		log.Warn(event.Message)
	default:
		log.Info(event.Message)
	}
}
func ErrorRules(message string, args ...any) {
	logger.LegacyPrintf("service.error_passthrough", message, args...)
}

// Failover 保留请求关联、日志级别和原字段，只适配输出技术。
func Failover(ctx context.Context, event string, values map[string]any) {
	fields := make([]zap.Field, 0, len(values))
	for k, v := range values {
		fields = append(fields, zap.Any(k, v))
	}
	logger.FromContext(ctx).Warn(event, fields...)
}
