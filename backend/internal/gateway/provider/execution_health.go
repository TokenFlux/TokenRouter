package provider

import (
	"context"
	"net/http"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// ApplyExecutionSchedulingThreshold 只回写本次健康裁决明确拥有的字段，保留执行快照隔离。
func ApplyExecutionSchedulingThreshold(ctx context.Context, observer *accountprovider.UpstreamHealth, value *ExecutionAccount) bool {
	if observer == nil {
		return false
	}
	record := ExecutionRecord(value)
	paused := observer.Core.ApplyAccountSchedulingThreshold(ctx, record)
	if value != nil && record != nil {
		value.Record.TempUnschedulableUntil = record.TempUnschedulableUntil
		value.Record.TempUnschedulableReason = record.TempUnschedulableReason
		value.Record.Extra = record.Extra
	}
	return paused
}

// ObserveExecutionSessionWindow 在空观测时先短路，不触碰可选健康依赖。
func ObserveExecutionSessionWindow(ctx context.Context, observer *accountprovider.UpstreamHealth, value *ExecutionAccount, headers http.Header) {
	observation := accountprovider.SessionWindowObservation(headers)
	if observation.Status == "" {
		return
	}
	observer.Core.UpdateSessionWindow(ctx, ExecutionRecord(value), observation)
}

// ObserveExecutionModelFailure 固化请求模型与端点后交给唯一平台健康观测。
func ObserveExecutionModelFailure(ctx context.Context, observer *accountprovider.UpstreamHealth, value *ExecutionAccount, model string, status int, body []byte) bool {
	if observer == nil {
		return false
	}
	return observer.Models.Observe(ctx, ExecutionRecord(value), model, status, body, requeststate.HealthThinking(ctx), requeststate.OpenAIImagesEndpointFromContext(ctx))
}

// ObserveExecutionTemporaryFailure 保留显式模型与请求状态的优先级。
func ObserveExecutionTemporaryFailure(ctx context.Context, observer *accountprovider.UpstreamHealth, value *ExecutionAccount, status int, body []byte, models ...string) bool {
	return observer.Core.HandleTempUnschedulable(ctx, ExecutionRecord(value), status, body, requeststate.HealthModel(ctx, models))
}

// TryExecutionTemporaryFailure 保留 Antigravity 的认证升级豁免；规则由账号核心执行。
func TryExecutionTemporaryFailure(ctx context.Context, observer *accountprovider.UpstreamHealth, value *ExecutionAccount, status int, body []byte, models ...string) bool {
	return observer.Core.TryTempUnschedulable(ctx, ExecutionRecord(value), status, body, value == nil || value.Record.Platform != capability.PlatformAntigravity, requeststate.HealthModel(ctx, models))
}

// ObserveExecutionSparkLimit 将当次 thinking 投影给模型范围的限流观测。
func ObserveExecutionSparkLimit(ctx context.Context, observer *accountprovider.UpstreamHealth, value *ExecutionAccount, model string, status int, headers http.Header, body []byte) bool {
	if observer == nil {
		return false
	}
	return observer.Models.ObserveSparkRateLimit(ctx, ExecutionRecord(value), model, status, headers, body, requeststate.HealthThinking(ctx))
}

// ApplyDefaultExecutionHealth 保留默认处理的独立记录与原写回范围。
func ApplyDefaultExecutionHealth(ctx context.Context, observer *accountprovider.UpstreamHealth, value *ExecutionAccount, status int, headers http.Header, body []byte, models ...string) bool {
	record := ExecutionRecord(value)
	result := observer.HandleDefault(ctx, record, HealthObservationFromContext(ctx, status, headers, body, models))
	if value != nil && record != nil {
		value.Record.Credentials, value.Record.Extra = record.Credentials, record.Extra
	}
	return result
}

// ObserveExecutionWorkspaceFailure 只传递供应商已识别的工作区停用事实。
func ObserveExecutionWorkspaceFailure(ctx context.Context, observer *accountprovider.UpstreamHealth, value *ExecutionAccount, status int, body []byte) {
	if observer == nil {
		return
	}
	observer.Team.HandleWorkspaceDeactivated(ctx, ExecutionRecord(value), status == http.StatusPaymentRequired && accountprovider.ClientRejectionObservation("", body).WorkspaceDeactivated)
}
