//go:build unit

package provider_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestRateLimitService_HandleUpstreamError_OpenAI403FirstHitTempUnschedulable(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{1}}
	blocker := &gatewaytestkit.RuntimeBlockRecorder{}
	service := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{ForbiddenCounter: counter, Block: func(v *accountcore.Record, until time.Time, reason string) {
		blocker.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 301,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth},
	}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"temporary edge rejection"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.SetErrorCalls)
	require.Equal(t, 1, repo.TempCalls)
	require.Contains(t, repo.LastTempReason, "temporary edge rejection")
	require.Contains(t, repo.LastTempReason, "(1/3)")
	require.Len(t, blocker.Accounts, 1)
	require.Equal(t, account.Record.ID, blocker.Accounts[0].Record.ID)
	require.Equal(t, "openai_403_temp", blocker.Reasons[0])
	require.True(t, blocker.Until[0].After(time.Now()))
}

func TestRateLimitService_HandleUpstreamError_OpenAI403ThresholdDisables(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{3}}
	service := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{ForbiddenCounter: counter}, nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 302,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth},
	}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"workspace forbidden by policy"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.SetErrorCalls)
	require.Equal(t, 0, repo.TempCalls)
	require.Contains(t, repo.LastErrorMsg, "workspace forbidden by policy")
	require.Contains(t, repo.LastErrorMsg, "consecutive_403=3/3")
}
