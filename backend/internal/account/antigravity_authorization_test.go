// 授权操作的会话消费、隐私顺序和有界停止使用可控供应商端口验证。
package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/stretchr/testify/require"
)

type antigravityAuthFixture struct {
	AntigravityAuthorizationClient
	events      *[]string
	exchangeErr error
	refresh     func(context.Context) (*google.AntigravityTokenResponse, error)
}

func (f *antigravityAuthFixture) ExchangeCode(context.Context, string, string) (*google.AntigravityTokenResponse, error) {
	*f.events = append(*f.events, "exchange")
	if f.exchangeErr != nil {
		return nil, f.exchangeErr
	}
	return &google.AntigravityTokenResponse{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600, TokenType: "Bearer"}, nil
}
func (f *antigravityAuthFixture) RefreshToken(ctx context.Context, _ string) (*google.AntigravityTokenResponse, error) {
	return f.refresh(ctx)
}
func (f *antigravityAuthFixture) GetUserInfo(context.Context, string) (*google.AntigravityUserInfo, error) {
	*f.events = append(*f.events, "user")
	return &google.AntigravityUserInfo{Email: "fixture@example.invalid"}, nil
}
func (f *antigravityAuthFixture) LoadCodeAssist(context.Context, string) (*google.AntigravityLoadCodeAssistResponse, map[string]any, error) {
	*f.events = append(*f.events, "load")
	return &google.AntigravityLoadCodeAssistResponse{CloudAICompanionProject: "project", PaidTier: &google.AntigravityPaidTierInfo{ID: "g1-pro-tier"}}, nil, nil
}
func (f *antigravityAuthFixture) SetUserSettings(context.Context, string) (*google.AntigravitySetUserSettingsResponse, error) {
	*f.events = append(*f.events, "set-privacy")
	return &google.AntigravitySetUserSettingsResponse{UserSettings: map[string]any{}}, nil
}
func (f *antigravityAuthFixture) FetchUserInfo(context.Context, string, string) (*google.AntigravityFetchUserInfoResponse, error) {
	*f.events = append(*f.events, "verify-privacy")
	return &google.AntigravityFetchUserInfoResponse{UserSettings: map[string]any{}}, nil
}
func authFixtureOptions(client AntigravityAuthorizationClient) AntigravityAuthorizationOptions {
	return AntigravityAuthorizationOptions{NewClient: func(string) (AntigravityAuthorizationClient, error) { return client, nil }, ResolveProxy: func(context.Context, int64) (string, bool) { return "", false }, GenerateState: func() (string, error) { return "state", nil }, GenerateCodeVerifier: func() (string, error) { return "verifier", nil }, GenerateSessionID: func() (string, error) { return "session", nil }, GenerateCodeChallenge: func(v string) string { return v }, BuildAuthorizationURL: func(string, string) string { return "https://fixture.invalid/authorize" }, IsConnectionError: func(error) bool { return false }, Printf: func(string, ...any) {}, Warn: func(string, ...any) {}, Info: func(string, ...any) {}}
}

func TestAntigravityAuthorizationConsumptionAndPrivacyOrder(t *testing.T) {
	events := []string{}
	client := &antigravityAuthFixture{events: &events}
	core := NewAntigravityAuthorization(authFixtureOptions(client))
	defer func() { require.NoError(t, core.StopContext(context.Background())) }()
	require.False(t, core.Store.runtimeStarted, "构造不得启动清理")
	generated, err := core.GenerateAuthURL(context.Background(), nil)
	require.NoError(t, err)
	_, err = core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{SessionID: generated.SessionID, State: "wrong", Code: "code"})
	require.Error(t, err)
	require.Empty(t, events)
	client.exchangeErr = errors.New("exchange failed")
	_, err = core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{SessionID: generated.SessionID, State: "state", Code: "code"})
	require.Error(t, err)
	_, exists := core.Store.Get(generated.SessionID)
	require.True(t, exists, "交换失败必须保留原会话")
	events = nil
	client.exchangeErr = nil
	got, err := core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{SessionID: generated.SessionID, State: "state", Code: "code"})
	require.NoError(t, err)
	require.Equal(t, []string{"exchange", "user", "load", "set-privacy", "verify-privacy"}, events)
	require.Equal(t, "Pro", got.PlanType)
	require.Equal(t, "project", got.ProjectID)
	require.Equal(t, AntigravityPrivacySet, got.PrivacyMode)
	require.InDelta(t, time.Now().Add(55*time.Minute).Unix(), got.ExpiresAt, 2)
	_, exists = core.Store.Get(generated.SessionID)
	require.False(t, exists)
	_, err = core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{SessionID: generated.SessionID, State: "state", Code: "code"})
	require.Error(t, err)
}

func TestAntigravityAuthorizationStopWaitsAndReportsBudget(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	client := &antigravityAuthFixture{refresh: func(context.Context) (*google.AntigravityTokenResponse, error) {
		close(entered)
		<-finish
		return nil, errors.New("invalid_grant: fixture")
	}}
	core := NewAntigravityAuthorization(authFixtureOptions(client))
	core.Start()
	core.Start()
	done := make(chan error, 1)
	go func() { _, err := core.RefreshToken(context.Background(), "refresh", ""); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("刷新未进入")
	}
	budget, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := core.StopContext(budget)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = core.GenerateAuthURL(context.Background(), nil)
	require.ErrorContains(t, err, "stopped")
	core.Start()
	require.True(t, core.Store.runtimeStopped)
	close(finish)
	select {
	case err := <-done:
		require.ErrorContains(t, err, "invalid_grant")
	case <-time.After(time.Second):
		t.Fatal("刷新未收尾")
	}
	require.ErrorIs(t, core.StopContext(context.Background()), context.DeadlineExceeded, "重复 Stop 共用第一次结果")
}
