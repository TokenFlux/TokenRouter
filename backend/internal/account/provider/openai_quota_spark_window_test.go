package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

// ── stub helpers ─────────────────────────────────────────────────────────────

// stubQuotaAccountRepo 是多账号 AccountRepository stub，实现配额测试需要的读取和 extra 写入。
type stubQuotaAccountRepo struct {
	accounts         map[int64]*accountcore.Record
	extraUpdates     map[int64]map[string]any
	extraUpdateCalls int
	extraUpdateErr   error
}

func (r *stubQuotaAccountRepo) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	acc, ok := r.accounts[id]
	if !ok {
		return nil, fmt.Errorf("account %d not found", id)
	}
	return acc, nil
}

type quotaAccountGetter interface {
	GetByID(context.Context, int64) (*accountcore.Record, error)
}

type stubQuotaAdminService struct {
	repo quotaAccountGetter
}

func (s stubQuotaAdminService) GetAccount(ctx context.Context, id int64) (*accountcore.Record, error) {
	return s.repo.GetByID(ctx, id)
}

func (r *stubQuotaAccountRepo) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	acc, ok := r.accounts[id]
	if !ok {
		return fmt.Errorf("account %d not found", id)
	}
	acc.Credentials = credentials
	return nil
}

func (r *stubQuotaAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	if r.extraUpdateErr != nil {
		return r.extraUpdateErr
	}
	r.extraUpdateCalls++
	if r.extraUpdates == nil {
		r.extraUpdates = make(map[int64]map[string]any)
	}
	r.extraUpdates[id] = updates
	return nil
}

// stubQuotaTokenCache 实现 accountcore.AccessTokenCache，返回预设静态 token。
type stubQuotaTokenCache struct {
	tokens map[string]string
}

func (c *stubQuotaTokenCache) GetAccessToken(_ context.Context, key string) (string, error) {
	if t, ok := c.tokens[key]; ok {
		return t, nil
	}
	return "", errors.New("token not found")
}

func (c *stubQuotaTokenCache) SetAccessToken(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}

func (c *stubQuotaTokenCache) DeleteAccessToken(_ context.Context, _ string) error { return nil }

func (c *stubQuotaTokenCache) AcquireRefreshLock(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

func (c *stubQuotaTokenCache) ReleaseRefreshLock(_ context.Context, _ string) error { return nil }

type stubQuotaHTTPUpstream struct {
	capturedAccountID string
	responseBody      string
	responses         map[string]stubQuotaHTTPResponse
	redirectTarget    *url.URL
}

func (s *stubQuotaHTTPUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (s *stubQuotaHTTPUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	s.capturedAccountID = req.Header.Get("chatgpt-account-id")
	if s.redirectTarget != nil {
		cloned := req.Clone(req.Context())
		target := *req.URL
		target.Scheme = s.redirectTarget.Scheme
		target.Host = s.redirectTarget.Host
		cloned.URL = &target
		cloned.Host = ""
		return http.DefaultClient.Do(cloned)
	}
	if s.responses != nil {
		if response, ok := s.responses[req.URL.Path]; ok {
			status := response.status
			if status == 0 {
				status = http.StatusOK
			}
			return &http.Response{
				StatusCode: status,
				Header:     http.Header{"content-type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(response.body)),
			}, nil
		}
	}
	body := s.responseBody
	if body == "" {
		body = `{}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"content-type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

type stubQuotaHTTPResponse struct {
	status int
	body   string
}

// newQuotaRedirectingUpstream 将配额请求重定向到本地测试服务，同时保留真实请求路径与请求头。
func newQuotaRedirectingUpstream(t *testing.T, srv *httptest.Server) *stubQuotaHTTPUpstream {
	t.Helper()
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return &stubQuotaHTTPUpstream{redirectTarget: target}
}

// ── Part A: buildCodexSparkWindowExtraUpdates ─────────────────────────────────

// ── Part C: ResetCredit 影子拒绝 ───────────────────────────────────────────

// TestResetCreditShadowRejected 验证:
//   - ResetCredit(ctx, shadowID) 返回 ErrSparkShadowResetNotSupported
//   - 不触达上游（HTTPUpstream 为 nil，若调用会先触发配置错误）
func TestResetCreditShadowRejected(t *testing.T) {
	pid := int64(100)
	shadow := &accountcore.Record{
		ID:              200,
		ParentAccountID: &pid,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		QuotaDimension:  accountcore.QuotaDimensionSpark,
	}
	repo := &stubQuotaAccountRepo{
		accounts: map[int64]*accountcore.Record{200: shadow},
	}
	// httpUpstream 故意为 nil：若流程误到上游会先命中配置检查，但这里应先拦截影子重置。
	svc := newQuotaForTest(stubQuotaAdminService{repo: repo}, nil, nil, nil, nil)

	_, err := svc.ResetCredit(context.Background(), 200)
	require.ErrorIs(t, err, accountcore.ErrSparkShadowResetNotSupported,
		"shadow ResetCredit should return ErrSparkShadowResetNotSupported, got: %v", err)
	// 外审 F6:必须是结构化 409(而非裸 error→500)。
	require.Equal(t, http.StatusConflict, s15httpx.ErrorCode(err),
		"shadow ResetCredit 应映射为 409 Conflict 而非 500")
}

func TestResetCreditAgentIdentityUsesAssertionAndRecoversInvalidTaskOnce(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	account := &accountcore.Record{
		ID:       201,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_mode":          accountcore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":   "runtime-reset-recovery",
			"agent_private_key":  base64.StdEncoding.EncodeToString(der),
			"task_id":            "task-reset-old",
			"chatgpt_account_id": "account-reset-recovery",
		},
	}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*accountcore.Record{account.ID: account}}
	resetCalls := 0
	registerCalls := 0
	var assertions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if strings.Contains(r.URL.Path, "/task/register") {
			registerCalls++
			_, _ = w.Write([]byte(`{"task_id":"task-reset-new"}`))
			return
		}
		resetCalls++
		assertions = append(assertions, r.Header.Get("authorization"))
		require.Equal(t, "account-reset-recovery", r.Header.Get("chatgpt-account-id"))
		if resetCalls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_task_id"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":"ok","windows_reset":2}`))
	}))
	defer srv.Close()

	invalidator := &agentIdentityWSInvalidationRecorder{}
	svc := newQuotaForTest(stubQuotaAdminService{repo: repo}, newQuotaRedirectingUpstream(t, srv), nil, nil, nil)
	bindQuotaRepositoryForTest(svc, repo, srv.URL)
	svc.factory.TaskOptions.Invalidate = invalidator.InvalidateAgentIdentityWSConnections

	result, err := svc.ResetCredit(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", result.Code)
	require.Equal(t, 2, result.WindowsReset)
	require.Equal(t, 2, resetCalls)
	require.Equal(t, 1, registerCalls)
	require.Len(t, assertions, 2)
	require.True(t, strings.HasPrefix(assertions[0], "AgentAssertion "))
	require.True(t, strings.HasPrefix(assertions[1], "AgentAssertion "))
	require.NotEqual(t, assertions[0], assertions[1])
	require.Equal(t, "task-reset-new", account.GetCredential("task_id"))
	require.Equal(t, []int64{account.ID}, invalidator.accountIDs)
}

// 验证并发请求已恢复 task 时，重置请求复用新 task 而不重复注册。
func TestResetCreditAgentIdentityReusesConcurrentlyRecoveredTask(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	account := &accountcore.Record{
		ID:       202,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_mode":          accountcore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":   "runtime-reset-concurrent",
			"agent_private_key":  base64.StdEncoding.EncodeToString(der),
			"task_id":            "task-reset-old",
			"chatgpt_account_id": "account-reset-concurrent",
		},
	}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*accountcore.Record{account.ID: account}}
	resetCalls := 0
	registerCalls := 0
	var assertions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if strings.Contains(r.URL.Path, "/task/register") {
			registerCalls++
			_, _ = w.Write([]byte(`{"task_id":"task-reset-unexpected"}`))
			return
		}
		resetCalls++
		assertions = append(assertions, r.Header.Get("authorization"))
		if resetCalls == 1 {
			credentials := querycache.ShallowMap(account.Credentials)
			credentials["task_id"] = "task-reset-concurrent"
			account.Credentials = credentials
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_task_id"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":"ok","windows_reset":1}`))
	}))
	defer srv.Close()

	svc := newQuotaForTest(stubQuotaAdminService{repo: repo}, newQuotaRedirectingUpstream(t, srv), nil, nil, nil)
	bindQuotaRepositoryForTest(svc, repo, srv.URL)
	result, err := svc.ResetCredit(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", result.Code)
	require.Equal(t, 2, resetCalls)
	require.Zero(t, registerCalls)
	require.Equal(t, "task-reset-old", decodeAgentAssertionTask(t, assertions[0]))
	require.Equal(t, "task-reset-concurrent", decodeAgentAssertionTask(t, assertions[1]))
}

// ── Part B: prepareAccount 影子 resolve ──────────────────────────────

// TestPrepareAccountShadowResolve 验证影子账号（200）QueryUsage 时:
//   - 不因 chatgpt_account_id 为空而报错
//   - 准备好的请求上下文使用母账号（100）的 chatgpt_account_id("org-parent123")
//
// 测试策略: 直接调用包内可见的 prepareAccount，注入 stubTokenCache（命中路径）
// 和 stubQuotaAdminService（同时持有影子+母账号），绕开 /wham/usage HTTP 往返。
func TestPrepareAccountShadowResolve(t *testing.T) {
	ctx := context.Background()
	pid := int64(100)

	// 影子账号：无 chatgpt_account_id credentials
	shadow := &accountcore.Record{
		ID:              200,
		ParentAccountID: &pid,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		Status:          billing.StatusActive,
		QuotaDimension:  accountcore.QuotaDimensionSpark,
	}
	// 母账号：有完整 credentials
	parent := &accountcore.Record{
		ID:       100,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"chatgpt_account_id": "org-parent123",
		},
	}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*accountcore.Record{200: shadow, 100: parent}}

	// stubTokenCache 为母账号 cache key 提供 fake token（走缓存命中路径，无需真实刷新）
	tokenCache := &stubQuotaTokenCache{tokens: map[string]string{
		accountcore.OpenAITokenCacheKey(parent): "fake-access-token",
	}}
	tokenProvider := newOpenAITokenSourceForTest(repo, tokenCache, nil)

	upstream := &stubQuotaHTTPUpstream{}
	svc := newQuotaForTest(stubQuotaAdminService{repo: repo}, upstream, tokenProvider, nil, nil)

	accountCtx, err := svc.PrepareAccount(ctx, 200)
	require.NoError(t, err, "shadow resolve should succeed; got error: %v", err)
	require.Equal(t, "org-parent123", accountCtx.Account.GetChatGPTAccountID(),
		"prepareAccount should use parent's chatgpt_account_id after shadow resolve")
}

func TestQueryUsageAgentIdentityUsesAssertionWithoutOAuthToken(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	account := &accountcore.Record{
		ID:       300,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_mode":                  accountcore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":           "runtime-quota",
			"agent_private_key":          base64.StdEncoding.EncodeToString(der),
			"task_id":                    "task-quota",
			"chatgpt_account_id":         "account-quota",
			"chatgpt_account_is_fedramp": true,
		},
	}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*accountcore.Record{account.ID: account}}
	var authorization string
	var accountHeader string
	var fedrampHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("authorization")
		accountHeader = r.Header.Get("chatgpt-account-id")
		fedrampHeader = r.Header.Get("x-openai-fedramp")
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"plan_type":"pro","rate_limit":{"allowed":true}}`))
	}))
	defer srv.Close()
	svc := newQuotaForTest(stubQuotaAdminService{repo: repo}, newQuotaRedirectingUpstream(t, srv), nil, nil, nil)
	bindQuotaRepositoryForTest(svc, repo, srv.URL)
	usage, err := svc.QueryUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.True(t, strings.HasPrefix(authorization, "AgentAssertion "))
	require.Equal(t, "account-quota", accountHeader)
	require.Equal(t, "true", fedrampHeader)
}

func TestQueryUsageAgentIdentityRecoversInvalidTaskOnce(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	account := &accountcore.Record{
		ID:       301,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_mode":          accountcore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":   "runtime-quota-recovery",
			"agent_private_key":  base64.StdEncoding.EncodeToString(der),
			"task_id":            "task-quota-old",
			"chatgpt_account_id": "account-quota-recovery",
		},
	}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*accountcore.Record{account.ID: account}}
	usageCalls := 0
	registerCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if strings.Contains(r.URL.Path, "/task/register") {
			registerCalls++
			_, _ = w.Write([]byte(`{"task_id":"task-quota-new"}`))
			return
		}
		if strings.Contains(r.URL.Path, "rate-limit-reset-credits") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		usageCalls++
		if usageCalls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_task_id"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"plan_type":"pro","rate_limit":{"allowed":true}}`))
	}))
	defer srv.Close()

	invalidator := &agentIdentityWSInvalidationRecorder{}
	svc := newQuotaForTest(stubQuotaAdminService{repo: repo}, newQuotaRedirectingUpstream(t, srv), nil, nil, nil)
	bindQuotaRepositoryForTest(svc, repo, srv.URL)
	svc.factory.TaskOptions.Invalidate = invalidator.InvalidateAgentIdentityWSConnections
	usage, err := svc.QueryUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 2, usageCalls)
	require.Equal(t, 1, registerCalls)
	require.Equal(t, "task-quota-new", account.GetCredential("task_id"))
	require.Equal(t, []int64{account.ID}, invalidator.accountIDs)
}

func TestQueryUsageIncludesResetCreditExpirations_EndToEnd(t *testing.T) {
	ctx := context.Background()
	account := &accountcore.Record{
		ID:       100,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"chatgpt_account_id": "org-parent123",
		},
	}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*accountcore.Record{100: account}}
	tokenCache := &stubQuotaTokenCache{tokens: map[string]string{
		accountcore.OpenAITokenCacheKey(account): "fake-token",
	}}
	tokenProvider := newOpenAITokenSourceForTest(repo, tokenCache, nil)
	upstream := &stubQuotaHTTPUpstream{
		responses: map[string]stubQuotaHTTPResponse{
			"/backend-api/wham/usage": {
				body: `{"rate_limit_reset_credits":{"available_count":2}}`,
			},
			"/backend-api/wham/rate-limit-reset-credits": {
				body: `{"credits":[{"id":"secret-credit-id","expires_at":"2026-07-03T04:05:06Z"},{"expiresAt":"2026-07-04T04:05:06Z"}]}`,
			},
		},
	}

	svc := newQuotaForTest(stubQuotaAdminService{repo: repo}, upstream, tokenProvider, nil, nil)
	usage, err := svc.QueryUsage(ctx, 100)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.NotNil(t, usage.RateLimitResetCredits)
	require.Equal(t, 2, usage.RateLimitResetCredits.AvailableCount)
	require.Equal(t, []openai.OpenAIRateLimitResetCreditDetail{
		{ExpiresAt: "2026-07-03T04:05:06Z"},
		{ExpiresAt: "2026-07-04T04:05:06Z"},
	}, usage.RateLimitResetCredits.Credits)

	encoded, err := json.Marshal(usage)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret-credit-id")
}

func TestQueryUsageResetCreditDetails401NonFatal(t *testing.T) {
	ctx := context.Background()
	account := &accountcore.Record{
		ID:       100,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"chatgpt_account_id": "org-parent123",
		},
	}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*accountcore.Record{100: account}}
	tokenCache := &stubQuotaTokenCache{tokens: map[string]string{
		accountcore.OpenAITokenCacheKey(account): "fake-token",
	}}
	tokenProvider := newOpenAITokenSourceForTest(repo, tokenCache, nil)
	upstream := &stubQuotaHTTPUpstream{
		responses: map[string]stubQuotaHTTPResponse{
			"/backend-api/wham/usage": {
				body: `{"rate_limit_reset_credits":{"available_count":1}}`,
			},
			"/backend-api/wham/rate-limit-reset-credits": {
				status: http.StatusUnauthorized,
				body:   `{"error":"unauthorized","id":"secret-error-id"}`,
			},
		},
	}

	svc := newQuotaForTest(stubQuotaAdminService{repo: repo}, upstream, tokenProvider, nil, nil)
	usage, err := svc.QueryUsage(ctx, 100)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.NotNil(t, usage.RateLimitResetCredits)
	require.Equal(t, 1, usage.RateLimitResetCredits.AvailableCount)
	require.Empty(t, usage.RateLimitResetCredits.Credits)
}

func TestCachePostResetSnapshot(t *testing.T) {
	repo := &stubQuotaAccountRepo{}
	svc := &accountcore.OpenAIQuotaService{Options: accountcore.OpenAIQuotaOptions{SaveExtra: repo.UpdateExtra}}
	credits := &openai.OpenAIRateLimitResetCredits{AvailableCount: 0}
	usage := &openai.OpenAIQuotaUsage{
		RateLimitResetCredits: credits,
		RateLimit: &openai.OpenAIRateLimit{
			PrimaryWindow: &openai.OpenAIRateLimitWindow{
				UsedPercent: 0, LimitWindowSeconds: 5 * 60 * 60, ResetAfterSeconds: 5 * 60 * 60,
			},
			SecondaryWindow: &openai.OpenAIRateLimitWindow{
				UsedPercent: 0, LimitWindowSeconds: 7 * 24 * 60 * 60, ResetAfterSeconds: 7 * 24 * 60 * 60,
			},
		},
	}

	require.NoError(t, svc.CachePostResetSnapshot(context.Background(), 100, usage))
	require.Equal(t, 1, repo.extraUpdateCalls)
	require.Equal(t, credits, repo.extraUpdates[100]["codex_reset_credit_snapshot"])
	require.Equal(t, 0.0, repo.extraUpdates[100]["codex_5h_used_percent"])
	require.Equal(t, 0.0, repo.extraUpdates[100]["codex_7d_used_percent"])
}

// TestResetCreditGetByIDError_FailsClosed 验证守卫「失败关闭」语义：
// 当守卫的 GetByID 发生瞬时错误时，ResetCredit 必须立即返回该错误，
// 不得旁路进入 prepareAccount 的上游准备流程（否则影子账号会借 resolve 路径操作母账号）。
//
// 区分方法：httpUpstream/tokenProvider 留 nil；
//   - 旁路路径：prepareAccount 配置检查先命中，报 "not configured"
//   - 守卫正确关闭：报 "account not found"（来自守卫的 infraerrors）
func TestResetCreditGetByIDError_FailsClosed(t *testing.T) {
	// 空 map：GetByID(200) 返回 "account 200 not found"
	repo := &stubQuotaAccountRepo{accounts: map[int64]*accountcore.Record{}}
	// tokenProvider / httpUpstream 故意为 nil：
	// 若代码泄漏到 prepareAccount，会因配置检查而报 "not configured" 而非 "account not found"。
	svc := newQuotaForTest(stubQuotaAdminService{repo: repo}, nil, nil, nil, nil)

	_, err := svc.ResetCredit(context.Background(), 200)
	require.Error(t, err, "GetByID error must propagate; got nil")
	require.NotContains(t, err.Error(), "not configured",
		"error reached prepareAccount config-check — guard did not fail-closed; got: %v", err)
}

// TestQueryUsageShadowResolve_EndToEnd 验证影子账号的 QueryUsage 能成功拿到响应
// 且 header 由母账号注入。
func TestQueryUsageShadowResolve_EndToEnd(t *testing.T) {
	ctx := context.Background()
	pid := int64(100)

	shadow := &accountcore.Record{
		ID: 200, ParentAccountID: &pid,
		Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, QuotaDimension: accountcore.QuotaDimensionSpark,
	}
	parent := &accountcore.Record{
		ID: 100, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive,
		Credentials: map[string]any{"chatgpt_account_id": "org-e2e-parent"},
	}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*accountcore.Record{200: shadow, 100: parent}}

	tokenCache := &stubQuotaTokenCache{tokens: map[string]string{
		accountcore.OpenAITokenCacheKey(parent): "fake-token-e2e",
	}}
	tokenProvider := newOpenAITokenSourceForTest(repo, tokenCache, nil)

	payload, err := json.Marshal(openai.OpenAIQuotaUsage{})
	require.NoError(t, err)
	upstream := &stubQuotaHTTPUpstream{responseBody: string(payload)}

	svc := newQuotaForTest(stubQuotaAdminService{repo: repo}, upstream, tokenProvider, nil, nil)
	usage, err := svc.QueryUsage(ctx, 200)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, "org-e2e-parent", upstream.capturedAccountID,
		"upstream should receive parent's chatgpt-account-id; got: %s", upstream.capturedAccountID)
}
