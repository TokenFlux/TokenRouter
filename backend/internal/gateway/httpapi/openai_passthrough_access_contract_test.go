package httpapi

import (
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestOpenAIUpstreamAccessStateClassification(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"workspace_code", `{"detail":{"code":"deactivated_workspace"}}`, true},
		{"disabled_account_message", `{"error":{"message":"Your account is disabled"}}`, false},
		{"suspended_workspace_message", `{"response":{"error":{"message":"This workspace has been suspended"}}}`, false},
		{"deactivated_organization_message", `{"detail":{"message":"The organization is deactivated"}}`, false},
		{"scalar_detail", `{"detail":"This workspace has been disabled"}`, false},
		{"suspended_org_code", `{"error":{"code":"org_suspended"}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(tt.body)
			require.Equal(t, tt.want, gatewayprovider.IsOpenAIUpstreamAccessStateError("", body))
			if !tt.want {
				return
			}
			require.True(t, gatewayprovider.ShouldFailoverOpenAIResponse(http.StatusForbidden, "", body))
			require.True(t, shouldFailoverOpenAIPassthroughResponse(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Type: capability.AccountTypeOAuth}}, http.StatusForbidden, body))

			err := gatewayprovider.NewOpenAIUpstreamFailure(http.StatusForbidden, nil, body, "", true)
			require.True(t, err.IsCredentialFailure())
			require.Equal(t, forwardcore.GatewayFailureScopeAccount, err.Scope)
			require.Equal(t, forwardcore.OpenAIUpstreamAccessStateReason, err.Reason)
			require.Equal(t, forwardcore.NextAccountRetry, err.NextAccountAction)
			require.False(t, err.RetryableOnSameAccount)
			require.False(t, err.RequestScopedTransient)
			require.Equal(t, http.StatusBadGateway, err.ClientStatusCode)
			require.Equal(t, "Upstream access is temporarily unavailable, please retry later", err.ClientMessage)
		})
	}
}
func TestOpenAIUpstreamAccessStateDoesNotScanEchoedJSON(t *testing.T) {
	body := []byte(`{"error":{"code":"invalid_request_error","message":"Invalid input"},"echo":{"prompt":"my account is disabled"}}`)
	require.False(t, gatewayprovider.IsOpenAIUpstreamAccessStateError("", body))
	require.False(t, shouldFailoverOpenAIPassthroughResponse(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Type: capability.AccountTypeOAuth}}, http.StatusBadRequest, body))
}
func TestOpenAIHTTPAccessStateDoesNotTrustBadRequestMessage(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","code":"unknown_parameter","message":"Unknown parameter: account disabled"}}`)

	require.False(t, gatewayprovider.IsOpenAIUpstreamAccessStateError("", body), "free-form stream messages are not durable account evidence")
	require.False(t, gatewayprovider.IsOpenAIHTTPUpstreamAccessStateError(http.StatusBadRequest, "", body))
	require.False(t, gatewayprovider.ShouldFailoverOpenAIResponse(http.StatusBadRequest, "", body))
	require.False(t, shouldFailoverOpenAIPassthroughResponse(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Type: capability.AccountTypeOAuth}}, http.StatusBadRequest, body))

	err := gatewayprovider.NewOpenAIUpstreamFailure(http.StatusBadRequest, nil, body, "", false)
	require.False(t, err.IsCredentialFailure())
}
func TestOpenAICyberPolicyWrapped5xxNeverFailsOver(t *testing.T) {
	body := []byte(`{"error":{"code":"cyber_policy","message":"blocked"}}`)

	require.False(t, gatewayprovider.ShouldFailoverOpenAIResponse(http.StatusBadGateway, "wrapped upstream failure", body))
	require.False(t, shouldFailoverOpenAIPassthroughResponse(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Type: capability.AccountTypeOAuth}}, http.StatusBadGateway, body))
}
