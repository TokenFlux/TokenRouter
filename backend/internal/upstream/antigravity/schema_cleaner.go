package antigravity

import (
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
)

// CleanJSONSchema 委托内部 Gemini schema 方言的纯实现。
func CleanJSONSchema(schema map[string]any) map[string]any { return bridge.CleanJSONSchema(schema) }

// DeepCleanUndefined 委托内部 Gemini schema 方言的纯实现。
func DeepCleanUndefined(value any) { bridge.DeepCleanUndefined(value) }
