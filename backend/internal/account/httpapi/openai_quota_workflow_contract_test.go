//go:build unit

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIQuotaWorkflowStub struct {
	resetResult *openai.OpenAIQuotaResetResult
	resetErr    error
	queryResult *openai.OpenAIQuotaUsage
	queryErr    error
	cacheErr    error

	resetCalls          int
	queryCalls          int
	cacheCalls          int
	cacheCreditsCalls   int
	cachePostResetCalls int
	queryCtxErr         error
	cacheCtxErr         error
}

func (s *openAIQuotaWorkflowStub) ResetCredit(context.Context, int64) (*openai.OpenAIQuotaResetResult, error) {
	s.resetCalls++
	return s.resetResult, s.resetErr
}

func (s *openAIQuotaWorkflowStub) QueryUsage(ctx context.Context, _ int64) (*openai.OpenAIQuotaUsage, error) {
	s.queryCalls++
	s.queryCtxErr = ctx.Err()
	return s.queryResult, s.queryErr
}

func (s *openAIQuotaWorkflowStub) CacheResetCreditsSnapshot(ctx context.Context, _ int64, _ *openai.OpenAIRateLimitResetCredits) error {
	s.cacheCalls++
	s.cacheCreditsCalls++
	s.cacheCtxErr = ctx.Err()
	return s.cacheErr
}

func (s *openAIQuotaWorkflowStub) CachePostResetSnapshot(ctx context.Context, _ int64, _ *openai.OpenAIQuotaUsage) error {
	s.cacheCalls++
	s.cachePostResetCalls++
	s.cacheCtxErr = ctx.Err()
	return s.cacheErr
}

type openAIAccountStateRecovererStub struct {
	err         error
	calls       int
	accountID   int64
	lastOptions accountcore.AccountRecoveryOptions
	lastCtxErr  error
}

func (s *openAIAccountStateRecovererStub) RecoverAccountState(ctx context.Context, accountID int64, options accountcore.AccountRecoveryOptions) (*accountcore.SuccessfulTestRecovery, error) {
	s.calls++
	s.accountID = accountID
	s.lastOptions = options
	s.lastCtxErr = ctx.Err()
	return &accountcore.SuccessfulTestRecovery{}, s.err
}

type openAIResetAdminServiceStub struct {
	OpenAIAdminOperations
	account *accountcore.Record
	err     error
	calls   int
}

func (s *openAIResetAdminServiceStub) GetAccount(context.Context, int64) (*accountcore.Record, error) {
	s.calls++
	return s.account, s.err
}

type openAIQuotaResetEnvelope struct {
	Data OpenAIQuotaResetResponse `json:"data"`
}

type openAIQuotaRefreshEnvelope struct {
	Data OpenAIQuotaRefreshResponse `json:"data"`
}

func performOpenAIQuotaResetRequest(t *testing.T, handler *OpenAIOAuthHandler, ctx context.Context) (int, openAIQuotaResetEnvelope) {
	t.Helper()

	router := gin.New()
	router.POST("/api/v1/admin/openai/accounts/:id/reset-quota", handler.ResetQuota)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/openai/accounts/42/reset-quota", nil)
	if ctx != nil {
		request = request.WithContext(ctx)
	}
	router.ServeHTTP(recorder, request)

	var envelope openAIQuotaResetEnvelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return recorder.Code, envelope
}

func performOpenAIQuotaRefreshRequest(t *testing.T, handler *OpenAIOAuthHandler) (int, openAIQuotaRefreshEnvelope) {
	t.Helper()

	router := gin.New()
	router.POST("/api/v1/admin/openai/accounts/:id/quota/refresh", handler.RefreshQuota)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/openai/accounts/42/quota/refresh", nil)
	router.ServeHTTP(recorder, request)

	var envelope openAIQuotaRefreshEnvelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return recorder.Code, envelope
}

func successfulOpenAIQuotaWorkflowStub() *openAIQuotaWorkflowStub {
	return &openAIQuotaWorkflowStub{
		resetResult: &openai.OpenAIQuotaResetResult{Code: "success", WindowsReset: 1},
		queryResult: &openai.OpenAIQuotaUsage{
			FetchedAt: 123,
			RateLimitResetCredits: &openai.OpenAIRateLimitResetCredits{
				AvailableCount: 0,
				Credits:        []openai.OpenAIRateLimitResetCreditDetail{},
			},
		},
	}
}

func recoveredOpenAIAccountStub() *openAIResetAdminServiceStub {
	return &openAIResetAdminServiceStub{account: &accountcore.Record{
		ID:          42,
		Name:        "recovered",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: false,
	}}
}

func TestOpenAIResetQuotaRecoversAccountBeforeRefreshingCache(t *testing.T) {
	quota := successfulOpenAIQuotaWorkflowStub()
	recoverer := &openAIAccountStateRecovererStub{}
	adminService := recoveredOpenAIAccountStub()
	handler := &OpenAIOAuthHandler{
		Admin:    adminService,
		Quota:    quota,
		Recovery: recoverer,
	}

	status, envelope := performOpenAIQuotaResetRequest(t, handler, nil)

	require.Equal(t, http.StatusOK, status)
	require.Empty(t, envelope.Data.WarningCode)
	require.True(t, envelope.Data.AccountStateRecovered)
	require.True(t, envelope.Data.CacheRefreshed)
	require.NotNil(t, envelope.Data.Quota)
	require.NotNil(t, envelope.Data.Account)
	require.False(t, envelope.Data.Account.Schedulable, "不得改动人工调度开关")
	require.Equal(t, int64(42), recoverer.accountID)
	require.True(t, recoverer.lastOptions.InvalidateToken)
	require.Equal(t, 1, quota.resetCalls)
	require.Equal(t, 1, quota.queryCalls)
	require.Equal(t, 1, quota.cacheCalls)
	require.Zero(t, quota.cacheCreditsCalls, "重置后应写入完整 usage 快照，不应只写 credits")
	require.Equal(t, 1, quota.cachePostResetCalls)
	require.Equal(t, 1, adminService.calls)
}

func TestOpenAIResetQuotaRecoveryFailureStopsPostProcessing(t *testing.T) {
	quota := successfulOpenAIQuotaWorkflowStub()
	recoverer := &openAIAccountStateRecovererStub{err: errors.New("recovery failed")}
	adminService := recoveredOpenAIAccountStub()
	handler := &OpenAIOAuthHandler{
		Admin:    adminService,
		Quota:    quota,
		Recovery: recoverer,
	}

	status, envelope := performOpenAIQuotaResetRequest(t, handler, nil)

	require.Equal(t, http.StatusOK, status)
	require.Equal(t, OpenAIQuotaResetWarningAccountRecoveryFailed, envelope.Data.WarningCode)
	require.False(t, envelope.Data.AccountStateRecovered)
	require.Zero(t, quota.queryCalls)
	require.Zero(t, quota.cacheCalls)
	require.Zero(t, adminService.calls)
}

func TestOpenAIResetQuotaCacheFailureStillReturnsRecoveredAccount(t *testing.T) {
	quota := successfulOpenAIQuotaWorkflowStub()
	quota.cacheErr = errors.New("cache write failed")
	recoverer := &openAIAccountStateRecovererStub{}
	adminService := recoveredOpenAIAccountStub()
	handler := &OpenAIOAuthHandler{
		Admin:    adminService,
		Quota:    quota,
		Recovery: recoverer,
	}

	status, envelope := performOpenAIQuotaResetRequest(t, handler, nil)

	require.Equal(t, http.StatusOK, status)
	require.Equal(t, OpenAIQuotaResetWarningCacheRefreshFailed, envelope.Data.WarningCode)
	require.True(t, envelope.Data.AccountStateRecovered)
	require.False(t, envelope.Data.CacheRefreshed)
	require.Nil(t, envelope.Data.Quota)
	require.NotNil(t, envelope.Data.Account)
}

func TestOpenAIResetQuotaPostProcessingSurvivesClientCancellation(t *testing.T) {
	quota := successfulOpenAIQuotaWorkflowStub()
	recoverer := &openAIAccountStateRecovererStub{}
	adminService := recoveredOpenAIAccountStub()
	handler := &OpenAIOAuthHandler{
		Admin:    adminService,
		Quota:    quota,
		Recovery: recoverer,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	status, _ := performOpenAIQuotaResetRequest(t, handler, ctx)

	require.Equal(t, http.StatusOK, status)
	require.NoError(t, recoverer.lastCtxErr)
	require.NoError(t, quota.queryCtxErr)
	require.NoError(t, quota.cacheCtxErr)
}

func TestOpenAIRefreshQuotaPersistFailureStillReturnsUsage(t *testing.T) {
	quota := successfulOpenAIQuotaWorkflowStub()
	quota.queryResult = &openai.OpenAIQuotaUsage{
		FetchedAt:             456,
		RateLimitResetCredits: &openai.OpenAIRateLimitResetCredits{AvailableCount: 2},
	}
	quota.cacheErr = errors.New("expiration details unavailable")
	handler := &OpenAIOAuthHandler{
		Admin: &openAIResetAdminServiceStub{},
		Quota: quota,
	}

	status, envelope := performOpenAIQuotaRefreshRequest(t, handler)

	require.Equal(t, http.StatusOK, status)
	require.False(t, envelope.Data.CachePersisted)
	require.Equal(t, int64(456), envelope.Data.FetchedAt)
	require.NotNil(t, envelope.Data.RateLimitResetCredits)
	require.Equal(t, 2, envelope.Data.RateLimitResetCredits.AvailableCount)
	require.Equal(t, 1, quota.cacheCreditsCalls, "手动 refresh 只应更新 credits 快照")
	require.Zero(t, quota.cachePostResetCalls)
}

func TestNewOpenAIOAuthHandlerKeepsNilQuotaCapabilitiesGuarded(t *testing.T) {
	handler := NewOpenAIOAuthHandler(nil, &openAIResetAdminServiceStub{}, nil, nil, OpenAIHTTPOptions{})

	require.Nil(t, handler.Quota)
	require.Nil(t, handler.Recovery)
}
