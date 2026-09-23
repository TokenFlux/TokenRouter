package httpapi

import (
	"sync/atomic"

	"go.uber.org/zap"
)

// CompatibilityLogSnapshot 只接收已有计数投影，不拥有第二份粘性统计。
type CompatibilityLogSnapshot struct {
	ReadTotal, ReadHit, DualWrite, MetadataTotal int64
	ReadHitRate                                  float64
}

var compatibilityLogCounter atomic.Uint64

// LogCompatibilityFallback 保留全部文本和计数入口共享的每 1024 次采样节奏。
func LogCompatibilityFallback(log *zap.Logger, read func() CompatibilityLogSnapshot) {
	if log == nil {
		return
	}
	if compatibilityLogCounter.Add(1)%1024 != 0 {
		return
	}
	value := read()
	log.Info("gateway.compatibility_fallback_metrics",
		zap.Int64("session_hash_legacy_read_fallback_total", value.ReadTotal),
		zap.Int64("session_hash_legacy_read_fallback_hit", value.ReadHit),
		zap.Int64("session_hash_legacy_dual_write_total", value.DualWrite),
		zap.Float64("session_hash_legacy_read_hit_rate", value.ReadHitRate),
		zap.Int64("metadata_legacy_fallback_total", value.MetadataTotal),
	)
}
