package provider

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
)

// ApplyGrokExecutionHealth 只投影当前尝试和已解码账号，原有写入仍由账号模块执行。
func ApplyGrokExecutionHealth(ctx context.Context, health *accountprovider.GrokHealth, target *ExecutionAccount, status int, headers http.Header, body []byte, teamModel string, models ...string) account.UpstreamErrorDecision {
	if health == nil || target == nil {
		return account.UpstreamErrorDecision{Policy: account.ErrorPolicyNone}
	}
	return health.ObserveError(ctx, target.View(), accountprovider.GrokHealthInput{
		Observation:     HealthObservationFromContext(ctx, status, headers, body, models),
		Models:          models,
		QuotaModel:      teamModel,
		TeamModel:       teamModel,
		RequestScoped:   IsRequestScopedAccountFailure(target, status, body),
		ServerTransient: IsTransientAccountFailure(status, body),
	})
}
