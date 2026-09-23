//go:build unit

package provider_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/stretchr/testify/require"
)

func TestRateLimitService_TempUnschedulableContextPreservesModelForPoolDependency(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)

	account := gatewaytestkit.ModelNotFoundAccount()
	account.Record.Credentials["pool_mode"] = true
	ctx := requeststate.WithHealthModel(context.Background(), []string{"gpt-5.4"})

	// #4496 的池模式分支调用 tryTempUnschedulable 时不会显式传入模型。
	// 请求上下文必须保留规范模型，确保组合后的行为仍限定在模型范围内。
	handled := gatewayprovider.TryExecutionTemporaryFailure(ctx, svc, account, http.StatusNotFound,

		[]byte(`{"error":{"message":"endpoint not found"}}`))

	require.True(t, handled)
	require.Zero(t, repo.TempCalls)
	require.Len(t, repo.ModelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.4", repo.ModelRateLimitCalls[0].Scope)
}

func TestRateLimitService_ModelTempUnschedulableIsolatesSchedulerByModel(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)

	account := gatewaytestkit.ModelNotFoundAccount()
	account.Record.Credentials["model_mapping"] = map[string]any{
		"public-a":   "upstream-a",
		"upstream-a": "upstream-b",
	}

	handled := gatewayprovider.ApplyExecutionHealth(context.Background(), svc, account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`), []string{"upstream-a"})).StopScheduling

	require.True(t, handled)
	require.Len(t, repo.ModelRateLimitCalls, 1)
	call := repo.ModelRateLimitCalls[0]
	require.Equal(t, "upstream-a", call.Scope, "canonical upstream model must not be mapped a second time")

	account.Record.Extra = map[string]any{"model_rate_limits": map[string]any{
		call.Scope: map[string]any{
			"rate_limit_reset_at": call.ResetAt.UTC().Format(time.RFC3339),
		},
	},
	}

	require.False(t, gatewayprovider.ExecutionModelPolicy(account).Schedulable(context.Background(), "public-a"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(account).Schedulable(context.Background(), "gpt-5.6-sol"))
}
