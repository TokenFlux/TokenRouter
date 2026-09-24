//go:build unit

package googleforward_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TestShouldFailoverGeminiUpstreamError — verifies the failover decision
// for the ErrorPolicyNone path (original logic preserved).
// ---------------------------------------------------------------------------

func TestShouldFailoverGeminiUpstreamError(t *testing.T) {
	svc := newGeminiFixture(geminiDependencies{})

	tests := []struct {
		name       string
		statusCode int
		expected   bool
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

// ---------------------------------------------------------------------------
// TestCheckErrorPolicy_GeminiAccounts — verifies CheckErrorPolicy works
// correctly for Gemini platform accounts (API Key type).
// ---------------------------------------------------------------------------

func TestCheckErrorPolicy_GeminiAccounts(t *testing.T) {
	tests := []struct {
		name       string
		account    *gatewayprovider.ExecutionAccount
		statusCode int
		body       []byte
		expected   accountcore.ErrorPolicyResult
	}{

		{

			name: "gemini_apikey_custom_codes_hit",

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           100,

				Type: capability.AccountTypeAPIKey,

				Platform: capability.PlatformGemini,

				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(429), float64(500)},
				},
			},
			},

			statusCode: 429,

			body: []byte(`{"error":"rate limited"}`),

			expected: accountcore.ErrorPolicyCustomMatched,
		},

		{

			name: "gemini_apikey_custom_codes_miss",

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           101,

				Type: capability.AccountTypeAPIKey,

				Platform: capability.PlatformGemini,

				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(429)},
				},
			},
			},

			statusCode: 500,

			body: []byte(`{"error":"internal"}`),

			expected: accountcore.ErrorPolicyCustomSkipped,
		},

		{

			name: "gemini_apikey_no_custom_codes_returns_none",

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           102,

				Type: capability.AccountTypeAPIKey,

				Platform: capability.PlatformGemini,
			},
			},

			statusCode: 500,

			body: []byte(`{"error":"internal"}`),

			expected: accountcore.ErrorPolicyNone,
		},

		{

			name: "gemini_apikey_temp_unschedulable_hit",

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           103,

				Type: capability.AccountTypeAPIKey,

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

			expected: accountcore.ErrorPolicyTempUnscheduled,
		},

		{

			name: "gemini_apikey_temp_unschedulable_401_second_hit_returns_none",

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           105,

				Type: capability.AccountTypeAPIKey,

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

			expected: accountcore.ErrorPolicyNone,
		},

		{

			name: "gemini_custom_codes_override_temp_unschedulable",

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           104,

				Type: capability.AccountTypeAPIKey,

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

			expected: accountcore.ErrorPolicyCustomMatched, // custom codes take precedence

		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &gatewaytestkit.ErrorPolicyStore{}
			svc := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, accountcore.HealthOptions{}, nil)

			result := svc.CheckErrorPolicy(context.Background(), gatewayprovider.ExecutionRecord(tt.account), gatewayprovider.HealthObservationFromContext(context.Background(), tt.statusCode, nil, tt.body, nil))
			require.Equal(t, tt.expected, result)
		})
	}
}

// ---------------------------------------------------------------------------
// TestGeminiErrorPolicyIntegration — verifies the Gemini error handling
// paths produce the correct behavior for each ErrorPolicyResult.
//
// These tests simulate the inline error policy switch in handleClaudeCompat
// and forwardNativeGemini by calling the same methods in the same order.
// ---------------------------------------------------------------------------

func TestGeminiErrorPolicyIntegration(t *testing.T) {

	tests := []struct {
		name                 string
		account              *gatewayprovider.ExecutionAccount
		statusCode           int
		respBody             []byte
		expectFailover       bool // expect UpstreamFailoverError
		expectHandleError    bool // expect handleGeminiUpstreamError to be called
		expectShouldFailover bool // for None path, whether shouldFailover triggers
		expectModelScope     string
	}{

		{

			name: "custom_codes_matched_429_failover",

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           200,

				Type: capability.AccountTypeAPIKey,

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

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           201,

				Type: capability.AccountTypeAPIKey,

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

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           202,

				Type: capability.AccountTypeAPIKey,

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

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           203,

				Type: capability.AccountTypeAPIKey,

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

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           204,

				Type: capability.AccountTypeAPIKey,

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
			rlSvc := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, accountcore.HealthOptions{}, nil)

			svc := newGeminiFixture(geminiDependencies{
				accountRepo:    repo,
				healthObserver: rlSvc,
			})

			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

			// Simulate the Claude compat error handling path (same logic as native).
			// This mirrors the inline switch in handleClaudeCompat.
			var handleErrorCalled bool
			var gotFailover bool

			ctx := context.Background()
			statusCode := tt.statusCode
			respBody := tt.respBody
			account := tt.account
			headers := http.Header{}

			if svc.Health != nil {
				policy := svc.Health.CheckErrorPolicy(ctx, gatewayprovider.ExecutionRecord(account), gatewayprovider.HealthObservationFromContext(ctx, statusCode, nil, respBody, []string{"gemini-2.5-pro"}))
				switch policy {
				case accountcore.ErrorPolicyCustomSkipped:
					// Skipped → return error directly (no handleGeminiUpstreamError, no failover)
					gotFailover = false
					handleErrorCalled = false
					goto verify
				case accountcore.ErrorPolicyCustomMatched:
					svc.Errors.Observe(ctx, gatewayprovider.ExecutionRecord(account), statusCode, headers, respBody, gatewayprovider.HealthObservationFromContext(ctx, statusCode, headers, respBody, nil))
					handleErrorCalled = true
					gotFailover = true
					goto verify
				case accountcore.ErrorPolicyTempUnscheduled:
					handleErrorCalled = false
					gotFailover = true
					goto verify
				}
			}

			// ErrorPolicyNone → original logic
			svc.Errors.Observe(ctx, gatewayprovider.ExecutionRecord(account), statusCode, headers, respBody, gatewayprovider.HealthObservationFromContext(ctx, statusCode, headers, respBody, nil))
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
				require.Zero(t, repo.setRateLimitedCalls, "model temp rule must not be widened into an account rate limit")
			}

			if tt.expectShouldFailover {
				require.True(t, googleforward.GeminiFailoverForTest(svc, statusCode),
					"shouldFailoverGeminiUpstreamError should return true for status %d", statusCode)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestGeminiErrorPolicy_NilRateLimitService — verifies nil safety
// ---------------------------------------------------------------------------

func TestGeminiErrorPolicy_NilRateLimitService(t *testing.T) {
	svc := newGeminiFixture(geminiDependencies{
		healthObserver: nil,
	})

	// When healthObserver is nil, error policy is skipped → falls through to
	// shouldFailoverGeminiUpstreamError (original logic).
	// Verify this doesn't panic and follows expected behavior.

	ctx := context.Background()
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           300,

		Type: capability.AccountTypeAPIKey,

		Platform: capability.PlatformGemini,

		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(429)},
		},
	},
	}

	// The nil check should prevent CheckErrorPolicy from being called
	if svc.Health != nil {
		t.Fatal("healthObserver should be nil for this test")
	}

	// shouldFailoverGeminiUpstreamError still works
	require.True(t, googleforward.GeminiFailoverForTest(svc, 429))
	require.False(t, googleforward.GeminiFailoverForTest(svc, 400))

	// handleGeminiUpstreamError should not panic with nil healthObserver
	require.NotPanics(t, func() {
		svc.Errors.Observe(ctx, gatewayprovider.ExecutionRecord(account), 500, http.Header{}, []byte(`error`), gatewayprovider.HealthObservationFromContext(ctx, 500, http.Header{}, []byte(`error`), nil))
	})
}

// ---------------------------------------------------------------------------
// geminiErrorPolicyRepo — minimal AccountRepository stub for Gemini error
// policy tests. Embeds gatewaytestkit.ErrorPolicyStore and adds tracking.
// ---------------------------------------------------------------------------

func TestHandleGeminiUpstreamError_GoogleOneCapacityExhaustedUsesTierCooldown(t *testing.T) {
	repo := &rateLimit429AccountRepoStub{}
	quotaSvc := accountcore.NewGeminiQuotaService(accountcore.GeminiQuotaOptions{})
	rlSvc := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, accountcore.HealthOptions{}, nil)

	svc := newGeminiFixture(geminiDependencies{

		quotaPrecheck: accountcore.NewGeminiPrecheck(quotaSvc, nil, accountcore.GeminiPrecheckOptions{Now: time.Now, Location: geminiQuotaLocation()}),

		accountRepo: repo,

		healthObserver: rlSvc,
	})

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           511,

		Platform: capability.PlatformGemini,

		Type: capability.AccountTypeOAuth,

		Credentials: map[string]any{
			"oauth_type": "google_one",
			"tier_id":    "google_ai_pro",
		},
	},
	}
	body := []byte(`{"error":{"code":429,"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","domain":"cloudcode-pa.googleapis.com","metadata":{"model":"gemini-3.1-pro-preview"},"reason":"MODEL_CAPACITY_EXHAUSTED"}],"message":"No capacity available for model gemini-3.1-pro-preview on the server","status":"RESOURCE_EXHAUSTED"}}`)

	before := time.Now()
	svc.Errors.Observe(context.Background(), gatewayprovider.ExecutionRecord(account), http.StatusTooManyRequests, http.Header{}, body, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusTooManyRequests, http.Header{}, body, nil))
	after := time.Now()

	require.Equal(t, 1, repo.rateLimitCalls)
	require.Equal(t, int64(511), repo.lastRateLimitID)
	require.WithinDuration(t, before.Add(5*time.Minute), repo.lastRateLimitReset, 2*time.Second)
	require.True(t, repo.lastRateLimitReset.After(before))
	require.True(t, repo.lastRateLimitReset.Before(after.Add(5*time.Minute).Add(2*time.Second)))
}

func TestHandleGeminiUpstreamError_ThirdPartyAPIKeyIgnoresOfficialQuotaMessage(t *testing.T) {
	repo := &rateLimit429AccountRepoStub{}
	quotaSvc := accountcore.NewGeminiQuotaService(accountcore.GeminiQuotaOptions{})
	rlSvc := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, accountcore.HealthOptions{}, nil)

	svc := newGeminiFixture(geminiDependencies{

		quotaPrecheck: accountcore.NewGeminiPrecheck(quotaSvc, nil, accountcore.GeminiPrecheckOptions{Now: time.Now, Location: geminiQuotaLocation()}),

		accountRepo: repo,

		healthObserver: rlSvc,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           512,

		Platform: capability.PlatformGemini,

		Type: capability.AccountTypeAPIKey,

		Credentials: map[string]any{
			accountcore.GeminiProviderTypeCredentialKey: accountcore.GeminiProviderTypeThirdParty,
		},
	},
	}

	before := time.Now()
	svc.Errors.Observe(context.Background(), gatewayprovider.ExecutionRecord(account), http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"code":429,"message":"Quota exceeded: 20 requests per day"}}`), gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"code":429,"message":"Quota exceeded: 20 requests per day"}}`), nil))
	after := time.Now()

	require.Equal(t, 1, repo.rateLimitCalls)
	require.Equal(t, int64(512), repo.lastRateLimitID)
	require.WithinDuration(t, before.Add(5*time.Minute), repo.lastRateLimitReset, 2*time.Second)
	require.True(t, repo.lastRateLimitReset.After(before))
	require.True(t, repo.lastRateLimitReset.Before(after.Add(5*time.Minute).Add(2*time.Second)))
}

// TestGeminiPoolMode429BypassesLocalRateLimit 验证池模式不再被当作自定义未命中，
// 也不会继续执行 Gemini 默认 429 限流写入。
func TestGeminiPoolMode429BypassesLocalRateLimit(t *testing.T) {
	repo := &geminiErrorPolicyRepo{}
	healthObserver := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, accountcore.HealthOptions{}, nil)

	svc := newGeminiFixture(geminiDependencies{accountRepo: repo, healthObserver: healthObserver})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           520,

		Type: capability.AccountTypeAPIKey,

		Platform: capability.PlatformGemini,

		Credentials: map[string]any{
			"pool_mode": true,
		},
	},
	}

	decision := googleforward.GeminiPolicyForTest(svc, context.Background(), account, http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"rate limited"}}`), "gemini-2.5-pro")

	require.Equal(t, accountcore.ErrorPolicyPoolBypassed, decision.Policy)
	require.True(t, decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), http.StatusTooManyRequests))
	require.Zero(t, repo.setRateLimitedCalls)
	require.Zero(t, repo.setTempCalls)
	require.Zero(t, repo.setErrorCalls)
}

func TestHandleGeminiUpstreamError_PoolMode429SkipsAccountLimit(t *testing.T) {
	body := []byte(`{"error":{"code":429,"message":"capacity exhausted"}}`)
	tests := []struct {
		name      string
		account   *gatewayprovider.ExecutionAccount
		wantCalls int
	}{

		{

			name: "池模式跳过默认账号限流",

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           530,
				Type:         capability.AccountTypeAPIKey,
				Platform:     capability.PlatformGemini,

				Credentials: map[string]any{"pool_mode": true},
			},
			},
		},

		{

			name: "自定义错误码命中优先于池模式",

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           531,
				Type:         capability.AccountTypeAPIKey,
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

			name: "自定义错误码未命中跳过账号限流",

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
				LoadLocation: time.LoadLocation,
				ID:           532,
				Type:         capability.AccountTypeAPIKey,
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

			name: "普通账号保留默认限流",

			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 533, Type: capability.AccountTypeAPIKey, Platform: capability.PlatformGemini}},

			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &rateLimit429AccountRepoStub{}
			svc := newGeminiFixture(geminiDependencies{accountRepo: repo})

			svc.Errors.Observe(context.Background(), gatewayprovider.ExecutionRecord(tt.account), http.StatusTooManyRequests, http.Header{}, body, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusTooManyRequests, http.Header{}, body, nil))

			require.Equal(t, tt.wantCalls, repo.rateLimitCalls)
		})
	}
}

// TestGeminiCustomNonFailoverStatusStopsScheduling 验证非默认故障转移状态也会执行
// 管理员显式策略并写入账号错误。
func TestGeminiCustomNonFailoverStatusStopsScheduling(t *testing.T) {
	repo := &geminiErrorPolicyRepo{}
	healthObserver := newUpstreamHealthForTest(repo, &googleforward.Options{}, nil, accountcore.HealthOptions{}, nil)

	svc := newGeminiFixture(geminiDependencies{accountRepo: repo, healthObserver: healthObserver})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           521,

		Type: capability.AccountTypeAPIKey,

		Platform: capability.PlatformGemini,

		Credentials: map[string]any{

			"pool_mode": true,

			"custom_error_codes_enabled": true,

			"custom_error_codes": []any{float64(http.StatusUnprocessableEntity)},
		},
	},
	}

	decision := googleforward.GeminiPolicyForTest(svc, context.Background(), account, http.StatusUnprocessableEntity, http.Header{}, []byte(`{"error":{"message":"configured"}}`), "gemini-2.5-pro")

	require.Equal(t, accountcore.ErrorPolicyCustomMatched, decision.Policy)
	require.True(t, decision.StopScheduling)
	require.False(t, decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), http.StatusUnprocessableEntity))
	require.Equal(t, 1, repo.setErrorCalls)
	require.Zero(t, repo.setRateLimitedCalls)
}

type geminiErrorPolicyRepo struct {
	gatewaytestkit.ErrorPolicyStore
	setErrorCalls            int
	setRateLimitedCalls      int
	setTempCalls             int
	setModelRateLimitedCalls int
	lastModelScope           string
}

func (r *geminiErrorPolicyRepo) SetError(_ context.Context, _ int64, _ string) error {
	r.setErrorCalls++
	return nil
}

func (r *geminiErrorPolicyRepo) SetRateLimited(_ context.Context, _ int64, _ time.Time) error {
	r.setRateLimitedCalls++
	return nil
}

func (r *geminiErrorPolicyRepo) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, _ string) error {
	r.setTempCalls++
	return nil
}

func (r *geminiErrorPolicyRepo) SetModelRateLimit(_ context.Context, _ int64, scope string, _ time.Time, _ ...string) error {
	r.setModelRateLimitedCalls++
	r.lastModelScope = scope
	return nil
}
