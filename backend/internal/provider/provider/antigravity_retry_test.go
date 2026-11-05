package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	acct "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// Antigravity model windows, credit exhaustion and provider error policies can
// send the same HTTP failure down different retry paths. These cases bind the
// provider adapter to the upstream loop and inspect selected request bodies,
// model keys and sticky callbacks. Single-provider mode and shared capacity
// have separate recovery rules. Endpoint overrides require serial tests and
// restoration of both global URL selectors.
//
// The response transport pairs configured responses with errors by index and
// captures request bodies before replaying fresh response readers. Its shallow
// copies retain shared headers, while health fixtures record only the methods
// each case asserts. Empty exchanges in several network-named cases contain
// neither a response nor an error; successful adapter-level cases and fixed
// wait constants do not establish full network timing guarantees.

// epFixedUpstream counts serial exchanges and returns a fixed response.
type epFixedUpstream struct {
	statusCode int    // Status for every exchange.
	body       string // Response bytes for every exchange.
	calls      int    // Serial transport invocation count.
}

// epProviderRepo separates provider-wide health counts from accepted model writes.
type epProviderRepo struct {
	// HealthStore supplies unconfigured methods through a nil embedded interface;
	// tests must call only the concrete methods implemented by this fixture.
	acct.HealthStore
	tempCalls   int // Provider-wide temporary write count.
	setErrCalls int // Generic provider error count.
}

// epTrackingRepo records generic health writes and their latest provider identities.
type epTrackingRepo struct {
	// HealthStore is nil for unsupported operations, which must not be invoked
	// by these policy cases; concrete fixture methods record the relevant writes.
	acct.HealthStore
	rateLimitedCalls int   // Generic rate-limit write count.
	rateLimitedID    int64 // Latest rate-limited provider identity.
	setErrCalls      int   // Generic error write count.
	setErrID         int64 // Latest errored provider identity.
	tempCalls        int   // Provider-wide temporary write count.
}

// antigravityFamilyStoreFixture records ordered exact-model and family-scope health keys.
type antigravityFamilyStoreFixture struct {
	// AntigravityHealthStore leaves unconfigured methods nil; callers must use
	// the concrete model-window method supplied here rather than other writes.
	acct.AntigravityHealthStore
	// modelRateLimitCalls retains each requested key in invocation order;
	// provider identities and reset times are deliberately not recorded here.
	modelRateLimitCalls []struct {
		modelKey string /* Requested model or family key. */
	}
}

// antigravityPolicyStoreFixture records policy model scopes and provider temporary writes.
type antigravityPolicyStoreFixture struct {
	// HealthStore supplies no default operations through this nil interface;
	// only the concrete fixture methods may be called during policy evaluation.
	acct.HealthStore
	tempCalls int // Provider-wide temporary write count.
	// modelRateLimitCalls records the key twice so separate scope and model
	// assertions can observe the same ordered production request.
	modelRateLimitCalls []struct { /* scope is the recorded health scope key;
		modelKey is the key inspected by model assertions. Both receive the same
		argument on each write, with neither value normalized by the fixture. */
		scope, modelKey string
	}
}

// stubAntigravityUpstream records URL selection and distinguishes the first endpoint.
type stubAntigravityUpstream struct {
	// firstBase is the URL prefix whose requests receive generic HTTP 429.
	// secondBase holds the owning test's configured alternative endpoint, but
	// transport response selection does not read it. All URLs outside firstBase
	// succeed, so this fixture does not enforce membership in the configured list.
	firstBase, secondBase string
	calls                 []string // Request URLs in call order.
}

// recordingOKUpstream counts serial requests that pass prechecks and returns success.
type recordingOKUpstream struct {
	calls int /* Serial transport invocation count. */
}

// stubSmartRetryCache records only the sticky deletions performed by bound callbacks.
type stubSmartRetryCache struct {
	// GatewayCache is nil for all unconfigured cache methods, so callers must
	// restrict this fixture to its concrete session-provider deletion method.
	session.GatewayCache
	deleteCalls []deleteSessionCall // Deletions in callback invocation order.
}

// deleteSessionCall retains both parts of one sticky-session cache identity.
type deleteSessionCall struct {
	groupID     int64  // Group owning the sticky session.
	sessionHash string // Hash identifying the sticky session.
}

// mockSmartRetryUpstream captures serial request observations and replays response bodies.
// A selected response and error occupy matching indexes, including nil responses;
// replaying a body does not copy headers or other referenced response values.
type mockSmartRetryUpstream struct {
	// responses selects each exchange by call index; repeatLast may reuse the
	// final entry, and a nil entry produces no response for its paired error.
	responses []*http.Response
	// responseBodies caches consumed bytes by response index and supplies fresh
	// readers on later exchanges. Original response readers are closed after capture.
	responseBodies [][]byte
	// errors must contain every selected response index, including a repeated
	// final entry; the transport performs no separate bounds check for this slice.
	errors     []error
	callIdx    int      // Next serial exchange index.
	calls      []string // Request URLs in exchange order.
	userAgents []string // User agents in exchange order.
	// requestBodies captures read bytes for every exchange, including partial
	// reads; a request without a body appends nil instead of skipping its slot.
	requestBodies [][]byte
	// repeatLast reuses the final response/error pair after sequence exhaustion;
	// an empty response list still returns a nil response and nil error.
	repeatLast bool
}

// antigravityRetryStoreFixture records ordered model windows and borrowed extra-data maps.
type antigravityRetryStoreFixture struct {
	// extraUpdateCalls retains the supplied map references rather than copies;
	// any later entry mutation is therefore visible to this fixture's assertions.
	extraUpdateCalls []map[string]any
	// AntigravityHealthStore leaves unconfigured operations nil; callers must
	// use only the concrete model-window and extra-data methods implemented here.
	acct.AntigravityHealthStore
	// modelRateLimitCalls retains each provider, scope and reset time in the
	// order supplied; writes succeed without applying windows to a provider row.
	modelRateLimitCalls []struct {
		providerID int64     // Provider receiving the model window.
		modelKey   string    // Requested model or family key.
		resetAt    time.Time // Reset time supplied by the writer.
	}
}

// TestIsCreditsExhausted_UsesAICreditsKey distinguishes missing, active and
// expired credit-exhaustion markers in provider extra data. Binding exposes
// availability independently of the ordinary model rate-limit key.
func TestIsCreditsExhausted_UsesAICreditsKey(t *testing.T) {
	t.Run("无 AICredits key 则积分可用", func(t *testing.T) {
		provider := &acct.Record{
			ID:       1,
			Platform: capability.PlatformAntigravity,
			Extra: map[string]any{
				"allow_overages": true,
			},
		}
		_, input := newAntigravityRetryFixture().Bind(AntigravityRetryRequest{Provider: provider})
		require.False(t, input.CreditsExhausted())
	})

	t.Run("AICredits key 生效则积分耗尽", func(t *testing.T) {
		provider := &acct.Record{
			ID:       2,
			Platform: capability.PlatformAntigravity,
			Extra: map[string]any{
				"allow_overages": true,
				"model_rate_limits": map[string]any{
					acct.CreditsExhaustedKey: map[string]any{
						"rate_limited_at":     time.Now().UTC().Format(time.RFC3339),
						"rate_limit_reset_at": time.Now().Add(5 * time.Hour).UTC().Format(time.RFC3339),
					},
				},
			},
		}
		_, input := newAntigravityRetryFixture().Bind(AntigravityRetryRequest{Provider: provider})
		require.True(t, input.CreditsExhausted())
	})

	t.Run("AICredits key 过期则积分可用", func(t *testing.T) {
		provider := &acct.Record{
			ID:       3,
			Platform: capability.PlatformAntigravity,
			Extra: map[string]any{
				"allow_overages": true,
				"model_rate_limits": map[string]any{
					acct.CreditsExhaustedKey: map[string]any{
						"rate_limited_at":     time.Now().Add(-6 * time.Hour).UTC().Format(time.RFC3339),
						"rate_limit_reset_at": time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
					},
				},
			},
		}
		_, input := newAntigravityRetryFixture().Bind(AntigravityRetryRequest{Provider: provider})
		require.False(t, input.CreditsExhausted())
	})
}

// TestHandleSmartRetry_QuotaExhausted_UsesCreditsAndStoresIndependentState
// retries an overage-enabled quota failure with AI credits. It checks enabled
// credit types, retained user agent and no ordinary model-limit writes on the
// returned response path.
func TestHandleSmartRetry_QuotaExhausted_UsesCreditsAndStoresIndependentState(t *testing.T) {
	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{successResp},
		errors:    []error{nil},
	}
	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       101,
		Name:     "acc-101",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
		Extra: map[string]any{
			"allow_overages": true,
		},
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-opus-4-6": "claude-sonnet-4-5",
			},
		},
	}

	respBody := []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","message":"QUOTA_EXHAUSTED"}}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}
	params := AntigravityRetryRequest{
		Context:     context.Background(),
		UserAgent:   "probe-client/9.9",
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"model":"claude-opus-4-6","request":{}}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore:     repo,
		RequestedModel: "claude-opus-4-6",
		HandleError: func(int, http.Header, []byte) {
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, []string{"https://ag-1.test"})

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp)
	require.Nil(t, result.SwitchError)
	require.Len(t, upstream.requestBodies, 1)
	require.Contains(t, string(upstream.requestBodies[0]), "enabledCreditTypes")
	require.Equal(t, "probe-client/9.9", upstream.userAgents[0])
	require.Empty(t, repo.modelRateLimitCalls, "overages 成功后不应写入普通 model_rate_limits")
}

// TestHandleSmartRetry_RateLimited_DoesNotUseCredits retries a short
// RATE_LIMIT_EXCEEDED response through the ordinary request path. The
// successful request contains no credit selection and produces no health
// persistence calls.
func TestHandleSmartRetry_RateLimited_DoesNotUseCredits(t *testing.T) {
	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{successResp},
		errors:    []error{nil},
	}
	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       102,
		Name:     "acc-102",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
		Extra: map[string]any{
			"allow_overages": true,
		},
	}

	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}
	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, []string{"https://ag-1.test"})

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp)
	require.Len(t, upstream.requestBodies, 1)
	require.NotContains(t, string(upstream.requestBodies[0]), "enabledCreditTypes")
	require.Empty(t, repo.extraUpdateCalls)
	require.Empty(t, repo.modelRateLimitCalls)
}

// TestAntigravityRetryLoop_ModelRateLimited_InjectsCredits starts with an
// active model window and available overage credits. The outer loop sends one
// successful request whose body enables AI credits.
func TestAntigravityRetryLoop_ModelRateLimited_InjectsCredits(t *testing.T) {
	// Copy the global endpoint slice before replacing it and retain the
	// current availability tracker; these fixtures share process-wide retry
	// inputs and run serially.
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	// Restore both process-wide inputs when this case returns, including the
	// previous tracker so later tests retain their original availability
	// state.
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-1.test"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			},
		},
		errors: []error{nil},
	}
	// A model limit with overages enabled and available credits permits credit injection.
	provider := &acct.Record{
		ID:          103,
		Name:        "acc-103",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Status:      billing.StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"allow_overages": true,
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limited_at":     time.Now().UTC().Format(time.RFC3339),
					"rate_limit_reset_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
				},
			},
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		RequestedModel: "claude-sonnet-4-5",
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requestBodies, 1)
	require.Contains(t, string(upstream.requestBodies[0]), "enabledCreditTypes")
}

// TestAntigravityRetryLoop_CreditsExhausted_DoesNotInject combines an active
// model window with an active credit-exhaustion marker. The loop returns a
// provider-switch error instead of bypassing the model window.
func TestAntigravityRetryLoop_CreditsExhausted_DoesNotInject(t *testing.T) {
	// Copy the global endpoint slice before replacing it and retain the
	// current availability tracker; these fixtures share process-wide retry
	// inputs and run serially.
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	// Restore both process-wide inputs when this case returns, including the
	// previous tracker so later tests retain their original availability
	// state.
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-1.test"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	// An active credits-exhausted window prevents credit injection and requests a provider switch.
	provider := &acct.Record{
		ID:          104,
		Name:        "acc-104",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Status:      billing.StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"allow_overages": true,
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limited_at":     time.Now().UTC().Format(time.RFC3339),
					"rate_limit_reset_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
				},
				acct.CreditsExhaustedKey: map[string]any{
					"rate_limited_at":     time.Now().UTC().Format(time.RFC3339),
					"rate_limit_reset_at": time.Now().Add(5 * time.Hour).UTC().Format(time.RFC3339),
				},
			},
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context:        context.Background(),
		Prefix:         "[test]",
		Provider:       provider,
		AccessToken:    "token",
		Action:         "generateContent",
		Body:           []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		RequestedModel: "claude-sonnet-4-5",
		HandleError: func(int, http.Header, []byte) {
		},
	})
	_, err := adapter.AntigravityRetryLoop(input)

	require.Error(t, err)
	var switchErr *antigravity.AntigravityProviderSwitchError
	require.ErrorAs(t, err, &switchErr)
}

// TestAntigravityRetryLoop_CreditErrorMarksExhausted receives a
// credit-specific HTTP 403 after injecting apparently available credits. It
// checks a returned retry result and one stored credit-exhaustion key after
// that configured rejection.
func TestAntigravityRetryLoop_CreditErrorMarksExhausted(t *testing.T) {
	// Copy the global endpoint slice before replacing it and retain the
	// current availability tracker; these fixtures share process-wide retry
	// inputs and run serially.
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	// Restore both process-wide inputs when this case returns, including the
	// previous tracker so later tests retain their original availability
	// state.
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-1.test"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	repo := &antigravityRetryStoreFixture{}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{
			{
				StatusCode: http.StatusForbidden,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Insufficient GOOGLE_ONE_AI credits"}}`)),
			},
		},
		errors: []error{nil},
	}
	// Credits appear available locally, but the upstream rejects the injected-credit request.
	provider := &acct.Record{
		ID:          105,
		Name:        "acc-105",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Status:      billing.StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"allow_overages": true,
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limited_at":     time.Now().UTC().Format(time.RFC3339),
					"rate_limit_reset_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
				},
			},
		},
	}

	svc := newAntigravityRetryFixture()
	svc.Health.Store = repo
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore:     repo,
		RequestedModel: "claude-sonnet-4-5",
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, repo.modelRateLimitCalls, 1, "应通过 SetModelRateLimit 写入 AICredits key")
	require.Equal(t, acct.CreditsExhaustedKey, repo.modelRateLimitCalls[0].modelKey)
}

// TestRetryLoop_ErrorPolicy_CustomErrorCodes compares configured matches and
// skips for upstream HTTP 429 and 500 responses. Each case checks callback
// count, upstream attempts and the final response status.
func TestRetryLoop_ErrorPolicy_CustomErrorCodes(t *testing.T) {
	tests := []struct {
		name              string // Subtest case description.
		upstreamStatus    int    // Configured upstream response status.
		upstreamBody      string // Configured upstream response bytes.
		customCodes       []any  // Allowed custom error statuses.
		expectHandleError int    // Expected error callback count.
		expectUpstream    int    // Expected transport invocation count.
		expectStatusCode  int    // Expected returned response status.
	}{
		{
			name:              "429_in_custom_codes_matched",
			upstreamStatus:    429,
			upstreamBody:      `{"error":"rate limited"}`,
			customCodes:       []any{float64(429)},
			expectHandleError: 1,
			expectUpstream:    1,
			expectStatusCode:  429,
		},
		{
			name:              "429_not_in_custom_codes_skipped",
			upstreamStatus:    429,
			upstreamBody:      `{"error":"rate limited"}`,
			customCodes:       []any{float64(500)},
			expectHandleError: 0,
			expectUpstream:    1,
			expectStatusCode:  500,
		},
		{
			name:              "500_in_custom_codes_matched",
			upstreamStatus:    500,
			upstreamBody:      `{"error":"internal"}`,
			customCodes:       []any{float64(500)},
			expectHandleError: 1,
			expectUpstream:    1,
			expectStatusCode:  500,
		},
		{
			name:              "500_not_in_custom_codes_skipped",
			upstreamStatus:    500,
			upstreamBody:      `{"error":"internal"}`,
			customCodes:       []any{float64(429)},
			expectHandleError: 0,
			expectUpstream:    1,
			expectStatusCode:  500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saveAndSetBaseURLs(t)

			upstream := &epFixedUpstream{statusCode: tt.upstreamStatus, body: tt.upstreamBody}
			repo := &epProviderRepo{}
			rlSvc := acct.NewHealthService(repo, nil, acct.HealthOptions{})

			provider := &acct.Record{
				ID:          100,
				Type:        capability.ProviderTypeAPIKey,
				Platform:    capability.PlatformAntigravity,
				Schedulable: true,
				Status:      billing.StatusActive,
				Concurrency: 1,
				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         tt.customCodes,
				},
			}

			svc := newAntigravityRetryFixture()
			svc.Policy = rlSvc

			var handleErrorCount int
			p := newRetryParams(provider, upstream, func(int, http.Header, []byte) {
				handleErrorCount++
			})

			adapter, input := svc.Bind(p)
			result, err := adapter.AntigravityRetryLoop(input)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, result.Resp)
			// The loop returns a caller-owned response reader; close it after
			// assertions and discard close errors because this fixture
			// observes status and callbacks.
			defer func() { _ = result.Resp.Body.Close() }()

			require.Equal(t, tt.expectStatusCode, result.Resp.StatusCode)
			require.Equal(t, tt.expectHandleError, handleErrorCount, "handleError call count")
			require.Equal(t, tt.expectUpstream, upstream.calls, "upstream call count")
		})
	}
}

// TestRetryLoop_ErrorPolicy_TempUnschedulable distinguishes matching
// overload/rate keywords from an unmatched temporary-unschedulable rule.
// Matching responses switch providers, while a short context deadline
// interrupts the default backoff.
func TestRetryLoop_ErrorPolicy_TempUnschedulable(t *testing.T) {
	tempRulesProvider := func(rules []any) *acct.Record {
		return &acct.Record{
			ID:          200,
			Type:        capability.ProviderTypeOAuth,
			Platform:    capability.PlatformAntigravity,
			Schedulable: true,
			Status:      billing.StatusActive,
			Concurrency: 1,
			Credentials: map[string]any{
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules":   rules,
			},
		}
	}

	overloadedRule := map[string]any{
		"error_code":       float64(503),
		"keywords":         []any{"overloaded"},
		"duration_minutes": float64(10),
	}

	rateLimitRule := map[string]any{
		"error_code":       float64(429),
		"keywords":         []any{"rate limited keyword"},
		"duration_minutes": float64(5),
	}

	t.Run("503_overloaded_matches_rule", func(t *testing.T) {
		saveAndSetBaseURLs(t)

		upstream := &epFixedUpstream{statusCode: 503, body: `overloaded`}
		repo := &epProviderRepo{}
		rlSvc := acct.NewHealthService(repo, nil, acct.HealthOptions{})
		svc := newAntigravityRetryFixture()
		svc.Policy = rlSvc

		provider := tempRulesProvider([]any{overloadedRule})
		p := newRetryParams(provider, upstream, func(int, http.Header, []byte) {
			t.Error("handleError should not be called for temp unschedulable")
		})

		adapter, input := svc.Bind(p)
		result, err := adapter.AntigravityRetryLoop(input)

		require.Nil(t, result)
		var switchErr *antigravity.AntigravityProviderSwitchError
		require.ErrorAs(t, err, &switchErr)
		require.Equal(t, provider.ID, switchErr.OriginalProviderID)
		require.Equal(t, 1, upstream.calls, "should not retry")
	})

	t.Run("429_rate_limited_keyword_matches_rule", func(t *testing.T) {
		saveAndSetBaseURLs(t)

		upstream := &epFixedUpstream{statusCode: 429, body: `rate limited keyword`}
		repo := &epProviderRepo{}
		rlSvc := acct.NewHealthService(repo, nil, acct.HealthOptions{})
		svc := newAntigravityRetryFixture()
		svc.Policy = rlSvc

		provider := tempRulesProvider([]any{rateLimitRule})
		p := newRetryParams(provider, upstream, func(int, http.Header, []byte) {
			t.Error("handleError should not be called for temp unschedulable")
		})

		adapter, input := svc.Bind(p)
		result, err := adapter.AntigravityRetryLoop(input)

		require.Nil(t, result)
		var switchErr *antigravity.AntigravityProviderSwitchError
		require.ErrorAs(t, err, &switchErr)
		require.Equal(t, provider.ID, switchErr.OriginalProviderID)
		require.Equal(t, 1, upstream.calls, "should not retry")
	})

	t.Run("503_body_no_match_continues_default_retry", func(t *testing.T) {
		saveAndSetBaseURLs(t)

		upstream := &epFixedUpstream{statusCode: 503, body: `random`}
		repo := &epProviderRepo{}
		rlSvc := acct.NewHealthService(repo, nil, acct.HealthOptions{})
		svc := newAntigravityRetryFixture()
		svc.Policy = rlSvc

		provider := tempRulesProvider([]any{overloadedRule})

		// The unmatched temporary rule leaves generic retry backoff active; a
		// short deadline terminates that path without waiting for the full
		// outer retry budget.
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		p := newRetryParams(provider, upstream, func(int, http.Header, []byte) {
		})
		p.Context = ctx

		adapter, input := svc.Bind(p)
		result, err := adapter.AntigravityRetryLoop(input)

		require.Nil(t, result)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.GreaterOrEqual(t, upstream.calls, 1, "should have called upstream at least once")
	})
}

// TestRetryLoop_ErrorPolicy_NilRateLimitService leaves the policy service
// unset while repeatedly receiving generic HTTP 429 responses. The default
// backoff remains usable and exits with the context deadline.
func TestRetryLoop_ErrorPolicy_NilRateLimitService(t *testing.T) {
	saveAndSetBaseURLs(t)

	upstream := &epFixedUpstream{statusCode: 429, body: `{"error":"rate limited"}`}
	svc := newAntigravityRetryFixture()

	provider := &acct.Record{
		ID:          300,
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
	}

	// Bound the generic backoff with a short deadline so the nil-policy case
	// observes context termination instead of waiting for all default retries.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	p := newRetryParams(provider, upstream, func(int, http.Header, []byte) {
	})
	p.Context = ctx

	// The unset policy service leaves the default retry path intact;
	// cancellation returns its deadline error after at least one upstream
	// exchange.
	adapter, input := svc.Bind(p)
	result, err := adapter.AntigravityRetryLoop(input)

	require.Nil(t, result)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.GreaterOrEqual(t, upstream.calls, 1)
}

// TestRetryLoop_ErrorPolicy_NoPolicy_OriginalBehavior supplies a health
// service without custom error or temporary-unschedulable rules. Generic HTTP
// 429 exhausts the outer attempt budget before one error callback handles the
// returned response.
func TestRetryLoop_ErrorPolicy_NoPolicy_OriginalBehavior(t *testing.T) {
	saveAndSetBaseURLs(t)

	upstream := &epFixedUpstream{statusCode: 429, body: `{"error":"rate limited"}`}
	repo := &epProviderRepo{}
	rlSvc := acct.NewHealthService(repo, nil, acct.HealthOptions{})
	svc := newAntigravityRetryFixture()
	svc.Policy = rlSvc

	// This active OAuth fixture configures no policy rules; generic HTTP 429
	// therefore follows the outer retry budget before the final error
	// callback.
	provider := &acct.Record{
		ID:          400,
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
	}

	var handleErrorCount int
	p := newRetryParams(provider, upstream, func(int, http.Header, []byte) {
		handleErrorCount++
	})

	adapter, input := svc.Bind(p)
	result, err := adapter.AntigravityRetryLoop(input)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Resp)
	// The loop returns a caller-owned response reader; close it after
	// assertions and discard close errors because this fixture observes status
	// and callbacks.
	defer func() { _ = result.Resp.Body.Close() }()

	require.Equal(t, http.StatusTooManyRequests, result.Resp.StatusCode)
	require.Equal(t, antigravity.AntigravityMaxRetries, upstream.calls, "should exhaust all retries")
	require.Equal(t, 1, handleErrorCount, "handleError should be called once after retries exhausted")
}

// TestCustomErrorCode599_SkippedErrors_Return500_NoRateLimit configures only
// HTTP 599 and submits several other upstream errors. Every skip becomes HTTP
// 500 after one request, with no generic rate-limit or error writes.
func TestCustomErrorCode599_SkippedErrors_Return500_NoRateLimit(t *testing.T) {
	errorCodes := []int{429, 500, 503, 401, 403}

	for _, upstreamStatus := range errorCodes {
		t.Run(http.StatusText(upstreamStatus), func(t *testing.T) {
			saveAndSetBaseURLs(t)

			upstream := &epFixedUpstream{
				statusCode: upstreamStatus,
				body:       `{"error":"some upstream error"}`,
			}
			repo := &epTrackingRepo{}
			rlSvc := acct.NewHealthService(repo, nil, acct.HealthOptions{})
			svc := newAntigravityRetryFixture()
			svc.Policy = rlSvc

			provider := &acct.Record{
				ID:          500,
				Type:        capability.ProviderTypeAPIKey,
				Platform:    capability.PlatformAntigravity,
				Schedulable: true,
				Status:      billing.StatusActive,
				Concurrency: 1,
				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(599)},
				},
			}

			var handleErrorCount int
			p := newRetryParams(provider, upstream, func(int, http.Header, []byte) {
				handleErrorCount++
			})

			adapter, input := svc.Bind(p)
			result, err := adapter.AntigravityRetryLoop(input)

			require.NoError(t, err, "should not return error")
			require.NotNil(t, result, "result should not be nil")
			require.NotNil(t, result.Resp, "response should not be nil")
			// The loop returns a caller-owned response reader; close it after
			// assertions and discard close errors because this fixture
			// observes status and callbacks.
			defer func() { _ = result.Resp.Body.Close() }()

			// Skipped custom errors are returned as HTTP 500.
			require.Equal(t, http.StatusInternalServerError, result.Resp.StatusCode,
				"skipped error should return 500, not %d", upstreamStatus)

			require.Equal(t, 0, handleErrorCount,
				"handleError should NOT be called for skipped errors")

			require.Equal(t, 0, repo.rateLimitedCalls,
				"SetRateLimited should NOT be called for skipped errors")

			require.Equal(t, 0, repo.setErrCalls,
				"SetError should NOT be called for skipped errors")

			require.Equal(t, 1, upstream.calls,
				"should call upstream exactly once (no retry)")
		})
	}
}

// TestSetAntigravityModelRateLimits_GeminiWritesFamilyScope applies a reset
// window to a Gemini model through the health helper. It records the exact
// model key before the shared Antigravity Gemini-family key.
func TestSetAntigravityModelRateLimits_GeminiWritesFamilyScope(t *testing.T) {
	repo := &antigravityFamilyStoreFixture{}
	svc := &acct.AntigravityHealth{ModelKeys: AntigravityModelLimitKeys, Logf: func(string, ...any) {}}
	provider := &acct.Record{ID: 789, Platform: capability.PlatformAntigravity}
	resetAt := time.Now().Add(30 * time.Second)

	success := svc.SetAntigravityModelRateLimits(
		context.Background(),
		repo,
		provider,
		"gemini-3-pro",
		"[test]",
		429,
		resetAt,
		false,
	)

	require.True(t, success)
	require.Len(t, repo.modelRateLimitCalls, 2)
	require.Equal(t, "gemini-3-pro", repo.modelRateLimitCalls[0].modelKey)
	require.Equal(t, "antigravity:gemini", repo.modelRateLimitCalls[1].modelKey)
}

// TestSetAntigravityModelRateLimits_ClaudeDoesNotWriteGeminiScope applies a
// reset window to a Claude model through the same health helper. Only the
// exact Claude key is recorded.
func TestSetAntigravityModelRateLimits_ClaudeDoesNotWriteGeminiScope(t *testing.T) {
	repo := &antigravityFamilyStoreFixture{}
	svc := &acct.AntigravityHealth{ModelKeys: AntigravityModelLimitKeys, Logf: func(string, ...any) {}}
	provider := &acct.Record{ID: 790, Platform: capability.PlatformAntigravity}
	resetAt := time.Now().Add(30 * time.Second)

	success := svc.SetAntigravityModelRateLimits(
		context.Background(),
		repo,
		provider,
		"claude-sonnet-4-5",
		"[test]",
		429,
		resetAt,
		false,
	)

	require.True(t, success)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "claude-sonnet-4-5", repo.modelRateLimitCalls[0].modelKey)
}

// TestApplyErrorPolicy compares absent rules, custom-code matches/skips and an
// overload rule mapped to a Claude model. The overload case writes a model
// scope and switches without a provider-wide temporary write.
func TestApplyErrorPolicy(t *testing.T) {
	tests := []struct {
		name              string       // Subtest case description.
		provider          *acct.Record // Provider carrying policy credentials.
		statusCode        int          // Upstream status under classification.
		body              []byte       // Upstream body under classification.
		expectedHandled   bool         // Expected policy handling decision.
		expectedStatus    int          // Expected classified output status.
		expectedSwitchErr bool         // Expected provider-switch error presence.
		handleErrorCalls  int          // Expected fallback callback count.
	}{
		{
			name: "none_not_handled",
			provider: &acct.Record{
				ID:       10,
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformAntigravity,
			},
			statusCode:       500,
			body:             []byte(`"error"`),
			expectedHandled:  false,
			expectedStatus:   500, // passthrough
			handleErrorCalls: 0,
		},
		{
			name: "skipped_handled_no_handleError",
			provider: &acct.Record{
				ID:       11,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformAntigravity,
				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(429)},
				},
			},
			statusCode:       500, // not in custom codes
			body:             []byte(`"error"`),
			expectedHandled:  true,
			expectedStatus:   http.StatusInternalServerError, // skipped → 500
			handleErrorCalls: 0,
		},
		{
			name: "matched_handled_calls_handleError",
			provider: &acct.Record{
				ID:       12,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformAntigravity,
				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(500)},
				},
			},
			statusCode:       500,
			body:             []byte(`"error"`),
			expectedHandled:  true,
			expectedStatus:   500, // matched → original status
			handleErrorCalls: 1,
		},
		{
			name: "temp_unscheduled_returns_switch_error",
			provider: &acct.Record{
				ID:       13,
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformAntigravity,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"claude-sonnet-4-5": "claude-sonnet-4-5",
					},
					"temp_unschedulable_enabled": true,
					"temp_unschedulable_rules": []any{
						map[string]any{
							"error_code":       float64(503),
							"keywords":         []any{"overloaded"},
							"duration_minutes": float64(10),
						},
					},
				},
			},
			statusCode:        503,
			body:              []byte(`overloaded`),
			expectedHandled:   true,
			expectedStatus:    503, // temp_unscheduled → original status
			expectedSwitchErr: true,
			handleErrorCalls:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &antigravityPolicyStoreFixture{}
			rlSvc := acct.NewHealthService(repo, nil, acct.HealthOptions{})
			svc := newAntigravityPolicyFixture(repo, rlSvc)

			var handleErrorCount int
			p := AntigravityRetryRequest{
				Context:        context.Background(),
				Prefix:         "[test]",
				Provider:       tt.provider,
				RequestedModel: "claude-sonnet-4-5",
				HandleError: func(int, http.Header, []byte) {
					handleErrorCount++
				},
				Sticky: true,
			}

			handled, outStatus, retErr := svc.applyPolicy(p, tt.statusCode, http.Header{}, tt.body)

			require.Equal(t, tt.expectedHandled, handled, "handled mismatch")
			require.Equal(t, tt.expectedStatus, outStatus, "outStatus mismatch")
			require.Equal(t, tt.handleErrorCalls, handleErrorCount, "handleError call count mismatch")

			if tt.expectedSwitchErr {
				var switchErr *antigravity.AntigravityProviderSwitchError
				require.ErrorAs(t, retErr, &switchErr)
				require.Equal(t, tt.provider.ID, switchErr.OriginalProviderID)
				require.Zero(t, repo.tempCalls)
				require.Len(t, repo.modelRateLimitCalls, 1)
				require.Equal(t, "claude-sonnet-4-5", repo.modelRateLimitCalls[0].scope)
			} else {
				require.NoError(t, retErr)
			}
		})
	}
}

// TestApplyErrorPolicy_GeminiRateLimitBypassesCustomSkip submits a Gemini rate
// limit excluded by the configured custom codes. Model handling still records
// both scopes and clears the bound sticky session before fallback.
func TestApplyErrorPolicy_GeminiRateLimitBypassesCustomSkip(t *testing.T) {
	repo := &antigravityPolicyStoreFixture{}
	var cleared []struct {
		groupID     int64  // Group owning the cleared session.
		sessionHash string // Hash identifying the cleared session.
	}
	rlSvc := acct.NewHealthService(repo, nil, acct.HealthOptions{})
	svc := newAntigravityPolicyFixture(repo, rlSvc)

	provider := &acct.Record{
		ID:       31,
		Type:     capability.ProviderTypeAPIKey,
		Platform: capability.PlatformAntigravity,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(500)},
		},
	}
	body := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "15s"}
			]
		}
	}`)
	p := AntigravityRetryRequest{
		Context:    context.Background(),
		Prefix:     "[test]",
		Provider:   provider,
		ModelStore: repo,
		// Record the bound group and session hash when model handling clears
		// affinity, so policy ordering is observed independently of any
		// external cache implementation.
		ClearSticky: func() {
			cleared = append(cleared, struct {
				groupID     int64  // Group owning the cleared session.
				sessionHash string // Hash identifying the cleared session.
			}{42, "gemini:sticky"})
		},
		HandleError: func(int, http.Header, []byte) {
			t.Fatal("model rate limit should be handled before custom error fallback")
		},
	}

	handled, outStatus, retErr := svc.applyPolicy(p, http.StatusTooManyRequests, http.Header{}, body)

	require.True(t, handled)
	require.Equal(t, http.StatusTooManyRequests, outStatus)
	require.NoError(t, retErr)
	require.Len(t, repo.modelRateLimitCalls, 2)
	require.Equal(t, "gemini-3-flash", repo.modelRateLimitCalls[0].modelKey)
	require.Equal(t, "antigravity:gemini", repo.modelRateLimitCalls[1].modelKey)
	require.Len(t, cleared, 1)
	require.Equal(t, int64(42), cleared[0].groupID)
	require.Equal(t, "gemini:sticky", cleared[0].sessionHash)
}

// TestAntigravityRetryLoop_NoURLFallback_UsesConfiguredBaseURL makes the first
// configured endpoint fail while other URLs would succeed. Every outer attempt
// stays on the configured endpoint, and its availability ordering remains
// unchanged.
func TestAntigravityRetryLoop_NoURLFallback_UsesConfiguredBaseURL(t *testing.T) {
	// Clear the forwarding override so endpoint resolution uses this case's
	// configured base list; testing restores the previous environment value
	// after the case completes.

	// Copy the global endpoint slice before replacing it and retain the
	// current availability tracker; these fixtures share process-wide retry
	// inputs and run serially.
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	// Restore both process-wide inputs when this case returns, including the
	// previous tracker so later tests retain their original availability
	// state.
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	base1 := "https://ag-1.test"
	base2 := "https://ag-2.test"
	antigravity.BaseURLs = []string{base1, base2}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	upstream := &stubAntigravityUpstream{firstBase: base1, secondBase: base2}
	provider := &acct.Record{
		ID:          1,
		Name:        "acc-1",
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
	}

	var handleErrorCalled bool
	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Prefix:      "[test]",
		Context:     context.Background(),
		Provider:    provider,
		ProxyURL:    "",
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		RequestedModel: "claude-sonnet-4-5",
		HandleError: func(int, http.Header, []byte) {
			handleErrorCalled = true
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Resp)
	// The loop returns a caller-owned response reader; close it after
	// assertions and discard close errors because this fixture observes status
	// and callbacks.
	defer func() { _ = result.Resp.Body.Close() }()
	require.Equal(t, http.StatusTooManyRequests, result.Resp.StatusCode)
	require.True(t, handleErrorCalled)
	require.Len(t, upstream.calls, antigravity.AntigravityMaxRetries)
	for _, callURL := range upstream.calls {
		require.True(t, strings.HasPrefix(callURL, base1))
	}

	available := antigravity.DefaultURLAvailability.GetAvailableURLsWithBase(antigravity.BaseURLs)
	require.NotEmpty(t, available)
	require.Equal(t, base1, available[0])
}

// TestAntigravityRetryLoop_PreCheck_SwitchesWhenRateLimited starts with a
// short active Claude model window and a sticky session. Prechecking switches
// providers with the expected identity and model before any upstream request.
func TestAntigravityRetryLoop_PreCheck_SwitchesWhenRateLimited(t *testing.T) {
	upstream := &recordingOKUpstream{}
	provider := &acct.Record{
		ID:          1,
		Name:        "acc-1",
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Extra: map[string]any{
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limit_reset_at": time.Now().Add(2 * time.Second).Format(time.RFC3339),
				},
			},
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context:        context.Background(),
		Prefix:         "[test]",
		Provider:       provider,
		AccessToken:    "token",
		Action:         "generateContent",
		Body:           []byte(`{"input":"test"}`),
		RequestedModel: "claude-sonnet-4-5",
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		Sticky: true,
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.Nil(t, result)
	var switchErr *antigravity.AntigravityProviderSwitchError
	require.ErrorAs(t, err, &switchErr)
	require.Equal(t, provider.ID, switchErr.OriginalProviderID)
	require.Equal(t, "claude-sonnet-4-5", switchErr.RateLimitedModel)
	require.True(t, switchErr.IsStickySession)
	require.Equal(t, 0, upstream.calls, "should not call upstream when switching on pre-check")
}

// TestAntigravityRetryLoop_PreCheck_SwitchesWhenRemainingLong starts with a
// longer active Claude model window and a sticky session. The same precheck
// returns a provider switch without sending an upstream request.
func TestAntigravityRetryLoop_PreCheck_SwitchesWhenRemainingLong(t *testing.T) {
	upstream := &recordingOKUpstream{}
	provider := &acct.Record{
		ID:          2,
		Name:        "acc-2",
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Extra: map[string]any{
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limit_reset_at": time.Now().Add(11 * time.Second).Format(time.RFC3339),
				},
			},
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context:        context.Background(),
		Prefix:         "[test]",
		Provider:       provider,
		AccessToken:    "token",
		Action:         "generateContent",
		Body:           []byte(`{"input":"test"}`),
		RequestedModel: "claude-sonnet-4-5",
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		Sticky: true,
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.Nil(t, result)
	var switchErr *antigravity.AntigravityProviderSwitchError
	require.ErrorAs(t, err, &switchErr)
	require.Equal(t, provider.ID, switchErr.OriginalProviderID)
	require.Equal(t, "claude-sonnet-4-5", switchErr.RateLimitedModel)
	require.True(t, switchErr.IsStickySession)
	require.Equal(t, 0, upstream.calls, "should not call upstream when switching on pre-check")
}

// TestSingleProviderRetryConstants fixes the in-place attempt count and
// maximum individual and accumulated retry waits. These constant assertions
// describe wait budgets without measuring a complete request's elapsed time.
func TestSingleProviderRetryConstants(t *testing.T) {
	require.Equal(t, 3, antigravity.AntigravitySingleProviderSmartRetryMaxAttempts,
		"单提供商原地重试最多 3 次")
	require.Equal(t, 15*time.Second, antigravity.AntigravitySingleProviderSmartRetryMaxWait,
		"单次最大等待 15s")
	require.Equal(t, 30*time.Second, antigravity.AntigravitySingleProviderSmartRetryTotalMaxWait,
		"总累计等待不超过 30s")
}

// TestHandleSmartRetry_503_LongDelay_SingleProviderRetry_RetryInPlace enables
// single-provider mode for a capacity-exhaustion response advertising a long
// delay. A successful retry returns HTTP 200 without switching or writing
// model limits.
func TestHandleSmartRetry_503_LongDelay_SingleProviderRetry_RetryInPlace(t *testing.T) {
	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"result":"ok"}`)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{successResp},
		errors:    []error{nil},
	}

	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:          1,
		Name:        "acc-single",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Concurrency: 1,
	}

	// The response advertises a delay above the rate-limit threshold, but capacity errors use a separate
	// retry path.
	respBody := []byte(`{
		"error": {
			"code": 503,
			"status": "UNAVAILABLE",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro-high"}, "reason": "MODEL_CAPACITY_EXHAUSTED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "39s"}
			],
			"message": "No capacity available for model gemini-3-pro-high on the server"
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context: context.Background(), SingleProvider: true,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp, "should return successful response from in-place retry")
	require.Equal(t, http.StatusOK, result.Resp.StatusCode)
	require.Nil(t, result.SwitchError, "should NOT return switchError in single provider mode")
	require.Nil(t, result.Err)

	require.Len(t, repo.modelRateLimitCalls, 0,
		"should NOT set model rate limit in single provider retry mode")

	require.GreaterOrEqual(t, len(upstream.calls), 1, "should have made at least one retry call")
}

// TestHandleSmartRetry_503_LongDelay_NoSingleProviderRetry_StillSwitches uses
// a long RATE_LIMIT_EXCEEDED delay without the single-provider flag. Handling
// switches providers and records the exact Gemini model and its family window.
func TestHandleSmartRetry_503_LongDelay_NoSingleProviderRetry_StillSwitches(t *testing.T) {
	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       2,
		Name:     "acc-multi",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	// RATE_LIMIT_EXCEEDED uses the advertised delay; capacity exhaustion has a separate retry path.
	respBody := []byte(`{
		"error": {
			"code": 503,
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro-high"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "39s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		ModelStore:  repo,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.SwitchError, "multi-provider mode should return switchError for 503")
	require.Nil(t, result.Resp, "should not return resp when switchError is set")

	require.Len(t, repo.modelRateLimitCalls, 2,
		"multi-provider mode SHOULD set model rate limit")
	require.Equal(t, "gemini-3-pro-high", repo.modelRateLimitCalls[0].modelKey)
	require.Equal(t, "antigravity:gemini", repo.modelRateLimitCalls[1].modelKey)
}

// TestHandleSmartRetry_429_LongDelay_SingleProviderRetry_StillSwitches enables
// single-provider mode but receives a long HTTP 429 model limit. The HTTP 503
// exception does not apply, so handling switches and records the model.
func TestHandleSmartRetry_429_LongDelay_SingleProviderRetry_StillSwitches(t *testing.T) {
	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       3,
		Name:     "acc-429",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	// The advertised delay exceeds the seven-second rate-limit threshold.
	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "15s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context: context.Background(), SingleProvider: true,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		ModelStore:  repo,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.SwitchError, "429 should still return switchError even with SingleProviderRetry")
	require.Len(t, repo.modelRateLimitCalls, 1,
		"429 should still set model rate limit even with SingleProviderRetry")
}

// TestHandleSmartRetry_503_ShortDelay_SingleProviderRetry_NoRateLimit exhausts
// the ordinary short-delay retry for RATE_LIMIT_EXCEEDED in single-provider
// mode. It returns the remaining HTTP 503 response without switching or
// writing model windows.
func TestHandleSmartRetry_503_ShortDelay_SingleProviderRetry_NoRateLimit(t *testing.T) {
	failRespBody := `{
		"error": {
			"code": 503,
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`
	failResp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(failRespBody)),
	}
	upstream := &mockSmartRetryUpstream{
		responses:  []*http.Response{failResp},
		errors:     []error{nil},
		repeatLast: true,
	}

	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       4,
		Name:     "acc-short-503",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{
		"error": {
			"code": 503,
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context: context.Background(), SingleProvider: true,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp, "should return 503 response directly for single provider mode")
	require.Equal(t, http.StatusServiceUnavailable, result.Resp.StatusCode)
	require.Nil(t, result.SwitchError, "should NOT switch provider in single provider mode")

	require.Len(t, repo.modelRateLimitCalls, 0,
		"should NOT set model rate limit for 503 in single provider mode")
}

// TestHandleSmartRetry_503_ShortDelay_NoSingleProviderRetry_SetsRateLimit
// exhausts the same short RATE_LIMIT_EXCEEDED retry without single-provider
// mode. Failure switches providers and records both the Gemini model and
// family scopes.
func TestHandleSmartRetry_503_ShortDelay_NoSingleProviderRetry_SetsRateLimit(t *testing.T) {
	failRespBody := `{
		"error": {
			"code": 503,
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`
	failResp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(failRespBody)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{failResp},
		errors:    []error{nil},
	}

	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       5,
		Name:     "acc-multi-503",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{
		"error": {
			"code": 503,
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.SwitchError, "multi-provider mode should return switchError for 503")
	require.Len(t, repo.modelRateLimitCalls, 2,
		"multi-provider mode should set model rate limit")
	require.Equal(t, "gemini-3-flash", repo.modelRateLimitCalls[0].modelKey)
	require.Equal(t, "antigravity:gemini", repo.modelRateLimitCalls[1].modelKey)
}

// TestHandleSingleProviderRetryInPlace_Success supplies one successful
// exchange after an initial HTTP 503 response. The direct in-place helper
// returns HTTP 200 with neither a switch nor a terminal error.
func TestHandleSingleProviderRetryInPlace_Success(t *testing.T) {
	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"result":"ok"}`)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{successResp},
		errors:    []error{nil},
	}

	provider := &acct.Record{
		ID:          10,
		Name:        "acc-inplace-ok",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Concurrency: 1,
	}

	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
	}

	params := AntigravityRetryRequest{
		Context: context.Background(), SingleProvider: true,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSingleProviderRetryInPlace(input, resp, nil, "https://ag-1.test", 1*time.Second, "gemini-3-pro")

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp, "should return successful response")
	require.Equal(t, http.StatusOK, result.Resp.StatusCode)
	require.Nil(t, result.SwitchError, "should not switch provider on success")
	require.Nil(t, result.Err)
}

// TestHandleSingleProviderRetryInPlace_AllRetriesFail supplies HTTP 503 for
// every permitted in-place attempt. It checks a failed HTTP 503 response after
// the maximum attempt count, with no provider switch or terminal error.
func TestHandleSingleProviderRetryInPlace_AllRetriesFail(t *testing.T) {
	var responses []*http.Response
	var errors []error
	for range antigravity.AntigravitySingleProviderSmartRetryMaxAttempts {
		responses = append(responses, &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{},
			Body: io.NopCloser(strings.NewReader(`{
				"error": {
					"code": 503,
					"status": "UNAVAILABLE",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro"}, "reason": "MODEL_CAPACITY_EXHAUSTED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
					]
				}
			}`)),
		})
		errors = append(errors, nil)
	}
	upstream := &mockSmartRetryUpstream{
		responses: responses,
		errors:    errors,
	}

	provider := &acct.Record{
		ID:          11,
		Name:        "acc-inplace-fail",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Concurrency: 1,
	}

	origBody := []byte(`{"error":{"code":503,"status":"UNAVAILABLE"}}`)
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"X-Test": {"original"}},
	}

	params := AntigravityRetryRequest{
		Context: context.Background(), SingleProvider: true,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSingleProviderRetryInPlace(input, resp, origBody, "https://ag-1.test", 1*time.Second, "gemini-3-pro")

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp, "should return 503 response directly")
	require.Equal(t, http.StatusServiceUnavailable, result.Resp.StatusCode)
	require.Nil(t, result.SwitchError, "should NOT return switchError - let Handler handle it")
	require.Nil(t, result.Err)

	require.Len(t, upstream.calls, antigravity.AntigravitySingleProviderSmartRetryMaxAttempts,
		"should have made exactly maxAttempts retry calls")
}

// TestHandleSingleProviderRetryInPlace_WaitDurationClamped passes a zero wait
// and then supplies a successful response. It checks successful completion
// without measuring the helper's minimum wait or elapsed duration.
func TestHandleSingleProviderRetryInPlace_WaitDurationClamped(t *testing.T) {
	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"result":"ok"}`)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{successResp},
		errors:    []error{nil},
	}

	provider := &acct.Record{
		ID:          12,
		Name:        "acc-clamp",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Concurrency: 1,
	}

	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
	}

	params := AntigravityRetryRequest{
		Context: context.Background(), SingleProvider: true,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
	}

	svc := newAntigravityRetryFixture()

	// The implementation raises zero to its minimum wait, but these assertions
	// check successful completion only and do not measure that wait.
	adapter, input := svc.Bind(params)
	result := adapter.HandleSingleProviderRetryInPlace(input, resp, nil, "https://ag-1.test", 0, "gemini-3-pro")
	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp)
	require.Equal(t, http.StatusOK, result.Resp.StatusCode)
}

// TestHandleSingleProviderRetryInPlace_ContextCanceled cancels the request
// context before entering the direct retry helper. Waiting exits with a
// terminal error before the transport records any request.
func TestHandleSingleProviderRetryInPlace_ContextCanceled(t *testing.T) {
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{nil},
		errors:    []error{nil},
	}

	provider := &acct.Record{
		ID:          13,
		Name:        "acc-cancel",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Concurrency: 1,
	}

	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel before entering retry handling so the context-aware wait returns
	// a terminal error before sending any subsequent exchange or persisting a
	// model window.
	cancel()

	params := AntigravityRetryRequest{
		Context: ctx, SingleProvider: true,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSingleProviderRetryInPlace(input, resp, nil, "https://ag-1.test", 1*time.Second, "gemini-3-pro")

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.Error(t, result.Err, "should return context error")
	require.Len(t, upstream.calls, 0, "should not call upstream when context is canceled")
}

// TestHandleSingleProviderRetryInPlace_NetworkError_ContinuesRetry supplies a
// nil response and nil error before a successful exchange. The empty exchange
// consumes one attempt, and the second request succeeds.
func TestHandleSingleProviderRetryInPlace_NetworkError_ContinuesRetry(t *testing.T) {
	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"result":"ok"}`)),
	}
	upstream := &mockSmartRetryUpstream{
		// Both response and error are nil for this configured slot; it
		// consumes a failed-exchange attempt before the second slot supplies
		// success.
		responses: []*http.Response{nil, successResp},
		errors:    []error{nil, nil},
	}

	provider := &acct.Record{
		ID:          14,
		Name:        "acc-net-retry",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Concurrency: 1,
	}

	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
	}

	params := AntigravityRetryRequest{
		Context: context.Background(), SingleProvider: true,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSingleProviderRetryInPlace(input, resp, nil, "https://ag-1.test", 1*time.Second, "gemini-3-pro")

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp, "should return successful response after network error recovery")
	require.Equal(t, http.StatusOK, result.Resp.StatusCode)
	require.Len(t, upstream.calls, 2, "first call fails (network error), second succeeds")
}

// TestAntigravityRetryLoop_PreCheck_SingleProviderRetry_SkipsRateLimit enables
// single-provider mode while a model window remains active. The precheck
// permits one successful upstream request instead of returning a provider
// switch.
func TestAntigravityRetryLoop_PreCheck_SingleProviderRetry_SkipsRateLimit(t *testing.T) {
	upstream := &recordingOKUpstream{}
	provider := &acct.Record{
		ID:          20,
		Name:        "acc-rate-limited",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Extra: map[string]any{
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limit_reset_at": time.Now().Add(30 * time.Second).Format(time.RFC3339),
				},
			},
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context: context.Background(), SingleProvider: true,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		RequestedModel: "claude-sonnet-4-5",
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.NoError(t, err, "should not return error")
	require.NotNil(t, result, "should return result")
	require.NotNil(t, result.Resp, "should have response")
	require.Equal(t, http.StatusOK, result.Resp.StatusCode)
	require.Equal(t, 1, upstream.calls, "should have reached upstream despite rate limit")
}

// TestAntigravityRetryLoop_PreCheck_NoSingleProviderRetry_SwitchesOnRateLimit
// leaves single-provider mode disabled with the same active model window. The
// outer loop switches before the transport records any request.
func TestAntigravityRetryLoop_PreCheck_NoSingleProviderRetry_SwitchesOnRateLimit(t *testing.T) {
	upstream := &recordingOKUpstream{}
	provider := &acct.Record{
		ID:          21,
		Name:        "acc-rate-limited-multi",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Extra: map[string]any{
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limit_reset_at": time.Now().Add(30 * time.Second).Format(time.RFC3339),
				},
			},
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		RequestedModel: "claude-sonnet-4-5",
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.Nil(t, result, "should not return result on rate limit switch")
	require.NotNil(t, err, "should return error")

	var switchErr *antigravity.AntigravityProviderSwitchError
	require.ErrorAs(t, err, &switchErr, "should return AntigravityProviderSwitchError")
	require.Equal(t, provider.ID, switchErr.OriginalProviderID)
	require.Equal(t, "claude-sonnet-4-5", switchErr.RateLimitedModel)

	require.Equal(t, 0, upstream.calls, "upstream should NOT be called when pre-check blocks")
}

// TestHandleSmartRetry_503_SingleProvider_RetryInPlace_ThenSuccess_E2E calls
// the in-place helper directly with a failed exchange followed by success. Two
// requests produce HTTP 200 without a provider switch.
func TestHandleSmartRetry_503_SingleProvider_RetryInPlace_ThenSuccess_E2E(t *testing.T) {
	fail503Body := `{
		"error": {
			"code": 503,
			"status": "UNAVAILABLE",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro"}, "reason": "MODEL_CAPACITY_EXHAUSTED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`
	resp503 := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(fail503Body)),
	}
	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"result":"ok"}`)),
	}

	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{resp503, successResp},
		errors:    []error{nil, nil},
	}

	provider := &acct.Record{
		ID:          30,
		Name:        "acc-e2e",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Concurrency: 1,
	}

	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
	}

	params := AntigravityRetryRequest{
		Context: context.Background(), SingleProvider: true,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSingleProviderRetryInPlace(input, resp, nil, "https://ag-1.test", 1*time.Second, "gemini-3-pro")

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp, "should return successful response after 2nd attempt")
	require.Equal(t, http.StatusOK, result.Resp.StatusCode)
	require.Nil(t, result.SwitchError)
	require.Len(t, upstream.calls, 2, "first 503, second OK")
}

// TestAntigravityRetryLoop_503_SingleProvider_InPlaceRetryUsed_E2E sends the
// outer loop through an initial capacity failure and successful in-place
// recovery. The returned HTTP 200 requires no model-limit persistence.
func TestAntigravityRetryLoop_503_SingleProvider_InPlaceRetryUsed_E2E(t *testing.T) {
	initial503Body := []byte(`{
		"error": {
			"code": 503,
			"status": "UNAVAILABLE",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro"}, "reason": "MODEL_CAPACITY_EXHAUSTED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "10s"}
			],
			"message": "No capacity available"
		}
	}`)
	initial503Resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(initial503Body)),
	}

	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"result":"ok"}`)),
	}

	upstream := &mockSmartRetryUpstream{
		// The initial exchange fails; the in-place retry succeeds.
		responses: []*http.Response{initial503Resp, successResp},
		errors:    []error{nil, nil},
	}

	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:          31,
		Name:        "acc-e2e-loop",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context: context.Background(), SingleProvider: true,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.NoError(t, err, "should not return error on successful retry")
	require.NotNil(t, result, "should return result")
	require.NotNil(t, result.Resp, "should return response")
	require.Equal(t, http.StatusOK, result.Resp.StatusCode)

	require.Len(t, repo.modelRateLimitCalls, 0,
		"should NOT set model rate limit in single provider retry mode")
}

// TestHandleSmartRetry_URLLevelRateLimit submits generic resource exhaustion
// while another URL remains in the attempt input. Handling requests URL
// continuation without a response, provider switch or terminal error.
func TestHandleSmartRetry_URLLevelRateLimit(t *testing.T) {
	provider := &acct.Record{
		ID:       1,
		Name:     "acc-1",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{"error":{"message":"Resource has been exhausted"}}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test", "https://ag-2.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionContinueURL, result.Action)
	require.Nil(t, result.Resp)
	require.Nil(t, result.Err)
	require.Nil(t, result.SwitchError)
}

// TestHandleSmartRetry_LongDelay_ReturnsSwitchError submits a model retry
// delay above the short-retry threshold for a sticky provider. The result
// records the model window and carries the provider, model and sticky flag.
func TestHandleSmartRetry_LongDelay_ReturnsSwitchError(t *testing.T) {
	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       1,
		Name:     "acc-1",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	// A delay above the seven-second threshold requests a provider switch.
	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "15s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		ModelStore:  repo,
		Sticky:      true,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.Nil(t, result.Resp, "should not return resp when switchError is set")
	require.Nil(t, result.Err)
	require.NotNil(t, result.SwitchError, "should return switchError for long delay")
	require.Equal(t, provider.ID, result.SwitchError.OriginalProviderID)
	require.Equal(t, "claude-sonnet-4-5", result.SwitchError.RateLimitedModel)
	require.True(t, result.SwitchError.IsStickySession)

	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "claude-sonnet-4-5", repo.modelRateLimitCalls[0].modelKey)
}

// TestHandleSmartRetry_ShortDelay_SmartRetrySuccess supplies a successful
// exchange after a sub-threshold model delay. One retry forwards the user
// agent and returns HTTP 200 without switching or a terminal error.
func TestHandleSmartRetry_ShortDelay_SmartRetrySuccess(t *testing.T) {
	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"result":"ok"}`)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{successResp},
		errors:    []error{nil},
	}

	provider := &acct.Record{
		ID:       1,
		Name:     "acc-1",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	// A sub-threshold delay permits a retry.
	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-opus-4"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.5s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		UserAgent:   "probe-client/9.9",
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp, "should return successful response")
	require.Equal(t, http.StatusOK, result.Resp.StatusCode)
	require.Nil(t, result.Err)
	require.Nil(t, result.SwitchError, "should not return switchError on success")
	require.Len(t, upstream.calls, 1, "should have made one retry call")
	require.Equal(t, "probe-client/9.9", upstream.userAgents[0])
}

// TestHandleSmartRetry_ShortDelay_SmartRetryFailed_ReturnsSwitchError supplies
// another HTTP 429 after the single permitted short-delay retry. Failure
// records both Gemini scopes and returns a non-sticky provider switch.
func TestHandleSmartRetry_ShortDelay_SmartRetryFailed_ReturnsSwitchError(t *testing.T) {
	// One failed response exhausts the rate-limit retry budget.
	failRespBody := `{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`
	failResp1 := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(failRespBody)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{failResp1},
		errors:    []error{nil},
	}

	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       2,
		Name:     "acc-2",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	// The 0.1-second delay permits the single retry.
	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		Sticky:     false,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.Nil(t, result.Resp, "should not return resp when switchError is set")
	require.Nil(t, result.Err)
	require.NotNil(t, result.SwitchError, "should return switchError after smart retry failed")
	require.Equal(t, provider.ID, result.SwitchError.OriginalProviderID)
	require.Equal(t, "gemini-3-flash", result.SwitchError.RateLimitedModel)
	require.False(t, result.SwitchError.IsStickySession)

	// Gemini limits include both the exact model and the shared family window.
	require.Len(t, repo.modelRateLimitCalls, 2)
	require.Equal(t, "gemini-3-flash", repo.modelRateLimitCalls[0].modelKey)
	require.Equal(t, "antigravity:gemini", repo.modelRateLimitCalls[1].modelKey)
	require.Len(t, upstream.calls, 1, "should have made one retry call (max attempts)")
}

// TestHandleSmartRetry_503_ModelCapacityExhausted_RetrySuccess advertises a
// 39-second delay for capacity exhaustion and then succeeds. Capacity handling
// uses its separate retry path without recording a model limit or switching.
func TestHandleSmartRetry_503_ModelCapacityExhausted_RetrySuccess(t *testing.T) {
	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       3,
		Name:     "acc-3",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	// Capacity exhaustion uses a fixed retry wait even when the upstream advertises 39 seconds.
	respBody := []byte(`{
		"error": {
			"code": 503,
			"status": "UNAVAILABLE",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro-high"}, "reason": "MODEL_CAPACITY_EXHAUSTED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "39s"}
			],
			"message": "No capacity available for model gemini-3-pro-high on the server"
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{
			{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))},
		},
		errors: []error{nil},
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		ModelStore:  repo,
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		Sticky: true,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp, "should return successful response")
	require.Equal(t, http.StatusOK, result.Resp.StatusCode)
	require.Nil(t, result.Err)
	require.Nil(t, result.SwitchError, "MODEL_CAPACITY_EXHAUSTED should not return switchError")

	require.Empty(t, repo.modelRateLimitCalls, "MODEL_CAPACITY_EXHAUSTED should not set model rate limit")
	require.Len(t, upstream.calls, 1, "should have made one retry call before success")
}

// TestHandleSmartRetry_503_ModelCapacityExhausted_ContextCancel cancels before
// the capacity retry wait can complete. The result carries a terminal error
// with no provider switch or model-limit persistence.
func TestHandleSmartRetry_503_ModelCapacityExhausted_ContextCancel(t *testing.T) {
	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       3,
		Name:     "acc-3",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{
		"error": {
			"code": 503,
			"status": "UNAVAILABLE",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro-high"}, "reason": "MODEL_CAPACITY_EXHAUSTED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "39s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel before entering retry handling so the context-aware wait returns
	// a terminal error before sending any subsequent exchange or persisting a
	// model window.
	cancel()

	params := AntigravityRetryRequest{
		Context:     ctx,
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		ModelStore:  repo,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, []string{"https://ag-1.test"})

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.Error(t, result.Err, "should return context error")
	require.Nil(t, result.SwitchError, "should not return switchError on context cancel")
	require.Empty(t, repo.modelRateLimitCalls, "should not set model rate limit on context cancel")
}

// TestHandleSmartRetry_NonAntigravityProvider_ContinuesDefaultLogic pairs
// Antigravity-shaped rate-limit details with an Anthropic provider. Platform
// handling declines the response and leaves default retry processing to its
// caller.
func TestHandleSmartRetry_NonAntigravityProvider_ContinuesDefaultLogic(t *testing.T) {
	provider := &acct.Record{
		ID:       4,
		Name:     "acc-4",
		Type:     capability.ProviderTypeAPIKey,
		Platform: capability.PlatformAnthropic,
	}

	// Antigravity model-limit handling does not apply to an Anthropic provider.
	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "15s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionContinue, result.Action, "non-Antigravity platform provider should continue default logic")
	require.Nil(t, result.Resp)
	require.Nil(t, result.Err)
	require.Nil(t, result.SwitchError)
}

// TestHandleSmartRetry_NonModelRateLimit_ContinuesDefaultLogic submits HTTP
// 429 with RetryInfo but no recognized model-limit or capacity reason.
// Handling continues without returning a response, switch or terminal error.
func TestHandleSmartRetry_NonModelRateLimit_ContinuesDefaultLogic(t *testing.T) {
	provider := &acct.Record{
		ID:       5,
		Name:     "acc-5",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	// The 429 lacks a model-limit or capacity-exhaustion reason.
	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "5s"}
			],
			"message": "Quota exceeded"
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionContinue, result.Action, "non-model rate limit should continue default logic")
	require.Nil(t, result.Resp)
	require.Nil(t, result.Err)
	require.Nil(t, result.SwitchError)
}

// TestHandleSmartRetry_ExactlyAtThreshold_ReturnsSwitchError supplies the
// seven-second boundary as the parsed model delay. The boundary takes the
// provider-switch path and retains the Gemini model identity.
func TestHandleSmartRetry_ExactlyAtThreshold_ReturnsSwitchError(t *testing.T) {
	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       6,
		Name:     "acc-6",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	// The threshold itself takes the provider-switch path.
	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-pro"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "7s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		ModelStore:  repo,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.Nil(t, result.Resp)
	require.NotNil(t, result.SwitchError, "exactly at threshold should return switchError")
	require.Equal(t, "gemini-pro", result.SwitchError.RateLimitedModel)
}

// TestAntigravityRetryLoop_HandleSmartRetry_SwitchError_Propagates sends the
// outer loop a long model rate limit for a sticky provider. It returns no
// response and preserves the switch error's provider, model and sticky flag.
func TestAntigravityRetryLoop_HandleSmartRetry_SwitchError_Propagates(t *testing.T) {
	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-opus-4-6"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "30s"}
			]
		}
	}`)
	rateLimitResp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{rateLimitResp},
		errors:    []error{nil},
	}

	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:          7,
		Name:        "acc-7",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		Sticky:     true,
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.Nil(t, result, "should not return result when switchError")
	require.NotNil(t, err, "should return error")

	var switchErr *antigravity.AntigravityProviderSwitchError
	require.ErrorAs(t, err, &switchErr, "error should be AntigravityProviderSwitchError")
	require.Equal(t, provider.ID, switchErr.OriginalProviderID)
	require.Equal(t, "claude-opus-4-6", switchErr.RateLimitedModel)
	require.True(t, switchErr.IsStickySession)
}

// TestHandleSmartRetry_NetworkError_ExhaustsRetry supplies a nil response and
// nil error for the one permitted short-delay exchange. The exhausted path
// records the Claude model limit and requests a provider switch.
func TestHandleSmartRetry_NetworkError_ExhaustsRetry(t *testing.T) {
	// The transport supplies nil response and nil error for its only slot; the
	// empty exchange exhausts the ordinary one-attempt retry budget.
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{nil},
		errors:    []error{nil},
	}

	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       8,
		Name:     "acc-8",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.Nil(t, result.Resp, "should not return resp when switchError is set")
	require.NotNil(t, result.SwitchError, "should return switchError after network error exhausted retry")
	require.Equal(t, provider.ID, result.SwitchError.OriginalProviderID)
	require.Equal(t, "claude-sonnet-4-5", result.SwitchError.RateLimitedModel)
	require.Len(t, upstream.calls, 1, "should have made one retry call")

	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "claude-sonnet-4-5", repo.modelRateLimitCalls[0].modelKey)
}

// TestHandleSmartRetry_NoRetryDelay_UsesDefaultRateLimit omits RetryInfo from
// a recognized model rate-limit response. Handling switches the sticky
// provider and records its model without asserting an exact default reset
// duration.
func TestHandleSmartRetry_NoRetryDelay_UsesDefaultRateLimit(t *testing.T) {
	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       9,
		Name:     "acc-9",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	// A missing retryDelay uses the default model-limit duration.
	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"}
			],
			"message": "You have exhausted your capacity on this model."
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		ModelStore:  repo,
		Sticky:      true,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.Nil(t, result.Resp, "should not return resp when switchError is set")
	require.NotNil(t, result.SwitchError, "should return switchError for no retryDelay")
	require.Equal(t, "claude-sonnet-4-5", result.SwitchError.RateLimitedModel)
	require.True(t, result.SwitchError.IsStickySession)

	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "claude-sonnet-4-5", repo.modelRateLimitCalls[0].modelKey)
}

// TestSmartRetryMaxAttempts_VerifyConstant asserts that ordinary model rate
// limits receive one smart retry. Capacity and single-provider paths have
// separate attempt budgets and are covered by their own cases.
func TestSmartRetryMaxAttempts_VerifyConstant(t *testing.T) {
	require.Equal(t, 1, antigravity.AntigravitySmartRetryMaxAttempts,
		"antigravity.AntigravitySmartRetryMaxAttempts should be 1 to prevent repeated rate limiting")
}

// TestHandleSmartRetry_ShortDelay_StickySession_FailedRetry_ClearsSession
// binds a sticky-cache deletion callback and exhausts a short Claude retry.
// It checks the model-window key, sticky switch details and one deletion using
// the supplied group/hash.
func TestHandleSmartRetry_ShortDelay_StickySession_FailedRetry_ClearsSession(t *testing.T) {
	failRespBody := `{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`
	failResp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(failRespBody)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{failResp},
		errors:    []error{nil},
	}

	repo := &antigravityRetryStoreFixture{}
	cache := &stubSmartRetryCache{}
	provider := &acct.Record{
		ID:       10,
		Name:     "acc-10",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		Sticky:     true,
		// Bind deletion to group 42 and sticky-hash-abc so assertions observe
		// the exact session identity; the fixture always succeeds and its
		// ignored error does not affect retry results.
		ClearSticky: func() { _ = cache.DeleteSessionProviderID(context.Background(), 42, "sticky-hash-abc") },
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.SwitchError)
	require.True(t, result.SwitchError.IsStickySession, "switchError should carry IsStickySession=true")
	require.Equal(t, provider.ID, result.SwitchError.OriginalProviderID)

	require.Len(t, cache.deleteCalls, 1, "should call DeleteSessionProviderID exactly once")
	require.Equal(t, int64(42), cache.deleteCalls[0].groupID)
	require.Equal(t, "sticky-hash-abc", cache.deleteCalls[0].sessionHash)

	require.Len(t, upstream.calls, 1, "should make exactly 1 retry call (maxAttempts=1)")

	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "claude-sonnet-4-5", repo.modelRateLimitCalls[0].modelKey)
}

// TestHandleSmartRetry_ShortDelay_NonStickySession_FailedRetry_NoDeleteSession
// exhausts a short retry without binding a sticky-clear callback. The switch
// remains non-sticky and the separately created, unbound cache records no
// deletion.
func TestHandleSmartRetry_ShortDelay_NonStickySession_FailedRetry_NoDeleteSession(t *testing.T) {
	failRespBody := `{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`
	failResp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(failRespBody)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{failResp},
		errors:    []error{nil},
	}

	repo := &antigravityRetryStoreFixture{}
	cache := &stubSmartRetryCache{}
	provider := &acct.Record{
		ID:       11,
		Name:     "acc-11",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		Sticky:     false,
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.SwitchError)
	require.False(t, result.SwitchError.IsStickySession)

	require.Len(t, cache.deleteCalls, 0, "should NOT call DeleteSessionProviderID when sessionHash is empty")
}

// TestHandleSmartRetry_ShortDelay_StickySession_FailedRetry_NilCache_NoPanic
// marks the request sticky but leaves the clear callback unset. Failed retry
// handling remains callable and returns a switch retaining the sticky flag.
func TestHandleSmartRetry_ShortDelay_StickySession_FailedRetry_NilCache_NoPanic(t *testing.T) {
	failRespBody := `{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`
	failResp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(failRespBody)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{failResp},
		errors:    []error{nil},
	}

	repo := &antigravityRetryStoreFixture{}
	provider := &acct.Record{
		ID:       12,
		Name:     "acc-12",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		Sticky:     true,

		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	require.NotPanics(t, func() {
		adapter, input := svc.Bind(params)
		result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)
		require.NotNil(t, result)
		require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
		require.NotNil(t, result.SwitchError)
		require.True(t, result.SwitchError.IsStickySession)
	})
}

// TestHandleSmartRetry_ShortDelay_StickySession_SuccessRetry_NoDeleteSession
// binds a sticky-clear callback and then completes the short retry
// successfully. The returned HTTP 200 leaves the cache deletion record empty.
func TestHandleSmartRetry_ShortDelay_StickySession_SuccessRetry_NoDeleteSession(t *testing.T) {
	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"result":"ok"}`)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{successResp},
		errors:    []error{nil},
	}

	cache := &stubSmartRetryCache{}
	provider := &acct.Record{
		ID:       13,
		Name:     "acc-13",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-opus-4"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.5s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		Sticky: true,
		// Bind deletion to group 42 and sticky-hash-success so assertions
		// observe the exact session identity; the fixture always succeeds and
		// its ignored error does not affect retry results.
		ClearSticky: func() { _ = cache.DeleteSessionProviderID(context.Background(), 42, "sticky-hash-success") },
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.Resp, "should return successful response")
	require.Equal(t, http.StatusOK, result.Resp.StatusCode)
	require.Nil(t, result.SwitchError, "should not return switchError on success")

	require.Len(t, cache.deleteCalls, 0, "should NOT call DeleteSessionProviderID on successful retry")
}

// TestHandleSmartRetry_LongDelay_StickySession_ClearsSession binds a
// sticky-clear callback before submitting a long model delay. The
// provider-switch result follows one deletion using the configured group and
// session hash.
func TestHandleSmartRetry_LongDelay_StickySession_ClearsSession(t *testing.T) {
	repo := &antigravityRetryStoreFixture{}
	cache := &stubSmartRetryCache{}
	provider := &acct.Record{
		ID:       14,
		Name:     "acc-14",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "15s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		ModelStore:  repo,
		Sticky:      true,
		// Bind deletion to group 42 and sticky-hash-long-delay so assertions
		// observe the exact session identity; the fixture always succeeds and
		// its ignored error does not affect retry results.
		ClearSticky: func() { _ = cache.DeleteSessionProviderID(context.Background(), 42, "sticky-hash-long-delay") },
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.Equal(t, antigravity.SmartRetryActionBreakWithResp, result.Action)
	require.NotNil(t, result.SwitchError)
	require.True(t, result.SwitchError.IsStickySession)

	require.Len(t, cache.deleteCalls, 1, "long delay path should clear sticky session in handleSmartRetry")
	require.Equal(t, int64(42), cache.deleteCalls[0].groupID)
	require.Equal(t, "sticky-hash-long-delay", cache.deleteCalls[0].sessionHash)
}

// TestHandleSmartRetry_ShortDelay_NetworkError_StickySession_ClearsSession
// returns an empty exchange with nil error after a short Gemini limit.
// It checks both Gemini scope keys, sticky switch details and one deletion
// using the bound session identity.
func TestHandleSmartRetry_ShortDelay_NetworkError_StickySession_ClearsSession(t *testing.T) {
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{nil}, // Simulate a failed exchange.
		errors:    []error{nil},
	}

	repo := &antigravityRetryStoreFixture{}
	cache := &stubSmartRetryCache{}
	provider := &acct.Record{
		ID:       15,
		Name:     "acc-15",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		Sticky:     true,
		// Bind deletion to group 99 and sticky-net-error so assertions observe
		// the exact session identity; the fixture always succeeds and its
		// ignored error does not affect retry results.
		ClearSticky: func() { _ = cache.DeleteSessionProviderID(context.Background(), 99, "sticky-net-error") },
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.NotNil(t, result.SwitchError)
	require.True(t, result.SwitchError.IsStickySession)

	require.Len(t, cache.deleteCalls, 1, "should call DeleteSessionProviderID after network error exhausts retry")
	require.Equal(t, int64(99), cache.deleteCalls[0].groupID)
	require.Equal(t, "sticky-net-error", cache.deleteCalls[0].sessionHash)

	require.Len(t, repo.modelRateLimitCalls, 2)
	require.Equal(t, "gemini-3-flash", repo.modelRateLimitCalls[0].modelKey)
	require.Equal(t, "antigravity:gemini", repo.modelRateLimitCalls[1].modelKey)
}

// TestHandleSmartRetry_ShortDelay_503_StickySession_FailedRetry_ClearsSession
// uses HTTP 429 for both actual exchanges despite its name. The failed short
// Gemini retry records both scopes and deletes the supplied sticky session.
func TestHandleSmartRetry_ShortDelay_503_StickySession_FailedRetry_ClearsSession(t *testing.T) {
	failRespBody := `{
		"error": {
			"code": 429,
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.5s"}
			]
		}
	}`
	failResp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(failRespBody)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{failResp},
		errors:    []error{nil},
	}

	repo := &antigravityRetryStoreFixture{}
	cache := &stubSmartRetryCache{}
	provider := &acct.Record{
		ID:       16,
		Name:     "acc-16",
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformAntigravity,
	}

	respBody := []byte(`{
		"error": {
			"code": 429,
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.5s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	params := AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		Sticky:     true,
		// Bind deletion to group 77 and sticky-503-short so assertions observe
		// the exact session identity; the fixture always succeeds and its
		// ignored error does not affect retry results.
		ClearSticky: func() { _ = cache.DeleteSessionProviderID(context.Background(), 77, "sticky-503-short") },
		HandleError: func(int, http.Header, []byte) {
		},
	}

	availableURLs := []string{"https://ag-1.test"}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(params)
	result := adapter.HandleSmartRetry(input, resp, respBody, "https://ag-1.test", 0, availableURLs)

	require.NotNil(t, result)
	require.NotNil(t, result.SwitchError)
	require.True(t, result.SwitchError.IsStickySession)

	require.Len(t, cache.deleteCalls, 1)
	require.Equal(t, int64(77), cache.deleteCalls[0].groupID)
	require.Equal(t, "sticky-503-short", cache.deleteCalls[0].sessionHash)

	// Gemini limits include both the exact model and the shared family window.
	require.Len(t, repo.modelRateLimitCalls, 2)
	require.Equal(t, "gemini-3-pro", repo.modelRateLimitCalls[0].modelKey)
	require.Equal(t, "antigravity:gemini", repo.modelRateLimitCalls[1].modelKey)
}

// TestAntigravityRetryLoop_SmartRetryFailed_StickySession_SwitchErrorPropagates
// exhausts a short retry through the outer loop with a bound sticky-clear
// callback. It checks the propagated switch retains provider/model identity
// and that one deletion uses the configured session.
func TestAntigravityRetryLoop_SmartRetryFailed_StickySession_SwitchErrorPropagates(t *testing.T) {
	initialRespBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-opus-4-6"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`)
	initialResp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(initialRespBody)),
	}

	retryRespBody := `{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-opus-4-6"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`
	retryResp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(retryRespBody)),
	}

	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{initialResp, retryResp},
		errors:    []error{nil, nil},
	}

	repo := &antigravityRetryStoreFixture{}
	cache := &stubSmartRetryCache{}
	provider := &acct.Record{
		ID:          17,
		Name:        "acc-17",
		Type:        capability.ProviderTypeOAuth,
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		ModelStore: repo,
		Sticky:     true,
		// Bind deletion to group 55 and sticky-loop-test so assertions observe
		// the exact session identity; the fixture always succeeds and its
		// ignored error does not affect retry results.
		ClearSticky: func() { _ = cache.DeleteSessionProviderID(context.Background(), 55, "sticky-loop-test") },
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.Nil(t, result, "should not return result when switchError")
	require.NotNil(t, err, "should return error")

	var switchErr *antigravity.AntigravityProviderSwitchError
	require.ErrorAs(t, err, &switchErr, "error should be AntigravityProviderSwitchError")
	require.Equal(t, provider.ID, switchErr.OriginalProviderID)
	require.Equal(t, "claude-opus-4-6", switchErr.RateLimitedModel)
	require.True(t, switchErr.IsStickySession, "IsStickySession must propagate through retryLoop")

	require.Len(t, cache.deleteCalls, 1, "should clear sticky session in handleSmartRetry")
	require.Equal(t, int64(55), cache.deleteCalls[0].groupID)
	require.Equal(t, "sticky-loop-test", cache.deleteCalls[0].sessionHash)
}

// Do counts a serial exchange and returns the configured status and body
// without inspecting routing inputs. Each response owns a fresh reader that
// its consumer may read and close.
func (u *epFixedUpstream) Do(req *http.Request, proxyURL string, providerID int64, providerConcurrency int) (*http.Response, error) {
	u.calls++
	return &http.Response{
		StatusCode: u.statusCode,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(u.body)),
	}, nil
}

// DoWithTLS delegates to the same fixed-response transport and counter. The
// supplied TLS profile is ignored because this fixture models response
// handling without opening a connection.
func (u *epFixedUpstream) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

// SetTempUnschedulable counts a provider-wide temporary health write and
// succeeds.
func (r *epProviderRepo) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, _ string) error {
	r.tempCalls++
	return nil
}

// SetError counts a generic provider error write and succeeds.
func (r *epProviderRepo) SetError(_ context.Context, _ int64, _ string) error {
	r.setErrCalls++
	return nil
}

// saveAndSetBaseURLs installs one deterministic endpoint and a fresh
// availability tracker. Its test cleanup restores the copied endpoint list and
// previous tracker; callers must keep these process-wide mutations serial.
func saveAndSetBaseURLs(t *testing.T) {
	t.Helper()
	// Copy the process-wide endpoints and retain their availability tracker
	// before installing the isolated test endpoint; callers must avoid
	// parallel mutation of these inputs.
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvail := antigravity.DefaultURLAvailability
	antigravity.BaseURLs = []string{"https://ep-test.example"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)
	// Restore the saved endpoint list and tracker after the owning test
	// completes, including cleanup registered by nested nonparallel cases.
	t.Cleanup(func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvail
	})
}

// newRetryParams binds a fixed transport, Claude model, request body and error
// callback to the supplied provider. Provider and transport must be non-nil
// when its Do callback executes; callers may replace the background context.
func newRetryParams(provider *acct.Record, upstream *epFixedUpstream, handleError func(int, http.Header, []byte)) AntigravityRetryRequest {
	return AntigravityRetryRequest{
		Context:     context.Background(),
		Prefix:      "[ep-test]",
		Provider:    provider,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", provider.ID, provider.Concurrency)
		},
		RequestedModel: "claude-sonnet-4-5",
		HandleError:    handleError,
	}
}

// SetRateLimited counts a generic rate-limit write and retains its provider
// ID.
func (r *epTrackingRepo) SetRateLimited(_ context.Context, id int64, _ time.Time) error {
	r.rateLimitedCalls++
	r.rateLimitedID = id
	return nil
}

// SetError counts a generic error write and retains its provider ID.
func (r *epTrackingRepo) SetError(_ context.Context, id int64, _ string) error {
	r.setErrCalls++
	r.setErrID = id
	return nil
}

// SetTempUnschedulable counts a provider-wide temporary write without
// retaining its parameters.
func (r *epTrackingRepo) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, _ string) error {
	r.tempCalls++
	return nil
}

// SetModelRateLimit accepts model writes without recording or updating
// provider state.
func (r *epProviderRepo) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	return nil
}

// SetModelRateLimit succeeds without changing the separately observed
// provider-level counters.
func (r *epTrackingRepo) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	return nil
}

// SetModelRateLimit appends the requested scope key in call order.
func (s *antigravityFamilyStoreFixture) SetModelRateLimit(_ context.Context, _ int64, key string, _ time.Time, _ ...string) error {
	s.modelRateLimitCalls = append(s.modelRateLimitCalls, struct {
		modelKey string /* Requested model or family key. */
	}{key})
	return nil
}

// SetTempUnschedulable counts provider-wide temporary writes independently of
// model scopes.
func (s *antigravityPolicyStoreFixture) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	s.tempCalls++
	return nil
}

// SetModelRateLimit records the requested key in both scope and modelKey
// observations.
func (s *antigravityPolicyStoreFixture) SetModelRateLimit(_ context.Context, _ int64, key string, _ time.Time, _ ...string) error {
	s.modelRateLimitCalls = append(s.modelRateLimitCalls, struct { /* scope records the health scope; modelKey records the model
		assertion key. Both retain the same argument without normalization, so
		the duplicated observations do not represent independently selected scopes. */
		scope, modelKey string
	}{key, key})
	return nil
}

// UpdateExtra accepts extra-data writes without recording or applying the
// supplied values.
func (s *antigravityPolicyStoreFixture) UpdateExtra(context.Context, int64, map[string]any) error {
	return nil
}

// newAntigravityPolicyFixture wires policy evaluation to the recording model
// store and production scope mapping. Its logging callbacks discard output,
// and it supplies no network transport for request execution.
func newAntigravityPolicyFixture(store *antigravityPolicyStoreFixture, policy *acct.HealthService) *AntigravityRetry {
	return &AntigravityRetry{Policy: policy, Health: &acct.AntigravityHealth{Store: store, ModelKeys: AntigravityModelLimitKeys, Info: func(string, ...any) {}, Logf: func(string, ...any) {}}}
}

// Do records each request URL and returns HTTP 429 for the firstBase prefix.
// Every other URL receives HTTP 200; secondBase does not participate in
// response selection, and each body has a fresh reader.
func (s *stubAntigravityUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	url := req.URL.String()
	s.calls = append(s.calls, url)
	if strings.HasPrefix(url, s.firstBase) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Resource has been exhausted"}}`))}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

// Do counts each serial exchange and returns a fresh HTTP 200 response.
// Routing arguments are unused, and the caller consumes and closes the newly
// allocated response body.
func (r *recordingOKUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	r.calls++
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

// DeleteSessionProviderID records the supplied group and session hash and
// always succeeds.
func (c *stubSmartRetryCache) DeleteSessionProviderID(_ context.Context, groupID int64, sessionHash string) error {
	c.deleteCalls = append(c.deleteCalls, deleteSessionCall{groupID: groupID, sessionHash: sessionHash})
	return nil
}

// Do captures the URL, user agent and request bytes before selecting the next
// configured response. Request and URL must be non-nil, error entries must
// cover every selected response slot, and calls must remain serial. A nil
// response returns its paired error; exhausting the list returns an empty
// exchange unless repeatLast selects the final slot.
//
// Response bodies are read once into a byte cache and the original reader is
// closed. Each non-nil result shallow-copies the response and replaces its
// cached body with a fresh reader, while headers and other referenced values
// remain shared. Read and close errors are discarded by this fixture;
// request-body replacement retains the captured bytes without closing the
// previous request reader.
func (m *mockSmartRetryUpstream) Do(req *http.Request, proxyURL string, providerID int64, providerConcurrency int) (*http.Response, error) {
	idx := m.callIdx
	m.calls = append(m.calls, req.URL.String())
	m.userAgents = append(m.userAgents, req.Header.Get("User-Agent"))
	// Capture request bytes and replace the consumed reader for later
	// inspection; read errors retain partial bytes, and this fixture does not
	// close the old request body.
	if req != nil && req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		m.requestBodies = append(m.requestBodies, body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	} else {
		m.requestBodies = append(m.requestBodies, nil)
	}
	m.callIdx++

	// Sequence exhaustion either produces an empty exchange or reuses the
	// final configured response/error pair; every transport call still
	// advances the captured call index.
	respIdx := idx
	if respIdx >= len(m.responses) {
		if !m.repeatLast || len(m.responses) == 0 {
			return nil, nil
		}
		respIdx = len(m.responses) - 1
	}

	resp := m.responses[respIdx]
	respErr := m.errors[respIdx]
	if resp == nil {
		return nil, respErr
	}

	// Grow the cache to the selected response slot before capturing its body;
	// later repeats reuse these bytes rather than rereading a consumed
	// original response reader.
	if respIdx >= len(m.responseBodies) {
		for len(m.responseBodies) <= respIdx {
			m.responseBodies = append(m.responseBodies, nil)
		}
	}
	// Read and close the original response body once, discarding both errors
	// while retaining any captured bytes so retries can rebuild readable
	// response bodies.
	if m.responseBodies[respIdx] == nil && resp.Body != nil {
		bodyBytes, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		m.responseBodies[respIdx] = bodyBytes
	}

	// Shallow-copy the configured response and rebuild only its cached body
	// reader; callers can consume each replay independently while headers and
	// other referenced values remain shared.
	cloned := *resp
	if m.responseBodies[respIdx] != nil {
		cloned.Body = io.NopCloser(bytes.NewReader(m.responseBodies[respIdx]))
	}
	return &cloned, respErr
}

// DoWithTLS uses the same serial response sequence and request capture as Do.
// The TLS profile is ignored because this fixture does not establish a
// transport connection.
func (m *mockSmartRetryUpstream) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return m.Do(req, proxyURL, providerID, providerConcurrency)
}

// newAntigravityRetryFixture supplies production URL redaction, UTF-8
// truncation, model-scope mapping and paid-tier endpoint resolution. It bounds
// reads at 512 KiB and suppresses health logging; callers inject stores,
// policy and request transport as needed.
func newAntigravityRetryFixture() *AntigravityRetry {
	// Suppress each health logging callback through the same no-op function;
	// this fixture observes persistence and retry decisions without retaining
	// or checking log output.
	noop := func(string, ...any) {}
	return &AntigravityRetry{
		SafeURL: logredact.SafeUpstreamURL, TruncateString: logredact.TruncateUTF8, Health: &acct.AntigravityHealth{ModelKeys: AntigravityModelLimitKeys, Logf: noop, Info: noop, Warn: noop, Error: noop},
		BaseURL: func(v *acct.Record) string {
			return antigravity.ResolveAntigravityForwardBaseURL("", AntigravityPaidTier(v))
		}, BodyLimit: func() int64 { return 512 << 10 },
	}
}

// SetModelRateLimit records provider identity, scope key and reset time in
// call order.
func (s *antigravityRetryStoreFixture) SetModelRateLimit(_ context.Context, id int64, key string, at time.Time, _ ...string) error {
	s.modelRateLimitCalls = append(s.modelRateLimitCalls, struct {
		providerID int64     // Provider receiving the model window.
		modelKey   string    // Requested model or family key.
		resetAt    time.Time // Reset time supplied by the writer.
	}{id, key, at})
	return nil
}

// UpdateExtra appends the supplied map reference without copying or applying
// its entries. Tests use serial writes and must account for later mutation
// when inspecting the retained map.
func (s *antigravityRetryStoreFixture) UpdateExtra(_ context.Context, _ int64, value map[string]any) error {
	s.extraUpdateCalls = append(s.extraUpdateCalls, value)
	return nil
}
