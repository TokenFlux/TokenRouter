package requeststate

import (
	"context"
	"strings"
)

// FirstHealthModel 保留只取第一个模型和空白规范化的入口语义。
func FirstHealthModel(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

// WithHealthModel 派生本次执行提示；空输入不覆盖已有模型或改变 nil context。
func WithHealthModel(ctx context.Context, models []string) context.Context {
	model := FirstHealthModel(models)
	if model == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return updateHints(ctx, func(value *ExecutionHints) { value.HealthModel = model })
}

// HealthModel 优先采用本次显式模型，缺省时读取当前 attempt 的提示。
func HealthModel(ctx context.Context, models []string) string {
	if model := FirstHealthModel(models); model != "" {
		return model
	}
	if ctx == nil {
		return ""
	}
	return strings.TrimSpace(ExecutionHintsFromContext(ctx).HealthModel)
}

// HealthThinking 返回独立的三态值，未提供与显式 false 保持区别。
func HealthThinking(ctx context.Context) *bool {
	if value, ok := ThinkingEnabledFromContext(ctx); ok {
		return &value
	}
	return nil
}
