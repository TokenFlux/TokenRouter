package provider

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
)

// HealthObservationFromContext 在网关边界固化模型、thinking 和端点意图，账号观测不回读业务 context。
func HealthObservationFromContext(ctx context.Context, status int, headers http.Header, body []byte, models []string) accountprovider.HealthObservation {
	input := accountprovider.HealthObservation{Status: status, Headers: headers, Body: body, EffectiveModel: requeststate.HealthModel(ctx, models), ModelProvided: len(models) > 0, Thinking: requeststate.HealthThinking(ctx), ImagesEndpoint: requeststate.OpenAIImagesEndpointFromContext(ctx)}
	if len(models) > 0 {
		input.Model = models[0]
	}
	return input
}

// ApplyExecutionHealth 保留观测使用独立记录、返回后仅回写凭据与附加状态的边界。
func ApplyExecutionHealth(ctx context.Context, observer *accountprovider.UpstreamHealth, target *ExecutionAccount, input accountprovider.HealthObservation) account.UpstreamErrorDecision {
	record := ExecutionRecord(target)
	result := observer.ApplyUpstreamError(ctx, record, input)
	if target != nil && record != nil {
		target.Record.Credentials, target.Record.Extra = record.Credentials, record.Extra
	}
	return result
}

// ExecutionErrorPolicy 只借用同步裁决所需的策略字段，不复制或持有完整账号。
func ExecutionErrorPolicy(value *ExecutionAccount) *account.Record {
	if value == nil {
		return nil
	}
	return &account.Record{ID: value.Record.ID, Platform: value.Record.Platform, Type: value.Record.Type, Credentials: value.Record.Credentials}
}
