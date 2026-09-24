package messageforward

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/stretchr/testify/require"
)

func TestGatewayService_AnthropicAPIKeyPassthrough_ForwardDirect_NonStreamingSuccess(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := newPrivateHTTPFixture(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	upstreamJSON := `{"id":"msg_1","type":"message","usage":{"input_tokens":12,"output_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":3},"cached_tokens":4}}`
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"x-request-id": []string{"rid-nonstream"},
			},
			Body: io.NopCloser(strings.NewReader(upstreamJSON)),
		},
	}
	svc := newPrivateRuntimeFixture(
		&Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728}, Dependencies{Transport: upstream, Health: newPrivateHealthFixture()}, nil,
	)

	result, err := passthroughFixture(svc, context.Background(), c, newAnthropicAPIKeyAccountForTest(), body, "claude-3-5-sonnet-latest", "claude-3-5-sonnet-latest", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 5, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
	require.Equal(t, upstreamJSON, rec.Body.String())
}

func TestGatewayService_AnthropicAPIKeyPassthrough_ForwardDirect_InvalidTokenType(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := newPrivateHTTPFixture(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 202,
		Name:     "anthropic-oauth",
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "oauth-token",
		}},
	}
	svc := newPrivateRuntimeFixture(nil, Dependencies{}, nil)

	result, err := passthroughFixture(svc, context.Background(), c, account, []byte(`{}`), "claude-3-5-sonnet-latest", "claude-3-5-sonnet-latest", false, time.Now())
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires apikey token")
}

func TestGatewayService_AnthropicAPIKeyPassthrough_ForwardDirect_UpstreamRequestError(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := newPrivateHTTPFixture(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstream := &anthropicHTTPUpstreamRecorder{
		err: errors.New("dial tcp timeout"),
	}
	svc := newPrivateRuntimeFixture(
		&Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728}, Dependencies{Transport: upstream}, nil,
	)
	account := newAnthropicAPIKeyAccountForTest()

	result, err := passthroughFixture(svc, context.Background(), c, account, []byte(`{"model":"x"}`), "x", "x", false, time.Now())
	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	// 传输层错误交给 handler failover，service 不得写响应。
	require.False(t, c.Writer.Written())
}

func TestGatewayService_AnthropicAPIKeyPassthrough_ForwardDirect_EmptyResponseBody(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := newPrivateHTTPFixture(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"x-request-id": []string{"rid-empty-body"}},
			Body:       nil,
		},
	}
	svc := newPrivateRuntimeFixture(
		&Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728}, Dependencies{Transport: upstream}, nil,
	)

	result, err := passthroughFixture(svc, context.Background(), c, newAnthropicAPIKeyAccountForTest(), []byte(`{"model":"x"}`), "x", "x", false, time.Now())
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty response")
}

func TestGatewayService_AnthropicAPIKeyPassthrough_TransportErrorRecordsOllamaActivity(t *testing.T) {

	deferred, activity := newDeferredActivityRecorder(t)
	upstream := &anthropicHTTPUpstreamRecorder{err: errors.New("dial tcp timeout")}
	svc := newPrivateRuntimeFixture(
		&Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728}, Dependencies{Transport: upstream, Deferred: deferred}, nil,
	)

	ollama := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 601, Name: "ollama-anthropic", Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "k-ollama", "base_url": "https://ollama.com"},
		Extra:       map[string]any{"anthropic_passthrough": true},
		Status:      billing.StatusActive, Schedulable: true},
	}
	other := newAnthropicAPIKeyAccountForTest()
	other.Record.ID = 602

	rec := httptest.NewRecorder()
	c, _ := newPrivateHTTPFixture(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	_, err := passthroughFixture(svc, context.Background(), c, ollama, []byte(`{"model":"x"}`), "x", "x", false, time.Now())
	require.Error(t, err)

	rec2 := httptest.NewRecorder()
	c2, _ := newPrivateHTTPFixture(rec2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	_, err = passthroughFixture(svc, context.Background(), c2, other, []byte(`{"model":"x"}`), "x", "x", false, time.Now())
	require.Error(t, err)

	require.NoError(t, deferred.Stop())
	_, ok := activity.Load(int64(601))
	require.True(t, ok, "Anthropic passthrough transport error on Ollama account must record activity")
	_, ok = activity.Load(int64(602))
	require.False(t, ok, "non-Ollama Anthropic passthrough transport error must not record Ollama activity")
}

func TestGatewayService_AnthropicAPIKeyPassthrough_ContextCanceledSkipsOllamaActivity(t *testing.T) {

	deferred, activity := newDeferredActivityRecorder(t)
	upstream := &anthropicHTTPUpstreamRecorder{err: context.Canceled}
	svc := newPrivateRuntimeFixture(
		&Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728}, Dependencies{Transport: upstream, Deferred: deferred}, nil,
	)
	ollama := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 603, Name: "ollama-canceled", Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "k-ollama", "base_url": "https://ollama.com"},
		Extra:       map[string]any{"anthropic_passthrough": true},
		Status:      billing.StatusActive, Schedulable: true},
	}
	rec := httptest.NewRecorder()
	c, _ := newPrivateHTTPFixture(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	_, err := passthroughFixture(svc, context.Background(), c, ollama, []byte(`{"model":"x"}`), "x", "x", false, time.Now())

	require.Error(t, err)
	require.NoError(t, deferred.Stop())
	_, ok := activity.Load(int64(603))
	require.False(t, ok, "context.Canceled on Anthropic passthrough must not count as Ollama activity")
}

func TestGatewayService_AnthropicAPIKeyPassthrough_Non2xxRecordsOllamaActivity(t *testing.T) {

	deferred, activity := newDeferredActivityRecorder(t)
	// 默认 API Key 账号不会重试或故障转移 400，因此该响应会进入 handleErrorResponse。
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`)),
		},
	}
	svc := newPrivateRuntimeFixture(
		&Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728}, Dependencies{Transport: upstream, Health: newPrivateHealthFixture(), Deferred: deferred}, nil,
	)
	ollama := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 604, Name: "ollama-400", Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "k-ollama", "base_url": "https://ollama.com"},
		Extra:       map[string]any{"anthropic_passthrough": true},
		Status:      billing.StatusActive, Schedulable: true},
	}
	rec := httptest.NewRecorder()
	c, _ := newPrivateHTTPFixture(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	_, _ = passthroughFixture(svc, context.Background(), c, ollama, []byte(`{"model":"x"}`), "x", "x", false, time.Now())

	require.NoError(t, deferred.Stop())
	_, ok := activity.Load(int64(604))
	require.True(t, ok, "Anthropic passthrough non-2xx on Ollama account must record activity via handleErrorResponse")
}
