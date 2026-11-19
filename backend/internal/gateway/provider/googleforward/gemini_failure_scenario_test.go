package googleforward_test

import (
	"context"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// gemini_failure_scenario_test.go compares final failure behavior through the
// public entry points in gemini_attempts.go and gemini_openai.go. Shared fixtures
// capture provider writes, HTTP headers, and diagnostic events in one sequence
// so protocol differences remain observable across both source files.

// geminiFailureTrace records synchronous health and output effects for one attempt.
type geminiFailureTrace struct {
	steps  []string                    // ordered external effect names
	events []ops.OpsUpstreamErrorEvent // captured upstream failure events
}

// geminiFailureOutput captures headers and diagnostics before delegating HTTP output.
type geminiFailureOutput struct {
	googleforward.Output // existing HTTP output boundary
	// trace is shared with the provider store so synchronous output and health
	// effects appear in one sequence during each forwarding invocation.
	trace *geminiFailureTrace
}

// geminiFailureStore captures health writes alongside the protocol output sequence.
type geminiFailureStore struct {
	gatewaytestkit.ErrorPolicyStore // remaining provider store methods
	// trace is shared with the output wrapper; health callbacks append while the
	// same test goroutine executes the provider attempt and terminal presentation.
	trace *geminiFailureTrace
}

// TestGeminiFinalFailureProtocols exercises identical failures through Messages,
// native Gemini, Responses, and Chat. It protects policy writes, diagnostic body
// differences, response-header timing, retry metadata, and the empty failover output.
func TestGeminiFinalFailureProtocols(t *testing.T) {
	const rawBody = `{"error":{"message":"invalid project resource name"},"response":{"error":{"message":"inner failure"}}}`
	const innerBody = `{"error":{"message":"inner failure"}}`
	for _, scenario := range []struct {
		name        string         // failure scenario label
		status      int            // upstream failure status
		oauth       bool           // OAuth response unwrapping eligibility
		credentials map[string]any // additional provider credential values
		health      string         // expected health write name
		raw         bool           // raw body retention expectation
		retry       bool           // same-provider replay expectation
	}{
		{name: "authentication", status: 401, oauth: true, health: "health:error"},
		{name: "project", status: 400, oauth: true},
		{
			name: "custom_match", status: 422, health: "health:error",
			credentials: map[string]any{
				"custom_error_codes_enabled": true,
				"custom_error_codes":         []any{float64(422)},
			},
		},
		{
			name: "custom_miss", status: 503, raw: true,
			credentials: map[string]any{
				"custom_error_codes_enabled": true,
				"custom_error_codes":         []any{float64(429)},
			},
		},
		{name: "pool_default", status: 503, credentials: map[string]any{"pool_mode": true}, raw: true, retry: true},
		{
			name: "pool_override_excludes_default", status: 503, raw: true,
			credentials: map[string]any{
				"pool_mode":                    true,
				"pool_mode_retry_status_codes": []any{float64(429)},
			},
		},
		{
			name: "custom_miss_pool", status: 503, raw: true, retry: true,
			credentials: map[string]any{
				"pool_mode":                    true,
				"pool_mode_retry_status_codes": []any{float64(503)},
				"custom_error_codes_enabled":   true,
				"custom_error_codes":           []any{float64(429)},
			},
		},
		{
			name: "temporary", status: 503, oauth: true, health: "health:model",
			credentials: map[string]any{
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{map[string]any{
					"error_code":       float64(503),
					"keywords":         []any{"inner failure"},
					"duration_minutes": float64(10),
				}},
			},
		},
	} {
		for _, client := range []string{"messages", "native", "responses", "chat"} {
			t.Run(scenario.name+"/"+client, func(t *testing.T) {
				trace := &geminiFailureTrace{}
				store := &geminiFailureStore{trace: trace}
				cfg := &googleforward.Options{Configured: true, LogErrorBody: true}
				transport := &geminiCompatHTTPUpstreamStub{response: &http.Response{
					StatusCode: scenario.status,
					Header:     http.Header{"X-Goog-Request-Id": {"failure-request"}, "Retry-After": {"30"}},
					Body:       io.NopCloser(strings.NewReader(rawBody)),
				}}
				svc := newGeminiFixture(geminiDependencies{
					httpUpstream:   transport,
					tokenProvider:  newGeminiTokenSourceForTest(),
					providerRepo:   store,
					cfg:            cfg,
					healthObserver: newUpstreamHealthForTest(store, cfg, nil, providercore.HealthOptions{}, nil),
				})
				provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 800, Name: "failure fixture", Platform: capability.PlatformGemini,
					Type:        capability.ProviderTypeOAuth,
					Credentials: map[string]any{"access_token": "test-token", "oauth_type": "ai_studio"},
				}}
				if !scenario.oauth {
					provider.Record.Type = capability.ProviderTypeAPIKey
					provider.Record.Credentials["api_key"] = "test-key"
				}
				maps.Copy(provider.Record.Credentials, scenario.credentials)
				c, recorder := newGeminiNativeTestContext(t)
				output := &geminiFailureOutput{Output: gatewayhttp.NewGoogleBoundary(c, svc.Options, false), trace: trace}
				result, err := forwardGeminiFailureClient(svc, output, provider, client)
				require.Nil(t, result)
				var failure *forwardcore.UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.Equal(t, 1, transport.calls)
				require.Equal(t, scenario.status, failure.StatusCode)
				require.Equal(t, capability.PlatformGemini, failure.Platform)
				require.Equal(t, scenario.retry, failure.RetryableOnSameProvider)
				require.Zero(t, failure.SameProviderRetryDelay)
				require.Equal(t, "30", http.Header(failure.ResponseHeaders).Get("Retry-After"))
				require.Empty(t, http.Header(failure.ResponseHeaders).Get("X-TokenRouter-Internal-Temp-Policy-Applied"))
				require.Zero(t, recorder.Body.Len())
				require.False(t, c.Writer.Written())
				wantBody, wantMessage := innerBody, "inner failure"
				if client == "messages" || scenario.raw || !scenario.oauth {
					wantBody, wantMessage = rawBody, "invalid project resource name"
				}
				require.Equal(t, wantBody, string(failure.ResponseBody))
				require.Len(t, trace.events, 1)
				event := trace.events[0]
				require.Equal(t, "failover", event.Kind)
				require.Equal(t, "failure-request", event.UpstreamRequestID)
				require.Equal(t, scenario.status, event.UpstreamStatusCode)
				require.Equal(t, provider.Record.ID, event.ProviderID)
				require.Equal(t, provider.Record.Name, event.ProviderName)
				require.Equal(t, provider.Record.Platform, event.Platform)
				require.Equal(t, wantMessage, event.Message)
				wantDetail := wantBody
				if (client == "responses" || client == "chat") && !scenario.raw {
					wantDetail = ""
				}
				require.Equal(t, wantDetail, event.Detail)
				var wantSteps []string
				if scenario.health != "" && (client == "messages" || scenario.name == "temporary") {
					wantSteps = append(wantSteps, scenario.health)
				}
				// A failed attempt retains correlation in its retry evidence without
				// taking ownership of client headers before a later attempt can deliver.
				require.Empty(t, recorder.Header().Get("X-Request-Id"))
				if scenario.health != "" && client != "messages" && scenario.name != "temporary" {
					wantSteps = append(wantSteps, scenario.health)
				}
				wantSteps = append(wantSteps, "observe:failover")
				require.Equal(t, wantSteps, trace.steps)
				// Returned headers belong to the failure value; later mutation must not
				// rewrite the transport fixture's original response header collection.
				http.Header(failure.ResponseHeaders).Set("Retry-After", "changed")
				require.Equal(t, "30", transport.response.Header.Get("Retry-After"))
			})
		}
	}
}

// TestGeminiNativeCountFallbackPrecedesFinalPolicy returns insufficient OAuth
// scope for a Google account. Native counting still returns an estimate with
// empty usage before final policy can persist an authentication failure.
func TestGeminiNativeCountFallbackPrecedesFinalPolicy(t *testing.T) {
	trace := &geminiFailureTrace{}
	store := &geminiFailureStore{trace: trace}
	cfg := &googleforward.Options{}
	transport := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: 403,
		Header: http.Header{
			"X-Request-Id":     {"count-request"},
			"Www-Authenticate": {`Bearer error="insufficient_scope"`},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"insufficient authentication scopes"}}`)),
	}}
	svc := newGeminiFixture(geminiDependencies{
		httpUpstream:   transport,
		tokenProvider:  newGeminiTokenSourceForTest(),
		providerRepo:   store,
		cfg:            cfg,
		healthObserver: newUpstreamHealthForTest(store, cfg, nil, providercore.HealthOptions{}, nil),
	})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
		LoadLocation: time.LoadLocation,
		ID:           801,
		Platform:     capability.PlatformGemini,
		Type:         capability.ProviderTypeOAuth,
		Credentials:  map[string]any{"access_token": "test-token", "oauth_type": "ai_studio"},
	}}
	c, recorder := newGeminiNativeTestContext(t)
	output := &geminiFailureOutput{Output: gatewayhttp.NewGoogleBoundary(c, svc.Options, false), trace: trace}
	result, err := svc.ForwardNative(
		context.Background(), output, provider, "gemini-2.5-flash", "countTokens", false,
		[]byte(`{"contents":[{"parts":[{"text":"hello world"}]}]}`),
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "count-request", result.RequestID)
	require.Zero(t, result.Usage)
	require.Equal(t, 1, transport.calls)
	require.Equal(t, []string{"header:x-request-id"}, trace.steps)
	require.Empty(t, trace.events)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"totalTokens":3}`, recorder.Body.String())
}

// TestGeminiFinalTerminalProtocols presents a nonretryable status through every
// public protocol entry. Pool responses retain their protocol body handling,
// while custom-code misses render a generic error without health writes.
func TestGeminiFinalTerminalProtocols(t *testing.T) {
	const rawBody = `{"error":{"message":"outer invalid schema"},"response":{"error":{"message":"inner invalid schema"}}}`
	for _, policy := range []string{"ordinary", "pool", "custom_miss"} {
		for _, client := range []string{"messages", "native", "responses", "chat"} {
			t.Run(policy+"/"+client, func(t *testing.T) {
				trace := &geminiFailureTrace{}
				store := &geminiFailureStore{trace: trace}
				cfg := &googleforward.Options{Configured: true, LogErrorBody: true}
				transport := &geminiCompatHTTPUpstreamStub{response: &http.Response{
					StatusCode: 400,
					Header:     http.Header{"X-Request-Id": {"terminal-request"}},
					Body:       io.NopCloser(strings.NewReader(rawBody)),
				}}
				svc := newGeminiFixture(geminiDependencies{
					httpUpstream:   transport,
					tokenProvider:  newGeminiTokenSourceForTest(),
					providerRepo:   store,
					cfg:            cfg,
					healthObserver: newUpstreamHealthForTest(store, cfg, nil, providercore.HealthOptions{}, nil),
				})
				provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           802,
					Platform:     capability.PlatformGemini,
					Type:         capability.ProviderTypeOAuth,
					Credentials:  map[string]any{"access_token": "test-token", "oauth_type": "ai_studio"},
				}}
				if policy != "ordinary" {
					provider.Record.Type = capability.ProviderTypeAPIKey
					provider.Record.Credentials["api_key"] = "test-key"
				}
				if policy == "pool" {
					provider.Record.Credentials["pool_mode"] = true
				}
				if policy == "custom_miss" {
					provider.Record.Credentials["custom_error_codes_enabled"] = true
					provider.Record.Credentials["custom_error_codes"] = []any{float64(429)}
				}
				c, recorder := newGeminiNativeTestContext(t)
				output := &geminiFailureOutput{Output: gatewayhttp.NewGoogleBoundary(c, svc.Options, false), trace: trace}
				result, err := forwardGeminiFailureClient(svc, output, provider, client)
				require.Nil(t, result)
				require.Error(t, err)
				var failure *forwardcore.UpstreamFailoverError
				require.NotErrorAs(t, err, &failure)
				require.Equal(t, 1, transport.calls)
				wantCode, wantMessage := 400, "inner invalid schema"
				if policy == "ordinary" && (client == "responses" || client == "chat") {
					wantMessage = "Invalid request"
				}
				if client == "messages" || policy == "pool" {
					wantMessage = "outer invalid schema"
				}
				if policy == "custom_miss" {
					wantCode, wantMessage = 500, "Upstream gateway error"
				}
				require.Equal(t, wantCode, recorder.Code)
				require.Contains(t, recorder.Body.String(), wantMessage)
				if client == "native" && policy != "custom_miss" {
					wantBody := `{"error":{"message":"inner invalid schema"}}`
					if policy == "pool" {
						wantBody = rawBody
					}
					require.JSONEq(t, wantBody, recorder.Body.String())
				}
				for _, step := range trace.steps {
					require.NotContains(t, step, "health:")
				}
				require.True(t, c.Writer.Written())
			})
		}
	}
}

// forwardGeminiFailureClient invokes a public forwarding entry with its matching
// request format. The caller owns provider configuration and captures all output
// through the supplied synchronous boundary during this invocation.
func forwardGeminiFailureClient(
	svc *googleforward.Gemini,
	output googleforward.Output,
	provider *gatewayprovider.ExecutionProvider,
	client string,
) (*forwardcore.MessagesResult, error) {
	switch client {
	case "messages":
		return svc.Forward(context.Background(), output, provider,
			[]byte(`{"model":"gemini-2.5-flash","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`),
		)
	case "native":
		return svc.ForwardNative(
			context.Background(), output, provider, "gemini-2.5-flash", "generateContent", false,
			[]byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`),
		)
	case "responses":
		return svc.ForwardAsResponses(context.Background(), output, provider, []byte(`{"model":"gemini-2.5-flash","input":"hi"}`), nil)
	default:
		return svc.ForwardAsChatCompletions(context.Background(), output, provider,
			[]byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`),
		)
	}
}

// Header records the outbound header operation before delegating its assignment.
func (o *geminiFailureOutput) Header(key, value string) {
	o.trace.steps = append(o.trace.steps, "header:"+key)
	o.Output.Header(key, value)
}

// Observe captures failure metadata before forwarding the diagnostic event downstream.
func (o *geminiFailureOutput) Observe(event ops.OpsUpstreamErrorEvent) {
	o.trace.steps = append(o.trace.steps, "observe:"+event.Kind)
	o.trace.events = append(o.trace.events, event)
	o.Output.Observe(event)
}

// SetError records an attempted provider error write for ordering assertions.
func (s *geminiFailureStore) SetError(context.Context, int64, string) error {
	s.trace.steps = append(s.trace.steps, "health:error")
	return nil
}

// SetModelRateLimit records an attempted model cooldown write for ordering assertions.
func (s *geminiFailureStore) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	s.trace.steps = append(s.trace.steps, "health:model")
	return nil
}

// SetTempUnschedulable records a provider pause write for ordering assertions.
func (s *geminiFailureStore) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	s.trace.steps = append(s.trace.steps, "health:temporary")
	return nil
}

// SetRateLimited records a provider quota write for ordering assertions.
func (s *geminiFailureStore) SetRateLimited(context.Context, int64, time.Time) error {
	s.trace.steps = append(s.trace.steps, "health:quota")
	return nil
}
