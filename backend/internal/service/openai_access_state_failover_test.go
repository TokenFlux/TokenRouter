package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIStream403AccountRepo struct {
	gatewayprovider.ExecutionAccountStore

	setErrorCalls int
}

func (r *openAIStream403AccountRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

type openAIAuthPolicyAccountRepo struct {
	gatewayprovider.ExecutionAccountStore

	tempCalls     int
	setErrorCalls int
}

func (r *openAIAuthPolicyAccountRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

func (r *openAIAuthPolicyAccountRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

type openAIAuthPolicy403Counter struct {
	counts []int64
}

func (s *openAIAuthPolicy403Counter) IncrementOpenAI403Count(context.Context, int64, int) (int64, error) {
	if len(s.counts) == 0 {
		return 1, nil
	}
	count := s.counts[0]
	s.counts = s.counts[1:]
	return count, nil
}

func (*openAIAuthPolicy403Counter) ResetOpenAI403Count(context.Context, int64) error {
	return nil
}

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

func TestOpenAIHTTPAccessStateBadRequestDoesNotDisableAccount(t *testing.T) {
	repo := &openAIStream403AccountRepo{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 925, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true}}
	body := []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: account disabled"}}`)

	disabled := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusBadRequest, nil, body, false).StopScheduling

	require.False(t, disabled)
	require.Zero(t, repo.setErrorCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIStreamEchoedAccessStateMessageDoesNotDisableOrFailover(t *testing.T) {
	repo := &openAIStream403AccountRepo{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 926, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true}}
	payload := []byte(`{"type":"response.failed","response":{"error":{"type":"invalid_request_error","code":"unknown_parameter","message":"Unknown parameter: account disabled"}}}`)
	message := openai.ExtractOpenAISSEErrorMessage(payload)

	require.False(t, gatewayprovider.IsOpenAIUpstreamAccessStateError(message, payload))
	require.False(t, openai.OpenAIStreamFailedEventShouldFailover(payload, message))
	status, disabled := svc.responseOutput.TerminalAccountEffects(nil, account, payload, message, nil)
	require.Equal(t, http.StatusBadGateway, status)
	require.False(t, disabled)
	require.Zero(t, repo.setErrorCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIHTTPAccessStateTrustsStructuredCode(t *testing.T) {
	repo := &openAIStream403AccountRepo{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 930, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true}}
	body := []byte(`{"error":{"code":"organization_deactivated","message":"request rejected"}}`)

	require.True(t, gatewayprovider.IsOpenAIHTTPUpstreamAccessStateError(http.StatusBadRequest, "", body))
	require.True(t, gatewayprovider.ShouldFailoverOpenAIResponse(http.StatusBadRequest, "", body))
	require.True(t, gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusBadRequest, nil, body, false).StopScheduling)
	require.Equal(t, 1, repo.setErrorCalls)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIHTTPAuthMessagesUseExistingStatusPolicies(t *testing.T) {
	t.Run("oauth 401 remains recoverable", func(t *testing.T) {
		repo := &openAIAuthPolicyAccountRepo{}
		rateLimits := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{}, nil)

		svc := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: rateLimits})
		account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 931, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true,
			Credentials: map[string]any{"refresh_token": "refreshable"}}}
		body := []byte(`{"error":{"message":"account is disabled"}}`)

		require.False(t, gatewayprovider.IsOpenAIHTTPUpstreamAccessStateError(http.StatusUnauthorized, "", body))
		require.True(t, gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusUnauthorized, nil, body, false).StopScheduling)
		require.Zero(t, repo.setErrorCalls)
		require.Equal(t, 1, repo.tempCalls)
	})

	t.Run("403 uses counter cooldown", func(t *testing.T) {
		repo := &openAIAuthPolicyAccountRepo{}
		counter := &openAIAuthPolicy403Counter{counts: []int64{1}}
		var svc *OpenAIGatewayService

		rateLimits := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{ForbiddenCounter: counter, Block: func(v *accountcore.Record, until time.Time, reason string) {
			svc.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
		}}, nil)

		svc = withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: rateLimits})
		rateLimits.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
			return svc.ShouldRetryOpenAIOAuth429(gatewayprovider.NewExecutionAccount(v), h, body)
		}

		account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 932, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true}}
		body := []byte(`{"error":{"message":"workspace has been suspended"}}`)

		require.False(t, gatewayprovider.IsOpenAIHTTPUpstreamAccessStateError(http.StatusForbidden, "", body))
		require.True(t, gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusForbidden, nil, body, false).StopScheduling)
		require.Zero(t, repo.setErrorCalls)
		require.Equal(t, 1, repo.tempCalls)
	})
}

func TestOpenAICyberPolicyWrapped5xxNeverFailsOver(t *testing.T) {
	body := []byte(`{"error":{"code":"cyber_policy","message":"blocked"}}`)

	require.False(t, gatewayprovider.ShouldFailoverOpenAIResponse(http.StatusBadGateway, "wrapped upstream failure", body))
	require.False(t, shouldFailoverOpenAIPassthroughResponse(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Type: capability.AccountTypeOAuth}}, http.StatusBadGateway, body))
}

func TestOpenAICapacityFailoverCarriesSafeTerminalResponse(t *testing.T) {
	message := "Our servers are currently overloaded. Please try again later."
	body := []byte(`{"error":{"code":"server_is_overloaded","message":"` + message + `"}}`)
	err := gatewayprovider.NewOpenAIUpstreamFailure(http.StatusBadRequest, nil, body, message, false)

	require.True(t, gatewayprovider.IsOpenAICapacityShed(err))
	require.Equal(t, http.StatusServiceUnavailable, err.ClientStatusCode)
	require.Equal(t, message, err.ClientMessage)
	require.NotContains(t, err.ClientMessage, "server_is_overloaded")
}

func TestOpenAIStreamSemanticStatusesPreservedAcrossTerminalShapes(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		status       int
		wantFailover bool
	}{
		{"unauthorized", `{"type":"error","error":{"type":"authentication_error","code":"invalid_api_key","message":"unauthorized"}}`, http.StatusUnauthorized, true},
		{"forbidden", `{"type":"response.failed","response":{"error":{"type":"permission_error","message":"forbidden"}}}`, http.StatusForbidden, false},
		{"rate_limit", `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`, http.StatusTooManyRequests, true},
		{"overload_529", `{"type":"error","error":{"status_code":529,"code":"overloaded","message":"overloaded"}}`, 529, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(tt.body)
			message := openai.ExtractOpenAISSEErrorMessage(payload)
			require.Equal(t, tt.status, openai.OpenAIStreamFailureStatus(payload, message))
			require.Equal(t, tt.wantFailover, openai.OpenAIStreamErrorEventShouldFailover(payload, message))
		})
	}
}

func TestOpenAIStreamBareErrorUsesSemanticFailover(t *testing.T) {
	payload := []byte(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`)
	require.True(t, openai.OpenAIStreamErrorEventShouldFailover(payload, "slow down"))
}

func TestOpenAIStream403FailoverRequiresStructuredAccountCredentialSignal(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{
			name:    "ordinary permission error",
			payload: `{"type":"response.failed","response":{"error":{"type":"permission_error","code":"forbidden","message":"access denied for this request"}}}`,
		},
		{
			name:    "explicit 403 request status",
			payload: `{"type":"error","error":{"type":"permission_error","code":"forbidden","status_code":403,"message":"forbidden content"}}`,
		},
		{
			name:    "structured access state",
			payload: `{"type":"response.failed","response":{"error":{"code":"workspace_suspended","message":"workspace is suspended"}}}`,
			want:    true,
		},
		{
			name:    "explicit credential auth code",
			payload: `{"type":"error","error":{"type":"permission_error","code":"invalid_api_key","status_code":403,"message":"credential rejected"}}`,
			want:    true,
		},
		{
			name:    "explicit authentication type",
			payload: `{"type":"error","error":{"type":"authentication_error","status_code":403,"message":"credential rejected"}}`,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(tt.payload)
			message := openai.ExtractOpenAISSEErrorMessage(payload)
			require.Equal(t, http.StatusForbidden, openai.OpenAIStreamFailureStatus(payload, message))
			require.Equal(t, tt.want, openai.OpenAIStreamFailedEventShouldFailover(payload, message))
			require.Equal(t, tt.want, openai.OpenAIStreamErrorEventShouldFailover(payload, message))
		})
	}
}

func TestOpenAIStream403PostOutputAccountSideEffectsIgnoreRequestPermissionErrors(t *testing.T) {
	repo := &openAIStream403AccountRepo{}
	rateLimits := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: rateLimits})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 918, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	payload := []byte(`{"type":"error","error":{"type":"permission_error","code":"forbidden","status_code":403,"message":"access denied for this request"}}`)

	status, disabled := svc.responseOutput.TerminalAccountEffects(nil, account, payload, "access denied for this request", nil)

	require.Equal(t, http.StatusForbidden, status)
	require.False(t, disabled)
	require.Zero(t, repo.setErrorCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIStream403ExplicitCredentialAuthAppliesAccountSideEffects(t *testing.T) {
	repo := &openAIStream403AccountRepo{}
	rateLimits := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: rateLimits})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 917, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	payload := []byte(`{"type":"error","error":{"type":"permission_error","code":"invalid_api_key","status_code":403,"message":"credential rejected"}}`)

	status, disabled := svc.responseOutput.TerminalAccountEffects(nil, account, payload, "credential rejected", nil)

	require.Equal(t, http.StatusForbidden, status)
	require.True(t, disabled)
	require.Equal(t, 1, repo.setErrorCalls)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIWSStandaloneFailedStructured403AppliesAccountSideEffectsOnce(t *testing.T) {
	repo := &openAIStream403AccountRepo{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 923, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	failed := []byte(`{"type":"response.failed","response":{"error":{"type":"permission_error","code":"invalid_api_key","status_code":403,"message":"credential rejected"}}}`)

	require.True(t, svc.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-5", nil, failed))
	require.Equal(t, 1, repo.setErrorCalls)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIWSPairedStructured403SideEffectsCanBeDeduplicated(t *testing.T) {
	repo := &openAIStream403AccountRepo{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 924, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	errorEvent := []byte(`{"type":"error","error":{"code":"workspace_suspended","status_code":403,"message":"workspace is suspended"}}`)
	failedEvent := []byte(`{"type":"response.failed","response":{"error":{"code":"workspace_suspended","status_code":403,"message":"workspace is suspended"}}}`)

	applied := svc.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-5", nil, errorEvent)
	if !applied {
		applied = svc.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-5", nil, failedEvent)
	}

	require.True(t, applied)
	require.Equal(t, 1, repo.setErrorCalls)
}

func TestOpenAIStreamAccessStateAppliesAccountHealthBeforeFailover(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 919, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeSetupToken}}
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"workspace_suspended","message":"workspace is suspended"}}}`)

	status, disabled := svc.responseOutput.TerminalAccountEffects(nil, account, payload, "workspace is suspended", nil)

	require.Equal(t, http.StatusForbidden, status)
	require.True(t, disabled)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIStreamPairedFailureAppliesAccountSideEffectsOnce(t *testing.T) {
	const upstream = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
		"data: {\"type\":\"error\",\"error\":{\"status_code\":403,\"code\":\"workspace_suspended\",\"message\":\"workspace is suspended\"}}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failed\",\"error\":{\"status_code\":403,\"code\":\"workspace_suspended\",\"message\":\"workspace is suspended\"}}}\n\n"

	t.Run("native", func(t *testing.T) {

		repo := &openAIStream403AccountRepo{}
		svc := withSchedulerParametersForTest(&OpenAIGatewayService{
			cfg:            &config.Config{},
			toolCorrector:  openai.NewCodexToolCorrector(),
			healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil),
		})
		account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 921, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
		recorder := newOpenAIResponseFlushRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(upstream)),
		}

		result, err := svc.responseOutput.Stream(context.Background(), resp, c, account, time.Now(), "gpt-5", "gpt-5", "")

		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 1, repo.setErrorCalls)
	})

	t.Run("passthrough", func(t *testing.T) {

		repo := &openAIStream403AccountRepo{}
		svc := withSchedulerParametersForTest(&OpenAIGatewayService{
			cfg:            &config.Config{},
			healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil),
		})
		account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 922, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		writer := &passthroughFlushTestWriter{
			ResponseWriter:  c.Writer,
			recorder:        recorder,
			failAfterWrites: -1,
		}
		c.Writer = writer
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(upstream)),
		}

		result, err := svc.responseOutput.PassthroughStream(
			context.Background(), resp, c, account, time.Now(), "gpt-5", "gpt-5",
		)

		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 1, repo.setErrorCalls)
	})
}

func TestOpenAIStreamOAuthLike429GetsDeadlineWithoutImmediateRuntimeBlock(t *testing.T) {
	for _, accountType := range []string{capability.AccountTypeOAuth, capability.AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 920, Platform: capability.PlatformOpenAI, Type: accountType}}
			payload := []byte(`{"type":"error","error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`)
			status, disabled := svc.responseOutput.TerminalAccountEffects(nil, account, payload, "slow down", nil)
			err := (gatewayprovider.OpenAIFailoverPolicy{Health: svc.responseOutput.Health}).NewAccountFailure(account, status, nil, payload, "slow down", disabled, false)

			require.Equal(t, http.StatusTooManyRequests, status)
			require.False(t, disabled)
			require.True(t, err.RetryableOnSameAccount)
			require.False(t, err.SameAccountRetryDeadline.IsZero())
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}
