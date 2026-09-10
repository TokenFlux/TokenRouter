// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package logger

import (
	context "context"

	foundation "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	zap "go.uber.org/zap"
)

// Level 保留旧调用方的类型身份；实现归目标包。
type Level = foundation.Level

// LevelDebug 兼容旧入口，值由唯一实现提供。
const LevelDebug = foundation.LevelDebug

// LevelInfo 兼容旧入口，值由唯一实现提供。
const LevelInfo = foundation.LevelInfo

// LevelWarn 兼容旧入口，值由唯一实现提供。
const LevelWarn = foundation.LevelWarn

// LevelError 兼容旧入口，值由唯一实现提供。
const LevelError = foundation.LevelError

// LevelFatal 兼容旧入口，值由唯一实现提供。
const LevelFatal = foundation.LevelFatal

// OpsSystemLogSkipField 兼容旧入口，值由唯一实现提供。
const OpsSystemLogSkipField = foundation.OpsSystemLogSkipField

// Sink 保留旧调用方的类型身份；实现归目标包。
type Sink = foundation.Sink

// LogEvent 保留旧调用方的类型身份；实现归目标包。
type LogEvent = foundation.LogEvent

// InitBootstrap 兼容旧入口；仅转发到目标实现。
func InitBootstrap() {
	foundation.InitBootstrap()
}

// Init 兼容旧入口；仅转发到目标实现。
func Init(options InitOptions) error {
	return foundation.Init(options)
}

// Reconfigure 兼容旧入口；仅转发到目标实现。
func Reconfigure(mutator func(*InitOptions) error) error {
	return foundation.Reconfigure(mutator)
}

// SetLevel 兼容旧入口；仅转发到目标实现。
func SetLevel(level string) error {
	return foundation.SetLevel(level)
}

// CurrentLevel 兼容旧入口；仅转发到目标实现。
func CurrentLevel() string {
	return foundation.CurrentLevel()
}

// SetSink 兼容旧入口；仅转发到目标实现。
func SetSink(sink Sink) {
	foundation.SetSink(sink)
}

// WriteSinkEvent 兼容旧入口；仅转发到目标实现。
func WriteSinkEvent(level, component, message string, fields map[string]any) {
	foundation.WriteSinkEvent(level, component, message, fields)
}

// L 兼容旧入口；仅转发到目标实现。
func L() *zap.Logger {
	return foundation.L()
}

// S 兼容旧入口；仅转发到目标实现。
func S() *zap.SugaredLogger {
	return foundation.S()
}

// With 兼容旧入口；仅转发到目标实现。
func With(fields ...zap.Field) *zap.Logger {
	return foundation.With(fields...)
}

// Sync 兼容旧入口；仅转发到目标实现。
func Sync() {
	foundation.Sync()
}

// LegacyPrintf 直接引用唯一实现，避免额外栈帧改变日志 caller。
var LegacyPrintf = foundation.LegacyPrintf

// IntoContext 兼容旧入口；仅转发到目标实现。
func IntoContext(ctx context.Context, l *zap.Logger) context.Context {
	return foundation.IntoContext(ctx, l)
}

// FromContext 兼容旧入口；仅转发到目标实现。
func FromContext(ctx context.Context) *zap.Logger {
	return foundation.FromContext(ctx)
}
