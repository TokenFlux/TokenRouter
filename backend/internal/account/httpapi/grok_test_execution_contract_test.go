//go:build unit

package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type grokAccountTestRateLimitRepo struct {
	*grokTestStoreFixture
	rateLimitedCalls int
	resetAt          time.Time
}

func (r *grokAccountTestRateLimitRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.resetAt = resetAt
	return nil
}

func TestAccountTestService_TestAccountConnection_GrokUsesXAIResponses(t *testing.T) {

	account := &accountcore.Record{
		ID:          13,
		Name:        "grok-oauth",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-access-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
			"model_mapping": map[string]any{
				"grok": "grok-latest",
			},
		},
	}
	repo := &grokTestStoreFixture{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
				"data: {\"type\":\"response.completed\"}\n\n",
		)),
	}}
	svc := &accountprovider.GrokAccountTest{
		Store:     repo,
		Tokens:    &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()},
		Transport: upstream,
	}

	rec := httptest.NewRecorder()

	err := executeGrokAccountTest(t, svc, account, rec, "grok", "", accountcore.AccountTestModeDefault)
	require.NoError(t, err)

	require.Equal(t, "https://cli-chat-proxy.grok.com/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer grok-access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, grok.CLIClientVersion, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.Equal(t, "application/json, text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, grok.DefaultResponsesModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "hi", gjson.GetBytes(upstream.lastBody, "input").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_output_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Exists())
	require.NotContains(t, rec.Body.String(), "claude")
	require.Contains(t, rec.Body.String(), `"model":"grok-4.5"`)
	require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
}

func TestAccountTestService_TestAccountConnection_GrokUsesCustomTextPrompt(t *testing.T) {
	account := &accountcore.Record{
		ID:          17,
		Name:        "grok-custom-prompt",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-access-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &grokTestStoreFixture{accountsByID: map[int64]*accountcore.Record{account.ID: account}}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\"}\n\n")),
	}}
	svc := &accountprovider.GrokAccountTest{
		Store:     repo,
		Tokens:    &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()},
		Transport: upstream,
	}
	recorder := httptest.NewRecorder()

	err := executeGrokAccountTest(t, svc, account, recorder, "grok-4.5", "describe this account in one sentence", accountcore.AccountTestModeDefault, accountcore.AccountTestTypeText)
	require.NoError(t, err)
	require.Equal(t, "describe this account in one sentence", gjson.GetBytes(upstream.lastBody, "input").String())
}

func TestAccountTestService_TestAccountConnection_GrokExplicitImageUsesMediaEndpoint(t *testing.T) {
	account := &accountcore.Record{
		ID:          18,
		Name:        "grok-image-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "grok-api-key"},
	}
	repo := &grokTestStoreFixture{accountsByID: map[int64]*accountcore.Record{account.ID: account}}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aGVsbG8=","mime_type":"image/png"}]}`)),
	}}
	svc := &accountprovider.GrokAccountTest{Store: repo, Transport: upstream, OperatorValidator: (egress.OperatorURLPolicy{}).Validate}
	recorder := httptest.NewRecorder()

	err := executeGrokAccountTest(t, svc, account, recorder, "custom-image-alias", "draw a lighthouse", accountcore.AccountTestModeDefault, accountcore.AccountTestTypeImage)
	require.NoError(t, err)
	require.Equal(t, "https://api.x.ai/v1/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer grok-api-key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "custom-image-alias", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "draw a lighthouse", gjson.GetBytes(upstream.lastBody, "prompt").String())
	require.Contains(t, recorder.Body.String(), "data:image/png;base64,aGVsbG8=")
}

func TestAccountTestService_TestAccountConnection_GrokDefaultsEmptyModelTo45(t *testing.T) {

	account := &accountcore.Record{
		ID:          16,
		Name:        "grok-oauth-default-model",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-access-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
			// 空模型必须在账号映射之后才回退默认值，因此不能命中这个显式键。
			"model_mapping": map[string]any{"grok-4.5": "grok-4.3"},
		},
	}
	repo := &grokTestStoreFixture{accountsByID: map[int64]*accountcore.Record{account.ID: account}}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
				"data: {\"type\":\"response.completed\"}\n\n",
		)),
	}}
	svc := &accountprovider.GrokAccountTest{
		Store:     repo,
		Tokens:    &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()},
		Transport: upstream,
	}
	recorder := httptest.NewRecorder()

	err := executeGrokAccountTest(t, svc, account, recorder, "", "", accountcore.AccountTestModeDefault)

	require.NoError(t, err)
	require.Equal(t, grok.DefaultResponsesModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, recorder.Body.String(), `"model":"grok-4.5"`)
}

func TestAccountTestService_Grok429PersistsRateLimitReset(t *testing.T) {

	account := &accountcore.Record{
		ID:          14,
		Name:        "grok-oauth-limited",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-access-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	baseRepo := &grokTestStoreFixture{accountsByID: map[int64]*accountcore.Record{account.ID: account}}
	repo := &grokAccountTestRateLimitRepo{grokTestStoreFixture: baseRepo}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"45"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}}
	svc := &accountprovider.GrokAccountTest{
		Store:     repo,
		Tokens:    &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()},
		Transport: upstream,
	}
	recorder := httptest.NewRecorder()

	err := executeGrokAccountTest(t, svc, account, recorder, "grok", "", accountcore.AccountTestModeDefault)

	require.Error(t, err)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, time.Now().Add(45*time.Second), repo.resetAt, time.Second)
}

func TestAccountTestService_Grok429WithoutQuotaHeadersUsesFallback(t *testing.T) {
	account := &accountcore.Record{
		ID: 15, Name: "grok-oauth-limited-no-headers", Platform: capability.PlatformGrok,
		Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-access-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	baseRepo := &grokTestStoreFixture{accountsByID: map[int64]*accountcore.Record{account.ID: account}}
	repo := &grokAccountTestRateLimitRepo{grokTestStoreFixture: baseRepo}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"quota exhausted"}}`)),
	}}
	svc := &accountprovider.GrokAccountTest{
		Store: repo, Tokens: &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, Transport: upstream,
	}
	recorder := httptest.NewRecorder()
	before := time.Now()

	err := executeGrokAccountTest(t, svc, account, recorder, "grok", "", accountcore.AccountTestModeDefault)

	require.Error(t, err)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, before.Add(2*time.Minute), repo.resetAt, time.Second)
}

// 夹具只记录存储写入和真实平台请求，不复制账号测试算法。
type grokTestStoreFixture struct{ accountsByID map[int64]*accountcore.Record }

func (s *grokTestStoreFixture) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	value, ok := s.accountsByID[id]
	if !ok {
		return nil, fmt.Errorf("account %d missing", id)
	}
	return accountcore.CloneRecord(value), nil
}
func (*grokTestStoreFixture) UpdateExtra(context.Context, int64, map[string]any) error { return nil }
func (*grokTestStoreFixture) SetRateLimited(context.Context, int64, time.Time) error   { return nil }
func (*grokTestStoreFixture) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	return nil
}

type grokTestTransportFixture struct {
	resp     *http.Response
	lastReq  *http.Request
	lastBody []byte
}

func (u *grokTestTransportFixture) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.lastReq = req
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.lastBody = body
	return u.resp, nil
}

type grokTestTargetLoader struct{ target accountcore.TestTarget }

func (l grokTestTargetLoader) LoadTestTarget(context.Context, accountcore.TestRequest) (accountcore.TestTarget, error) {
	return l.target, nil
}
func executeGrokAccountTest(t *testing.T, executor *accountprovider.GrokAccountTest, value *accountcore.Record, recorder *httptest.ResponseRecorder, model, prompt, mode string, types ...string) error {
	t.Helper()
	core := accountcore.NewTestService(grokTestTargetLoader{target: executor.Target(value)}, accountcore.TestOptions{Now: time.Now, Error: func(string) {}, WriteError: func(error) {}})
	request := accountcore.TestRequest{AccountID: value.ID, Model: model, Prompt: prompt, Mode: mode}
	if len(types) > 0 {
		request.Type = &types[0]
	}
	return core.Test(t.Context(), request, NewTestEventSink(recorder))
}
