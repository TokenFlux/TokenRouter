package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestOpenAIRequestBodyLimitFailover_CompatAndWSBridgeKeepAccountFailover 验证
// Chat Completions 共用错误管线和 WS HTTP Bridge 不会把账号代理 413 当成请求级拒绝。
func TestOpenAIRequestBodyLimitFailover_CompatAndWSBridgeKeepAccountFailover(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(nil))

	const upstreamMessage = "request body exceeds this account's proxy limit"
	upstreamBody := []byte(`{"error":{"message":"` + upstreamMessage + `"}}`)
	resp := &http.Response{
		StatusCode: http.StatusRequestEntityTooLarge,
		Header:     http.Header{"X-Request-Id": []string{"rid-compat-413"}},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 163, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	svc := textFailureFixture(nil, false)

	failoverErr := svc.httpFailover(
		context.Background(), c, account, resp, upstreamBody, upstreamMessage, "gpt-5.2",
	)

	require.NotNil(t, failoverErr)
	require.Equal(t, forwardcore.GatewayFailureScopeAccount, failoverErr.Scope)
	require.Equal(t, forwardcore.NextAccountRetry, failoverErr.NextAccountAction)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.False(t, gatewayprovider.OpenAIWSHTTPBridgeRequestScopedError(
		account, http.StatusRequestEntityTooLarge, upstreamMessage, upstreamBody,
	))
}
func TestFailoverOpenAIUpstreamHTTPError_NilContextSkipsTempUnschedulablePolicy(t *testing.T) {
	repo := &textFailureStore{}
	svc := textFailureFixture(repo, true)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5099, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{map[string]any{
				"error_code":       float64(http.StatusBadRequest),
				"keywords":         []any{"custom temporary outage"},
				"duration_minutes": float64(1),
			}},
		}},
	}
	body := []byte(`{"error":{"message":"Custom temporary outage."}}`)
	resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}}

	got := svc.httpFailover(
		context.Background(), nil, account, resp, body,
		"Custom temporary outage.", "gpt-5.4",
	)

	require.Nil(t, got)
	require.Zero(t, repo.modelRateLimitAccountID)
	require.Empty(t, repo.modelRateLimitKey)
}
