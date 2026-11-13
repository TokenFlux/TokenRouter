package googleforward_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// errorRulesFixtureRepo supplies fixed presentation rules to Gemini HTTP error
// tests.
type errorRulesFixtureRepo struct {
	// ErrorPassthroughRepository embeds unused methods; these fixtures
	// supply List and expose unexpected rule repository operations through
	// the nil embedded implementation.
	errorpolicy.ErrorPassthroughRepository // unused rule repository methods
	// rules retains the supplied slice and pointers; List returns those
	// references while the presentation service and test share the
	// configured rule values.
	//
	// borrowed error presentation rules
	rules []*errorpolicy.ErrorPassthroughRule
}

// geminiErrorPolicyRepo counts provider policy writes for Gemini forwarding
// assertions.
type geminiErrorPolicyRepo struct {
	gatewaytestkit.ErrorPolicyStore        // remaining provider policy methods
	setErrorCalls                   int    // provider error write count
	setRateLimitedCalls             int    // provider cooldown write count
	setTempCalls                    int    // provider temporary exclusion count
	setModelRateLimitedCalls        int    // model cooldown write count
	lastModelScope                  string // last model cooldown scope
}

// rateLimit429ProviderRepoStub records provider cooldown writes and their
// selected deadline.
type rateLimit429ProviderRepoStub struct {
	gatewaytestkit.ErrorPolicyStore           // remaining provider policy methods
	rateLimitCalls                  int       // provider cooldown write count
	lastRateLimitID                 int64     // last cooled provider identifier
	lastRateLimitReset              time.Time // last cooldown expiration time
}

// TestGeminiNativeStreamFailurePreservesUsage retains accounting through transport loss.
// Sparse usage must survive the failed attempt, while delivered content prevents
// failover and the native client receives exactly one structured stream error.
func TestGeminiNativeStreamFailurePreservesUsage(t *testing.T) {
	payload := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":1}}\n\n" +
		"data: {\"usageMetadata\":{\"candidatesTokenCount\":5}}\n\n"
	stub := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(io.MultiReader(strings.NewReader(payload), iotest.ErrReader(io.ErrUnexpectedEOF))),
	}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: stub, cfg: &googleforward.Options{}})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 203, Platform: capability.PlatformGemini, Type: capability.ProviderTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "fixture-key"}}}
	c, recorder := newGeminiNativeTestContext(t)
	result, err := svc.ForwardNative(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, "gemini-2.5-flash", "streamGenerateContent", true, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.NotNil(t, result)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	var failover *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "partial")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"error"`))
	failure, ok := gatewayhttp.GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, http.StatusBadGateway, failure.IntendedStatus)
}

// TestParseGeminiRateLimitResetTime_QuotaResetDelay_RoundsUp parses a
// fractional quota reset delay away from a Unix-second edge. The resulting
// reset is exactly one second after the captured base timestamp for this
// fixture.
func TestParseGeminiRateLimitResetTime_QuotaResetDelay_RoundsUp(t *testing.T) {
	// Begin the subsecond reset check before the final fifth of a Unix
	// second so rounding plus fixture scheduling remains comparable with
	// the recorded base second.
	for {
		now := time.Now()
		if now.Nanosecond() < 800*1e6 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	baseUnix := time.Now().Unix()
	ts := parseGeminiReset(buildGeminiRateLimitBody("0.1s"))
	require.NotNil(t, ts)
	require.Equal(t, baseUnix+1, *ts, "fractional seconds should be rounded up to the next second")
}

// TestGeminiForwardNative_PoolModeSkipped400PassthroughRealStatus forwards
// native input to a pool provider returning status 400. The failure remains
// terminal and the client receives the exact upstream body and status without
// typed failover.
func TestGeminiForwardNative_PoolModeSkipped400PassthroughRealStatus(t *testing.T) {
	upstreamBody := geminiSkippedTestUpstreamBody()
	svc, _ := newGeminiErrorFixture(http.StatusBadRequest, upstreamBody)
	c, rec := newGeminiNativeTestContext(t)

	result, err := svc.ForwardNative(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiPoolModeAPIKeyProvider(), "gemini-2.5-flash", "generateContent", false, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "池模式 400 不应换号")
	require.Contains(t, err.Error(), "gemini upstream error: 400")
	require.Equal(t, http.StatusBadRequest, rec.Code, "状态码应保真为上游 400")
	require.Equal(t, upstreamBody, rec.Body.String(), "响应体应原样透传")
}

// TestGeminiForwardNative_PoolModeSkipped503Failover forwards native input to a
// pool provider returning status 503. The result is nil, typed failover retains
// that status, and the recorder receives no client body.
func TestGeminiForwardNative_PoolModeSkipped503Failover(t *testing.T) {
	svc, _ := newGeminiErrorFixture(http.StatusServiceUnavailable, `{"error":{"message":"Upstream service temporarily unavailable"}}`)
	c, rec := newGeminiNativeTestContext(t)

	result, err := svc.ForwardNative(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiPoolModeAPIKeyProvider(), "gemini-2.5-flash", "generateContent", false, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "池模式 503 应换号")
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.Zero(t, rec.Body.Len(), "换号场景不应写客户端响应")
}

// TestGeminiForwardNative_CustomCodesMiss400HiddenAs500 returns status 400
// outside an enabled custom-code set. The native client receives a generic
// status-500 message and the upstream fixture detail is absent from output.
func TestGeminiForwardNative_CustomCodesMiss400HiddenAs500(t *testing.T) {
	svc, _ := newGeminiErrorFixture(http.StatusBadRequest, geminiSkippedTestUpstreamBody())
	c, rec := newGeminiNativeTestContext(t)

	result, err := svc.ForwardNative(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiCustomCodesAPIKeyProvider(), "gemini-2.5-flash", "generateContent", false, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not in custom error codes")
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	errObj, ok := got["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Upstream gateway error", errObj["message"])
	require.NotContains(t, rec.Body.String(), geminiSkippedTestUpstreamMsg, "上游细节不应透传给客户端")
}

// TestGeminiForwardNative_CustomCodesMiss500Failover returns server status 500
// outside an enabled custom-code set. The result is nil, typed failover retains
// that status, same-provider replay is false, and client output is empty.
func TestGeminiForwardNative_CustomCodesMiss500Failover(t *testing.T) {
	svc, _ := newGeminiErrorFixture(http.StatusInternalServerError, `{"error":{"message":"internal"}}`)
	c, rec := newGeminiNativeTestContext(t)

	result, err := svc.ForwardNative(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiCustomCodesAPIKeyProvider(), "gemini-2.5-flash", "generateContent", false, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "自定义错误码未命中的 500 应换号")
	require.Equal(t, http.StatusInternalServerError, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameProvider, "非池模式不应同提供商重试")
	require.Zero(t, rec.Body.Len())
}

// TestWriteGeminiMappedError_400KeepsUpstreamMessage presents a mapped Gemini
// status-400 error through Messages HTTP output. The selected status remains
// 400 and the decoded client message equals the captured upstream fixture text.
func TestWriteGeminiMappedError_400KeepsUpstreamMessage(t *testing.T) {
	svc := newGeminiFixture(geminiDependencies{cfg: &googleforward.Options{}})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	err := gatewayhttp.NewGoogleBoundary(c, svc.Options, false).GeminiMappedError(&gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 702, Platform: capability.PlatformGemini}}, http.StatusBadRequest, "req-1", []byte(geminiSkippedTestUpstreamBody()))

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	errObj, ok := got["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, geminiSkippedTestUpstreamMsg, errObj["message"], "应回传上游 message")
}

// TestShouldFailoverGeminiUpstreamError compares authentication, quota, server,
// and ordinary client-error statuses. The classifier accepts each exercised
// replay status and rejects the tested status-400/404/422 inputs.
func TestShouldFailoverGeminiUpstreamError(t *testing.T) {
	svc := newGeminiFixture(geminiDependencies{})

	tests := []struct {
		name       string // table scenario name
		statusCode int    // upstream HTTP status
		expected   bool   // expected provider policy result
	}{
		{"401_failover", 401, true},

		{"403_failover", 403, true},

		{"429_failover", 429, true},

		{"529_failover", 529, true},

		{"500_failover", 500, true},

		{"502_failover", 502, true},

		{"503_failover", 503, true},

		{"400_no_failover", 400, false},

		{"404_no_failover", 404, false},

		{"422_no_failover", 422, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := googleforward.GeminiFailoverForTest(svc, tt.statusCode)
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestCheckErrorPolicy_GeminiProviders compares custom-code and temporary-rule
// configurations across Gemini provider fixtures. The returned policy follows
// tested matches, misses, repeat authorization handling, and custom-code
// precedence.
func TestCheckErrorPolicy_GeminiProviders(t *testing.T) {
	tests := []struct {
		name string // table scenario name
		// selected provider policy fixture
		provider   *gatewayprovider.ExecutionProvider
		statusCode int    // upstream HTTP status
		body       []byte // upstream error fixture bytes
		// expected provider policy result
		expected providercore.ErrorPolicyResult
	}{
		{
			name: "gemini_apikey_custom_codes_hit",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           100,

					Type: capability.ProviderTypeAPIKey,

					Platform: capability.PlatformGemini,

					Credentials: map[string]any{
						"custom_error_codes_enabled": true,
						"custom_error_codes":         []any{float64(429), float64(500)},
					},
				},
			},

			statusCode: 429,

			body: []byte(`{"error":"rate limited"}`),

			expected: providercore.ErrorPolicyCustomMatched,
		},

		{
			name: "gemini_apikey_custom_codes_miss",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           101,

					Type: capability.ProviderTypeAPIKey,

					Platform: capability.PlatformGemini,

					Credentials: map[string]any{
						"custom_error_codes_enabled": true,
						"custom_error_codes":         []any{float64(429)},
					},
				},
			},

			statusCode: 500,

			body: []byte(`{"error":"internal"}`),

			expected: providercore.ErrorPolicyCustomSkipped,
		},

		{
			name: "gemini_apikey_no_custom_codes_returns_none",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           102,

					Type: capability.ProviderTypeAPIKey,

					Platform: capability.PlatformGemini,
				},
			},

			statusCode: 500,

			body: []byte(`{"error":"internal"}`),

			expected: providercore.ErrorPolicyNone,
		},

		{
			name: "gemini_apikey_temp_unschedulable_hit",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           103,

					Type: capability.ProviderTypeAPIKey,

					Platform: capability.PlatformGemini,

					Credentials: map[string]any{
						"temp_unschedulable_enabled": true,

						"temp_unschedulable_rules": []any{
							map[string]any{
								"error_code": float64(503),

								"keywords": []any{"overloaded"},

								"duration_minutes": float64(10),
							},
						},
					},
				},
			},

			statusCode: 503,

			body: []byte(`overloaded service`),

			expected: providercore.ErrorPolicyTempUnscheduled,
		},

		{
			name: "gemini_apikey_temp_unschedulable_401_second_hit_returns_none",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           105,

					Type: capability.ProviderTypeAPIKey,

					Platform: capability.PlatformGemini,

					TempUnschedulableReason: `{"status_code":401,"until_unix":1735689600}`,

					Credentials: map[string]any{
						"temp_unschedulable_enabled": true,

						"temp_unschedulable_rules": []any{
							map[string]any{
								"error_code": float64(401),

								"keywords": []any{"unauthorized"},

								"duration_minutes": float64(10),
							},
						},
					},
				},
			},

			statusCode: 401,

			body: []byte(`unauthorized`),

			expected: providercore.ErrorPolicyNone,
		},

		{
			name: "gemini_custom_codes_override_temp_unschedulable",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           104,

					Type: capability.ProviderTypeAPIKey,

					Platform: capability.PlatformGemini,

					Credentials: map[string]any{
						"custom_error_codes_enabled": true,

						"custom_error_codes": []any{float64(503)},

						"temp_unschedulable_enabled": true,

						"temp_unschedulable_rules": []any{
							map[string]any{
								"error_code": float64(503),

								"keywords": []any{"overloaded"},

								"duration_minutes": float64(10),
							},
						},
					},
				},
			},

			statusCode: 503,

			body: []byte(`overloaded`),

			expected: providercore.ErrorPolicyCustomMatched, // custom codes take precedence

		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &gatewaytestkit.ErrorPolicyStore{}
			svc := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, providercore.HealthOptions{}, nil)

			result := svc.CheckErrorPolicy(context.Background(), gatewayprovider.ExecutionRecord(tt.provider), gatewayprovider.HealthObservationFromContext(context.Background(), tt.statusCode, nil, tt.body, nil))
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestGeminiErrorPolicyIntegration locally simulates policy-result branches
// after querying health policy and default observation. Assertions check
// simulated replay/observer flags and model-scoped writes; the fixture does not
// execute a forwarding entry.
func TestGeminiErrorPolicyIntegration(t *testing.T) {
	tests := []struct {
		name string // table scenario name
		// selected provider policy fixture
		provider       *gatewayprovider.ExecutionProvider
		statusCode     int    // upstream HTTP status
		respBody       []byte // upstream response fixture bytes
		expectFailover bool   // expected simulated failover choice
		// expected simulated observer invocation
		expectHandleError bool
		// expected default status classification
		expectShouldFailover bool
		expectModelScope     string // expected persisted model scope
	}{
		{
			name: "custom_codes_matched_429_failover",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           200,

					Type: capability.ProviderTypeAPIKey,

					Platform: capability.PlatformGemini,

					Credentials: map[string]any{
						"custom_error_codes_enabled": true,
						"custom_error_codes":         []any{float64(429)},
					},
				},
			},

			statusCode: 429,

			respBody: []byte(`{"error":"rate limited"}`),

			expectFailover: true,

			expectHandleError: true,
		},

		{
			name: "custom_codes_skipped_500_no_failover",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           201,

					Type: capability.ProviderTypeAPIKey,

					Platform: capability.PlatformGemini,

					Credentials: map[string]any{
						"custom_error_codes_enabled": true,
						"custom_error_codes":         []any{float64(429)},
					},
				},
			},

			statusCode: 500,

			respBody: []byte(`{"error":"internal"}`),

			expectFailover: false,

			expectHandleError: false,
		},

		{
			name: "temp_unschedulable_matched_failover",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           202,

					Type: capability.ProviderTypeAPIKey,

					Platform: capability.PlatformGemini,

					Credentials: map[string]any{
						"temp_unschedulable_enabled": true,

						"temp_unschedulable_rules": []any{
							map[string]any{
								"error_code": float64(503),

								"keywords": []any{"overloaded"},

								"duration_minutes": float64(10),
							},
						},
					},
				},
			},

			statusCode: 503,

			respBody: []byte(`overloaded`),

			expectFailover: true,

			expectHandleError: false,

			expectModelScope: "gemini-2.5-pro",
		},

		{
			name: "no_policy_429_failover_via_shouldFailover",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           203,

					Type: capability.ProviderTypeAPIKey,

					Platform: capability.PlatformGemini,
				},
			},

			statusCode: 429,

			respBody: []byte(`{"error":"rate limited"}`),

			expectFailover: true,

			expectHandleError: true,

			expectShouldFailover: true,
		},

		{
			name: "no_policy_400_no_failover",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           204,

					Type: capability.ProviderTypeAPIKey,

					Platform: capability.PlatformGemini,
				},
			},

			statusCode: 400,

			respBody: []byte(`{"error":"bad request"}`),

			expectFailover: false,

			expectHandleError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &geminiErrorPolicyRepo{}
			rlSvc := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, providercore.HealthOptions{}, nil)

			svc := newGeminiFixture(geminiDependencies{
				providerRepo:   repo,
				healthObserver: rlSvc,
			})

			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

			// Simulate the selected policy branch after querying
			// Health so observer counts and the local gotFailover
			// flag can be compared with table expectations.
			var handleErrorCalled bool
			var gotFailover bool

			ctx := context.Background()
			statusCode := tt.statusCode
			respBody := tt.respBody
			provider := tt.provider
			headers := http.Header{}

			if svc.Health != nil {
				policy := svc.Health.CheckErrorPolicy(ctx, gatewayprovider.ExecutionRecord(provider), gatewayprovider.HealthObservationFromContext(ctx, statusCode, nil, respBody, []string{"gemini-2.5-pro"}))
				switch policy {
				case providercore.ErrorPolicyCustomSkipped:
					// CustomSkipped ends this local branch
					// simulation before default
					// observation, leaving its failover
					// flag false for the table expectation
					// checked below.
					gotFailover = false
					handleErrorCalled = false
					goto verify
				case providercore.ErrorPolicyCustomMatched:
					svc.Errors.Observe(ctx, gatewayprovider.ExecutionRecord(provider), statusCode, headers, respBody, gatewayprovider.HealthObservationFromContext(ctx, statusCode, headers, respBody, nil))
					handleErrorCalled = true
					gotFailover = true
					goto verify
				case providercore.ErrorPolicyTempUnscheduled:
					handleErrorCalled = false
					gotFailover = true
					goto verify
				}
			}

			// The unmatched-policy simulation observes default
			// health before checking status-based replay, allowing
			// model scope and cooldown assertions to inspect that
			// separate branch.
			svc.Errors.Observe(ctx, gatewayprovider.ExecutionRecord(provider), statusCode, headers, respBody, gatewayprovider.HealthObservationFromContext(ctx, statusCode, headers, respBody, nil))
			handleErrorCalled = true
			if googleforward.GeminiFailoverForTest(svc, statusCode) {
				gotFailover = true
			}

		verify:
			require.Equal(t, tt.expectFailover, gotFailover, "failover mismatch")
			require.Equal(t, tt.expectHandleError, handleErrorCalled, "handleGeminiUpstreamError call mismatch")
			if tt.expectModelScope != "" {
				require.Equal(t, 1, repo.setModelRateLimitedCalls)
				require.Equal(t, tt.expectModelScope, repo.lastModelScope)
				require.Zero(t, repo.setTempCalls)
				require.Zero(t, repo.setRateLimitedCalls, "model temp rule must not be widened into a provider rate limit")
			}

			if tt.expectShouldFailover {
				require.True(t, googleforward.GeminiFailoverForTest(svc, statusCode),
					"shouldFailoverGeminiUpstreamError should return true for status %d", statusCode)
			}
		})
	}
}

// TestGeminiErrorPolicy_NilRateLimitService constructs a Gemini adapter with no
// explicit health policy observer. Status-based replay remains available, and
// the separate default error observer accepts the exercised failure without
// panic.
func TestGeminiErrorPolicy_NilRateLimitService(t *testing.T) {
	svc := newGeminiFixture(geminiDependencies{
		healthObserver: nil,
	})

	// A missing health policy observer leaves status-based failover
	// available. The separate error observer must tolerate that missing
	// dependency.

	ctx := context.Background()
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           300,

			Type: capability.ProviderTypeAPIKey,

			Platform: capability.PlatformGemini,

			Credentials: map[string]any{
				"custom_error_codes_enabled": true,
				"custom_error_codes":         []any{float64(429)},
			},
		},
	}

	if svc.Health != nil {
		t.Fatal("healthObserver should be nil for this test")
	}

	// Status-based failover remains available.
	require.True(t, googleforward.GeminiFailoverForTest(svc, 429))
	require.False(t, googleforward.GeminiFailoverForTest(svc, 400))

	// Health observation tolerates the missing policy observer.
	require.NotPanics(t, func() {
		svc.Errors.Observe(ctx, gatewayprovider.ExecutionRecord(provider), 500, http.Header{}, []byte(`error`), gatewayprovider.HealthObservationFromContext(ctx, 500, http.Header{}, []byte(`error`), nil))
	})
}

// TestHandleGeminiUpstreamError_GoogleOneCapacityExhaustedUsesTierCooldown
// observes Google One capacity exhaustion with a known tier and persistence
// recorder. One provider cooldown write selects the tested five-minute window
// around observation time.
func TestHandleGeminiUpstreamError_GoogleOneCapacityExhaustedUsesTierCooldown(t *testing.T) {
	repo := &rateLimit429ProviderRepoStub{}
	quotaSvc := providercore.NewGeminiQuotaService(providercore.GeminiQuotaOptions{})
	rlSvc := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, providercore.HealthOptions{}, nil)

	svc := newGeminiFixture(geminiDependencies{
		quotaPrecheck: providercore.NewGeminiPrecheck(quotaSvc, nil, providercore.GeminiPrecheckOptions{Now: time.Now, Location: geminiQuotaLocation()}),

		providerRepo: repo,

		healthObserver: rlSvc,
	})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           511,

			Platform: capability.PlatformGemini,

			Type: capability.ProviderTypeOAuth,

			Credentials: map[string]any{
				"oauth_type": "google_one",
				"tier_id":    "google_ai_pro",
			},
		},
	}
	body := []byte(`{"error":{"code":429,"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","domain":"cloudcode-pa.googleapis.com","metadata":{"model":"gemini-3.1-pro-preview"},"reason":"MODEL_CAPACITY_EXHAUSTED"}],"message":"No capacity available for model gemini-3.1-pro-preview on the server","status":"RESOURCE_EXHAUSTED"}}`)

	before := time.Now()
	svc.Errors.Observe(context.Background(), gatewayprovider.ExecutionRecord(provider), http.StatusTooManyRequests, http.Header{}, body, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusTooManyRequests, http.Header{}, body, nil))
	after := time.Now()

	require.Equal(t, 1, repo.rateLimitCalls)
	require.Equal(t, int64(511), repo.lastRateLimitID)
	// Allow observer work and clock sampling around the captured deadline.
	require.WithinDuration(t, before.Add(5*time.Minute), repo.lastRateLimitReset, 2*time.Second)
	require.True(t, repo.lastRateLimitReset.After(before))
	require.True(t, repo.lastRateLimitReset.Before(after.Add(5*time.Minute).Add(2*time.Second)))
}

// TestHandleGeminiUpstreamError_ThirdPartyAPIKeyIgnoresOfficialQuotaMessage
// observes daily-quota-like text on a third-party API key provider. One
// provider cooldown write selects the tested generic five-minute window rather
// than a daily reset.
func TestHandleGeminiUpstreamError_ThirdPartyAPIKeyIgnoresOfficialQuotaMessage(t *testing.T) {
	repo := &rateLimit429ProviderRepoStub{}
	quotaSvc := providercore.NewGeminiQuotaService(providercore.GeminiQuotaOptions{})
	rlSvc := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, providercore.HealthOptions{}, nil)

	svc := newGeminiFixture(geminiDependencies{
		quotaPrecheck: providercore.NewGeminiPrecheck(quotaSvc, nil, providercore.GeminiPrecheckOptions{Now: time.Now, Location: geminiQuotaLocation()}),

		providerRepo: repo,

		healthObserver: rlSvc,
	})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           512,

			Platform: capability.PlatformGemini,

			Type: capability.ProviderTypeAPIKey,

			Credentials: map[string]any{
				providercore.GeminiProviderTypeCredentialKey: providercore.GeminiProviderTypeThirdParty,
			},
		},
	}

	before := time.Now()
	svc.Errors.Observe(context.Background(), gatewayprovider.ExecutionRecord(provider), http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"code":429,"message":"Quota exceeded: 20 requests per day"}}`), gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"code":429,"message":"Quota exceeded: 20 requests per day"}}`), nil))
	after := time.Now()

	require.Equal(t, 1, repo.rateLimitCalls)
	require.Equal(t, int64(512), repo.lastRateLimitID)
	// Allow observer work and clock sampling around the captured deadline.
	require.WithinDuration(t, before.Add(5*time.Minute), repo.lastRateLimitReset, 2*time.Second)
	require.True(t, repo.lastRateLimitReset.After(before))
	require.True(t, repo.lastRateLimitReset.Before(after.Add(5*time.Minute).Add(2*time.Second)))
}

// TestGeminiPoolMode429BypassesLocalRateLimit applies status-429 policy to a
// pool API key provider. The result reports pool bypass and same-provider
// replay, while cooldown, temporary exclusion, and provider error write counts
// stay zero.
func TestGeminiPoolMode429BypassesLocalRateLimit(t *testing.T) {
	repo := &geminiErrorPolicyRepo{}
	healthObserver := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, providercore.HealthOptions{}, nil)

	svc := newGeminiFixture(geminiDependencies{providerRepo: repo, healthObserver: healthObserver})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           520,

			Type: capability.ProviderTypeAPIKey,

			Platform: capability.PlatformGemini,

			Credentials: map[string]any{
				"pool_mode": true,
			},
		},
	}

	decision := googleforward.GeminiPolicyForTest(svc, context.Background(), provider, http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"rate limited"}}`), "gemini-2.5-pro")

	require.Equal(t, providercore.ErrorPolicyPoolBypassed, decision.Policy)
	require.True(t, decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), http.StatusTooManyRequests))
	require.Zero(t, repo.setRateLimitedCalls)
	require.Zero(t, repo.setTempCalls)
	require.Zero(t, repo.setErrorCalls)
}

// TestHandleGeminiUpstreamError_PoolMode429SkipsProviderLimit compares pool
// quota handling, custom-code matches/misses, and a normal provider. Recorded
// cooldown write counts reflect the exercised policy precedence for each
// provider fixture.
func TestHandleGeminiUpstreamError_PoolMode429SkipsProviderLimit(t *testing.T) {
	body := []byte(`{"error":{"code":429,"message":"capacity exhausted"}}`)
	tests := []struct {
		name string // table scenario name
		// selected provider policy fixture
		provider  *gatewayprovider.ExecutionProvider
		wantCalls int // expected provider cooldown writes
	}{
		{
			name: "池模式跳过默认提供商限流",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           530,
					Type:         capability.ProviderTypeAPIKey,
					Platform:     capability.PlatformGemini,

					Credentials: map[string]any{"pool_mode": true},
				},
			},
		},

		{
			name: "自定义错误码命中优先于池模式",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           531,
					Type:         capability.ProviderTypeAPIKey,
					Platform:     capability.PlatformGemini,

					Credentials: map[string]any{
						"pool_mode": true,

						"custom_error_codes_enabled": true,

						"custom_error_codes": []any{float64(http.StatusTooManyRequests)},
					},
				},
			},

			wantCalls: 1,
		},

		{
			name: "自定义错误码未命中跳过提供商限流",

			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           532,
					Type:         capability.ProviderTypeAPIKey,
					Platform:     capability.PlatformGemini,

					Credentials: map[string]any{
						"pool_mode": true,

						"custom_error_codes_enabled": true,

						"custom_error_codes": []any{float64(http.StatusInternalServerError)},
					},
				},
			},
		},

		{
			name: "普通提供商保留默认限流",

			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 533, Type: capability.ProviderTypeAPIKey, Platform: capability.PlatformGemini}},

			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &rateLimit429ProviderRepoStub{}
			svc := newGeminiFixture(geminiDependencies{providerRepo: repo})

			svc.Errors.Observe(context.Background(), gatewayprovider.ExecutionRecord(tt.provider), http.StatusTooManyRequests, http.Header{}, body, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusTooManyRequests, http.Header{}, body, nil))

			require.Equal(t, tt.wantCalls, repo.rateLimitCalls)
		})
	}
}

// TestGeminiCustomNonFailoverStatusStopsScheduling configures status 422 as a
// custom error on a pool provider. Policy stops scheduling, writes provider
// error once, and leaves same-provider replay and default cooldown unavailable.
func TestGeminiCustomNonFailoverStatusStopsScheduling(t *testing.T) {
	repo := &geminiErrorPolicyRepo{}
	healthObserver := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, providercore.HealthOptions{}, nil)

	svc := newGeminiFixture(geminiDependencies{providerRepo: repo, healthObserver: healthObserver})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           521,

			Type: capability.ProviderTypeAPIKey,

			Platform: capability.PlatformGemini,

			Credentials: map[string]any{
				"pool_mode": true,

				"custom_error_codes_enabled": true,

				"custom_error_codes": []any{float64(http.StatusUnprocessableEntity)},
			},
		},
	}

	decision := googleforward.GeminiPolicyForTest(svc, context.Background(), provider, http.StatusUnprocessableEntity, http.Header{}, []byte(`{"error":{"message":"configured"}}`), "gemini-2.5-pro")

	require.Equal(t, providercore.ErrorPolicyCustomMatched, decision.Policy)
	require.True(t, decision.StopScheduling)
	require.False(t, decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), http.StatusUnprocessableEntity))
	require.Equal(t, 1, repo.setErrorCalls)
	require.Zero(t, repo.setRateLimitedCalls)
}

// TestGeminiWriteGeminiMappedError_NoRuleKeepsDefault presents a Gemini schema
// failure with no bound error rule. Messages output uses the tested status-400
// invalid_request_error envelope and generic client-safe message.
func TestGeminiWriteGeminiMappedError_NoRuleKeepsDefault(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := newGeminiFixture(geminiDependencies{})
	respBody := []byte(`{"error":{"code":422,"message":"Invalid schema for field messages","status":"INVALID_ARGUMENT"}}`)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 13, Platform: capability.PlatformGemini, Type: capability.ProviderTypeAPIKey}}

	err := gatewayhttp.NewGoogleBoundary(c, svc.Options, false).GeminiMappedError(provider, http.StatusUnprocessableEntity, "req-2", respBody)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "invalid_request_error", errField["type"])
	assert.Equal(t, "Upstream request failed", errField["message"])
}

// TestGeminiWriteGeminiMappedError_AppliesRuleFor422 binds a schema-error
// presentation rule for upstream status 422. The client receives the configured
// teapot status and custom message under an upstream_error envelope.
func TestGeminiWriteGeminiMappedError_AppliesRuleFor422(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	ruleSvc := newErrorRulesTestService([]*errorpolicy.ErrorPassthroughRule{newNonFailoverPassthroughRule(http.StatusUnprocessableEntity, "invalid schema", http.StatusTeapot, "Gemini上游失败")})
	gatewayhttp.BindErrorPassthroughService(c, ruleSvc)

	svc := newGeminiFixture(geminiDependencies{})
	respBody := []byte(`{"error":{"code":422,"message":"Invalid schema for field messages","status":"INVALID_ARGUMENT"}}`)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 3, Platform: capability.PlatformGemini, Type: capability.ProviderTypeAPIKey}}

	err := gatewayhttp.NewGoogleBoundary(c, svc.Options, false).GeminiMappedError(provider, http.StatusUnprocessableEntity, "req-1", respBody)
	require.Error(t, err)
	assert.Equal(t, http.StatusTeapot, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errField["type"])
	assert.Equal(t, "Gemini上游失败", errField["message"])
}

// TestGeminiWriteGeminiMappedError_SetsResponseCommitted presents an ordinary
// mapped Gemini status-400 failure before output. The HTTP request records
// semantic commitment, protecting outer error handling from issuing another
// response.
func TestGeminiWriteGeminiMappedError_SetsResponseCommitted(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := newGeminiFixture(geminiDependencies{})
	body := []byte(`{"error":{"message":"invalid field"}}`)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 102, Platform: capability.PlatformGemini, Type: capability.ProviderTypeAPIKey}}

	err := gatewayhttp.NewGoogleBoundary(c, svc.Options, false).GeminiMappedError(provider, http.StatusBadRequest, "req-99", body)
	require.Error(t, err)
	assert.True(t, gatewayhttp.IsResponseCommitted(c), "Gemini path must mark response committed")
}

// TestGeminiMessagesCompatServiceForward_OAuthAppliesProviderModelMapping
// forwards Messages through an OAuth provider alias mapping. Completion keeps
// the client model and mapped UpstreamModel, while captured wire bytes carry
// the provider target.
func TestGeminiMessagesCompatServiceForward_OAuthAppliesProviderModelMapping(t *testing.T) {
	upstreamBody := `data: {"response":{"candidates":[{"content":{"parts":[{"text":"hello"}]} ,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2}}}` + "\n\n" +
		"data: [DONE]\n\n"
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK,

		Header: http.Header{"Content-Type": []string{"text/event-stream"}},

		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newGeminiFixture(geminiDependencies{
		tokenProvider: newGeminiTokenSourceForTest(),

		httpUpstream: httpStub,

		cfg: &googleforward.Options{},
	})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           103,

			Platform: capability.PlatformGemini,

			Type: capability.ProviderTypeOAuth,

			Credentials: map[string]any{
				"access_token": "ya29.test-token",

				"project_id": "project-1",

				"model_mapping": map[string]any{
					"group-model": "oauth-upstream-model",
				},
			},

			Concurrency: 1,
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"group-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "group-model", result.Model)
	require.Equal(t, "oauth-upstream-model", result.UpstreamModel)
	require.NotNil(t, httpStub.lastReq)
	sentBody, err := io.ReadAll(httpStub.lastReq.Body)
	require.NoError(t, err)
	require.Equal(t, "oauth-upstream-model", gjson.GetBytes(sentBody, "model").String())
}

// TestGeminiMessagesCompatServiceForwardNative_OAuthAppliesProviderModelMapping
// forwards native input through an OAuth provider alias mapping. Completion
// keeps client and upstream names separate, and the captured envelope carries
// the mapped provider target.
func TestGeminiMessagesCompatServiceForwardNative_OAuthAppliesProviderModelMapping(t *testing.T) {
	upstreamBody := `data: {"response":{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2}}}` + "\n\n" +
		"data: [DONE]\n\n"
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK,

		Header: http.Header{"Content-Type": []string{"text/event-stream"}},

		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newGeminiFixture(geminiDependencies{
		tokenProvider: newGeminiTokenSourceForTest(),

		httpUpstream: httpStub,

		cfg: &googleforward.Options{},
	})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           104,

			Platform: capability.PlatformGemini,

			Type: capability.ProviderTypeOAuth,

			Credentials: map[string]any{
				"access_token": "ya29.test-token",

				"project_id": "project-1",

				"model_mapping": map[string]any{
					"group-model": "oauth-upstream-model",
				},
			},

			Concurrency: 1,
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/group-model:generateContent", bytes.NewReader(body))

	result, err := svc.ForwardNative(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, "group-model", "generateContent", false, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "group-model", result.Model)
	require.Equal(t, "oauth-upstream-model", result.UpstreamModel)
	require.NotNil(t, httpStub.lastReq)
	sentBody, err := io.ReadAll(httpStub.lastReq.Body)
	require.NoError(t, err)
	require.Equal(t, "oauth-upstream-model", gjson.GetBytes(sentBody, "model").String())
}

// TestGeminiMessagesCompatServiceForward_StreamingClosesToolUseBeforeText reads
// a function-call payload followed by text through Messages conversion. Parsed
// output events close tool block zero before opening text block one for the
// later content.
func TestGeminiMessagesCompatServiceForward_StreamingClosesToolUseBeforeText(t *testing.T) {
	upstreamBody := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"query":"weather"}}}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":2}}` + "\n\n" +
		"data: [DONE]\n\n"
	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,

			Header: http.Header{"Content-Type": []string{"text/event-stream"}},

			Body: io.NopCloser(strings.NewReader(upstreamBody)),
		},
	}
	svc := newGeminiFixture(geminiDependencies{
		httpUpstream: httpStub,
		cfg:          &googleforward.Options{},
	})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           103,

			Platform: capability.PlatformGemini,

			Type: capability.ProviderTypeAPIKey,

			Credentials: map[string]any{
				"api_key": "gemini-api-key",
			},

			Concurrency: 1,
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gemini-2.5-flash","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)

	events := parseSSEEventsForTest(t, rec.Body.String())
	toolStart := findSSEEventForTest(events, "content_block_start", 0, "tool_use")
	toolStop := findSSEEventForTest(events, "content_block_stop", 0, "")
	textStart := findSSEEventForTest(events, "content_block_start", 1, "text")
	require.GreaterOrEqual(t, toolStart, 0, "应先开始 tool_use 块")
	require.Greater(t, toolStop, toolStart, "tool_use 块应被关闭")
	require.Greater(t, textStart, toolStop, "文本块必须在 tool_use 块关闭后开始")
}

// TestGeminiToolCallReplayAcrossProtocols obtains parallel same-name calls
// from a Gemini fixture through each compatible client protocol. Replaying
// their results restores the original identifiers and only the first call's
// signature. A different key, provider, or model uses the legacy fallback.
func TestGeminiToolCallReplayAcrossProtocols(t *testing.T) {
	const model = "gemini-2.5-flash"
	const calls = `{"candidates":[{"content":{"parts":[{"functionCall":{"id":"gemini-paris","name":"lookup","args":{"city":"Paris"}},"thoughtSignature":"original-signature"},{"functionCall":{"id":"gemini-london","name":"lookup","args":{"city":"London"}}}]},"finishReason":"STOP"}]}`
	const answer = `{"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}`

	for _, protocol := range []string{"messages", "responses", "chat"} {
		for _, mode := range []string{"buffered", "stream", "collected OAuth stream"} {
			t.Run(protocol+"/"+mode, func(t *testing.T) {
				stream := mode == "stream"
				oauth := mode == "collected OAuth stream"
				stub := &queuedHTTPUpstreamStub{}
				svc := newGeminiFixture(geminiDependencies{
					httpUpstream: stub, cfg: &googleforward.Options{}, tokenProvider: newGeminiTokenSourceForTest(),
				})
				executor := &gatewayhttp.GeminiExecutor{Runtime: svc}
				provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
					ID:           101,
					Platform:     capability.PlatformGemini,
					Type:         capability.ProviderTypeAPIKey,
					Credentials:  map[string]any{"api_key": "fixture-key"},
					LoadLocation: time.LoadLocation,
				}}
				initialBody := fmt.Sprintf(`{"model":%q,"stream":%t,"messages":[{"role":"user","content":"weather"}]}`, model, stream)
				if protocol == "responses" {
					initialBody = fmt.Sprintf(`{"model":%q,"stream":%t,"input":"weather"}`, model, stream)
				}
				contentType, raw := "application/json", calls
				if stream {
					// The second frame adds a signature to an already announced
					// call. The client should still execute each upstream ID once.
					firstFrame := `{"candidates":[{"content":{"parts":[{"functionCall":{"id":"gemini-paris","name":"lookup","args":{"city":"Paris"}}},{"functionCall":{"id":"gemini-london","name":"lookup","args":{"city":"London"}}}]}}]}`
					contentType = "text/event-stream"
					raw = "data: " + firstFrame + "\n\ndata: " + calls + "\n\n"
				}
				answerFrame := answer
				if oauth {
					// The buffered converter must reconstruct the argument object
					// from string fragments and retain the later signature.
					firstFrame := `{"candidates":[{"content":{"parts":[{"functionCall":{"id":"gemini-paris","name":"lookup","args":"{\"city\":"}}]}}]}`
					lastFrame := `{"candidates":[{"content":{"parts":[{"functionCall":{"id":"gemini-paris","name":"lookup","args":"\"Paris\"}"},"thoughtSignature":"original-signature"},{"functionCall":{"id":"gemini-london","name":"lookup","args":{"city":"London"}}}]},"finishReason":"STOP"}]}`
					provider.Record.Type = capability.ProviderTypeOAuth
					provider.Record.Credentials = map[string]any{"access_token": "ya29.fixture", "project_id": "project-1"}
					contentType = "text/event-stream"
					raw = "data: {\"response\":" + firstFrame + "}\n\ndata: {\"response\":" + lastFrame + "}\n\n"
					answerFrame = "data: {\"response\":" + answer + "}\n\n"
				}
				stub.responses = []*http.Response{{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {contentType}},
					Body:       io.NopCloser(strings.NewReader(raw)),
				}}

				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Set("api_key", &apikey.APIKey{ID: 21})
				path := "/v1/" + protocol
				if protocol == "chat" {
					path = "/v1/chat/completions"
				}
				c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(initialBody))
				var err error
				switch protocol {
				case "messages":
					_, err = executor.Forward(t.Context(), c, provider, []byte(initialBody))
				case "responses":
					_, err = executor.ForwardAsResponses(t.Context(), c, provider, []byte(initialBody), nil)
				case "chat":
					_, err = executor.ForwardAsChatCompletions(t.Context(), c, provider, []byte(initialBody))
				}
				require.NoError(t, err)
				require.NotContains(t, rec.Body.String(), "original-signature")
				require.NotContains(t, rec.Body.String(), "gemini-paris")
				var ids []string
				payloads := []string{rec.Body.String()}
				if stream {
					payloads = nil
					for line := range strings.SplitSeq(rec.Body.String(), "\n") {
						if payload, ok := strings.CutPrefix(line, "data: "); ok {
							payloads = append(payloads, payload)
						}
					}
				}
				for _, payload := range payloads {
					var idsInPayload []gjson.Result
					switch {
					case protocol == "messages" && stream:
						idsInPayload = []gjson.Result{gjson.Get(payload, "content_block.id")}
					case protocol == "messages":
						idsInPayload = gjson.Get(payload, "content.#.id").Array()
					case protocol == "responses" && stream:
						if gjson.Get(payload, "type").String() == "response.output_item.added" {
							idsInPayload = []gjson.Result{gjson.Get(payload, "item.call_id")}
						}
					case protocol == "responses":
						idsInPayload = gjson.Get(payload, "output.#.call_id").Array()
					case stream:
						idsInPayload = gjson.Get(payload, "choices.0.delta.tool_calls.#.id").Array()
					default:
						idsInPayload = gjson.Get(payload, "choices.0.message.tool_calls.#.id").Array()
					}
					for _, id := range idsInPayload {
						if id.String() != "" {
							ids = append(ids, id.String())
						}
					}
				}
				if oauth {
					var parisInput string
					switch protocol {
					case "messages":
						parisInput = gjson.Get(rec.Body.String(), "content.0.input").Raw
					case "responses":
						parisInput = gjson.Get(rec.Body.String(), "output.0.arguments").String()
					case "chat":
						parisInput = gjson.Get(rec.Body.String(), "choices.0.message.tool_calls.0.function.arguments").String()
					}
					require.JSONEq(t, `{"city":"Paris"}`, parisInput)
				}
				require.Len(t, ids, 2)
				require.NotEqual(t, ids[0], ids[1])

				// Return the results in completion order, which can differ from
				// invocation order when the client runs parallel tools.
				for _, replay := range []struct {
					name     string // destination used on replay
					keyID    int64  // authenticated replay key
					provider int64  // selected replay provider
					model    string // requested replay model
					match    bool   // scope permits restoration
					reject   bool   // upstream rejects the first signature
				}{
					{"same scope", 21, 101, model, true, false},
					{"different key", 22, 101, model, false, false},
					{"different provider", 21, 102, model, false, false},
					{"different model", 21, 101, "gemini-2.5-pro", false, false},
					{"signature rejected", 21, 101, model, true, true},
				} {
					t.Run(replay.name, func(t *testing.T) {
						body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"weather"},{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"lookup","input":{"city":"Paris"}},{"type":"tool_use","id":%q,"name":"lookup","input":{"city":"London"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":"London result"},{"type":"tool_result","tool_use_id":%q,"content":"Paris result"}]}]}`, replay.model, ids[0], ids[1], ids[1], ids[0])
						switch protocol {
						case "responses":
							body = fmt.Sprintf(`{"model":%q,"input":[{"role":"user","content":"weather"},{"type":"function_call","call_id":%q,"name":"lookup","arguments":"{\"city\":\"Paris\"}"},{"type":"function_call","call_id":%q,"name":"lookup","arguments":"{\"city\":\"London\"}"},{"type":"function_call_output","call_id":%q,"output":"London result"},{"type":"function_call_output","call_id":%q,"output":"Paris result"}]}`, replay.model, ids[0], ids[1], ids[1], ids[0])
						case "chat":
							body = fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"weather"},{"role":"assistant","tool_calls":[{"type":"function","id":%q,"function":{"name":"lookup","arguments":"{\"city\":\"Paris\"}"}},{"type":"function","id":%q,"function":{"name":"lookup","arguments":"{\"city\":\"London\"}"}}]},{"role":"tool","tool_call_id":%q,"content":"London result"},{"role":"tool","tool_call_id":%q,"content":"Paris result"}]}`, replay.model, ids[0], ids[1], ids[1], ids[0])
						}
						firstAttempt := stub.callCount
						if replay.reject {
							stub.responses = append(stub.responses, &http.Response{
								StatusCode: http.StatusBadRequest,
								Header:     make(http.Header),
								Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Invalid thought signature"}}`)),
							})
						}
						stub.responses = append(stub.responses, &http.Response{
							StatusCode: http.StatusOK,
							Header:     make(http.Header),
							Body:       io.NopCloser(strings.NewReader(answerFrame)),
						})
						provider.Record.ID = replay.provider
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Set("api_key", &apikey.APIKey{ID: replay.keyID})
						c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
						var err error
						switch protocol {
						case "messages":
							_, err = executor.Forward(t.Context(), c, provider, []byte(body))
						case "responses":
							_, err = executor.ForwardAsResponses(t.Context(), c, provider, []byte(body), nil)
						case "chat":
							_, err = executor.ForwardAsChatCompletions(t.Context(), c, provider, []byte(body))
						}
						require.NoError(t, err)
						wire := stub.requestBodies[firstAttempt]
						prefix := "contents"
						if oauth {
							prefix = "request.contents"
						}
						if replay.reject {
							require.Equal(t, firstAttempt+2, stub.callCount)
							fallback := gjson.GetBytes(stub.requestBodies[firstAttempt+1], prefix+".1.parts.0")
							require.False(t, fallback.Get("functionCall.id").Exists())
							require.Equal(t, "skip_thought_signature_validator", fallback.Get("thoughtSignature").String())
						} else {
							require.Equal(t, firstAttempt+1, stub.callCount)
						}

						first := gjson.GetBytes(wire, prefix+".1.parts.0")
						second := gjson.GetBytes(wire, prefix+".1.parts.1")
						paris := gjson.GetBytes(wire, prefix+".2.parts.0.functionResponse")
						london := gjson.GetBytes(wire, prefix+".2.parts.1.functionResponse")
						require.Equal(t, "Paris", first.Get("functionCall.args.city").String())
						require.Equal(t, "London", second.Get("functionCall.args.city").String())
						require.Equal(t, "London result", london.Get("response.content").String())
						require.Equal(t, "Paris result", paris.Get("response.content").String())
						if replay.match {
							require.Equal(t, "gemini-paris", first.Get("functionCall.id").String())
							require.Equal(t, "original-signature", first.Get("thoughtSignature").String())
							require.Equal(t, "gemini-london", second.Get("functionCall.id").String())
							require.False(t, second.Get("thoughtSignature").Exists())
							require.Equal(t, "gemini-london", london.Get("id").String())
							require.Equal(t, "gemini-paris", paris.Get("id").String())
						} else {
							require.False(t, first.Get("functionCall.id").Exists())
							require.False(t, second.Get("functionCall.id").Exists())
							require.False(t, london.Get("id").Exists())
							require.False(t, paris.Get("id").Exists())
							require.Equal(t, "skip_thought_signature_validator", first.Get("thoughtSignature").String())
						}
					})
				}
			})
		}
	}
}

// TestGeminiMessagesCompatServiceForward_PreservesRequestedModelAndMappedUpstreamModel
// forwards a mapped Messages request through an API key provider. Completion
// names remain separate, client text is delivered, and the captured endpoint
// selects the mapped model.
func TestGeminiMessagesCompatServiceForward_PreservesRequestedModelAndMappedUpstreamModel(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,

			Header: http.Header{"x-request-id": []string{"gemini-req-1"}},

			Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"hello"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`)),
		},
	}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           1,

			Type: capability.ProviderTypeAPIKey,

			Credentials: map[string]any{
				"api_key": "test-key",
				"model_mapping": map[string]any{
					"claude-sonnet-4": "claude-sonnet-4-20250514",
				},
			},
		},
	}
	body := []byte(`{"model":"claude-sonnet-4","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`)

	result, err := svc.Forward(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "claude-sonnet-4", result.Model)
	require.Equal(t, "claude-sonnet-4-20250514", result.UpstreamModel)
	require.Equal(t, "hello", gjson.GetBytes(w.Body.Bytes(), "content.0.text").String())
	require.Equal(t, 1, httpStub.calls)
	require.NotNil(t, httpStub.lastReq)
	require.Contains(t, httpStub.lastReq.URL.String(), "/models/claude-sonnet-4-20250514:")
}

// TestGeminiMessagesCompatServiceForward_NormalizesWebSearchToolForAIStudio
// converts a Messages function tool alongside server web search. Captured AI
// Studio input uses the expected search spelling and keeps that search object
// separate from function declarations.
func TestGeminiMessagesCompatServiceForward_NormalizesWebSearchToolForAIStudio(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,

			Header: http.Header{"x-request-id": []string{"gemini-req-2"}},

			Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"hello"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`)),
		},
	}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           1,

			Type: capability.ProviderTypeAPIKey,

			Credentials: map[string]any{
				"api_key": "test-key",
			},
		},
	}
	body := []byte(`{"model":"claude-sonnet-4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"get_weather","description":"Get weather info","input_schema":{"type":"object"}},{"type":"web_search_20250305","name":"web_search"}]}`)

	result, err := svc.Forward(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, httpStub.lastReq)

	postedBody, err := io.ReadAll(httpStub.lastReq.Body)
	require.NoError(t, err)

	var posted map[string]any
	require.NoError(t, json.Unmarshal(postedBody, &posted))
	tools, ok := posted["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)

	searchTool, ok := tools[1].(map[string]any)
	require.True(t, ok)
	_, hasSnake := searchTool["google_search"]
	_, hasCamel := searchTool["googleSearch"]
	require.True(t, hasSnake)
	require.False(t, hasCamel)
	_, hasFuncDecl := searchTool["functionDeclarations"]
	require.False(t, hasFuncDecl)
}

// TestConvertClaudeMessagesToGeminiGenerateContent_AddsThoughtSignatureForToolUse
// converts assistant tool history lacking a signature through the native
// bridge. The output contains a functionCall and the compatibility
// thoughtSignature sentinel for the exercised history.
func TestConvertClaudeMessagesToGeminiGenerateContent_AddsThoughtSignatureForToolUse(t *testing.T) {
	claudeReq := map[string]any{
		"model": "claude-haiku-4-5-20251001",

		"max_tokens": 10,

		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "hi"},
				},
			},

			map[string]any{
				"role": "assistant",

				"content": []any{
					map[string]any{"type": "text", "text": "ok"},

					map[string]any{
						"type": "tool_use",

						"id": "toolu_123",

						"name": "default_api:write_file",

						"input": map[string]any{"path": "a.txt", "content": "x"},
						// no signature on purpose

					},
				},
			},
		},

		"tools": []any{
			map[string]any{
				"name": "default_api:write_file",

				"description": "write file",

				"input_schema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"path": map[string]any{"type": "string"}},
				},
			},
		},
	}
	// The constructed history contains JSON-compatible maps, slices,
	// strings, and integers, so marshaling supplies the bytes checked by
	// the converter.
	b, _ := json.Marshal(claudeReq)

	out, err := googleforward.ConvertClaudeForTest(b)
	if err != nil {
		t.Fatalf("convert failed: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "\"functionCall\"") {
		t.Fatalf("expected functionCall in output, got: %s", s)
	}
	if !strings.Contains(s, "\"thoughtSignature\":\""+"skip_thought_signature_validator"+"\"") {
		t.Fatalf("expected injected thoughtSignature %q, got: %s", "skip_thought_signature_validator", s)
	}
}

// TestEnsureGeminiFunctionCallThoughtSignatures_InsertsWhenMissing supplies
// native function-call history without its signature property. The returned
// request contains the configured compatibility sentinel required by the
// missing-signature preparation path.
func TestEnsureGeminiFunctionCallThoughtSignatures_InsertsWhenMissing(t *testing.T) {
	geminiReq := map[string]any{
		"contents": []any{
			map[string]any{
				"role": "user",

				"parts": []any{
					map[string]any{
						"functionCall": map[string]any{
							"name": "default_api:write_file",
							"args": map[string]any{"path": "a.txt"},
						},
					},
				},
			},
		},
	}
	// The constructed history contains JSON-compatible maps, slices, and
	// strings, so marshaling supplies the native bytes inspected by the
	// signature helper.
	b, _ := json.Marshal(geminiReq)
	out := googleforward.EnsureSignatureForTest(b)
	s := string(out)
	if !strings.Contains(s, "\"thoughtSignature\":\""+"skip_thought_signature_validator"+"\"") {
		t.Fatalf("expected injected thoughtSignature %q, got: %s", "skip_thought_signature_validator", s)
	}
}

// TestEstimateGeminiCountTokens compares system/content text, empty parts,
// nontext parts, and whitespace-only text. Estimates are positive for the
// exercised text inputs and exactly zero for the remaining fixtures.
func TestEstimateGeminiCountTokens(t *testing.T) {
	zero := 0
	tests := []struct {
		name    string // table scenario name
		input   string // native counting request JSON
		wantGt0 bool   // expected positive estimate flag
		// wantExact takes precedence when nonnil; otherwise wantGt0
		// selects positive or zero classification, allowing text
		// fixtures to avoid promising a specific estimated count.
		wantExact *int // optional exact token estimate
	}{
		{
			name: "含 systemInstruction 和 contents",

			input: `{
				"systemInstruction":{"parts":[{"text":"You are a helpful assistant."}]},
				"contents":[{"parts":[{"text":"Hello, how are you?"}]}]
			}`,

			wantGt0: true,
		},

		{
			name: "仅 contents，无 systemInstruction",

			input: `{
				"contents":[{"parts":[{"text":"Hello, how are you?"}]}]
			}`,

			wantGt0: true,
		},

		{
			name:      "空 parts",
			input:     `{"contents":[{"parts":[]}]}`,
			wantGt0:   false,
			wantExact: &zero,
		},

		{
			name: "非文本 parts（inlineData）",

			input: `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png"}}]}]}`,

			wantGt0: false,

			wantExact: &zero,
		},

		{
			name:      "空白文本",
			input:     `{"contents":[{"parts":[{"text":"   "}]}]}`,
			wantGt0:   false,
			wantExact: &zero,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gemininative.EstimateGeminiCountTokens([]byte(tt.input))
			if tt.wantExact != nil {
				if got != *tt.wantExact {
					t.Errorf("期望精确值 %d，实际 %d", *tt.wantExact, got)
				}
				return
			}
			if tt.wantGt0 && got <= 0 {
				t.Errorf("期望返回 > 0，实际 %d", got)
			}
			if !tt.wantGt0 && got != 0 {
				t.Errorf("期望返回 0，实际 %d", got)
			}
		})
	}
}

// TestParseGeminiRateLimitResetTime compares structured, daily, text-pattern,
// malformed, and unmatched reset evidence. Results are absent or future/reset-
// offset values within the stated fixture wall-clock tolerance.
func TestParseGeminiRateLimitResetTime(t *testing.T) {
	tests := []struct {
		name    string // table scenario name
		input   string // rate-limit response fixture text
		wantNil bool   // expected missing-reset result flag
		// approxDelta uses seconds for wall-clock comparison; minus one
		// selects a future-time presence check rather than a specific
		// offset for daily reset fixtures.
		approxDelta int64 // expected offset in seconds
	}{
		{
			name: "正常 quotaResetDelay",

			input: `{"error":{"details":[{"metadata":{"quotaResetDelay":"12.345s"}}]}}`,

			wantNil: false,

			approxDelta: 13, // Round 12.345 seconds up to 13.

		},

		{
			name: "daily quota",

			input: `{"error":{"message":"quota per day exceeded"}}`,

			wantNil: false,

			approxDelta: -1, // Check presence and future time rather than an exact offset.

		},

		{
			name:    "无 details 且无 regex 匹配",
			input:   `{"error":{"message":"rate limit"}}`,
			wantNil: true,
		},

		{
			name:        "regex 回退匹配",
			input:       `Please retry in 30s`,
			wantNil:     false,
			approxDelta: 30,
		},

		{
			name:    "完全无匹配",
			input:   `{"error":{"code":429}}`,
			wantNil: true,
		},

		{
			name: "非法 JSON 但 regex 回退仍工作",

			input: `not json but Please retry in 10s`,

			wantNil: false,

			approxDelta: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now().Unix()
			got := parseGeminiReset([]byte(tt.input))

			if tt.wantNil {
				if got != nil {
					t.Fatalf("期望返回 nil，实际返回 %d", *got)
				}
				return
			}

			if got == nil {
				t.Fatalf("期望返回非 nil，实际返回 nil")
			}

			// An offset of -1 denotes a future reset whose exact
			// time is not fixed by the fixture.
			if tt.approxDelta == -1 {
				if *got < now {
					t.Errorf("期望返回的时间戳 >= now(%d)，实际 %d", now, *got)
				}
				return
			}

			// Allow two seconds for wall-clock sampling and test
			// execution between the measured timestamps.
			delta := *got - now
			if delta < tt.approxDelta-2 || delta > tt.approxDelta+2 {
				t.Errorf("期望 delta 约为 %d 秒（+/-2），实际 delta = %d 秒（返回值=%d, now=%d）",
					tt.approxDelta, delta, *got, now)
			}
		})
	}
}

// buildGeminiRateLimitBody encodes a supplied quotaResetDelay string into error
// fixture bytes.
func buildGeminiRateLimitBody(delay string) []byte {
	return []byte(fmt.Sprintf(`{"error":{"message":"too many requests","details":[{"metadata":{"quotaResetDelay":%q}}]}}`, delay))
}

// List returns the supplied presentation-rule slice and rule pointers for
// Gemini fixtures. The service reads these shared values, so tests retain them
// unchanged while matching HTTP errors.
func (r errorRulesFixtureRepo) List(context.Context) ([]*errorpolicy.ErrorPassthroughRule, error) {
	return r.rules, nil
}

// newErrorRulesTestService starts a presentation service using the supplied
// rule pointers. Startup failure panics in the fixture, and rule values remain
// caller-owned while the service matches HTTP errors.
func newErrorRulesTestService(rules []*errorpolicy.ErrorPassthroughRule) *errorpolicy.ErrorPassthroughService {
	s := errorpolicy.NewErrorPassthroughService(errorRulesFixtureRepo{rules: rules}, nil, gatewaytelemetry.ErrorRules)
	if err := s.StartContext(context.Background()); err != nil {
		panic(err)
	}
	return s
}

// newGeminiNativeTestContext constructs a native request context and response
// recorder for error assertions.
func newGeminiNativeTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent", strings.NewReader("{}"))
	return c, rec
}

// SetError increments the fixture provider-error persistence counter for policy
// assertions.
func (r *geminiErrorPolicyRepo) SetError(_ context.Context, _ int64, _ string) error {
	r.setErrorCalls++
	return nil
}

// SetRateLimited increments the fixture provider-cooldown persistence counter
// for policy assertions.
func (r *geminiErrorPolicyRepo) SetRateLimited(_ context.Context, _ int64, _ time.Time) error {
	r.setRateLimitedCalls++
	return nil
}

// SetTempUnschedulable increments the fixture provider-exclusion persistence
// counter for policy assertions.
func (r *geminiErrorPolicyRepo) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, _ string) error {
	r.setTempCalls++
	return nil
}

// SetModelRateLimit increments the model-write count and saves its scope for
// assertions.
func (r *geminiErrorPolicyRepo) SetModelRateLimit(_ context.Context, _ int64, scope string, _ time.Time, _ ...string) error {
	r.setModelRateLimitedCalls++
	r.lastModelScope = scope
	return nil
}

// newNonFailoverPassthroughRule returns an all-keyword rule with custom client
// status and message. The returned rule remains adjustable by the caller before
// the presentation service starts using it.
func newNonFailoverPassthroughRule(statusCode int, keyword string, respCode int, customMessage string) *errorpolicy.ErrorPassthroughRule {
	return &errorpolicy.ErrorPassthroughRule{
		ID: 1,

		Name: "non-failover-rule",

		Enabled: true,

		Priority: 1,

		ErrorCodes: []int{statusCode},

		Keywords: []string{keyword},

		MatchMode: errorpolicy.MatchModeAll,

		PassthroughCode: false,

		ResponseCode: &respCode,

		PassthroughBody: false,

		CustomMessage: &customMessage,
	}
}

// parseSSEEventsForTest collects JSON event objects from simple blank-separated
// fixture frames. It keeps the last data line in each frame, skips DONE/empty
// data, and records event names for ordering assertions.
func parseSSEEventsForTest(t *testing.T, stream string) []map[string]any {
	t.Helper()
	chunks := strings.Split(stream, "\n\n")
	events := make([]map[string]any, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		eventName := ""
		dataLine := ""
		for line := range strings.SplitSeq(chunk, "\n") {
			if after, ok := strings.CutPrefix(line, "event:"); ok {
				eventName = strings.TrimSpace(after)
			}
			if after, ok := strings.CutPrefix(line, "data:"); ok {
				dataLine = strings.TrimSpace(after)
			}
		}
		// Skip empty data and DONE markers because this helper collects
		// JSON event objects for block ordering assertions rather than
		// the complete protocol stream.
		if dataLine == "" || dataLine == "[DONE]" {
			continue
		}
		var data map[string]any
		require.NoError(t, json.Unmarshal([]byte(dataLine), &data))
		data["_event"] = eventName
		events = append(events, data)
	}
	return events
}

// findSSEEventForTest returns the first matching fixture event position or
// minus one. Numeric index is checked when present, and a nonempty blockType
// additionally matches the decoded content_block type.
func findSSEEventForTest(events []map[string]any, event string, index int, blockType string) int {
	for i, data := range events {
		if data["_event"] != event {
			continue
		}
		if gotIndex, ok := data["index"].(float64); ok && int(gotIndex) != index {
			continue
		}
		if blockType != "" {
			block, _ := data["content_block"].(map[string]any)
			if block["type"] != blockType {
				continue
			}
		}
		return i
	}
	return -1
}

// SetRateLimited records the provider identifier and selected cooldown
// expiration for assertions.
func (r *rateLimit429ProviderRepoStub) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitCalls++
	r.lastRateLimitID = id
	r.lastRateLimitReset = resetAt
	return nil
}

// TestGeminiMessagesForwardsThinkingBudget maps Messages input to a budget-
// capable Gemini model with an explicit thinking budget. Captured
// thinkingConfig retains that budget and includeThoughts while response text
// reaches the client.
func TestGeminiMessagesForwardsThinkingBudget(t *testing.T) {
	stub := &geminiCompatHTTPUpstreamStub{response: &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`))}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: stub, cfg: &googleforward.Options{}})
	svc.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{ID: 1, Platform: "gemini", Type: "apikey", Credentials: map[string]any{"api_key": "test", "model_mapping": map[string]any{"alias": "gemini-2.5-flash"}}}}
	body := []byte(`{"model":"alias","max_tokens":32000,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":4096}}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	_, err := svc.Forward(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body)
	require.NoError(t, err)
	wire, err := io.ReadAll(stub.lastReq.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"includeThoughts":true,"thinkingBudget":4096}`, gjson.GetBytes(wire, "generationConfig.thinkingConfig").Raw)
	require.Contains(t, recorder.Body.String(), "answer")
}

// TestGeminiForwardNativeSamplingFieldSpellings crosses both config and topP
// spellings with known support enabled/disabled. Captured topP presence follows
// capability, temperature remains, and the original client body retains its
// sampling value.
func TestGeminiForwardNativeSamplingFieldSpellings(t *testing.T) {
	for _, config := range []string{"generationConfig", "generation_config"} {
		for _, field := range []string{"topP", "top_p"} {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", config, field, enabled), func(t *testing.T) {
					stub := &geminiCompatHTTPUpstreamStub{response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`))}}
					svc := newGeminiFixture(geminiDependencies{httpUpstream: stub, cfg: &googleforward.Options{}})
					svc.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{Lookup: func(string) modelcatalog.Attributes { return modelcatalog.Attributes{TopP: &enabled} }}}
					provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{ID: 1, Platform: "gemini", Type: "apikey", Credentials: map[string]any{"api_key": "fixture"}}}
					body := []byte(fmt.Sprintf(`{"contents":[{"parts":[{"text":"hi"}]}],%q:{%q:0.8,"temperature":0.4}}`, config, field))
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/private:generateContent", bytes.NewReader(body))
					_, err := svc.ForwardNative(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, "private", "generateContent", false, body)
					require.NoError(t, err)
					sent, err := io.ReadAll(stub.lastReq.Body)
					require.NoError(t, err)
					require.Equal(t, enabled, gjson.GetBytes(sent, config+"."+field).Exists())
					require.Equal(t, 0.4, gjson.GetBytes(sent, config+".temperature").Float())
					require.Equal(t, 0.8, gjson.GetBytes(body, config+"."+field).Float())
				})
			}
		}
	}
}
