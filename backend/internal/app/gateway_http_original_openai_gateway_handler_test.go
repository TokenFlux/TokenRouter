package app

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	moderationflow "github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"

	slog "log/slog"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	usage "github.com/TokenFlux/TokenRouter/internal/usage"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	testkit "github.com/TokenFlux/TokenRouter/internal/apikey/testkit"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"

	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/service"

	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestHandleGroupSelectionBusinessError 验证客户端策略拒绝不会被误报为服务不可用。
func TestHandleGroupSelectionBusinessError(t *testing.T) {

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	var status int
	var errType string
	var message string
	handled := gatewayhttp.WriteGroupSelectionBusinessError(
		c,
		fmt.Errorf("select account: %w", routing.ErrClaudeCodeOnly),
		false,
		keyhttp.GetAPIKeyFromContext, gatewayprovider.ModelDisplayCatalogue{},
		func(gotStatus int, gotErrType string, gotMessage string, _ bool) {
			status = gotStatus
			errType = gotErrType
			message = gotMessage
		},
	)

	require.True(t, handled)
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, "permission_error", errType)
	require.Equal(t, routing.ErrClaudeCodeOnly.Error(), message)
}

func TestOpenAIResponsesRequiredCapability(t *testing.T) {
	tests := []struct {
		name        string
		imageIntent bool
		platform    string
		want        accountcore.OpenAIEndpointCapability
	}{
		{
			name:        "OpenAI explicit image intent requires Responses",
			imageIntent: true,
			platform:    capability.PlatformOpenAI,
			want:        accountcore.OpenAIEndpointCapabilityResponses,
		},
		{
			name:        "Grok explicit image intent keeps chat capability",
			imageIntent: true,
			platform:    capability.PlatformGrok,
			want:        accountcore.OpenAIEndpointCapabilityTextGeneration,
		},
		{
			name:     "non-image intent keeps chat capability",
			platform: capability.PlatformOpenAI,
			want:     accountcore.OpenAIEndpointCapabilityTextGeneration,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, textflow.ResponsesCapability(tt.imageIntent, tt.platform))
		})
	}
}

func TestOpenAIEnsureForwardErrorResponse_ResponsesRouteCyberWarningEmitsOriginalMessage(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointResponses, nil)
	_, _ = c.Writer.WriteString(":\n\n")

	message := "This request has been flagged for potentially high-risk cyber activity."
	err := &openAIHandlerTestWarningError{
		warning: &forwardcore.UpstreamWarning{
			StatusCode:   http.StatusForbidden,
			ResponseBody: []byte(`{"type":"response.failed","error":{"message":"` + message + `"}}`),
			Message:      message,
		},
		err: errors.New("upstream response failed"),
	}

	wrote := gatewayhttp.DefaultOpenAIErrorOutput().EnsureResponse(c, false, err)

	require.True(t, wrote)
	body := w.Body.String()
	assert.Contains(t, body, "event: response.failed\n")
	assert.Contains(t, body, `"code":"invalid_request"`)
	assert.Contains(t, body, message)
	assert.NotContains(t, body, "Upstream request failed")
}

func TestOpenAIForwardErrorAlreadyCommunicated(t *testing.T) {

	t.Run("upstream response failed after write", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointResponses, nil)
		before := c.Writer.Size()
		_, _ = c.Writer.WriteString(`event: response.failed
data: {"type":"response.failed","error":{"message":"This content was flagged"}}

`)

		reported := gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(c, before, errors.New("upstream response failed: This content was flagged"))

		require.True(t, reported)
	})

	t.Run("no write still needs fallback", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointResponses, nil)

		reported := gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(c, c.Writer.Size(), errors.New("upstream response failed: This content was flagged"))

		require.False(t, reported)
	})

	t.Run("generic error after write still needs fallback", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointResponses, nil)
		before := c.Writer.Size()
		_, _ = c.Writer.WriteString(":\n\n")

		reported := gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(c, before, errors.New("stream read error: unexpected EOF"))

		require.False(t, reported)
	})
}

func TestOpenAIHandleFailoverExhausted_CyberWarningPassesThroughMessage(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	gatewayhttp.SetOpsRequestContext(c, "gpt-5.1", false)

	message := "This content was flagged for possible cybersecurity risk. If this seems wrong, try rephrasing your request."
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:   http.StatusForbidden,
		ResponseBody: []byte(`{"error":{"message":"` + message + `"}}`),
	}, false)

	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), message)
	assert.NotContains(t, w.Body.String(), "All available accounts exhausted")
}

func TestShouldLogOpenAIForwardFailureAsWarn(t *testing.T) {

	t.Run("fallback_written_should_not_downgrade", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		require.False(t, gatewayhttp.ShouldLogOpenAIForwardFailureAsWarn(c, true))
	})

	t.Run("context_nil_should_not_downgrade", func(t *testing.T) {
		require.False(t, gatewayhttp.ShouldLogOpenAIForwardFailureAsWarn(nil, false))
	})

	t.Run("response_not_written_should_not_downgrade", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		require.False(t, gatewayhttp.ShouldLogOpenAIForwardFailureAsWarn(c, false))
	})

	t.Run("response_already_written_should_downgrade", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.String(http.StatusForbidden, "already written")
		require.True(t, gatewayhttp.ShouldLogOpenAIForwardFailureAsWarn(c, false))
	})
}

func TestOpenAIGatewayMessagesProtocolPolicyAllowsGrokGroups(t *testing.T) {

	t.Run("openai_group_without_messages_protocol_is_rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`))
		groupID := int64(4101)
		c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
			ID:      5101,
			GroupID: &groupID,
			User:    &identity.User{ID: 6101},
			Group: &routing.Group{
				ID:       groupID,
				Platform: capability.PlatformOpenAI,
				AllowedProtocols: []protocol.ProtocolID{
					protocol.ProtocolOpenAIResponses,
					protocol.ProtocolOpenAIChatCompletions,
				},
			},
		})
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 6101, Concurrency: 1})

		h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
		h.Messages(c)

		require.Equal(t, http.StatusForbidden, rec.Code)
		require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
		require.Contains(t, rec.Body.String(), "This group does not allow Anthropic Messages requests")
	})

	t.Run("grok_group_with_messages_protocol_reaches_gateway_dependencies", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"grok-4.3","messages":[{"role":"user","content":"hi"}]}`))
		groupID := int64(4102)
		c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
			ID:      5102,
			GroupID: &groupID,
			User:    &identity.User{ID: 6102},
			Group: &routing.Group{
				ID:       groupID,
				Platform: capability.PlatformGrok,
				AllowedProtocols: []protocol.ProtocolID{
					protocol.ProtocolAnthropicMessages,
					protocol.ProtocolOpenAIResponses,
					protocol.ProtocolOpenAIChatCompletions,
				},
			},
		})
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 6102, Concurrency: 1})

		h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
		h.Messages(c)

		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Equal(t, "api_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
		require.NotContains(t, rec.Body.String(), "This group does not allow Anthropic Messages requests")
	})
}

func TestOpenAIResponses_MissingDependencies_ReturnsServiceUnavailable(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      10,
		GroupID: &groupID,
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{
		UserID:      1,
		Concurrency: 1,
	})

	// 故意使用未初始化依赖，验证快速失败而不是崩溃。
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	require.NotPanics(t, func() {
		h.Responses(c)
	})

	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	var parsed map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &parsed)
	require.NoError(t, err)

	errorObj, ok := parsed["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "api_error", errorObj["type"])
	assert.Equal(t, "Service temporarily unavailable", errorObj["message"])
}

func TestOpenAIResponses_SetsClientTransportHTTP(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{"model":"gpt-5"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	h.Responses(c)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, gatewayhttp.OpenAIClientTransportHTTP, gatewayhttp.GetOpenAIClientTransport(c))
}

func TestOpenAIResponses_RejectsMessageIDAsPreviousResponseID(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
		`{"model":"gpt-5.1","stream":false,"previous_response_id":"msg_123456","input":[{"type":"input_text","text":"hello"}]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      101,
		GroupID: &groupID,
		User:    &identity.User{ID: 1},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{
		UserID:      1,
		Concurrency: 1,
	})

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "previous_response_id must be a response.id")
}

func TestOpenAIResponses_AcceptsHTTPContinuationPreviousResponseIDBeforeRouting(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
		`{"model":"gpt-5.1","stream":false,"previous_response_id":"resp_123456","input":[{"type":"input_text","text":"hello"}]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      101,
		GroupID: &groupID,
		User:    &identity.User{ID: 1},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{
		UserID:      1,
		Concurrency: 1,
	})

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	require.NoError(t, h.Input.Source.ResponseStateStore().BindHTTPResponseOwner(context.Background(), groupID, "resp_123456", 1, 101, h.Input.Source.OpenAIHTTPResponseStickyTTL()))
	h.Responses(c)

	require.NotEqual(t, http.StatusBadRequest, w.Code)
	require.NotContains(t, w.Body.String(), "Responses WebSocket v2")
}

func TestOpenAIResponses_RejectsHTTPContinuationOwnedByAnotherUser(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
		`{"model":"gpt-5.1","stream":false,"previous_response_id":"resp_other_tenant","input":"hello"}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      202,
		UserID:  2,
		GroupID: &groupID,
		User:    &identity.User{ID: 2},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 2, Concurrency: 1})

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	require.NoError(t, h.Input.Source.ResponseStateStore().BindHTTPResponseOwner(context.Background(), groupID, "resp_other_tenant", 1, 101, h.Input.Source.OpenAIHTTPResponseStickyTTL()))
	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "previous_response_id is not available for this user")
}

func TestOpenAIResponses_RejectsUnownedHTTPContinuation(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
		`{"model":"gpt-5.1","stream":false,"previous_response_id":"resp_unknown","input":"hello"}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 101, UserID: 1, GroupID: &groupID, User: &identity.User{ID: 1}})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1, Concurrency: 1})

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "previous_response_id is not available for this user")
}

func TestOpenAIResponses_FunctionCallOutputHTTPGuidanceDoesNotSuggestPreviousResponseReuse(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
		`{"model":"gpt-5.1","stream":false,"input":[{"type":"function_call_output","output":"{}"}]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      101,
		GroupID: &groupID,
		User:    &identity.User{ID: 1},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{
		UserID:      1,
		Concurrency: 1,
	})

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "Responses WebSocket v2")
	require.NotContains(t, w.Body.String(), "reuse previous_response_id")
}

func TestOpenAIResponsesWebSocket_SetsClientTransportWSWhenUpgradeValid(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	c.Request.Header.Set("Upgrade", "websocket")
	c.Request.Header.Set("Connection", "Upgrade")

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	h.ResponsesWebSocket(c)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, gatewayhttp.OpenAIClientTransportWS, gatewayhttp.GetOpenAIClientTransport(c))
}

func TestOpenAIResponsesWebSocket_InvalidUpgradeDoesNotSetTransport(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	h.ResponsesWebSocket(c)

	require.Equal(t, http.StatusUpgradeRequired, w.Code)
	require.Equal(t, gatewayhttp.OpenAIClientTransportUnknown, gatewayhttp.GetOpenAIClientTransport(c))
}

func TestOpenAIResponsesWebSocket_IngressCapacityRejected(t *testing.T) {

	cache := &httptestkit.ConcurrencyHooks{
		AcquireIngressLeaseFn: func(context.Context, int64, int, string) (bool, error) {
			return false, nil
		},
	}
	h := newOpenAIHandlerForPreviousResponseIDValidation(t, cache)
	h.Input.Config = &config.Config{}
	h.Input.Config.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey = 1
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, response, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.Error(t, err)
	require.Nil(t, clientConn)
	require.NotNil(t, response)
	require.Equal(t, http.StatusTooManyRequests, response.StatusCode)
	_ = response.Body.Close()
}

func TestOpenAIResponsesWebSocket_IngressLeaseBackendUnavailableBeforeUpgrade(t *testing.T) {

	cache := &httptestkit.ConcurrencyHooks{
		AcquireIngressLeaseFn: func(context.Context, int64, int, string) (bool, error) {
			return false, errors.New("redis unavailable")
		},
	}
	h := newOpenAIHandlerForPreviousResponseIDValidation(t, cache)
	h.Input.Config = &config.Config{}
	h.Input.Config.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey = 1
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, response, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.Error(t, err)
	require.Nil(t, clientConn)
	require.NotNil(t, response)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	_ = response.Body.Close()
}

func TestOpenAIResponsesWebSocket_FirstMessageTimeoutUsesConfig(t *testing.T) {

	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	h.Input.Config = &config.Config{}
	h.Input.Config.Gateway.OpenAIWS.ClientFirstMessageTimeoutSeconds = 1
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	started := time.Now()
	readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	_, _, err = clientConn.Read(readCtx)
	cancelRead()
	elapsed := time.Since(started)

	require.Error(t, err)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.GreaterOrEqual(t, elapsed, 500*time.Millisecond)
	require.Less(t, elapsed, 4*time.Second)
	require.Eventually(t, func() bool {
		readTimeout, ok := logSink.FieldValueForMessage("openai.websocket_read_first_message_failed", "read_timeout")
		return ok && readTimeout == time.Second &&
			logSink.ContainsMessageAtLevel("openai.websocket_read_first_message_failed", "warn")
	}, time.Second, 10*time.Millisecond)
}

func TestOpenAIResponsesWebSocket_IngressLeaseReleasedOnEarlyReturn(t *testing.T) {

	cache := &httptestkit.ConcurrencyHooks{
		AcquireIngressLeaseFn: func(context.Context, int64, int, string) (bool, error) {
			return true, nil
		},
	}
	h := newOpenAIHandlerForPreviousResponseIDValidation(t, cache)
	h.Input.Config = &config.Config{}
	h.Input.Config.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey = 1
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageBinary, []byte("not a response.create frame"))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, _, err = clientConn.Read(readCtx)
	cancelRead()
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&cache.ReleaseIngressCalled) == 1
	}, time.Second, 10*time.Millisecond)
}

func TestOpenAIResponsesWebSocket_IngressLeaseReleasedWhenUpgradeFails(t *testing.T) {

	cache := &httptestkit.ConcurrencyHooks{
		AcquireIngressLeaseFn: func(context.Context, int64, int, string) (bool, error) {
			return true, nil
		},
	}
	h := newOpenAIHandlerForPreviousResponseIDValidation(t, cache)
	h.Input.Config = &config.Config{}
	h.Input.Config.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey = 1
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	req, err := http.NewRequest(http.MethodGet, wsServer.URL+"/openai/v1/responses", nil)
	require.NoError(t, err)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Version", "13")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.NotEqual(t, http.StatusSwitchingProtocols, resp.StatusCode)
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&cache.ReleaseIngressCalled) == 1
	}, time.Second, 10*time.Millisecond)
}

func TestOpenAIResponsesWebSocket_RejectsMessageIDAsPreviousResponseID(t *testing.T) {

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	defer func() {
		_ = clientConn.CloseNow()
	}()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(
		`{"type":"response.create","model":"gpt-5.1","stream":false,"previous_response_id":"msg_abc123"}`,
	))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, _, err = clientConn.Read(readCtx)
	cancelRead()
	require.Error(t, err)
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
	require.Contains(t, strings.ToLower(closeErr.Reason), "previous_response_id")
}

func TestOpenAIResponsesWebSocket_PreviousResponseIDKindLoggedBeforeAcquireFailure(t *testing.T) {

	cache := &httptestkit.ConcurrencyHooks{
		AcquireUserSlotFn: func(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
			return false, errors.New("user slot unavailable")
		},
	}
	h := newOpenAIHandlerForPreviousResponseIDValidation(t, cache)
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	defer func() {
		_ = clientConn.CloseNow()
	}()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(
		`{"type":"response.create","model":"gpt-5.1","stream":false,"previous_response_id":"resp_prev_123"}`,
	))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, _, err = clientConn.Read(readCtx)
	cancelRead()
	require.Error(t, err)
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusInternalError, closeErr.Code)
	require.Contains(t, strings.ToLower(closeErr.Reason), "failed to acquire user concurrency slot")
}

type contentModerationHandlerSettingRepo struct {
	values map[string]string
}

func (r *contentModerationHandlerSettingRepo) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	if value, ok := r.values[key]; ok {
		return &settingscore.Setting{Key: key, Value: value}, nil
	}
	return nil, settingscore.ErrSettingNotFound
}

func (r *contentModerationHandlerSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", settingscore.ErrSettingNotFound
}

func (r *contentModerationHandlerSettingRepo) Set(ctx context.Context, key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}

func (r *contentModerationHandlerSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *contentModerationHandlerSettingRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *contentModerationHandlerSettingRepo) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *contentModerationHandlerSettingRepo) Delete(ctx context.Context, key string) error {
	delete(r.values, key)
	return nil
}

// cyberSessionBlockHandlerCacheStub 模拟已命中的会话屏蔽缓存，并记录读取次数。
type cyberSessionBlockHandlerCacheStub struct {
	blocked   bool
	readCalls int
}

func (s *cyberSessionBlockHandlerCacheStub) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, errors.New("not found")
}

func (s *cyberSessionBlockHandlerCacheStub) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func (s *cyberSessionBlockHandlerCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *cyberSessionBlockHandlerCacheStub) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (s *cyberSessionBlockHandlerCacheStub) SetSessionOwnerGroupID(context.Context, int64, string, string, int64, time.Duration) (bool, error) {
	return true, nil
}

func (s *cyberSessionBlockHandlerCacheStub) GetSessionOwnerGroupID(context.Context, int64, string, string) (int64, error) {
	return 0, errors.New("not found")
}

func (s *cyberSessionBlockHandlerCacheStub) RefreshSessionOwnerTTL(context.Context, int64, string, string, time.Duration) error {
	return nil
}

func (s *cyberSessionBlockHandlerCacheStub) SetCyberSessionBlocked(context.Context, string, time.Duration) error {
	s.blocked = true
	return nil
}

func (s *cyberSessionBlockHandlerCacheStub) IsCyberSessionBlocked(context.Context, string) (bool, error) {
	s.readCalls++
	return s.blocked, nil
}

type contentModerationHandlerTestRepo struct {
	mu            sync.Mutex
	logs          []moderation.ContentModerationLog
	cyberWarnings []moderation.ContentModerationCyberWarning
}

func (r *contentModerationHandlerTestRepo) CreateLog(ctx context.Context, log *moderation.ContentModerationLog) error {
	if log != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.logs = append(r.logs, *log)
	}
	return nil
}

func (r *contentModerationHandlerTestRepo) resetLogs() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = nil
}

func (r *contentModerationHandlerTestRepo) logSnapshot() []moderation.ContentModerationLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]moderation.ContentModerationLog(nil), r.logs...)
}

func (r *contentModerationHandlerTestRepo) ListLogs(ctx context.Context, filter moderation.ContentModerationLogFilter) ([]moderation.ContentModerationLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (r *contentModerationHandlerTestRepo) CountFlaggedByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	return 0, nil
}

func (r *contentModerationHandlerTestRepo) CreateCyberWarning(ctx context.Context, warning *moderation.ContentModerationCyberWarning) error {
	if warning != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.cyberWarnings = append(r.cyberWarnings, *warning)
	}
	return nil
}

func (r *contentModerationHandlerTestRepo) CreateCyberWarningAndApplyUserBan(ctx context.Context, warning *moderation.ContentModerationCyberWarning, policy moderation.ContentModerationCyberWarningPolicy) (bool, error) {
	if warning != nil {
		if warning.ViolationCount <= 0 {
			warning.ViolationCount = 1
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		r.cyberWarnings = append(r.cyberWarnings, *warning)
	}
	return false, nil
}

func (r *contentModerationHandlerTestRepo) ListCyberWarnings(ctx context.Context, filter moderation.ContentModerationCyberWarningFilter) ([]moderation.ContentModerationCyberWarning, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (r *contentModerationHandlerTestRepo) CountCyberWarningsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	return 0, nil
}

func (r *contentModerationHandlerTestRepo) GetCyberSummary(ctx context.Context, filter moderation.ContentModerationCyberWarningFilter) (*moderation.ContentModerationCyberSummary, error) {
	return &moderation.ContentModerationCyberSummary{}, nil
}

func (r *contentModerationHandlerTestRepo) MarkCyberWarningEmailSent(ctx context.Context, id int64) error {
	return nil
}

func (r *contentModerationHandlerTestRepo) CleanupExpiredLogs(ctx context.Context, hitBefore time.Time, nonHitBefore time.Time) (*moderation.ContentModerationCleanupResult, error) {
	return &moderation.ContentModerationCleanupResult{}, nil
}

func TestOpenAIResponsesWebSocket_ContentModerationBlocksFirstFrame(t *testing.T) {

	moderationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/moderations", r.URL.Path)
		_, _ = w.Write([]byte(`{"results":[{"category_scores":{"sexual":0.9}}]}`))
	}))
	defer moderationServer.Close()

	cfg := &moderation.ContentModerationConfig{
		Enabled:      true,
		Mode:         moderation.ContentModerationModePreBlock,
		BaseURL:      moderationServer.URL,
		Model:        "omni-moderation-latest",
		APIKeys:      []string{"sk-test"},
		SampleRate:   100,
		AllGroups:    true,
		BlockMessage: "内容审计测试阻断",
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo,
		repo,
	)
	moderationSvc.Start()
	decision, err := moderationSvc.Check(context.Background(), moderation.ContentModerationCheckInput{
		UserID:   1,
		Endpoint: "/v1/responses",
		Provider: "openai",
		Model:    "gpt-5.5",
		Protocol: moderation.ContentModerationProtocolOpenAIResponses,
		Body:     []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"bad prompt"}]}]}`),
	})
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Eventually(t, func() bool {
		return len(repo.logSnapshot()) == 1
	}, time.Second, 10*time.Millisecond)
	repo.resetLogs()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:    &service.OpenAIGatewayService{},
		Funding:   &admission.FundingAdmission{},
		Keys:      &apikey.APIKeyService{},
		Moderator: moderationSvc,
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(&httptestkit.ConcurrencyHooks{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		), gatewayhttp.SSEPingFormatNone, time.Second),
	})
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	defer func() {
		_ = clientConn.CloseNow()
	}()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{
		"type":"response.create",
		"model":"gpt-5.5",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"bad prompt"}]}]
	}`))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, payload, readErr := clientConn.Read(readCtx)
	cancelRead()
	if readErr == nil {
		require.Contains(t, string(payload), "content_policy_violation")
		require.Contains(t, string(payload), "内容审计测试阻断")
	} else {
		var closeErr coderws.CloseError
		require.ErrorAs(t, readErr, &closeErr)
		require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
		require.Contains(t, closeErr.Reason, "内容审计测试阻断")
	}
	var logs []moderation.ContentModerationLog
	require.Eventually(t, func() bool {
		logs = repo.logSnapshot()
		return len(logs) == 1
	}, time.Second, 10*time.Millisecond)
	require.True(t, logs[0].Flagged)
	require.Equal(t, moderation.ContentModerationActionBlock, logs[0].Action)
	require.Equal(t, "bad prompt", logs[0].InputExcerpt)
}

func TestOpenAIRecordCyberWarning_RecordsStructuredResponseBody(t *testing.T) {

	cfg := moderation.ContentModerationConfig{
		CyberWarningEnabled: true,
		CyberWindowHours:    720,
		AllGroups:           true,
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo, repo)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Moderator: moderationSvc})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAICyberWarningRequestSnapshot(c, moderation.ContentModerationProtocolOpenAIResponses, []byte(`{"model":"gpt-5.1","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"bad cyber prompt"}]}]}`))
	apiKey := &apikey.APIKey{
		ID:     101,
		Name:   "test-key",
		UserID: 1001,
		User:   &identity.User{ID: 1001, Email: "user@example.com"},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2001,
		Name: "openai-1"},
	}

	h.openAIAttemptSupport().RecordOpenAICyberWarning(
		c,
		nil,
		apiKey,
		account,
		"gpt-5.1",
		400,
		[]byte(`{"error":{"message":"This request may pose a cybersecurity risk."}}`),
		"",
	)

	require.Len(t, repo.cyberWarnings, 1)
	warning := repo.cyberWarnings[0]
	require.Equal(t, "user@example.com", warning.UserEmail)
	require.Equal(t, int64(2001), *warning.AccountID)
	require.Equal(t, "/v1/responses", warning.Endpoint)
	require.Equal(t, "bad cyber prompt", warning.PromptExcerpt)
	require.Contains(t, warning.WarningText, "cybersecurity risk")
}

func TestOpenAIRecordCyberWarning_UsesExplicitPromptExcerpt(t *testing.T) {

	cfg := moderation.ContentModerationConfig{
		CyberWarningEnabled: true,
		CyberWindowHours:    720,
		AllGroups:           true,
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo, repo)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Moderator: moderationSvc})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAICyberWarningPromptExcerpt(c, "second turn prompt")

	apiKey := &apikey.APIKey{ID: 101, Name: "test-key", UserID: 1001, User: &identity.User{ID: 1001, Email: "user@example.com"}}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2001, Name: "openai-1"}}

	h.openAIAttemptSupport().RecordOpenAICyberWarningWithPromptExcerpt(
		c,
		nil,
		apiKey,
		account,
		"gpt-5.1",
		400,
		[]byte(`{"error":{"message":"This request may pose a cybersecurity risk."}}`),
		"",
		"first turn prompt",
	)

	require.Len(t, repo.cyberWarnings, 1)
	require.Equal(t, "first turn prompt", repo.cyberWarnings[0].PromptExcerpt)
}

func TestOpenAIRecordCyberWarning_RequestSnapshotUsesCurrentToolOutput(t *testing.T) {

	cfg := moderation.ContentModerationConfig{
		CyberWarningEnabled: true,
		CyberWindowHours:    720,
		AllGroups:           true,
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo, repo)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Moderator: moderationSvc})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAICyberWarningRequestSnapshot(c, moderation.ContentModerationProtocolOpenAIResponses, []byte(`{
		"model":"gpt-5.1",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"latest cyber prompt"}]},
			{"type":"function_call","call_id":"call_1","name":"run_tests","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"done"}
		]
	}`))

	apiKey := &apikey.APIKey{ID: 101, Name: "test-key", UserID: 1001, User: &identity.User{ID: 1001, Email: "user@example.com"}}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2001, Name: "openai-1"}}

	h.openAIAttemptSupport().RecordOpenAICyberWarning(
		c,
		nil,
		apiKey,
		account,
		"gpt-5.1",
		http.StatusOK,
		[]byte(`{"type":"response.failed","error":{"message":"This request has been flagged for potentially high-risk cyber activity."}}`),
		"",
	)

	require.Len(t, repo.cyberWarnings, 1)
	require.Equal(t, "done", repo.cyberWarnings[0].PromptExcerpt)
	require.Equal(t, moderation.ContentModerationSourceTool, repo.cyberWarnings[0].Source)
	require.True(t, repo.cyberWarnings[0].ContentComplete)
	require.Len(t, repo.cyberWarnings[0].InputItems, 1)
	require.Equal(t, "done", repo.cyberWarnings[0].InputItems[0].Text)
	require.Equal(t, http.StatusOK, repo.cyberWarnings[0].UpstreamStatus)
}

func TestOpenAIRecordForwardResultCyberWarning_RecordsWSV2TerminalWarning(t *testing.T) {

	cfg := moderation.ContentModerationConfig{
		CyberWarningEnabled: true,
		CyberWindowHours:    720,
		AllGroups:           true,
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo, repo)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Moderator: moderationSvc})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	apiKey := &apikey.APIKey{
		ID:     101,
		Name:   "test-key",
		UserID: 1001,
		User:   &identity.User{ID: 1001, Email: "user@example.com"},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2001,
		Name: "openai-1"},
	}
	result := &forwardcore.OpenAIResult{
		Model: "gpt-5.1",
		UpstreamWarning: &forwardcore.UpstreamWarning{
			ResponseBody: []byte(`{"type":"response.failed","response":{"error":{"message":"This request may pose a cybersecurity risk."}}}`),
			Message:      "This request may pose a cybersecurity risk.",
		},
	}

	h.openAIAttemptSupport().RecordOpenAICyberWarning(c, nil, apiKey, account, result.Model, result.UpstreamWarning.StatusCode, result.UpstreamWarning.ResponseBody, result.UpstreamWarning.Message)

	require.Len(t, repo.cyberWarnings, 1)
	warning := repo.cyberWarnings[0]
	require.Equal(t, "gpt-5.1", warning.Model)
	require.Equal(t, "user@example.com", warning.UserEmail)
	require.Equal(t, int64(2001), *warning.AccountID)
	require.Contains(t, warning.WarningText, "cybersecurity risk")
}

type openAIHandlerTestWarningError struct {
	warning *forwardcore.UpstreamWarning
	err     error
}

func (e *openAIHandlerTestWarningError) Error() string {
	if e == nil || e.err == nil {
		return "test warning error"
	}
	return e.err.Error()
}

func (e *openAIHandlerTestWarningError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *openAIHandlerTestWarningError) OpenAIUpstreamWarning() *forwardcore.UpstreamWarning {
	if e == nil {
		return nil
	}
	return e.warning
}

func TestOpenAIRecordForwardErrorCyberWarning_RecordsWSV2TerminalWarning(t *testing.T) {

	cfg := moderation.ContentModerationConfig{
		CyberWarningEnabled: true,
		CyberWindowHours:    720,
		AllGroups:           true,
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo, repo)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Moderator: moderationSvc})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	apiKey := &apikey.APIKey{
		ID:     101,
		Name:   "test-key",
		UserID: 1001,
		User:   &identity.User{ID: 1001, Email: "user@example.com"},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2001,
		Name: "openai-1"},
	}
	err = fmt.Errorf("openai ws fallback: missing_final_response: %w", &openAIHandlerTestWarningError{
		warning: &forwardcore.UpstreamWarning{
			ResponseBody: []byte(`{"type":"response.failed","error":{"type":"safety_error","message":"This request has been flagged for potentially high-risk cyber activity."}}`),
			Message:      "This request has been flagged for potentially high-risk cyber activity.",
		},
		err: errors.New("no terminal response payload"),
	})

	recorded := h.openAIAttemptSupport().RecordOpenAIForwardErrorCyberWarning(c, nil, apiKey, account, "gpt-5.1", 502, err)

	require.True(t, recorded)
	require.Len(t, repo.cyberWarnings, 1)
	warning := repo.cyberWarnings[0]
	require.Equal(t, "gpt-5.1", warning.Model)
	require.Equal(t, "user@example.com", warning.UserEmail)
	require.Equal(t, int64(2001), *warning.AccountID)
	require.Equal(t, 502, warning.UpstreamStatus)
	require.Contains(t, warning.WarningText, "high-risk cyber")
}

func TestOpenAIRecordCyberPolicyIfMarked_SkipsSideEffectsOutOfScope(t *testing.T) {

	cfg := moderation.ContentModerationConfig{
		CyberWarningEnabled: true,
		CyberWindowHours:    720,
		AllGroups:           false,
		GroupIDs:            []int64{101},
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo, repo)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Moderator: moderationSvc})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.MarkOpsCyberPolicy(c, moderationflow.Mark{
		Message:        "Request blocked by upstream cyber policy",
		Body:           `{"response":{"error":{"code":"cyber_policy","message":"Request blocked by upstream cyber policy"}}}`,
		UpstreamStatus: http.StatusOK,
	})
	outOfScopeGroupID := int64(202)
	apiKey := &apikey.APIKey{
		ID:      101,
		Name:    "test-key",
		UserID:  1001,
		GroupID: &outOfScopeGroupID,
		User:    &identity.User{ID: 1001, Email: "user@example.com"},
		Group:   &routing.Group{ID: outOfScopeGroupID, Name: "out-of-scope"},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2001, Name: "openai-1"}}

	handled := h.openAIAttemptSupport().RecordCyberPolicyIfMarked(c, apiKey, account, nil, "gpt-5.1", true, "cyber-session-key", routing.ChannelUsageFields{}, "payload-hash")

	require.False(t, handled)
	require.Empty(t, repo.cyberWarnings)
	require.False(t, c.GetBool(gatewayhttp.CyberPolicyRecordedKey))
}

func TestOpenAIRejectCyberSessionBlocked_OnlyChecksRiskControlGroups(t *testing.T) {

	selectedGroupID := int64(101)
	outOfScopeGroupID := int64(202)
	cfg := moderation.ContentModerationConfig{
		AllGroups: false,
		GroupIDs:  []int64{selectedGroupID},
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:          "true",
		moderation.SettingKeyContentModerationConfig:     string(rawCfg),
		moderation.SettingKeyCyberSessionBlockEnabled:    "true",
		moderation.SettingKeyCyberSessionBlockTTLSeconds: "3600",
	}}
	cache := &cyberSessionBlockHandlerCacheStub{blocked: true}
	gatewaySvc := service.NewOpenAIGatewayService(
		nil, nil, cache, nil, nil, nil, nil,
		nil, nil, nil, newOpenAIExecutionCredentialsForTest(nil,
			nil), nil, nil, nil, gatewaytestkit.RuntimeReaders(settingRepo), nil, responseHeaderFilterForTest(nil), nil,
	)
	gatewaySvc.BindCompletionRecorder(newHTTPCompletionFixture(nil, nil, nil,
		nil, nil, nil, nil, true))

	moderationSvc := newHTTPModeration(t, settingRepo, nil)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:    gatewaySvc,
		Moderator: moderationSvc,
	})
	body := []byte(`{"prompt_cache_key":"cyber-scope-session"}`)
	tests := []struct {
		name        string
		groupID     int64
		wantBlocked bool
		wantReads   int
	}{
		{name: "未纳入风控的分组放行", groupID: outOfScopeGroupID, wantBlocked: false, wantReads: 0},
		{name: "已纳入风控的分组保持屏蔽", groupID: selectedGroupID, wantBlocked: true, wantReads: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cache.readCalls = 0
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
			groupID := tc.groupID
			apiKey := &apikey.APIKey{ID: 1001, GroupID: &groupID}

			blocked := h.rejectIfCyberSessionBlocked(c, apiKey, body, "gpt-5.1", gatewayhttp.CyberBlockResponses)

			require.Equal(t, tc.wantBlocked, blocked)
			require.Equal(t, tc.wantReads, cache.readCalls)
			if tc.wantBlocked {
				require.Equal(t, http.StatusForbidden, w.Code)
				require.Contains(t, w.Body.String(), "session_blocked_by_cyber_policy")
			} else {
				require.Empty(t, w.Body.String())
			}
		})
	}
}

func TestOpenAIResponsesWebSocket_PassthroughUsageLogPersistsUserAgentAndReasoningEffort(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload: `{"type":"response.create","model":"gpt-5.4","stream":false,"reasoning":{"effort":"HIGH"}}`,
		userAgent:    testStringPtr("codex_cli_rs/0.125.0 test"),
	})

	require.NotNil(t, got.log.UserAgent)
	require.Equal(t, "codex_cli_rs/0.125.0 test", *got.log.UserAgent)
	require.NotNil(t, got.log.ReasoningEffort)
	require.Equal(t, "high", *got.log.ReasoningEffort)
	require.True(t, got.log.OpenAIWSMode)
}

func TestOpenAIResponsesWebSocket_PassthroughUsageLogInfersReasoningFromInitialRequestModel(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload: `{"type":"response.create","model":"gpt-5.4-xhigh","stream":false}`,
		userAgent:    testStringPtr("codex_cli_rs/0.125.0 mapped"),
		channelMapping: map[string]string{
			"gpt-5.4-xhigh": "gpt-5.4",
		},
	})

	require.Equal(t, "gpt-5.4", gjson.GetBytes(got.upstreamFirstPayload, "model").String(),
		"上游首帧应使用渠道映射后的模型")
	require.NotNil(t, got.log.ReasoningEffort)
	require.Equal(t, "xhigh", *got.log.ReasoningEffort,
		"usage log reasoning effort 必须使用渠道映射前首帧模型后缀推导")
}

func TestOpenAIResponsesWebSocket_StripsPreviousResponseIDWhenStickyPreviousMisses(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload: `{"type":"response.create","model":"gpt-5.4","stream":false,"previous_response_id":"resp_other_group","input":[{"type":"input_text","text":"hello"}]}`,
	})

	require.False(t, gjson.GetBytes(got.upstreamFirstPayload, "previous_response_id").Exists(),
		"跨组 sticky miss 时首包应剥离 previous_response_id，避免上游会话链鉴权失败")
	require.Equal(t, "hello", gjson.GetBytes(got.upstreamFirstPayload, "input.0.text").String())
}

func TestOpenAIResponsesWebSocket_KeepsFunctionCallOutputPreviousResponseIDWhenStickyPreviousMisses(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload: `{"type":"response.create","model":"gpt-5.4","stream":false,"previous_response_id":"resp_tool_chain","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
	})

	require.Equal(t, "resp_tool_chain", gjson.GetBytes(got.upstreamFirstPayload, "previous_response_id").String(),
		"工具续链无法用完整 input 重建，sticky miss 时也应保留 previous_response_id")
}

func TestOpenAIResponsesWebSocket_PassthroughUsageLogLeavesUserAgentNilWhenMissing(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload: `{"type":"response.create","model":"gpt-5.4","stream":false,"reasoning":{"effort":"medium"}}`,
		userAgent:    testStringPtr(""),
	})

	require.Nil(t, got.log.UserAgent, "空入站 User-Agent 不应由上游握手 UA 或默认 UA 兜底")
	require.NotNil(t, got.log.ReasoningEffort)
	require.Equal(t, "medium", *got.log.ReasoningEffort)
}

func newOpenAIHandlerForPreviousResponseIDValidation(t *testing.T, cache *httptestkit.ConcurrencyHooks) *gatewayHTTPEndpointsFixture {
	t.Helper()
	if cache == nil {
		cache = &httptestkit.ConcurrencyHooks{
			AcquireUserSlotFn: func(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
				return true, nil
			},
			AcquireAccountSlotFn: func(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
				return true, nil
			},
		}
	}
	return newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:  &service.OpenAIGatewayService{},
		Funding: &admission.FundingAdmission{},
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		), gatewayhttp.SSEPingFormatNone, time.Second),
	})
}

func newOpenAIWSHandlerTestServer(t *testing.T, h *gatewayHTTPEndpointsFixture, subject authctx.AuthSubject) *httptest.Server {
	t.Helper()
	groupID := int64(2)
	apiKey := &apikey.APIKey{
		ID:      101,
		GroupID: &groupID,
		User:    &identity.User{ID: subject.UserID},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
		c.Set(string(authctx.ContextKeyUser), subject)
		c.Next()
	})
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	return httptest.NewServer(router)
}

type openAIResponsesWSUsageLogCase struct {
	firstPayload   string
	userAgent      *string
	channelMapping map[string]string
}

type openAIResponsesWSUsageLogResult struct {
	log                  *usage.UsageLog
	upstreamFirstPayload []byte
}

type openAIWSUsageHandlerAccountRepoStub struct {
	gatewayprovider.ExecutionAccountStore

	account gatewayprovider.ExecutionAccount
}

func (s *openAIWSUsageHandlerAccountRepoStub) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	if s.account.Record.Platform != platform {
		return nil, nil
	}
	return []gatewayprovider.ExecutionAccount{s.account}, nil
}

func (s *openAIWSUsageHandlerAccountRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	return s.ListSchedulableByPlatform(ctx, platform)
}

func (s *openAIWSUsageHandlerAccountRepoStub) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	if s.account.Record.ID != id {
		return nil, nil
	}
	account := s.account
	return &account, nil
}

type openAIWSFailoverHandlerAccountRepoStub struct {
	gatewayprovider.ExecutionAccountStore

	accounts       []gatewayprovider.ExecutionAccount
	rateLimitedIDs []int64
}

type openAIHTTPPassthroughFailoverUpstream struct {
	httpclient.
		UpstreamTransport
	mu         sync.Mutex
	accountIDs []int64
}

type openAIHTTPPassthroughAuthFailoverUpstream struct {
	httpclient.
		UpstreamTransport
	mu         sync.Mutex
	accountIDs []int64
	statusCode int
}

type openAIHTTPPassthroughSSERateLimitUpstream struct {
	httpclient.
		UpstreamTransport
	mu         sync.Mutex
	accountIDs []int64
}

func (u *openAIHTTPPassthroughFailoverUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temporary upstream failure"}}`)),
	}, nil
}

// DoWithTLS 使故障转移测试桩兼容 fork 的 TLS 指纹上游接口。
func (u *openAIHTTPPassthroughFailoverUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func (u *openAIHTTPPassthroughFailoverUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func (u *openAIHTTPPassthroughAuthFailoverUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	if accountID == 9911 {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_healthy","object":"response","model":"gpt-5.2","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)),
		}, nil
	}
	return &http.Response{
		StatusCode: u.statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"upstream credential rejected"}}`)),
	}, nil
}

// DoWithTLS 使认证故障转移测试桩兼容 fork 的 TLS 指纹上游接口。
func (u *openAIHTTPPassthroughAuthFailoverUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func (u *openAIHTTPPassthroughAuthFailoverUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func (u *openAIHTTPPassthroughSSERateLimitUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	body := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_rate_limited"}}`,
		"",
		"event: response.failed",
		`data: {"type":"response.failed","response":{"id":"resp_rate_limited","status":"failed","error":{"type":"invalid_request_error","code":"rate_limit_exceeded","message":"Concurrency limit exceeded for account, please retry later"}}}`,
		"",
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"Retry-After":  []string{"1"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}, nil
}

// DoWithTLS 使 SSE 限流测试桩兼容 fork 的 TLS 指纹上游接口。
func (u *openAIHTTPPassthroughSSERateLimitUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func (u *openAIHTTPPassthroughSSERateLimitUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func (s *openAIWSFailoverHandlerAccountRepoStub) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	out := make([]gatewayprovider.ExecutionAccount, 0, len(s.accounts))
	for _, account := range s.accounts {
		if account.Record.Platform == platform && account.View().IsSchedulable() {
			out = append(out, account)
		}
	}
	return out, nil
}

func (s *openAIWSFailoverHandlerAccountRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	return s.ListSchedulableByPlatform(ctx, platform)
}

func (s *openAIWSFailoverHandlerAccountRepoStub) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	return s.ListSchedulableByPlatform(ctx, platform)
}

func (s *openAIWSFailoverHandlerAccountRepoStub) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	for _, account := range s.accounts {
		if account.Record.ID == id {
			acc := account
			return &acc, nil
		}
	}
	return nil, nil
}

func (s *openAIWSFailoverHandlerAccountRepoStub) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	s.rateLimitedIDs = append(s.rateLimitedIDs, id)
	for i := range s.accounts {
		if s.accounts[i].Record.ID == id {
			reset := resetAt
			s.accounts[i].Record.RateLimitResetAt = &reset
			break
		}
	}
	return nil
}

type openAIWSUsageHandlerUsageLogRepoStub struct {
	usage.UsageLogRepository
	created chan *usage.UsageLog
}

func (s *openAIWSUsageHandlerUsageLogRepoStub) Create(ctx context.Context, log *usage.UsageLog) (bool, error) {
	if s.created != nil {
		s.created <- log
	}
	return true, nil
}

type openAIWSUsageHandlerChannelRepoStub struct {
	routing.ChannelRepository
	channels       []routing.Channel
	groupPlatforms map[int64]string
}

func (s *openAIWSUsageHandlerChannelRepoStub) ListAll(ctx context.Context) ([]routing.Channel, error) {
	return s.channels, nil
}

func (s *openAIWSUsageHandlerChannelRepoStub) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(groupIDs))
	for _, groupID := range groupIDs {
		if platform := strings.TrimSpace(s.groupPlatforms[groupID]); platform != "" {
			out[groupID] = platform
		}
	}
	return out, nil
}

func TestOpenAIResponses_APIKeyPassthroughPool5xxRetriesThenExhaustsMaxSwitches(t *testing.T) {

	groupID := int64(4203)
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9910, Name: "pool-api-key", Platform: capability.PlatformOpenAI,
			Type: capability.AccountTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Priority: 1,
			Credentials: map[string]any{
				"api_key":                      "sk-pool",
				"base_url":                     "https://api.example.test",
				"pool_mode":                    true,
				"pool_mode_retry_count":        float64(1),
				"pool_mode_retry_status_codes": []any{float64(http.StatusBadGateway)},
			},
			Extra: map[string]any{"openai_passthrough": true}},
		},
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9911, Name: "fallback-api-key", Platform: capability.PlatformOpenAI,
			Type: capability.AccountTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Priority: 2,
			Credentials: map[string]any{
				"api_key":  "sk-fallback",
				"base_url": "https://api.example.test",
			},
			Extra: map[string]any{"openai_passthrough": true}},
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1

	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
	upstream := &openAIHTTPPassthroughFailoverUpstream{}
	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()
	t.Cleanup(billingCacheSvc.Stop)
	completionInput4 := billingtestkit.Calculator(cfg.Default.RateMultiplier, nil, nil)
	completionInput5 := &accountcore.DeferredService{}
	gatewaySvc := service.NewOpenAIGatewayService(
		accountRepo,
		nil,

		nil,
		cfg,
		nil,
		nil, nil,

		upstream,
		nil, completionInput5, newOpenAIExecutionCredentialsForTest(accountRepo,

			nil), nil,
		nil,
		nil,

		nil,
		nil, responseHeaderFilterForTest(cfg), nil,
	)
	gatewaySvc.BindCompletionRecorder(newHTTPCompletionFixture(cfg, nil, completionInput4, billingCacheSvc, completionInput5, nil, nil, true))

	h := newGatewayHTTPEndpointsFromDeps(
		gatewaySvc, scheduler.NewConcurrencyService(nil, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		), newFundingAdmissionFixture(billingCacheSvc, cfg), testkit.NewService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg, nil,
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{"model":"gpt-5.2","input":"hello","stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID: 1803, GroupID: &groupID,
		User:  &identity.User{ID: 1703, Status: billing.StatusActive},
		Group: &routing.Group{ID: groupID, Platform: capability.PlatformOpenAI, Status: billing.StatusActive},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1703, Concurrency: 0})

	h.Responses(c)

	require.Equal(t, []int64{9910, 9910, 9911}, upstream.calls())
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "upstream_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Upstream service temporarily unavailable", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
}

// TestOpenAIResponses_APIKeyPassthroughPoolAuthFailureRetriesThenSwitchesToHealthyAccount 验证认证错误耗尽同号预算后切换账号。
func TestOpenAIResponses_APIKeyPassthroughPoolAuthFailureRetriesThenSwitchesToHealthyAccount(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{name: "401", statusCode: http.StatusUnauthorized},
		{name: "403", statusCode: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			groupID := int64(4203)
			accounts := []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9910, Name: "pool-api-key", Platform: capability.PlatformOpenAI,
					Type: capability.AccountTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Priority: 1,
					Credentials: map[string]any{
						"api_key":                      "sk-pool",
						"base_url":                     "https://api.example.test",
						"pool_mode":                    true,
						"pool_mode_retry_count":        float64(1),
						"pool_mode_retry_status_codes": []any{float64(tt.statusCode)},
					},
					Extra: map[string]any{"openai_passthrough": true}},
				},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9911, Name: "fallback-api-key", Platform: capability.PlatformOpenAI,
					Type: capability.AccountTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Priority: 2,
					Credentials: map[string]any{
						"api_key":  "sk-fallback",
						"base_url": "https://api.example.test",
					},
					Extra: map[string]any{"openai_passthrough": true}},
				},
			}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Gateway.MaxAccountSwitches = 1

			accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
			upstream := &openAIHTTPPassthroughAuthFailoverUpstream{statusCode: tt.statusCode}
			rateLimitSvc := service.NewRateLimitService(accountRepo, nil, cfg, nil)
			billingCacheSvc := newBillingEligibilityFixture(cfg)
			billingCacheSvc.Start()
			t.Cleanup(billingCacheSvc.Stop)
			completionInput6 := billingtestkit.Calculator(cfg.Default.RateMultiplier, nil, nil)
			completionInput7 := &accountcore.DeferredService{}
			gatewaySvc := service.NewOpenAIGatewayService(
				accountRepo,
				nil,

				nil,
				cfg,
				nil,
				nil, rateLimitSvc,

				upstream,
				nil, completionInput7, newOpenAIExecutionCredentialsForTest(accountRepo,

					nil), nil,
				nil,
				nil,

				nil,
				nil, responseHeaderFilterForTest(cfg), nil,
			)
			gatewaySvc.BindCompletionRecorder(newHTTPCompletionFixture(cfg, nil, completionInput6, billingCacheSvc, completionInput7, nil, rateLimitSvc, true))

			h := newGatewayHTTPEndpointsFromDeps(
				gatewaySvc, scheduler.NewConcurrencyService(nil, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

					Event: logging.Event},
				), newFundingAdmissionFixture(billingCacheSvc, cfg), testkit.NewService(nil, nil, nil, nil, nil, nil, cfg),
				nil,
				nil,
				nil,
				nil,
				cfg, nil,
			)

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{"model":"gpt-5.2","input":"hello","stream":false}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
				ID: 1803, GroupID: &groupID,
				User:  &identity.User{ID: 1703, Status: billing.StatusActive},
				Group: &routing.Group{ID: groupID, Platform: capability.PlatformOpenAI, Status: billing.StatusActive},
			})
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1703, Concurrency: 0})

			h.Responses(c)

			require.Equal(t, []int64{9910, 9910, 9911}, upstream.calls())
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, "resp_healthy", gjson.GetBytes(rec.Body.Bytes(), "id").String())
		})
	}
}

func TestOpenAIResponses_APIKeyPassthroughSSERateLimitUsesConfiguredPoolRetry(t *testing.T) {

	groupID := int64(4204)
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9912, Name: "pool-sse-rate-limit", Platform: capability.PlatformOpenAI,
			Type: capability.AccountTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Priority: 1,
			Credentials: map[string]any{
				"api_key":                      "sk-pool",
				"base_url":                     "https://api.example.test",
				"pool_mode":                    true,
				"pool_mode_retry_count":        float64(1),
				"pool_mode_retry_status_codes": []any{float64(http.StatusTooManyRequests)},
			},
			Extra: map[string]any{"openai_passthrough": true}},
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1

	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
	upstream := &openAIHTTPPassthroughSSERateLimitUpstream{}
	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()
	t.Cleanup(billingCacheSvc.Stop)
	completionInput8 := billingtestkit.Calculator(cfg.Default.RateMultiplier, nil, nil)
	completionInput9 := &accountcore.DeferredService{}
	gatewaySvc := service.NewOpenAIGatewayService(
		accountRepo,
		nil,

		nil,
		cfg,
		nil,
		nil, nil,

		upstream,
		nil, completionInput9, newOpenAIExecutionCredentialsForTest(accountRepo,

			nil), nil,
		nil,
		nil,

		nil,
		nil, responseHeaderFilterForTest(cfg), nil,
	)
	gatewaySvc.BindCompletionRecorder(newHTTPCompletionFixture(cfg, nil, completionInput8, billingCacheSvc, completionInput9, nil, nil, true))

	h := newGatewayHTTPEndpointsFromDeps(
		gatewaySvc, scheduler.NewConcurrencyService(nil, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		), newFundingAdmissionFixture(billingCacheSvc, cfg), testkit.NewService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg, nil,
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{"model":"gpt-5.2","input":"hello","stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID: 1804, GroupID: &groupID,
		User:  &identity.User{ID: 1704, Status: billing.StatusActive},
		Group: &routing.Group{ID: groupID, Platform: capability.PlatformOpenAI, Status: billing.StatusActive},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1704, Concurrency: 0})

	h.Responses(c)

	require.Equal(t, []int64{9912, 9912}, upstream.calls())
	require.Empty(t, accountRepo.rateLimitedIDs)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, "1", rec.Header().Get("Retry-After"))
	require.Equal(t, "rate_limit_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Upstream rate limit exceeded, please retry later", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
}

func TestOpenAIResponsesWebSocket_FailoverOnUpstreamUsageLimitEvent(t *testing.T) {

	firstHitCh := make(chan []byte, 1)
	secondHitCh := make(chan []byte, 1)

	firstUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, payload, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr == nil {
			firstHitCh <- payload
		}

		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		_ = conn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"error","error":{"code":"rate_limit_exceeded","type":"usage_limit_reached","message":"The usage limit has been reached"}}`))
		cancelWrite()
	}))
	defer firstUpstream.Close()

	secondUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, payload, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr == nil {
			secondHitCh <- payload
		}

		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		_ = conn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_ws_failover_ok","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`))
		cancelWrite()
		_ = conn.Close(coderws.StatusNormalClosure, "done")
	}))
	defer secondUpstream.Close()

	groupID := int64(4202)
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9902,
			Name:        "openai-ws-rate-limited",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
			Credentials: map[string]any{
				"api_key":  "sk-first",
				"base_url": firstUpstream.URL,
			},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
				"openai_apikey_responses_websockets_v2_mode":    accountcore.OpenAIWSIngressModePassthrough,
			}},
		},
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9903,
			Name:        "openai-ws-healthy",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    2,
			Credentials: map[string]any{
				"api_key":  "sk-second",
				"base_url": secondUpstream.URL,
			},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
				"openai_apikey_responses_websockets_v2_mode":    accountcore.OpenAIWSIngressModePassthrough,
			}},
		},
	}

	cfg := &config.Config{}
	cfg.RunMode = config.RunModeSimple
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	cfg.Gateway.MaxAccountSwitches = 3

	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
	rateLimitSvc := service.NewRateLimitService(accountRepo, nil, cfg, nil)
	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()
	completionInput10 := billingtestkit.Calculator(cfg.Default.RateMultiplier, nil, nil)
	completionInput11 := &accountcore.DeferredService{}
	gatewaySvc := service.NewOpenAIGatewayService(
		accountRepo,
		nil,

		nil,
		cfg,
		nil,
		nil, rateLimitSvc,

		nil,
		nil, completionInput11, newOpenAIExecutionCredentialsForTest(accountRepo,

			nil), nil,
		nil,
		nil,

		nil,
		nil, responseHeaderFilterForTest(cfg), nil,
	)
	gatewaySvc.BindCompletionRecorder(newHTTPCompletionFixture(cfg, nil, completionInput10, billingCacheSvc, completionInput11, nil, rateLimitSvc, true))

	cache := &httptestkit.ConcurrencyHooks{
		AcquireUserSlotFn: func(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
			return true, nil
		},
		AcquireAccountSlotFn: func(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
			return true, nil
		},
	}
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:  gatewaySvc,
		Funding: newFundingAdmissionFixture(billingCacheSvc, cfg),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		), gatewayhttp.SSEPingFormatNone, time.Second),
		MaxSwitches: 3,
	})

	apiKey := &apikey.APIKey{
		ID:      1802,
		GroupID: &groupID,
		User:    &identity.User{ID: 1702, Status: billing.StatusActive},
		Group:   &routing.Group{ID: groupID, Platform: capability.PlatformOpenAI, Status: billing.StatusActive},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	handlerServer := httptest.NewServer(router)
	defer handlerServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(
		dialCtx,
		"ws"+strings.TrimPrefix(handlerServer.URL, "http")+"/openai/v1/responses",
		&coderws.DialOptions{CompressionMode: coderws.CompressionContextTakeover},
	)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false}`))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	_, event, err := clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
	require.Equal(t, "resp_ws_failover_ok", gjson.GetBytes(event, "response.id").String())

	select {
	case <-firstHitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("等待第一个上游收到首帧超时")
	}
	select {
	case <-secondHitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("等待第二个上游收到重放首帧超时")
	}
	require.Equal(t, []int64{int64(9902)}, accountRepo.rateLimitedIDs)
}

func TestOpenAIResponsesWebSocket_FirstOutputTimeoutWithoutDownstreamReusesClientForOneFailover(t *testing.T) {

	firstHitCh := make(chan []byte, 1)
	secondHitCh := make(chan []byte, 1)
	var firstConnections atomic.Int32
	var secondConnections atomic.Int32

	firstUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstConnections.Add(1)
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, payload, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr == nil {
			firstHitCh <- payload
		}

		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer firstUpstream.Close()

	secondUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondConnections.Add(1)
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, payload, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr == nil {
			secondHitCh <- payload
		}

		for _, event := range []string{
			`{"type":"response.created","response":{"id":"resp_ws_timeout_b","model":"gpt-5.1"}}`,
			`{"type":"response.output_text.delta","response_id":"resp_ws_timeout_b","delta":"recovered"}`,
			`{"type":"response.completed","response":{"id":"resp_ws_timeout_b","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`,
		} {
			writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
			writeErr := conn.Write(writeCtx, coderws.MessageText, []byte(event))
			cancelWrite()
			if writeErr != nil {
				return
			}
		}
		readCtx, cancelRead = context.WithTimeout(r.Context(), 3*time.Second)
		_, _, _ = conn.Read(readCtx)
		cancelRead()
	}))
	defer secondUpstream.Close()

	groupID := int64(4212)
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9912,
			Name:        "openai-ws-first-semantic-timeout",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
			Credentials: map[string]any{"api_key": "sk-first", "base_url": firstUpstream.URL},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
				"openai_apikey_responses_websockets_v2_mode":    accountcore.OpenAIWSIngressModePassthrough,
			}},
		},
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9913,
			Name:        "openai-ws-failover-healthy",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    2,
			Credentials: map[string]any{"api_key": "sk-second", "base_url": secondUpstream.URL},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
				"openai_apikey_responses_websockets_v2_mode":    accountcore.OpenAIWSIngressModePassthrough,
			}},
		},
	}

	cfg := &config.Config{}
	cfg.RunMode = config.RunModeSimple
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 1
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 3
	cfg.Gateway.MaxAccountSwitches = 3

	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
	rateLimitSvc := service.NewRateLimitService(accountRepo, nil, cfg, nil)
	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()
	completionInput12 := billingtestkit.Calculator(cfg.Default.RateMultiplier, nil, nil)
	completionInput13 := &accountcore.DeferredService{}
	gatewaySvc := service.NewOpenAIGatewayService(
		accountRepo, nil, nil, cfg, nil, nil, rateLimitSvc,
		nil, nil, completionInput13, newOpenAIExecutionCredentialsForTest(accountRepo,
			nil), nil, nil, nil, nil, nil, responseHeaderFilterForTest(cfg), nil,
	)
	gatewaySvc.BindCompletionRecorder(newHTTPCompletionFixture(cfg, nil, completionInput12, billingCacheSvc, completionInput13, nil, rateLimitSvc, true))

	cache := &httptestkit.ConcurrencyHooks{
		AcquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
		AcquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) {
			return true, nil
		},
	}
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:  gatewaySvc,
		Funding: newFundingAdmissionFixture(billingCacheSvc, cfg),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		), gatewayhttp.SSEPingFormatNone, time.Second),
		MaxSwitches: 3,
	})

	apiKey := &apikey.APIKey{
		ID:      1812,
		GroupID: &groupID,
		User:    &identity.User{ID: 1712, Status: billing.StatusActive},
		Group:   &routing.Group{ID: groupID, Platform: capability.PlatformOpenAI, Status: billing.StatusActive},
	}
	handlerDone := make(chan struct{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		h.ResponsesWebSocket(c)
		close(handlerDone)
	})
	handlerServer := httptest.NewServer(router)
	defer handlerServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(
		dialCtx,
		"ws"+strings.TrimPrefix(handlerServer.URL, "http")+"/openai/v1/responses",
		&coderws.DialOptions{CompressionMode: coderws.CompressionContextTakeover},
	)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false}`))
	cancelWrite()
	require.NoError(t, err)

	var eventTypes []string
	readCtx, cancelRead := context.WithTimeout(context.Background(), 6*time.Second)
	for {
		_, event, readErr := clientConn.Read(readCtx)
		require.NoError(t, readErr)
		eventType := gjson.GetBytes(event, "type").String()
		eventTypes = append(eventTypes, eventType)
		if eventType == "response.completed" {
			require.Equal(t, "resp_ws_timeout_b", gjson.GetBytes(event, "response.id").String())
			break
		}
	}
	cancelRead()
	require.Contains(t, eventTypes, "response.output_text.delta")
	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))

	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket handler did not finish after healthy failover turn")
	}
	select {
	case <-firstHitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("first upstream did not receive replayable request")
	}
	select {
	case <-secondHitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("second upstream did not receive replayed request")
	}
	require.Equal(t, int32(1), firstConnections.Load())
	require.Equal(t, int32(1), secondConnections.Load())
	require.NotContains(t, accountRepo.rateLimitedIDs, int64(9913), "healthy failover account must not be penalized")
}

func runOpenAIResponsesWebSocketUsageLogCase(t *testing.T, tc openAIResponsesWSUsageLogCase) openAIResponsesWSUsageLogResult {
	t.Helper()

	upstreamPayloadCh := make(chan []byte, 1)
	upstreamErrCh := make(chan error, 1)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{
			CompressionMode: coderws.CompressionContextTakeover,
		})
		if err != nil {
			upstreamErrCh <- err
			return
		}
		defer func() {
			_ = conn.CloseNow()
		}()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, payload, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr != nil {
			upstreamErrCh <- readErr
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			upstreamErrCh <- errors.New("unexpected upstream websocket message type")
			return
		}
		upstreamPayloadCh <- payload

		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		writeErr := conn.Write(writeCtx, coderws.MessageText, []byte(
			`{"type":"response.completed","response":{"id":"resp_usage_e2e","model":"gpt-5.4","usage":{"input_tokens":2,"output_tokens":1}}}`,
		))
		cancelWrite()
		if writeErr != nil {
			upstreamErrCh <- writeErr
			return
		}
		_ = conn.Close(coderws.StatusNormalClosure, "done")
		upstreamErrCh <- nil
	}))
	defer upstreamServer.Close()

	groupID := int64(4201)
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9901,
		Name:        "openai-ws-passthrough-usage-e2e",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": upstreamServer.URL,
		},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
			"openai_apikey_responses_websockets_v2_mode":    accountcore.OpenAIWSIngressModePassthrough,
		}},
	}

	cfg := &config.Config{}
	cfg.RunMode = config.RunModeSimple
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	accountRepo := &openAIWSUsageHandlerAccountRepoStub{account: account}
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *usage.UsageLog, 1)}

	var channelSvc *routing.ChannelService
	if len(tc.channelMapping) > 0 {
		channelSvc = routing.NewChannelService(&openAIWSUsageHandlerChannelRepoStub{
			channels: []routing.Channel{{
				ID:           7701,
				Name:         "openai-ws-e2e-channel",
				Status:       billing.StatusActive,
				GroupIDs:     []int64{groupID},
				ModelMapping: map[string]map[string]string{capability.PlatformOpenAI: tc.channelMapping},
			}},
			groupPlatforms: map[int64]string{groupID: capability.PlatformOpenAI},
		}, nil, routing.ChannelOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation},
		)
	}

	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()
	completionInput14 := billingtestkit.Calculator(cfg.Default.RateMultiplier, nil, nil)
	completionInput15 := &accountcore.DeferredService{}
	gatewaySvc := service.NewOpenAIGatewayService(
		accountRepo,
		usageRepo,

		nil,
		cfg,
		nil,
		nil, nil,

		nil,
		nil, completionInput15, newOpenAIExecutionCredentialsForTest(accountRepo,

			nil), nil,
		nil,
		channelSvc,

		nil,
		nil, responseHeaderFilterForTest(cfg), nil,

		// 用户平台配额仓库
	)
	gatewaySvc.BindCompletionRecorder(newHTTPCompletionFixture(cfg, usageRepo, completionInput14, billingCacheSvc, completionInput15, channelSvc, nil, true))

	cache := &httptestkit.ConcurrencyHooks{
		AcquireUserSlotFn: func(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
			return true, nil
		},
		AcquireAccountSlotFn: func(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
			return true, nil
		},
	}
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:  gatewaySvc,
		Funding: newFundingAdmissionFixture(billingCacheSvc, cfg),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		), gatewayhttp.SSEPingFormatNone, time.Second),
	})

	apiKey := &apikey.APIKey{
		ID:      1801,
		GroupID: &groupID,
		User:    &identity.User{ID: 1701, Status: billing.StatusActive},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	handlerServer := httptest.NewServer(router)
	defer handlerServer.Close()

	headers := http.Header{}
	if tc.userAgent != nil {
		headers.Set("User-Agent", *tc.userAgent)
	}
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(
		dialCtx,
		"ws"+strings.TrimPrefix(handlerServer.URL, "http")+"/openai/v1/responses",
		&coderws.DialOptions{HTTPHeader: headers, CompressionMode: coderws.CompressionContextTakeover},
	)
	cancelDial()
	require.NoError(t, err)
	defer func() {
		_ = clientConn.CloseNow()
	}()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(tc.firstPayload))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, event, err := clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
	_ = clientConn.Close(coderws.StatusNormalClosure, "done")

	var usageLog *usage.UsageLog
	select {
	case usageLog = <-usageRepo.created:
		require.NotNil(t, usageLog)
	case <-time.After(3 * time.Second):
		t.Fatal("等待 WebSocket usage log 写入超时")
	}

	var upstreamFirstPayload []byte
	select {
	case upstreamFirstPayload = <-upstreamPayloadCh:
	case <-time.After(3 * time.Second):
		t.Fatal("等待上游 WebSocket 首帧超时")
	}

	select {
	case upstreamErr := <-upstreamErrCh:
		require.NoError(t, upstreamErr)
	case <-time.After(3 * time.Second):
		t.Fatal("等待上游 WebSocket 结束超时")
	}

	return openAIResponsesWSUsageLogResult{
		log:                  usageLog,
		upstreamFirstPayload: upstreamFirstPayload,
	}
}

func testStringPtr(v string) *string {
	return &v
}
