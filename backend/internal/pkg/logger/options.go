// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package logger

import (
	foundation "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// DefaultContainerLogPath 兼容旧入口，值由唯一实现提供。
const DefaultContainerLogPath = foundation.DefaultContainerLogPath

// InitOptions 保留旧调用方的类型身份；实现归目标包。
type InitOptions = foundation.InitOptions

// OutputOptions 保留旧调用方的类型身份；实现归目标包。
type OutputOptions = foundation.OutputOptions

// RotationOptions 保留旧调用方的类型身份；实现归目标包。
type RotationOptions = foundation.RotationOptions

// SamplingOptions 保留旧调用方的类型身份；实现归目标包。
type SamplingOptions = foundation.SamplingOptions
