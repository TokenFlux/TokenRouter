package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"go.uber.org/zap"
)

// WarnReasoningCacheFailure 保留原日志字段，不记录推理正文。
func WarnReasoningCacheFailure(itemID string, err error) {
	logging.L().Warn("openai responses chat fallback: cache reasoning content failed", zap.Error(err), zap.String("item_id", itemID))
}
