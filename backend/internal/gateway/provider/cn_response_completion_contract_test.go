//go:build unit

package provider_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestHandle403_OtherCNProviderWithKimiConcurrencyMessageUsesNormalPolicy(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{accountcore.OpenAI403DisableThresholdDefault}}
	blocker := &gatewaytestkit.RuntimeBlockRecorder{}
	service := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{ForbiddenCounter: counter, Block: func(v *accountcore.Record, until time.Time, reason string) {
		blocker.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 405, Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.SetErrorCalls, "non-Kimi CN provider must retain the normal permanent-error policy")
	require.Equal(t, 0, repo.TempCalls)
	require.Empty(t, counter.Counts, "normal CN 403 policy must consume the counter result")
	require.Equal(t, []string{"auth_error"}, blocker.Reasons, "the Kimi-specific runtime block must not apply")
}

func TestHandle403_CNProviderConcurrencyLimitAlwaysUsesTemporaryCooldown(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{accountcore.OpenAI403DisableThresholdDefault}}
	blocker := &gatewaytestkit.RuntimeBlockRecorder{}
	service := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{ForbiddenCounter: counter, Block: func(v *accountcore.Record, until time.Time, reason string) {
		blocker.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 403, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`), nil)).StopScheduling

	require.True(t, shouldDisable, "the request must still fail over to another account")
	require.Equal(t, 0, repo.SetErrorCalls)
	require.Equal(t, 1, repo.TempCalls)
	require.Contains(t, repo.LastTempReason, accountcore.CNConcurrencyLimitReason)
	require.Equal(t, []int64{accountcore.OpenAI403DisableThresholdDefault}, counter.Counts, "transient concurrency 403 must bypass the permanent-error counter")
	require.Len(t, blocker.Accounts, 1)
	require.Equal(t, accountcore.CNConcurrencyLimitReason, blocker.Reasons[0])
	require.True(t, blocker.Until[0].After(time.Now()))
}

func TestHandle403_KimiConcurrencyLimitRepositoryFailureKeepsRuntimeBlock(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{TempErr: errors.New("repository unavailable")}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{accountcore.OpenAI403DisableThresholdDefault}}
	blocker := &gatewaytestkit.RuntimeBlockRecorder{}
	service := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{ForbiddenCounter: counter, Block: func(v *accountcore.Record, until time.Time, reason string) {
		blocker.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 406, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`), nil)).StopScheduling

	require.True(t, shouldDisable, "the current request must fail over even when persistence fails")
	require.Equal(t, 1, repo.TempCalls, "the temporary cooldown should still be persisted when possible")
	require.Equal(t, 0, repo.SetErrorCalls, "persistence failure must not fall back to permanent account error")
	require.Equal(t, []int64{accountcore.OpenAI403DisableThresholdDefault}, counter.Counts, "persistence failure must not enter the permanent-error counter path")
	require.Len(t, blocker.Accounts, 1, "the in-memory runtime block must survive repository failure")
	// 原实体没有时钟依赖；保留全部业务字段和路线比较，函数本身不属于运行阻断数据。
	expectedAccount, observedAccount := *account, *blocker.Accounts[0]
	expectedAccount.Record.Now, observedAccount.Record.Now = nil, nil
	expectedAccount.Record.LoadLocation, observedAccount.Record.LoadLocation = nil, nil
	require.Equal(t, expectedAccount, observedAccount)
	require.Equal(t, accountcore.CNConcurrencyLimitReason, blocker.Reasons[0])
	require.True(t, blocker.Until[0].After(time.Now()))
}

func TestHandle403_CNProviderNearMatchRetainsNormalPermanentErrorPolicy(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{accountcore.OpenAI403DisableThresholdDefault}}
	service := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{ForbiddenCounter: counter}, nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 404, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please contact support."}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.SetErrorCalls, "non-exact 403 must retain existing permission/auth protection")
	require.Equal(t, 0, repo.TempCalls)
}
