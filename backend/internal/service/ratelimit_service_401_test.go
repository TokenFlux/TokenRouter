//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type rateLimitAccountRepoStub struct {
	mockAccountRepoForGemini
	setErrorCalls          int
	tempCalls              int
	updateCredentialsCalls int
	updateExtraCalls       int
	lastCredentials        map[string]any
	lastExtraUpdates       map[string]any
	lastErrorMsg           string
	lastTempUntil          time.Time
	lastTempReason         string
	lastErrorID            int64
	lastTempID             int64
	tempErr                error
}

func (r *rateLimitAccountRepoStub) SetError(ctx context.Context, id int64, errorMsg string) error {
	r.setErrorCalls++
	r.lastErrorID = id
	r.lastErrorMsg = errorMsg
	return nil
}

func (r *rateLimitAccountRepoStub) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.tempCalls++
	r.lastTempUntil = until
	r.lastTempID = id
	r.lastTempReason = reason
	return r.tempErr
}

func (r *rateLimitAccountRepoStub) UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error {
	r.updateCredentialsCalls++
	r.lastCredentials = querycache.ShallowMap(credentials)
	return nil
}

func (r *rateLimitAccountRepoStub) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	r.updateExtraCalls++
	r.lastExtraUpdates = querycache.ShallowMap(updates)
	return nil
}

type openAI403CounterCacheStub struct {
	counts     []int64
	resetCalls []int64
	err        error
}

func (s *openAI403CounterCacheStub) IncrementOpenAI403Count(_ context.Context, _ int64, _ int) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	if len(s.counts) == 0 {
		return 1, nil
	}
	count := s.counts[0]
	s.counts = s.counts[1:]
	return count, nil
}

func (s *openAI403CounterCacheStub) ResetOpenAI403Count(_ context.Context, accountID int64) error {
	s.resetCalls = append(s.resetCalls, accountID)
	return nil
}

func TestRateLimitService_HandleUpstreamError_OpenAIOAuth403UsesTempUnschedulable(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{1}}
	settingRepo := newMockSettingRepo()
	data, err := json.Marshal(accountcore.OpenAI403CooldownSettings{
		Enabled:                 true,
		CooldownMinutes:         7,
		ErrorOnThresholdEnabled: true,
		ThresholdCount:          accountcore.OpenAI403DisableThresholdDefault,
		ThresholdWindowMinutes:  accountcore.OpenAI403CounterWindowMinutesDefault,
	})
	require.NoError(t, err)
	settingRepo.data[accountcore.SettingKeyOpenAI403CooldownSettings] = string(data)
	service := NewRateLimitService(repo, nil, &config.Config{}, nil)
	service.SetOpenAI403CounterCache(counter)
	service.SetSettingService(newExecutionReadersFixture(settingRepo, &config.Config{}))
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 104,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth},
	}

	before := time.Now()
	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service.UpstreamHealth(), account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"temporary forbidden"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, 1, repo.tempCalls)
	require.WithinDuration(t, before.Add(7*time.Minute), repo.lastTempUntil, 2*time.Second)
	require.Contains(t, repo.lastTempReason, "temporary forbidden")
	require.Contains(t, repo.lastTempReason, "(1/3)")
}

func TestRateLimitService_HandleUpstreamError_OpenAIOAuth403DisabledUsesSetError(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{1}}
	settingRepo := newMockSettingRepo()
	data, err := json.Marshal(accountcore.OpenAI403CooldownSettings{Enabled: false, CooldownMinutes: 7})
	require.NoError(t, err)
	settingRepo.data[accountcore.SettingKeyOpenAI403CooldownSettings] = string(data)
	service := NewRateLimitService(repo, nil, &config.Config{}, nil)
	service.SetOpenAI403CounterCache(counter)
	service.SetSettingService(newExecutionReadersFixture(settingRepo, &config.Config{}))
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 105,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth},
	}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service.UpstreamHealth(), account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"temporary forbidden"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, 0, repo.tempCalls)
	require.Contains(t, repo.lastErrorMsg, "temporary forbidden")
}

func TestRateLimitService_HandleUpstreamError_OpenAIOAuth403WithoutCounterUsesSetError(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 106,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth},
	}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service.UpstreamHealth(), account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"temporary forbidden"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, 0, repo.tempCalls)
	require.Contains(t, repo.lastErrorMsg, "temporary forbidden")
}

func TestRateLimitService_HandleUpstreamError_NonOpenAIOAuth403UsesSetError(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 107,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth},
	}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service.UpstreamHealth(), account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"forbidden"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, 0, repo.tempCalls)
	require.Contains(t, repo.lastErrorMsg, "Access forbidden (403)")
}
