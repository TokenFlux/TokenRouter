//go:build unit

package httpapi

import (
	"context"
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
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

func healthyGrokOAuthGatewayTestAccount(id int64, token string) *accountcore.Record {
	return &accountcore.Record{
		ID:          id,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  token,
			"refresh_token": "refresh-token",
			"expires_at":    time.Now().Add(2 * accountcore.GrokTokenRefreshSkew).UTC().Format(time.RFC3339),
			"base_url":      xai.DefaultCLIBaseURL,
		},
	}
}

func TestAccountTestServiceGrokAPIKeyUsesXAIResponses(t *testing.T) {

	account := &accountcore.Record{
		ID:          54,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "xai-test-key",
			"base_url": "https://api.x.ai/v1",
		},
	}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
				"data: {\"type\":\"response.completed\"}\n\n",
		)),
	}}
	svc := &accountprovider.GrokAccountTest{Transport: upstream}
	recorder := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: recorder}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/54/test", nil)

	err := executeGrokProbe(t, svc, c, account, "grok")
	require.NoError(t, err)
	require.Equal(t, "https://api.x.ai/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer xai-test-key", upstream.lastReq.Header.Get("Authorization"))
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}

func TestAccountTestServiceGrokAPIKeyAllowsConfiguredHTTPWhenGlobalPolicyDoes(t *testing.T) {

	account := &accountcore.Record{
		ID:          55,
		Name:        "grok-api-key-http",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "third-party-key",
			"base_url": "http://grok.example.test/v1",
		},
	}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
				"data: {\"type\":\"response.completed\"}\n\n",
		)),
	}}
	svc := &accountprovider.GrokAccountTest{OperatorValidator: (egress.OperatorURLPolicy{AllowInsecureHTTP: true}).Validate, Transport: upstream}
	recorder := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: recorder}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/55/test", nil)

	err := executeGrokProbe(t, svc, c, account, "grok")
	require.NoError(t, err)
	require.Equal(t, "http://grok.example.test/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer third-party-key", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}

func TestAccountTestServiceGrokOAuthPaymentRequiredTemporarilyUnschedulesAccount(t *testing.T) {

	account := healthyGrokOAuthGatewayTestAccount(56, "access-token")
	repo := &grokTestFailureStoreFixture{}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusPaymentRequired,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"code":"personal-team-blocked:spending-limit"}`)),
	}}
	svc := &accountprovider.GrokAccountTest{
		Store:     repo,
		Tokens:    &accountcore.GrokTokenSource{},
		Transport: upstream,
	}
	recorder := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: recorder}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/56/test", nil)
	before := time.Now()

	err := executeGrokProbe(t, svc, c, account, "grok")

	require.Error(t, err)
	require.Zero(t, repo.tempUnschedCalls)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, account.ID, repo.lastRateLimitedID)
	require.WithinDuration(t, before.Add(10*time.Minute), repo.lastRateLimitResetAt, time.Second)
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.Contains(t, recorder.Body.String(), "Grok Responses API returned 402")
}

// 只记录原健康写入字段与次数，不模拟状态规则。
type grokTestFailureStoreFixture struct {
	tempUnschedCalls, rateLimitedCalls int
	lastRateLimitedID                  int64
	lastRateLimitResetAt               time.Time
}

func (f *grokTestFailureStoreFixture) UpdateExtra(context.Context, int64, map[string]any) error {
	return nil
}
func (f *grokTestFailureStoreFixture) SetRateLimited(_ context.Context, id int64, reset time.Time) error {
	f.rateLimitedCalls++
	f.lastRateLimitedID = id
	f.lastRateLimitResetAt = reset
	return nil
}
func (f *grokTestFailureStoreFixture) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	f.tempUnschedCalls++
	return nil
}
func executeGrokProbe(t *testing.T, executor *accountprovider.GrokAccountTest, output *openAIProbeOutput, value *accountcore.Record, model string) error {
	t.Helper()
	run := accountprovider.NewTestRun(output.Request.Context(), output.Request.Header, NewTestEventSink(output.recorder))
	defer run.Cancel()
	return run.Result(executor.Execute(run, value, model))
}
