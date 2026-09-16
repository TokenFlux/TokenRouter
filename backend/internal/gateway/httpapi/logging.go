package httpapi

import (
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func RequestLogger(c *gin.Context, component string, fields ...zap.Field) *zap.Logger {
	base := logger.L()
	if c != nil && c.Request != nil {
		base = logger.FromContext(c.Request.Context())
	}

	if component != "" {
		fields = append([]zap.Field{zap.String("component", component)}, fields...)
	}
	return base.With(fields...)
}
