//go:build unit

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	opsprovider "github.com/TokenFlux/TokenRouter/internal/ops/provider"

	failover "github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	opscore "github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGatewayChatCredentialStopDoesNotSelectAnotherAccountAndReturnsSafe503(t *testing.T) {

	stopErr := &forwardcore.UpstreamFailoverError{
		Stage:             forwardcore.GatewayFailureStageAccountAuth,
		Scope:             forwardcore.GatewayFailureScopeProvider,
		Reason:            forwardcore.GrokCredentialReasonProviderConfig,
		NextAccountAction: forwardcore.NextAccountStop,
		ClientStatusCode:  http.StatusTeapot,
		ClientMessage:     "invalid_client client_secret=must-not-leak",
	}
	state := failover.NewFailoverState[*forwardcore.UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
	action := state.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 71, capability.PlatformGrok, 0, stopErr)

	require.Equal(t, failover.FailoverExhausted, action)
	require.Zero(t, state.SwitchCount)
	require.Empty(t, state.FailedAccountIDs)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	(gatewayhttp.MessagesErrorOutput{}).ChatExhausted(c, state.LastFailoverErr, false)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), forwardcore.GrokCredentialUnavailableClientMessage)
	require.NotContains(t, recorder.Body.String(), "invalid_client")
	require.NotContains(t, recorder.Body.String(), "client_secret")
}

func TestGatewayChatAntigravityCredentialFailureReturnsActionableMessage(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	(gatewayhttp.MessagesErrorOutput{}).ChatExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:        http.StatusUnauthorized,
		Stage:             forwardcore.GatewayFailureStageAccountAuth,
		Scope:             forwardcore.GatewayFailureScopeAccount,
		Reason:            forwardcore.AntigravityCredentialRejectedReason,
		NextAccountAction: forwardcore.NextAccountRetry,
		ClientStatusCode:  http.StatusBadGateway,
		ClientMessage:     forwardcore.AntigravityCredentialRejectedClientMessage,
		ResponseBody:      []byte(`{"error":{"message":"Invalid bearer token","refresh_token":"must-not-leak"}}`),
	}, false)

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), forwardcore.AntigravityCredentialRejectedClientMessage)
	require.NotContains(t, strings.ToLower(recorder.Body.String()), "bearer")
	require.NotContains(t, strings.ToLower(recorder.Body.String()), "refresh_token")
}

func TestOpenAIAccessStateCredentialFailureUsesTypedSafeResponse(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	(newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})).openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:        http.StatusForbidden,
		Stage:             forwardcore.GatewayFailureStageAccountAuth,
		Scope:             forwardcore.GatewayFailureScopeAccount,
		Reason:            forwardcore.OpenAIUpstreamAccessStateReason,
		NextAccountAction: forwardcore.NextAccountRetry,
		ClientStatusCode:  http.StatusBadGateway,
		ClientMessage:     "Upstream access is temporarily unavailable, please retry later",
		ResponseBody:      []byte(`{"error":{"message":"Your workspace is deactivated","token":"must-not-leak"}}`),
	}, false)

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Upstream access is temporarily unavailable")
	require.NotContains(t, strings.ToLower(recorder.Body.String()), "deactivated")
	require.NotContains(t, recorder.Body.String(), "must-not-leak")
}

func TestOpenAICapacityFailoverExhaustionPreservesMessageAsServerError(t *testing.T) {

	message := "Our servers are currently overloaded. Please try again later."
	failoverErr := &forwardcore.UpstreamFailoverError{
		StatusCode:             http.StatusBadRequest,
		ResponseBody:           []byte(`{"error":{"code":"server_is_overloaded","message":"` + message + `"}}`),
		RetryableOnSameAccount: true,
		RequestScopedTransient: true,
		ClientStatusCode:       http.StatusServiceUnavailable,
		ClientMessage:          message,
	}

	t.Run("native_openai", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		(newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})).openAIAttemptSupport().HandleFailoverExhausted(c, failoverErr, false)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Equal(t, "server_error", gjson.Get(recorder.Body.String(), "error.type").String())
		require.Equal(t, message, gjson.Get(recorder.Body.String(), "error.message").String())
		require.NotContains(t, recorder.Body.String(), "server_is_overloaded")
	})

	t.Run("responses_compat", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		(gatewayhttp.MessagesErrorOutput{}).ResponsesExhausted(c, failoverErr, false)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Equal(t, "server_error", gjson.Get(recorder.Body.String(), "error.code").String())
		require.Equal(t, message, gjson.Get(recorder.Body.String(), "error.message").String())
	})

	t.Run("anthropic_compat", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		(newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})).openAIAttemptSupport().HandleAnthropicFailoverExhausted(c, failoverErr, false)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Equal(t, "api_error", gjson.Get(recorder.Body.String(), "error.type").String())
		require.Equal(t, message, gjson.Get(recorder.Body.String(), "error.message").String())
	})
}

func TestResponsesFailoverExhaustedAfterForwardedTerminalMarksOpsWithoutDuplicateFrame(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	official := "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"official failure\"}}}\n\n"
	_, err := c.Writer.Write([]byte(official))
	require.NoError(t, err)
	gatewayhttp.MarkOpsStreamError(c, "server_error", "official failure", http.StatusBadGateway)

	(gatewayhttp.MessagesErrorOutput{}).ResponsesExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: []byte(`{"error":{"message":"fallback failure"}}`),
	}, true)

	require.Equal(t, official, recorder.Body.String())
	streamErr, ok := gatewayhttp.GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "official failure", streamErr.Message)

	markerRecorder := httptest.NewRecorder()
	markerContext, _ := gin.CreateTestContext(markerRecorder)
	(gatewayhttp.MessagesErrorOutput{}).ResponsesExhausted(markerContext, &forwardcore.UpstreamFailoverError{
		StatusCode: http.StatusTooManyRequests,
	}, true)
	require.Contains(t, markerRecorder.Body.String(), "event: response.failed")
	require.Equal(t, 1, strings.Count(markerRecorder.Body.String(), "event: response.failed"))
	streamErr, ok = gatewayhttp.GetOpsStreamError(markerContext)
	require.True(t, ok)
	require.Equal(t, http.StatusTooManyRequests, streamErr.IntendedStatus)
	require.Equal(t, "rate_limit_error", streamErr.ErrType)

	heartbeatRecorder := httptest.NewRecorder()
	heartbeatContext, _ := gin.CreateTestContext(heartbeatRecorder)
	heartbeat := ": keepalive\n\n"
	written, err := heartbeatRecorder.Write([]byte(heartbeat))
	require.NoError(t, err)
	gatewayhttp.RecordStreamHeartbeat(heartbeatContext, written)
	(gatewayhttp.MessagesErrorOutput{}).ResponsesExhausted(heartbeatContext, &forwardcore.UpstreamFailoverError{
		StatusCode: http.StatusBadGateway,
	}, true)
	require.True(t, strings.HasPrefix(heartbeatRecorder.Body.String(), heartbeat))
	require.Equal(t, 1, strings.Count(heartbeatRecorder.Body.String(), "event: response.failed"))
}

func TestGatewayChatInferenceExhaustionRestoresRetryAfter(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	(gatewayhttp.MessagesErrorOutput{}).ChatExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusTooManyRequests,
		ResponseHeaders: http.Header{"Retry-After": []string{"45"}},
	}, false)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "45", recorder.Header().Get("Retry-After"))
}

func TestCredentialFailoverExhaustionReturnsFixedSafe503(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})

	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		Stage:             forwardcore.GatewayFailureStageAccountAuth,
		Scope:             forwardcore.GatewayFailureScopeAccount,
		Reason:            forwardcore.GrokCredentialReasonRevoked,
		NextAccountAction: forwardcore.NextAccountRetry,
		ClientStatusCode:  http.StatusTeapot,
		ClientMessage:     "invalid_grant refresh_token=must-not-leak",
	}, false)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), forwardcore.GrokCredentialUnavailableClientMessage)
	require.NotContains(t, strings.ToLower(recorder.Body.String()), "invalid_grant")
	require.NotContains(t, strings.ToLower(recorder.Body.String()), "refresh_token")
	require.NotContains(t, recorder.Body.String(), "must-not-leak")
}

func TestInferenceFailoverExhaustionRestoresRetryAfter(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})

	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusTooManyRequests,
		ResponseHeaders: http.Header{"Retry-After": []string{"17"}},
	}, false)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "17", recorder.Header().Get("Retry-After"))
}

func TestFailoverExhaustionRejectsSecretBearingRetryAfter(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})

	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusTooManyRequests,
		ResponseHeaders: http.Header{"Retry-After": []string{"refresh_token=must-not-leak"}},
	}, false)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Empty(t, recorder.Header().Get("Retry-After"))
	require.NotContains(t, recorder.Body.String(), "must-not-leak")
}

func TestFailoverExhaustionRejectsFarFutureRetryAfterDate(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})

	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode: http.StatusTooManyRequests,
		ResponseHeaders: http.Header{
			"Retry-After": []string{time.Now().Add(30 * 24 * time.Hour).UTC().Format(http.TimeFormat)},
		},
	}, false)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Empty(t, recorder.Header().Get("Retry-After"))
}

func TestFailoverExhaustionAllowsBoundedRetryAfterDate(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	retryAfter := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)

	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusTooManyRequests,
		ResponseHeaders: http.Header{"Retry-After": []string{retryAfter}},
	}, false)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, retryAfter, recorder.Header().Get("Retry-After"))
}

func TestOpsRecoveredCredentialFailoverDoesNotCreateRequestError(t *testing.T) {
	queue := newOpsCaptureQueue(2)

	ops := opscore.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	router := gin.New()
	router.Use(gatewayhttp.OpsErrorLoggerMiddleware(ops, queue, gatewayhttp.OpsObservationAccess{}))
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		c.Set(gatewayhttp.OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{
			{Stage: string(forwardcore.GatewayFailureStageInference), UpstreamStatusCode: http.StatusForbidden, Message: "earlier inference failure"},
			{
				Stage: string(forwardcore.GatewayFailureStageAccountAuth), Scope: string(forwardcore.GatewayFailureScopeAccount),
				Reason: string(forwardcore.GrokCredentialReasonRevoked), Message: "Grok OAuth credentials require account action",
			},
		})
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), queue.health.Length)
	job := <-queue.jobs
	require.Equal(t, http.StatusOK, job.entry.StatusCode)
	require.Equal(t, string(forwardcore.GatewayFailureStageAccountAuth), job.entry.ErrorPhase)
	require.NotNil(t, job.entry.UpstreamErrorsJSON)
	events, err := opscore.ParseOpsUpstreamErrors(*job.entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, string(forwardcore.GatewayFailureStageAccountAuth), events[1].Stage)
}

func TestOpsWebSocketCredentialFailoverSuccessDoesNotCreateRequestError(t *testing.T) {
	queue := newOpsCaptureQueue(2)

	ops := opscore.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	router := gin.New()
	router.Use(gatewayhttp.OpsErrorLoggerMiddleware(ops, queue, gatewayhttp.OpsObservationAccess{}))
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		c.Set(gatewayhttp.OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{{
			Stage: string(forwardcore.GatewayFailureStageAccountAuth), Scope: string(forwardcore.GatewayFailureScopeAccount),
			Reason: string(forwardcore.GrokCredentialReasonRevoked), Message: "Grok OAuth credentials require account action",
		}})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), queue.health.Length)
	job := <-queue.jobs
	require.Equal(t, http.StatusOK, job.entry.StatusCode)
	require.Equal(t, string(forwardcore.GatewayFailureStageAccountAuth), job.entry.ErrorPhase)
	require.NotNil(t, job.entry.UpstreamErrorsJSON)
	events, err := opscore.ParseOpsUpstreamErrors(*job.entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, string(forwardcore.GatewayFailureStageAccountAuth), events[0].Stage)
}

func TestOpsWebSocketCredentialFailoverExhaustedIsRecorded(t *testing.T) {
	queue := newOpsCaptureQueue(2)

	ops := opscore.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	router := gin.New()
	router.Use(gatewayhttp.OpsErrorLoggerMiddleware(ops, queue, gatewayhttp.OpsObservationAccess{}))
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		c.Set(gatewayhttp.OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{{
			Stage: string(forwardcore.GatewayFailureStageAccountAuth), Scope: string(forwardcore.GatewayFailureScopeAccount),
			Reason: string(forwardcore.GrokCredentialReasonRevoked), Message: "Grok OAuth credentials require account action",
		}})
		closeOpenAIWSFailoverExhausted(c, nil, &forwardcore.UpstreamFailoverError{
			Stage:             forwardcore.GatewayFailureStageAccountAuth,
			Scope:             forwardcore.GatewayFailureScopeAccount,
			Reason:            forwardcore.GrokCredentialReasonRevoked,
			NextAccountAction: forwardcore.NextAccountStop,
		})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), queue.health.Length)
	job := <-queue.jobs
	require.Equal(t, "account_auth", job.entry.ErrorPhase)
	require.Equal(t, http.StatusServiceUnavailable, job.entry.StatusCode)
	require.Equal(t, forwardcore.GrokCredentialUnavailableClientMessage, job.entry.ErrorMessage)
}
