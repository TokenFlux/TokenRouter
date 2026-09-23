package selection

import (
	"context"
	"maps"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// pickerEngine 仅供选择适配器组合原生评分、计数及反馈，不承接请求重试。
type pickerEngine interface {
	Select(context.Context, schedulercore.PlatformSelectionInput) (*provider.SelectionResult, schedulercore.PlatformDecision, error)
	ReportResult(int64, bool, *int, ...policy.FeedbackConfig)
	ReportSwitch()
	SnapshotMetrics() schedulercore.PlatformMetricsSnapshot
}

// requestRoutingModel 保留未显式提供账号层模型时的客户端模型回退。
func requestRoutingModel(input schedulercore.PlatformSelectionInput) string {
	if model := strings.TrimSpace(input.RoutingModel); model != "" {
		return model
	}
	return input.RequestedModel
}

// cloneSelectionInput 保留原单次选择入口对排除集合的独立副本。
func cloneSelectionInput(input schedulercore.PlatformSelectionInput) schedulercore.PlatformSelectionInput {
	input.ExcludedIDs = maps.Clone(input.ExcludedIDs)
	return input
}
