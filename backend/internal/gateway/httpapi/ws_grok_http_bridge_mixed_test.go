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

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/require"
)

func TestOpenAIWSHTTPBridgeGrok429PersistsRateLimit(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"45"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}}
	svc := newWSFixture(wsFixtureInputs{accounts: repo, transport: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 68, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Concurrency: 1}}
	before := time.Now()

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), nil, account, "token",
		[]byte(`{"type":"response.create","model":"grok-4.3","input":"hi"}`),
		64, "grok-4.3", "grok-4.3", "", "", "", "cache-id", 1,
		func([]byte) error { return nil },
	)

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, before.Add(45*time.Second), repo.lastRateLimitResetAt, time.Second)
	require.Zero(t, repo.tempUnschedCalls)
	require.True(t, wsFixtureAccountBlocked(svc, account))
}
func TestOpenAIWSHTTPBridgeSSEErrorSideEffectsRunOncePerPlatform(t *testing.T) {

	for _, platform := range []string{capability.PlatformOpenAI, capability.PlatformGrok} {
		t.Run(platform, func(t *testing.T) {
			repo := &grokQuotaAccountRepo{}
			options := &wsFixtureOptions{}
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"code\":\"rate_limit_exceeded\",\"message\":\"limited\"}}\n\n",
				)),
			}}
			svc := newWSFixture(wsFixtureInputs{options: options, accounts: repo, transport: upstream})
			if platform == capability.PlatformOpenAI {
				setWSFixtureHealth(svc, newUpstreamHealthForTest(repo, options, nil, accountcore.HealthOptions{}, nil))

			}
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 70, Platform: platform, Type: capability.AccountTypeOAuth, Concurrency: 1}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			writes := 0

			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
				context.Background(), c, account, "sk-test", payload, len(payload),
				"gpt-5", "gpt-5", "", "", "", "", 1,
				func([]byte) error {
					writes++
					return nil
				},
			)

			require.Nil(t, result)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
			require.Zero(t, writes)
			require.Equal(t, 1, repo.rateLimitedCalls)
		})
	}
}
func TestOpenAIWSHTTPBridgeGrokExhaustedSuccessPersistsRateLimit(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	resetAt := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Second)
	resp := grokMessagesSSECompletedResponse("resp_ws_limited", 0)
	resp.Header.Set("X-Ratelimit-Limit-Requests", "10")
	resp.Header.Set("X-Ratelimit-Remaining-Requests", "0")
	resp.Header.Set("X-Ratelimit-Reset-Requests", fmt.Sprintf("%d", resetAt.Unix()))
	upstream := &auxiliaryHTTPRecorder{resp: resp}
	svc := newWSFixture(wsFixtureInputs{accounts: repo, transport: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 69, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Concurrency: 1}}

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), nil, account, "token",
		[]byte(`{"type":"response.create","model":"grok-4.3","input":"hi"}`),
		64, "grok-4.3", "grok-4.3", "", "", "", "cache-id", 1,
		func([]byte) error { return nil },
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, resetAt, repo.lastRateLimitResetAt, time.Second)
	require.True(t, wsFixtureAccountBlocked(svc, account))
}
func grokMessagesSSECompletedResponse(responseID string, cachedTokens int) *http.Response {
	body := strings.Join([]string{
		fmt.Sprintf(`data: {"type":"response.completed","response":{"id":%q,"object":"response","model":"grok-4.3","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7,"input_tokens_details":{"cached_tokens":%d}}}}`, responseID, cachedTokens),
		"",
		"data: [DONE]",
		"",
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
