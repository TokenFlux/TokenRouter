package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/stretchr/testify/require"
)

type upstreamUsageAccountRepoStub struct {
	mu       sync.Mutex
	account  *accountcore.Record
	getEvent chan struct{}
}

func (s *upstreamUsageAccountRepoStub) GetByID(_ context.Context, _ int64) (*accountcore.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account == nil {
		return nil, accountcore.ErrAccountNotFound
	}
	if s.getEvent != nil {
		select {
		case s.getEvent <- struct{}{}:
		default:
		}
	}
	copy := *s.account
	return &copy, nil
}

type blockingUpstreamUsageHTTP struct {
	started chan struct{}
	release chan struct{}
	body    string
	once    sync.Once
	calls   atomic.Int32
}

func (s *blockingUpstreamUsageHTTP) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxyURL, accountID, concurrency, nil)
}

func (s *blockingUpstreamUsageHTTP) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	s.calls.Add(1)
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(s.body))}, nil
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

type upstreamUsageHTTPStub struct {
	mu        sync.Mutex
	requests  []*http.Request
	responses []struct {
		status int
		body   string
		err    error
	}
}

func (s *upstreamUsageHTTPStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return s.DoWithTLS(req, "", 0, 0, nil)
}

func (s *upstreamUsageHTTPStub) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req.Clone(req.Context()))
	if len(s.responses) == 0 {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}
	response := s.responses[0]
	s.responses = s.responses[1:]
	if response.err != nil {
		return nil, response.err
	}
	return &http.Response{StatusCode: response.status, Body: io.NopCloser(strings.NewReader(response.body))}, nil
}

func testUpstreamUsageConfig() egress.UsageURLPolicy {
	return egress.UsageURLPolicy{Configured: true, AllowInsecureHTTP: true}
}

func TestCNUpstreamUsageAdaptersPreserveConfiguredHostAndNormalizeResults(t *testing.T) {
	tests := []struct {
		name        string
		account     *accountcore.Record
		response    string
		wantAdapter string
		wantPath    string
		wantQuery   string
		wantAuth    string
		wantOrg     string
		wantProject string
		assert      func(*testing.T, *accountcore.UpstreamUsageQueryResult)
	}{
		{
			name: "Kimi Coding 百分比窗口",
			account: &accountcore.Record{ID: 11, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
				Credentials: map[string]any{"api_key": "kimi-key", "account_mode": accountcore.AccountModeCoding, "base_url": "https://relay.example/coding/v1"}},
			response:    `{"limits":[{"detail":{"limit":100,"remaining":25,"resetTime":"2026-08-24T00:00:00Z"}}],"usage":{"limit":1000,"remaining":800,"resetTime":"2026-08-30T00:00:00Z"}}`,
			wantAdapter: accountcore.UpstreamUsageAdapterKimiCoding,
			wantPath:    "/coding/v1/usages",
			wantAuth:    "Bearer kimi-key",
			assert: func(t *testing.T, result *accountcore.UpstreamUsageQueryResult) {
				require.Equal(t, "PERCENT", result.Unit)
				require.Len(t, result.Limits, 2)
				require.InDelta(t, 75, *result.Limits[0].Used, 1e-9)
			},
		},
		{
			name: "智谱 Coding 裸密钥",
			account: &accountcore.Record{ID: 12, Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
				Credentials: map[string]any{"api_key": "zhipu-key", "account_mode": accountcore.AccountModeCoding, "base_url": "https://relay.example/api/coding/paas/v4"}},
			response:    `{"success":true,"data":{"limits":[{"type":"TOKENS_LIMIT","unit":3,"percentage":32,"nextResetTime":1787529600000}]}}`,
			wantAdapter: accountcore.UpstreamUsageAdapterZhipuCoding,
			wantPath:    "/api/monitor/usage/quota/limit",
			wantQuery:   "",
			wantAuth:    "zhipu-key",
			assert: func(t *testing.T, result *accountcore.UpstreamUsageQueryResult) {
				require.Len(t, result.Limits, 1)
				require.InDelta(t, 32, *result.Limits[0].Used, 1e-9)
			},
		},
		{
			name: "智谱团队 Coding 组织项目",
			account: &accountcore.Record{ID: 121, Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
				Credentials: map[string]any{"api_key": "zhipu-team-key", "account_mode": accountcore.AccountModeCoding, "base_url": "https://relay.example/api/coding/paas/v4", "zhipu_organization": "org-demo", "zhipu_project": "proj-demo"}},
			response:    `{"success":true,"data":{"limits":[{"type":"TOKENS_LIMIT","unit":3,"percentage":12,"nextResetTime":1787529600000}]}}`,
			wantAdapter: accountcore.UpstreamUsageAdapterZhipuCoding,
			wantPath:    "/api/monitor/usage/quota/limit",
			wantQuery:   "2",
			wantAuth:    "zhipu-team-key",
			wantOrg:     "org-demo",
			wantProject: "proj-demo",
			assert: func(t *testing.T, result *accountcore.UpstreamUsageQueryResult) {
				require.Len(t, result.Limits, 1)
				require.InDelta(t, 12, *result.Limits[0].Used, 1e-9)
			},
		},
		{
			name: "Kimi 按量余额",
			account: &accountcore.Record{ID: 13, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
				Credentials: map[string]any{"api_key": "kimi-payg", "base_url": "https://relay.example/v1"}},
			response:    `{"code":0,"data":{"available_balance":"6.25"}}`,
			wantAdapter: accountcore.UpstreamUsageAdapterKimiBalance,
			wantPath:    "/v1/users/me/balance",
			wantAuth:    "Bearer kimi-payg",
			assert: func(t *testing.T, result *accountcore.UpstreamUsageQueryResult) {
				require.Equal(t, "CNY", result.Unit)
				require.InDelta(t, 6.25, *result.Balance.Remaining, 1e-9)
			},
		},
		{
			name: "DeepSeek 多币种余额",
			account: &accountcore.Record{ID: 14, Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
				Credentials: map[string]any{"api_key": "deepseek-key", "base_url": "https://relay.example/anthropic", "api_protocol": accountcore.APIProtocolAnthropic}},
			response:    `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"3.5"},{"currency":"USD","total_balance":"1.25"}]}`,
			wantAdapter: accountcore.UpstreamUsageAdapterDeepseekBalance,
			wantPath:    "/user/balance",
			wantAuth:    "Bearer deepseek-key",
			assert: func(t *testing.T, result *accountcore.UpstreamUsageQueryResult) {
				require.Len(t, result.Balances, 2)
				require.NotNil(t, result.Available)
				require.True(t, *result.Available)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &upstreamUsageAccountRepoStub{account: test.account}
			upstream := &upstreamUsageHTTPStub{responses: []struct {
				status int
				body   string
				err    error
			}{{status: http.StatusOK, body: test.response}}}
			service := newUsageContractService(repo, upstream, testUpstreamUsageConfig())
			result, err := service.QueryAccount(context.Background(), test.account.ID)
			require.NoError(t, err)
			require.Equal(t, test.wantAdapter, result.Adapter)
			require.Len(t, upstream.requests, 1)
			request := upstream.requests[0]
			require.Equal(t, "relay.example", request.URL.Hostname())
			require.Equal(t, test.wantPath, request.URL.Path)
			require.Equal(t, test.wantQuery, request.URL.Query().Get("type"))
			require.Equal(t, test.wantAuth, request.Header.Get("Authorization"))
			require.Equal(t, test.wantOrg, request.Header.Get("bigmodel-organization"))
			require.Equal(t, test.wantProject, request.Header.Get("bigmodel-project"))
			require.True(t, upstreamcore.HTTPUpstreamRedirectsDisabled(request.Context()))
			test.assert(t, result)
		})
	}
}

func TestCNUpstreamUsageUnsupportedPayGDoesNotSendRequest(t *testing.T) {
	account := &accountcore.Record{
		ID: 15, Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive,
		Credentials: map[string]any{"api_key": "zhipu-key", "account_mode": accountcore.AccountModePayG},
	}
	repo := &upstreamUsageAccountRepoStub{account: account}
	upstream := &upstreamUsageHTTPStub{}
	service := newUsageContractService(repo, upstream, testUpstreamUsageConfig())
	_, err := service.QueryAccount(context.Background(), account.ID)
	require.ErrorIs(t, err, accountcore.ErrUpstreamUsageUnsupported)
	require.Empty(t, upstream.requests)
}

func TestZivvUsageQueryUsesVersionedBalanceEndpoint(t *testing.T) {
	account := &accountcore.Record{
		ID: 17, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-zivv", "base_url": "https://zivv.example/"},
		Extra:       map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{"adapter": accountcore.UpstreamUsageAdapterZivv}},
	}
	upstream := &upstreamUsageHTTPStub{responses: []struct {
		status int
		body   string
		err    error
	}{
		{status: http.StatusOK, body: `{"balance":900.49,"currency":"USD","is_available":true,"key_limit":0,"key_used":20646.4,"plan_name":"cc b","total_used":22460.66}`},
	}}
	service := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, upstream, testUpstreamUsageConfig())
	result, err := service.QueryAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, accountcore.UpstreamUsageAdapterZivv, result.Adapter)
	require.Equal(t, 900.49, *result.Balance.Remaining)

	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/user/balance", upstream.requests[0].URL.Path)
	require.Equal(t, "Bearer sk-zivv", upstream.requests[0].Header.Get("Authorization"))
	require.True(t, upstreamcore.HTTPUpstreamRedirectsDisabled(upstream.requests[0].Context()))
}

func TestUpstreamUsageAccountBaseURLUsesPlatformNormalization(t *testing.T) {
	account := &accountcore.Record{
		Platform:    capability.PlatformAntigravity,
		Type:        capability.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://gateway.example/"},
	}
	require.Equal(t, "https://gateway.example/antigravity", UpstreamUsageBaseURL(account))
}

func TestNewAPIUsageQueryUsesTokenQuotaEndpoint(t *testing.T) {
	account := &accountcore.Record{
		ID: 11, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-new-api", "base_url": "https://new-api.example/v1",
			"header_override_enabled": true,
			"header_overrides":        map[string]any{"x-custom": "usage-query", "authorization": "Bearer wrong-key"},
		},
		Extra: map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{"adapter": accountcore.UpstreamUsageAdapterNewAPI}},
	}
	upstream := &upstreamUsageHTTPStub{responses: []struct {
		status int
		body   string
		err    error
	}{
		{status: http.StatusOK, body: `{"success":true,"data":{"quota_display_type":"USD","quota_per_unit":500000}}`},
		{status: http.StatusOK, body: `{"code":true,"message":"ok","data":{"object":"token_usage","name":"Default Token","total_granted":632500000,"total_used":360000,"total_available":632140000,"unlimited_quota":false,"expires_at":1893456000}}`},
		{status: http.StatusOK, body: `{"balance_infos":[{"currency":"USD","total_balance":"1264.28"}]}`},
	}}
	service := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, upstream, testUpstreamUsageConfig())
	result, err := service.QueryAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, 1264.28, *result.Usage.Balance.Remaining)
	require.Equal(t, 1264.28, *result.Usage.Limits[0].Remaining)
	require.Equal(t, "Default Token", result.Usage.Subscription.PlanName)
	require.Equal(t, 1264.28, *result.Usage.Subscription.Remaining)
	require.NotContains(t, string(mustJSONMarshal(t, result)), "sk-new-api")

	upstream.mu.Lock()
	require.Len(t, upstream.requests, 3)
	require.Empty(t, upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "Bearer sk-new-api", upstream.requests[1].Header.Get("Authorization"))
	require.Equal(t, "/api/status", upstream.requests[0].URL.Path)
	require.Equal(t, "/api/usage/token/", upstream.requests[1].URL.Path)
	require.Equal(t, "/user/balance", upstream.requests[2].URL.Path)
	for _, request := range upstream.requests {
		require.Equal(t, "usage-query", anthropic.GetHeaderRaw(request.Header, "x-custom"))
		require.True(t, upstreamcore.HTTPUpstreamRedirectsDisabled(request.Context()))
	}
	upstream.mu.Unlock()
}

func TestNewAPIUsageEndpointMissingIsUnsupported(t *testing.T) {
	account := &accountcore.Record{
		ID: 12, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive,
		Credentials: map[string]any{"api_key": "sk-new-api", "base_url": "https://new-api.example/v1"},
		Extra:       map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{"adapter": accountcore.UpstreamUsageAdapterNewAPI}},
	}
	upstream := &upstreamUsageHTTPStub{responses: []struct {
		status int
		body   string
		err    error
	}{
		{status: http.StatusOK, body: `{"success":true,"data":{"quota_display_type":"USD","quota_per_unit":500000}}`},
		{status: http.StatusNotFound, body: `{}`},
	}}
	service := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, upstream, testUpstreamUsageConfig())
	_, err := service.QueryAccount(context.Background(), account.ID)
	require.ErrorIs(t, err, accountcore.ErrUpstreamUsageUnsupported)
}

// DeepSeek relay 返回结构缺失或数值非法时不能伪造 CNY=0，否则会把上游协议故障
// 误显示成真实余额；合法的零余额仍应保留为成功结果。
func TestDeepSeekBalanceAdapterRejectsMalformedPayloads(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "missing balance_infos", body: `{"is_available":true}`},
		{name: "non array balance_infos", body: `{"balance_infos":{}}`},
		{name: "empty balance_infos", body: `{"balance_infos":[]}`},
		{name: "invalid total_balance", body: `{"balance_infos":[{"currency":"CNY","total_balance":"not-a-number"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := &accountcore.Record{ID: 901, Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
				Credentials: map[string]any{"api_key": "deepseek-key", "base_url": "https://relay.example/anthropic", "api_protocol": accountcore.APIProtocolAnthropic}}
			upstream := &upstreamUsageHTTPStub{responses: []struct {
				status int
				body   string
				err    error
			}{{status: http.StatusOK, body: tc.body}}}
			svc := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, upstream, testUpstreamUsageConfig())
			_, err := svc.QueryAccount(context.Background(), account.ID)
			require.ErrorIs(t, err, accountcore.ErrUpstreamUsageInvalidResponse)
		})
	}
}

func TestDeepSeekBalanceAdapterPreservesValidZeroBalance(t *testing.T) {
	account := &accountcore.Record{ID: 902, Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
		Credentials: map[string]any{"api_key": "deepseek-key", "base_url": "https://relay.example/anthropic", "api_protocol": accountcore.APIProtocolAnthropic}}
	upstream := &upstreamUsageHTTPStub{responses: []struct {
		status int
		body   string
		err    error
	}{{status: http.StatusOK, body: `{"is_available":false,"balance_infos":[{"currency":"CNY","total_balance":"0"}]}`}}}
	svc := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, upstream, testUpstreamUsageConfig())

	result, err := svc.QueryAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	require.NotNil(t, result.Usage.Balance)
	require.Zero(t, *result.Usage.Balance.Remaining)
	require.False(t, *result.Usage.Available)
}

func TestNewAPIUsageContinuesWhenStatusProbeFails(t *testing.T) {
	account := &accountcore.Record{
		ID: 13, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive,
		Credentials: map[string]any{"api_key": "sk-new-api", "base_url": "https://new-api.example/v1"},
		Extra:       map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{"adapter": accountcore.UpstreamUsageAdapterNewAPI}},
	}
	upstream := &upstreamUsageHTTPStub{responses: []struct {
		status int
		body   string
		err    error
	}{
		{status: http.StatusServiceUnavailable, body: `{"success":false}`},
		{status: http.StatusOK, body: `{"code":true,"data":{"object":"token_usage","name":"Token","total_granted":632500000,"total_used":360000,"total_available":632140000,"unlimited_quota":false,"expires_at":0}}`},
		{status: http.StatusOK, body: `{"balance_infos":[{"currency":"USD","total_balance":"1264.28"}]}`},
	}}
	service := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, upstream, testUpstreamUsageConfig())
	result, err := service.QueryAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, 1264.28, *result.Usage.Balance.Remaining)

	upstream.mu.Lock()
	require.Len(t, upstream.requests, 3)
	require.Empty(t, upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "Bearer sk-new-api", upstream.requests[1].Header.Get("Authorization"))
	require.Equal(t, "Bearer sk-new-api", upstream.requests[2].Header.Get("Authorization"))
	upstream.mu.Unlock()
}

func TestNewAPIUsageUsesConfiguredUserWalletToken(t *testing.T) {
	account := &accountcore.Record{
		ID: 14, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-new-api", "base_url": "https://new-api.example/v1",
			accountcore.NewAPIUserAccessTokenCredentialKey: "pat-secret", accountcore.NewAPIUserIDCredentialKey: "42",
		},
		Extra: map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{"adapter": accountcore.UpstreamUsageAdapterNewAPI}},
	}
	upstream := &upstreamUsageHTTPStub{responses: []struct {
		status int
		body   string
		err    error
	}{
		{status: http.StatusOK, body: `{"success":true,"data":{"quota_display_type":"USD","quota_per_unit":500000}}`},
		{status: http.StatusOK, body: `{"code":true,"data":{"object":"token_usage","name":"tf","total_granted":-1,"total_used":1,"total_available":-2,"unlimited_quota":true,"expires_at":0}}`},
		{status: http.StatusOK, body: `{"success":true,"data":{"id":42,"quota":632140000,"used_quota":360000}}`},
	}}
	service := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, upstream, testUpstreamUsageConfig())
	result, err := service.QueryAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, 1264.28, *result.Usage.Balance.Remaining)
	require.True(t, result.Usage.Subscription.Unlimited)

	upstream.mu.Lock()
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "/api/user/self", upstream.requests[2].URL.Path)
	require.Equal(t, "Bearer pat-secret", upstream.requests[2].Header.Get("Authorization"))
	require.Equal(t, "42", upstream.requests[2].Header.Get("New-Api-User"))
	forbidden := string(mustJSONMarshal(t, result))
	require.NotContains(t, forbidden, "pat-secret")
	upstream.mu.Unlock()
}

func TestNewAPIUsageUsesWalletTokenWithoutConfiguredUserID(t *testing.T) {
	account := &accountcore.Record{
		ID: 16, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-new-api", "base_url": "https://new-api.example/v1",
			accountcore.NewAPIUserAccessTokenCredentialKey: "pat-secret",
		},
		Extra: map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{"adapter": accountcore.UpstreamUsageAdapterNewAPI}},
	}
	upstream := &upstreamUsageHTTPStub{responses: []struct {
		status int
		body   string
		err    error
	}{
		{status: http.StatusOK, body: `{"success":true,"data":{"quota_display_type":"USD","quota_per_unit":500000}}`},
		{status: http.StatusOK, body: `{"code":true,"data":{"object":"token_usage","name":"tf","total_granted":-27753,"total_used":2006775843,"total_available":-2006803596,"unlimited_quota":true,"expires_at":0}}`},
		{status: http.StatusOK, body: `{"success":true,"data":{"id":2,"quota":608218554,"used_quota":2006781446}}`},
	}}
	service := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, upstream, testUpstreamUsageConfig())
	result, err := service.QueryAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.InDelta(t, 1216.437108, *result.Usage.Balance.Remaining, 0.000001)
	require.Nil(t, result.Usage.Balance.Used)
	require.Nil(t, result.Usage.Balance.Total)
	require.True(t, result.Usage.Subscription.Unlimited)

	upstream.mu.Lock()
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "/api/user/self", upstream.requests[2].URL.Path)
	require.Equal(t, "Bearer pat-secret", upstream.requests[2].Header.Get("Authorization"))
	require.Empty(t, upstream.requests[2].Header.Get("New-Api-User"))
	upstream.mu.Unlock()
}

func TestNewAPIUsageRequiresWalletInsteadOfTokenQuota(t *testing.T) {
	account := &accountcore.Record{
		ID: 15, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive,
		Credentials: map[string]any{"api_key": "sk-new-api", "base_url": "https://new-api.example/v1"},
		Extra:       map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{"adapter": accountcore.UpstreamUsageAdapterNewAPI}},
	}
	upstream := &upstreamUsageHTTPStub{responses: []struct {
		status int
		body   string
		err    error
	}{
		{status: http.StatusOK, body: `{"success":true,"data":{"quota_display_type":"USD","quota_per_unit":500000}}`},
		{status: http.StatusOK, body: `{"code":true,"data":{"object":"token_usage","name":"tf","total_granted":-1,"total_used":1,"total_available":-2,"unlimited_quota":true,"expires_at":0}}`},
		{status: http.StatusOK, body: `<html>frontend</html>`},
	}}
	service := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, upstream, testUpstreamUsageConfig())
	_, err := service.QueryAccount(context.Background(), account.ID)
	require.ErrorIs(t, err, accountcore.ErrUpstreamUsageWalletUnavailable)
}

func mustJSONMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

func TestUpstreamUsageServiceQueriesAPIKeyWithoutMutatingAccount(t *testing.T) {
	account := &accountcore.Record{
		ID:          7,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "http://usage.example/v1"},
		Extra:       map[string]any{},
		Concurrency: 2,
	}
	original := *account
	upstream := &upstreamUsageHTTPStub{responses: []struct {
		status int
		body   string
		err    error
	}{
		{status: http.StatusOK, body: `{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"payg","remaining":12.5,"balance":12.5}`},
	}}
	repo := &upstreamUsageAccountRepoStub{account: account}
	service := newUsageContractService(repo, upstream, testUpstreamUsageConfig())
	result, err := service.QueryAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, account.ID, result.AccountID)
	require.Equal(t, accountcore.UpstreamUsageAdapterSub2API, result.Adapter)
	require.Equal(t, 12.5, *result.Usage.Balance.Remaining)
	require.Equal(t, original.Credentials, account.Credentials)
	require.Equal(t, original.Extra, account.Extra)
	require.Equal(t, int64(1), service.SnapshotMetrics().Counts[accountcore.UpstreamUsageAdapterSub2API+":success"])

	upstream.mu.Lock()
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/usage", upstream.requests[0].URL.Path)
	require.Equal(t, "Bearer sk-test", upstream.requests[0].Header.Get("Authorization"))
	require.True(t, upstreamcore.HTTPUpstreamRedirectsDisabled(upstream.requests[0].Context()))
	upstream.mu.Unlock()
}

func TestUpstreamUsageServiceRejectsBedrockAndSupportsBatchErrors(t *testing.T) {
	account := &accountcore.Record{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeBedrock, Credentials: map[string]any{"api_key": "x", "base_url": "https://example.com"}}
	repo := &upstreamUsageAccountRepoStub{account: account}
	service := newUsageContractService(repo, &upstreamUsageHTTPStub{}, testUpstreamUsageConfig())
	_, err := service.QueryAccount(context.Background(), 1)
	require.ErrorIs(t, err, accountcore.ErrUpstreamUsageAccountInvalid)

	_, errorsByID, err := service.QueryBatch(context.Background(), []int64{1, 0, -1})
	require.NoError(t, err)
	require.ErrorIs(t, errorsByID[1], accountcore.ErrUpstreamUsageAccountInvalid)
}

func TestUpstreamUsageServiceReportsMissingProxyAsRequestFailure(t *testing.T) {
	proxyID := int64(9)
	account := &accountcore.Record{
		ID: 9, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive,
		ProxyID: &proxyID, Credentials: map[string]any{"api_key": "key", "base_url": "https://example.com"},
	}
	service := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, &upstreamUsageHTTPStub{}, testUpstreamUsageConfig())
	_, err := service.QueryAccount(context.Background(), account.ID)
	require.ErrorIs(t, err, accountcore.ErrUpstreamUsageRequestFailed)
}

func TestUpstreamUsageServiceTimeoutError(t *testing.T) {
	upstream := &upstreamUsageHTTPStub{responses: []struct {
		status int
		body   string
		err    error
	}{{err: context.DeadlineExceeded}}}
	account := &accountcore.Record{ID: 3, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "x", "base_url": "https://example.com"}}
	service := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, upstream, testUpstreamUsageConfig())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := service.QueryAccount(ctx, account.ID)
	require.ErrorIs(t, err, accountcore.ErrUpstreamUsageTimeout)
}

func TestUpstreamUsageServiceRejectsDisabledQueryAndOversizedResponse(t *testing.T) {
	account := &accountcore.Record{
		ID: 4, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive,
		Credentials: map[string]any{"api_key": "key", "base_url": "https://example.com"},
		Extra:       map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{"enabled": false}},
	}
	upstream := &upstreamUsageHTTPStub{}
	service := newUsageContractService(&upstreamUsageAccountRepoStub{account: account}, upstream, testUpstreamUsageConfig())
	_, err := service.QueryAccount(context.Background(), account.ID)
	require.ErrorIs(t, err, accountcore.ErrUpstreamUsageDisabled)
	require.Empty(t, upstream.requests)

	account.Extra = nil
	upstream.responses = append(upstream.responses, struct {
		status int
		body   string
		err    error
	}{status: http.StatusOK, body: strings.Repeat("x", 512*1024+1)})
	_, err = service.QueryAccount(context.Background(), account.ID)
	require.ErrorIs(t, err, accountcore.ErrUpstreamUsageInvalidResponse)
}

func TestUpstreamUsageServiceSingleflightWaitersCancelIndependently(t *testing.T) {
	account := &accountcore.Record{
		ID: 5, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
		Credentials: map[string]any{"api_key": "key", "base_url": "https://example.com"},
	}
	repo := &upstreamUsageAccountRepoStub{account: account, getEvent: make(chan struct{}, 16)}
	upstream := &blockingUpstreamUsageHTTP{
		started: make(chan struct{}),
		release: make(chan struct{}),
		body:    `{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"payg","remaining":3,"balance":3}`,
	}
	service := newUsageContractService(repo, upstream, testUpstreamUsageConfig())
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := service.QueryAccount(firstCtx, account.ID)
		firstErr <- err
	}()
	select {
	case <-upstream.started:
	case <-time.After(time.Second):
		t.Fatal("shared query did not start")
	}
	for len(repo.getEvent) > 0 {
		<-repo.getEvent
	}

	secondResult := make(chan *accountcore.UpstreamUsageQueryResult, 1)
	secondErr := make(chan error, 1)
	go func() {
		result, err := service.QueryAccount(context.Background(), account.ID)
		secondResult <- result
		secondErr <- err
	}()
	select {
	case <-repo.getEvent:
	case <-time.After(time.Second):
		t.Fatal("second waiter did not load its identity snapshot")
	}
	cancelFirst()
	require.ErrorIs(t, <-firstErr, context.Canceled)
	// 等待方完成预读后给它一次调度机会进入 singleflight。
	time.Sleep(10 * time.Millisecond)
	close(upstream.release)
	require.NoError(t, <-secondErr)
	require.NotNil(t, <-secondResult)
	require.Equal(t, int32(1), upstream.calls.Load())
}

func TestUpstreamUsageServiceRejectsIdentityChangeAfterQuery(t *testing.T) {
	account := &accountcore.Record{
		ID: 6, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
		Credentials: map[string]any{"api_key": "key", "base_url": "https://example.com"},
	}
	repo := &upstreamUsageAccountRepoStub{account: account}
	upstream := &blockingUpstreamUsageHTTP{
		started: make(chan struct{}),
		release: make(chan struct{}),
		body:    `{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"payg","remaining":3,"balance":3}`,
	}
	service := newUsageContractService(repo, upstream, testUpstreamUsageConfig())
	resultErr := make(chan error, 1)
	go func() {
		_, err := service.QueryAccount(context.Background(), account.ID)
		resultErr <- err
	}()
	select {
	case <-upstream.started:
	case <-time.After(time.Second):
		t.Fatal("query did not start")
	}
	repo.mu.Lock()
	repo.account.Status = "inactive"
	repo.mu.Unlock()
	close(upstream.release)
	require.ErrorIs(t, <-resultErr, accountcore.ErrUpstreamUsageIdentityChanged)
}

func TestUpstreamUsageServiceTreatsDeletionDuringQueryAsIdentityChange(t *testing.T) {
	account := &accountcore.Record{
		ID: 8, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Concurrency: 1,
		Credentials: map[string]any{"api_key": "key", "base_url": "https://example.com"},
	}
	repo := &upstreamUsageAccountRepoStub{account: account}
	upstream := &blockingUpstreamUsageHTTP{
		started: make(chan struct{}),
		release: make(chan struct{}),
		body:    `{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"payg","remaining":3,"balance":3}`,
	}
	service := newUsageContractService(repo, upstream, testUpstreamUsageConfig())
	resultErr := make(chan error, 1)
	go func() {
		_, err := service.QueryAccount(context.Background(), account.ID)
		resultErr <- err
	}()
	select {
	case <-upstream.started:
	case <-time.After(time.Second):
		t.Fatal("query did not start")
	}
	repo.mu.Lock()
	repo.account = nil
	repo.mu.Unlock()
	close(upstream.release)
	require.ErrorIs(t, <-resultErr, accountcore.ErrUpstreamUsageIdentityChanged)
}
