package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamkimi "github.com/TokenFlux/TokenRouter/internal/upstream/kimi"
)

// 替身记录健康字段写入。
type errorPolicyRepoStub struct {
	providercore.HealthStore
	tempCalls, setErrCalls int
	modelRateLimitCalls    []int64
}

// jevRateLimitRepoStub 记录决策上游的冷却时间。
type jevRateLimitRepoStub struct {
	providercore.HealthStore
	calls int
	reset time.Time
}

type modelNotFoundRateLimitCall struct {
	providerID int64
	scope      string
	resetAt    time.Time
	reason     string
}

type modelNotFoundProviderRepoStub struct {
	providercore.HealthStore
	tempCalls           int
	modelRateLimitCalls []modelNotFoundRateLimitCall
	modelRateLimitErr   error
}

type overloadProviderRepoStub struct {
	providercore.HealthStore
	overloadCalls   int
	errorCalls      int
	lastOverloadID  int64
	lastOverloadEnd time.Time
}

type errSettingRepo struct {
	cooldownSettingsStore
	readErr error
}

type unauthorizedHealthStore struct {
	providercore.HealthStore
	providersByID          map[int64]*providercore.Record
	setErrorCalls          int
	tempCalls              int
	updateCredentialsCalls int
	updateExtraCalls       int
	lastCredentials        map[string]any
	lastExtraUpdates       map[string]any
	lastErrorMsg           string
	lastTempUntil          time.Time
	lastTempReason         string
	lastErrorID            int64
	lastTempID             int64
}

type unauthorizedTokenRecorder struct {
	providers []*providercore.Record
	err       error
}

func (r *jevRateLimitRepoStub) SetRateLimited(_ context.Context, _ int64, reset time.Time) error {
	r.calls++
	r.reset = reset
	return nil
}

// TestJevHealthUsesRetryAfter 检查通用健康入口处理决策限流，池模式仍由上游管理冷却。
func TestJevHealthUsesRetryAfter(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, 529} {
		for _, pool := range []bool{false, true} {
			repo := &jevRateLimitRepoStub{}
			observer := &UpstreamHealth{Core: providercore.NewHealthService(repo, nil, providercore.HealthOptions{})}
			value := &providercore.Record{ID: 9, Platform: providercore.PlatformJev, Type: providercore.ProviderTypeAPIKey, Credentials: map[string]any{"pool_mode": pool}}
			before := time.Now()
			decision := observer.ApplyUpstreamError(context.Background(), value, HealthObservation{Status: status, Headers: http.Header{"Retry-After": []string{"17"}}})
			require.False(t, decision.StopScheduling)
			if pool {
				require.Zero(t, repo.calls)
			} else {
				require.Equal(t, 1, repo.calls)
				require.WithinDuration(t, before.Add(17*time.Second), repo.reset, time.Second)
			}
		}
	}
}

// TestJevOverloadHonorsTemporaryRule 检查缺少 Retry-After 时的管理员过载暂停规则。
func TestJevOverloadHonorsTemporaryRule(t *testing.T) {
	repo := &errorPolicyRepoStub{}
	observer := newErrorPolicyObserver(repo)
	value := &providercore.Record{
		ID: 9, Platform: providercore.PlatformJev, Type: providercore.ProviderTypeAPIKey,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{map[string]any{
				"error_code": float64(529), "keywords": []any{"maintenance"}, "duration_minutes": float64(30),
			}},
		},
	}
	decision := observer.ApplyUpstreamError(context.Background(), value, HealthObservation{Status: 529, Body: []byte("Service maintenance")})
	require.True(t, decision.StopScheduling)
	require.Equal(t, 1, repo.tempCalls)
}

func TestIsCNProviderConcurrencyLimit403_ExactClassification(t *testing.T) {
	kimi := &providercore.Record{Platform: capability.PlatformKimi}

	require.True(t, CNConcurrencyLimit403(kimi, upstreamkimi.ConcurrentRequestLimitMessage))
	require.True(t, CNConcurrencyLimit403(kimi, "  "+upstreamkimi.ConcurrentRequestLimitMessage+"\n"))

	for name, tc := range map[string]struct {
		provider *providercore.Record
		message  string
	}{
		"permission denied":              {kimi, "You do not have permission to access this resource."},
		"generic concurrency wording":    {kimi, "concurrent request limit reached"},
		"near match missing punctuation": {kimi, "You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again"},
		"other CN provider":              {&providercore.Record{Platform: capability.PlatformZhipu}, upstreamkimi.ConcurrentRequestLimitMessage},
		"non CN provider":                {&providercore.Record{Platform: capability.PlatformOpenAI}, upstreamkimi.ConcurrentRequestLimitMessage},
		"nil provider":                   {nil, upstreamkimi.ConcurrentRequestLimitMessage},
	} {
		t.Run(name, func(t *testing.T) {
			require.False(t, CNConcurrencyLimit403(tc.provider, tc.message))
		})
	}
}

func TestCheckErrorPolicy(t *testing.T) {
	tests := []struct {
		name       string
		provider   *providercore.Record
		statusCode int
		body       []byte
		expected   providercore.ErrorPolicyResult
	}{
		{
			name: "no_policy_oauth_returns_none",
			provider: &providercore.Record{
				ID:       1,
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformAntigravity,
				// no custom error codes, no temp rules
			},
			statusCode: 500,
			body:       []byte(`"error"`),
			expected:   providercore.ErrorPolicyNone,
		},
		{
			name: "custom_error_codes_hit_returns_matched",
			provider: &providercore.Record{
				ID:       2,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformAntigravity,
				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(429), float64(500)},
				},
			},
			statusCode: 500,
			body:       []byte(`"error"`),
			expected:   providercore.ErrorPolicyCustomMatched,
		},
		{
			name: "custom_error_codes_miss_returns_skipped",
			provider: &providercore.Record{
				ID:       3,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformAntigravity,
				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(429), float64(500)},
				},
			},
			statusCode: 503,
			body:       []byte(`"error"`),
			expected:   providercore.ErrorPolicyCustomSkipped,
		},
		{
			name: "custom_error_codes_excluding_529_skip_global_cooldown",
			provider: &providercore.Record{
				ID:       33,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(429)},
				},
			},
			statusCode: 529,
			body:       []byte(`{"error":{"message":"overloaded"}}`),
			expected:   providercore.ErrorPolicyCustomSkipped,
		},
		{
			name: "pool_mode_skips_global_529_cooldown",
			provider: &providercore.Record{
				ID:       34,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode": true,
				},
			},
			statusCode: 529,
			body:       []byte(`{"error":{"message":"overloaded"}}`),
			expected:   providercore.ErrorPolicyPoolBypassed,
		},
		{
			name: "ordinary_provider_uses_global_529_cooldown",
			provider: &providercore.Record{
				ID:       35,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
			},
			statusCode: 529,
			body:       []byte(`{"error":{"message":"overloaded"}}`),
			expected:   providercore.ErrorPolicyCustomMatched,
		},
		{
			name: "custom_error_codes_including_529_take_precedence",
			provider: &providercore.Record{
				ID:       36,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(529)},
				},
			},
			statusCode: 529,
			body:       []byte(`{"error":{"message":"overloaded"}}`),
			expected:   providercore.ErrorPolicyCustomMatched,
		},
		{
			name: "temp_unschedulable_hit_returns_temp_unscheduled",
			provider: &providercore.Record{
				ID:       4,
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformAntigravity,
				Credentials: map[string]any{
					"temp_unschedulable_enabled": true,
					"temp_unschedulable_rules": []any{
						map[string]any{
							"error_code":       float64(503),
							"keywords":         []any{"overloaded"},
							"duration_minutes": float64(10),
							"description":      "overloaded rule",
						},
					},
				},
			},
			statusCode: 503,
			body:       []byte(`overloaded service`),
			expected:   providercore.ErrorPolicyTempUnscheduled,
		},
		{
			name: "temp_unschedulable_401_first_hit_returns_temp_unscheduled",
			provider: &providercore.Record{
				ID:       14,
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformAntigravity,
				Credentials: map[string]any{
					"temp_unschedulable_enabled": true,
					"temp_unschedulable_rules": []any{
						map[string]any{
							"error_code":       float64(401),
							"keywords":         []any{"unauthorized"},
							"duration_minutes": float64(10),
						},
					},
				},
			},
			statusCode: 401,
			body:       []byte(`unauthorized`),
			expected:   providercore.ErrorPolicyTempUnscheduled,
		},
		{
			// Antigravity 401 不走升级逻辑（由 applyErrorPolicy 的 temp_unschedulable_rules 自行控制），
			// second hit 仍然返回 TempUnscheduled。
			name: "temp_unschedulable_401_second_hit_antigravity_stays_temp",
			provider: &providercore.Record{
				ID:                      15,
				Type:                    capability.ProviderTypeOAuth,
				Platform:                capability.PlatformAntigravity,
				TempUnschedulableReason: `{"status_code":401,"until_unix":1735689600}`,
				Credentials: map[string]any{
					"temp_unschedulable_enabled": true,
					"temp_unschedulable_rules": []any{
						map[string]any{
							"error_code":       float64(401),
							"keywords":         []any{"unauthorized"},
							"duration_minutes": float64(10),
						},
					},
				},
			},
			statusCode: 401,
			body:       []byte(`unauthorized`),
			expected:   providercore.ErrorPolicyTempUnscheduled,
		},
		{
			name: "temp_unschedulable_body_miss_returns_none",
			provider: &providercore.Record{
				ID:       5,
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformAntigravity,
				Credentials: map[string]any{
					"temp_unschedulable_enabled": true,
					"temp_unschedulable_rules": []any{
						map[string]any{
							"error_code":       float64(503),
							"keywords":         []any{"overloaded"},
							"duration_minutes": float64(10),
							"description":      "overloaded rule",
						},
					},
				},
			},
			statusCode: 503,
			body:       []byte(`random msg`),
			expected:   providercore.ErrorPolicyNone,
		},
		{
			name: "custom_error_codes_override_temp_unschedulable",
			provider: &providercore.Record{
				ID:       6,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformAntigravity,
				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(503)},
					"temp_unschedulable_enabled": true,
					"temp_unschedulable_rules": []any{
						map[string]any{
							"error_code":       float64(503),
							"keywords":         []any{"overloaded"},
							"duration_minutes": float64(10),
							"description":      "overloaded rule",
						},
					},
				},
			},
			statusCode: 503,
			body:       []byte(`overloaded`),
			expected:   providercore.ErrorPolicyCustomMatched, // custom codes take precedence
		},
		{
			name: "pool_mode_custom_error_codes_hit_returns_matched",
			provider: &providercore.Record{
				ID:       7,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":                  true,
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(401), float64(403)},
				},
			},
			statusCode: 401,
			body:       []byte(`unauthorized`),
			expected:   providercore.ErrorPolicyCustomMatched,
		},
		{
			name: "pool_mode_without_custom_error_codes_returns_bypassed",
			provider: &providercore.Record{
				ID:       8,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode": true,
				},
			},
			statusCode: 401,
			body:       []byte(`unauthorized`),
			expected:   providercore.ErrorPolicyPoolBypassed,
		},
		{
			name: "pool_mode_temp_unschedulable_hit_returns_temp_unscheduled",
			provider: &providercore.Record{
				ID:       9,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":                  true,
					"temp_unschedulable_enabled": true,
					"temp_unschedulable_rules": []any{
						map[string]any{
							"error_code":       float64(http.StatusServiceUnavailable),
							"keywords":         []any{"unavailable"},
							"duration_minutes": float64(30),
						},
					},
				},
			},
			statusCode: http.StatusServiceUnavailable,
			body:       []byte(`Service temporarily unavailable`),
			expected:   providercore.ErrorPolicyTempUnscheduled,
		},
		{
			name: "pool_mode_repeated_401_explicit_rule_stays_temp_unscheduled",
			provider: &providercore.Record{
				ID:                      11,
				Type:                    capability.ProviderTypeAPIKey,
				Platform:                capability.PlatformOpenAI,
				TempUnschedulableReason: `{"status_code":401,"until_unix":1735689600}`,
				Credentials: map[string]any{
					"pool_mode":                  true,
					"temp_unschedulable_enabled": true,
					"temp_unschedulable_rules": []any{
						map[string]any{
							"error_code":       float64(http.StatusUnauthorized),
							"keywords":         []any{"unauthorized"},
							"duration_minutes": float64(30),
						},
					},
				},
			},
			statusCode: http.StatusUnauthorized,
			body:       []byte(`unauthorized`),
			expected:   providercore.ErrorPolicyTempUnscheduled,
		},
		{
			name: "pool_mode_temp_unschedulable_miss_returns_bypassed",
			provider: &providercore.Record{
				ID:       10,
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":                  true,
					"temp_unschedulable_enabled": true,
					"temp_unschedulable_rules": []any{
						map[string]any{
							"error_code":       float64(http.StatusServiceUnavailable),
							"keywords":         []any{"maintenance"},
							"duration_minutes": float64(30),
						},
					},
				},
			},
			statusCode: http.StatusServiceUnavailable,
			body:       []byte(`Service temporarily unavailable`),
			expected:   providercore.ErrorPolicyPoolBypassed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &errorPolicyRepoStub{}
			svc := newErrorPolicyObserver(repo)

			result := svc.CheckErrorPolicy(context.Background(), tt.provider, HealthObservation{Status: tt.statusCode, Body: tt.body})
			require.Equal(t, tt.expected, result, "unexpected ErrorPolicyResult")
		})
	}
}

func TestHandleUpstreamError_PoolModePolicies(t *testing.T) {
	t.Run("pool_mode_without_custom_error_codes_still_skips", func(t *testing.T) {
		repo := &errorPolicyRepoStub{}
		svc := newErrorPolicyObserver(repo)
		provider := &providercore.Record{
			ID:       30,
			Type:     capability.ProviderTypeAPIKey,
			Platform: capability.PlatformOpenAI,
			Credentials: map[string]any{
				"pool_mode": true,
			},
		}

		shouldDisable := svc.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: 401, Headers: http.Header{}, Body: []byte("unauthorized")}).StopScheduling

		require.False(t, shouldDisable)
		require.Equal(t, 0, repo.setErrCalls)
		require.Equal(t, 0, repo.tempCalls)
	})

	t.Run("pool_mode_with_custom_error_codes_uses_local_error_policy", func(t *testing.T) {
		repo := &errorPolicyRepoStub{}
		svc := newErrorPolicyObserver(repo)
		provider := &providercore.Record{
			ID:       31,
			Type:     capability.ProviderTypeAPIKey,
			Platform: capability.PlatformOpenAI,
			Credentials: map[string]any{
				"pool_mode":                  true,
				"custom_error_codes_enabled": true,
				"custom_error_codes":         []any{float64(401)},
			},
		}

		shouldDisable := svc.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: 401, Headers: http.Header{}, Body: []byte("unauthorized")}).StopScheduling

		require.True(t, shouldDisable)
		require.Equal(t, 1, repo.setErrCalls)
		require.Equal(t, 0, repo.tempCalls)
	})

	t.Run("pool_mode_explicit_temp_rule_stops_scheduling", func(t *testing.T) {
		repo := &errorPolicyRepoStub{}
		svc := newErrorPolicyObserver(repo)
		provider := &providercore.Record{
			ID:       32,
			Type:     capability.ProviderTypeAPIKey,
			Platform: capability.PlatformOpenAI,
			Credentials: map[string]any{
				"pool_mode":                  true,
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{
					map[string]any{
						"error_code":       float64(http.StatusServiceUnavailable),
						"keywords":         []any{"unavailable"},
						"duration_minutes": float64(30),
					},
				},
			},
		}

		shouldDisable := svc.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: http.StatusServiceUnavailable, Headers: http.Header{}, Body: []byte("Service temporarily unavailable")}).StopScheduling

		require.True(t, shouldDisable)
		require.Equal(t, 0, repo.setErrCalls)
		require.Equal(t, 1, repo.tempCalls)
	})

	t.Run("pool_mode_temp_rule_miss_still_skips", func(t *testing.T) {
		repo := &errorPolicyRepoStub{}
		svc := newErrorPolicyObserver(repo)
		provider := &providercore.Record{
			ID:       33,
			Type:     capability.ProviderTypeAPIKey,
			Platform: capability.PlatformOpenAI,
			Credentials: map[string]any{
				"pool_mode":                  true,
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{
					map[string]any{
						"error_code":       float64(http.StatusServiceUnavailable),
						"keywords":         []any{"maintenance"},
						"duration_minutes": float64(30),
					},
				},
			},
		}

		shouldDisable := svc.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: http.StatusServiceUnavailable, Headers: http.Header{}, Body: []byte("Service temporarily unavailable")}).StopScheduling

		require.False(t, shouldDisable)
		require.Equal(t, 0, repo.setErrCalls)
		require.Equal(t, 0, repo.tempCalls)
	})
}

// TestHandleUpstreamError_CustomCodesAlwaysStopScheduling 验证自定义错误码不会再被
// 400、429、529 的内置分支覆盖。
func TestHandleUpstreamError_CustomCodesAlwaysStopScheduling(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{name: "bad_request", statusCode: http.StatusBadRequest},
		{name: "rate_limit", statusCode: http.StatusTooManyRequests},
		{name: "overloaded", statusCode: 529},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &errorPolicyRepoStub{}
			svc := newErrorPolicyObserver(repo)
			provider := &providercore.Record{
				ID:       int64(1000 + tt.statusCode),
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":                  true,
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(tt.statusCode)},
				},
			}

			decision := svc.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: tt.statusCode, Headers: http.Header{}, Body: []byte(`{"error":{"message":"configured failure"}}`)})

			require.Equal(t, providercore.ErrorPolicyCustomMatched, decision.Policy)
			require.True(t, decision.StopScheduling)
			require.False(t, decision.RetryableOnSameProvider(provider, tt.statusCode))
			require.Equal(t, 1, repo.setErrCalls)
			require.Equal(t, 0, repo.tempCalls)
		})
	}
}

// TestUpstreamErrorDecision_PoolRetryStatusPromotesFailover 验证管理员配置的池模式
// 重试状态码可以提升非默认故障转移错误，同时不会写入本地状态。
func TestUpstreamErrorDecision_PoolRetryStatusPromotesFailover(t *testing.T) {
	repo := &errorPolicyRepoStub{}
	svc := newErrorPolicyObserver(repo)
	provider := &providercore.Record{
		ID:       20422,
		Type:     capability.ProviderTypeAPIKey,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(http.StatusUnprocessableEntity)},
		},
	}

	decision := svc.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: http.StatusUnprocessableEntity, Headers: http.Header{}, Body: []byte(`{"error":{"message":"unprocessable"}}`)})

	require.Equal(t, providercore.ErrorPolicyPoolBypassed, decision.Policy)
	require.True(t, decision.ShouldFailover(provider, http.StatusUnprocessableEntity, false))
	require.True(t, decision.RetryableOnSameProvider(provider, http.StatusUnprocessableEntity))
	require.Zero(t, repo.setErrCalls)
	require.Zero(t, repo.tempCalls)
	require.Empty(t, repo.modelRateLimitCalls)
}

// TestUpstreamErrorDecision_UsesSeparateEntryDefaults 检查普通提供商使用各入口的默认处理规则，
// 池模式使用平台错误分类，配置的错误策略优先。
func TestUpstreamErrorDecision_UsesSeparateEntryDefaults(t *testing.T) {
	provider := &providercore.Record{
		ID:          20423,
		Type:        capability.ProviderTypeAPIKey,
		Credentials: map[string]any{"pool_mode": true},
	}

	require.False(t, (providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}).ShouldFailoverWithDefaults(provider, http.StatusBadGateway, false, true))
	require.True(t, (providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyPoolBypassed}).ShouldFailoverWithDefaults(provider, http.StatusBadGateway, false, true))
	require.True(t, (providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyCustomMatched}).ShouldFailoverWithDefaults(provider, http.StatusUnprocessableEntity, false, false))
	require.False(t, (providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyCustomSkipped}).ShouldFailoverWithDefaults(provider, http.StatusBadGateway, true, true))
}

func TestRateLimitService_HandleUpstreamError_ModelNotFoundUsesModelRateLimit(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAIModelNotFoundTempProvider()

	handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusNotFound, http.Header{}, []byte(`{"error":{"code":"model_not_found","message":"model not found"}}`), "gpt-5.4")).StopScheduling

	require.True(t, handled)
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
	call := repo.modelRateLimitCalls[0]
	require.Equal(t, provider.ID, call.providerID)
	require.Equal(t, "gpt-5.4", call.scope)
	require.Equal(t, providercore.ModelNotFoundReason, call.reason)
	require.WithinDuration(t, time.Now().Add(providercore.ModelNotFoundCooldown), call.resetAt, 5*time.Second)
}

// TestRateLimitService_PoolModeModelNotFoundSkipsDefaultModelPause 验证池模式不会根据
// 聚合上游的一次 404 暂停本地提供商与模型组合。
func TestRateLimitService_PoolModeModelNotFoundSkipsDefaultModelPause(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := &providercore.Record{
		ID:          102,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"pool_mode": true},
	}

	decision := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusNotFound, http.Header{}, []byte(`{"error":{"code":"model_not_found","message":"model not found"}}`), "gpt-5.4"))

	require.Equal(t, providercore.ErrorPolicyPoolBypassed, decision.Policy)
	require.False(t, decision.StopScheduling)
	require.Zero(t, repo.tempCalls)
	require.Empty(t, repo.modelRateLimitCalls)
}

func TestRateLimitService_HandleUpstreamError_ModelNotFoundWriteFailureDoesNotTempUnschedule(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{modelRateLimitErr: errors.New("write failed")}
	svc := newModelHealthObserver(repo)
	provider := openAIModelNotFoundTempProvider()

	handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusNotFound, http.Header{}, []byte(`{"error":{"code":"model_not_found","message":"model not found"}}`), "gpt-5.4")).StopScheduling

	require.True(t, handled)
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
}

func TestRateLimitService_HandleUpstreamError_Bare404UsesModelScopedTempUnschedulableWhenModelKnown(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAIModelNotFoundTempProvider()

	handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`), "gpt-5.4")).StopScheduling

	require.True(t, handled)
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
	call := repo.modelRateLimitCalls[0]
	require.Equal(t, provider.ID, call.providerID)
	require.Equal(t, "gpt-5.4", call.scope)
	require.WithinDuration(t, time.Now().Add(10*time.Minute), call.resetAt, 5*time.Second)

	var state providercore.TempUnschedState
	require.NoError(t, json.Unmarshal([]byte(call.reason), &state))
	require.Equal(t, http.StatusNotFound, state.StatusCode)
	require.Equal(t, "not found", state.MatchedKeyword)
}

func TestRateLimitService_HandleUpstreamError_Bare404WithoutModelKeepsProviderTempUnschedulable(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAIModelNotFoundTempProvider()

	handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`))).StopScheduling

	require.True(t, handled)
	require.Equal(t, 1, repo.tempCalls)
	require.Empty(t, repo.modelRateLimitCalls)
}

func TestRateLimitService_HandleUpstreamError_ModelTempWriteFailureNeverWidensToProvider(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{modelRateLimitErr: errors.New("write failed")}
	svc := newModelHealthObserver(repo)
	provider := openAIModelNotFoundTempProvider()

	handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`), "gpt-5.4")).StopScheduling

	require.True(t, handled)
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
}

func TestRateLimitService_HandleTempUnschedulable_PoolModeAppliesExplicitModelRule(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAIModelNotFoundTempProvider()
	provider.Credentials["pool_mode"] = true

	handled := svc.Core.HandleTempUnschedulable(context.Background(), provider, http.StatusNotFound, []byte(`{"error":{"message":"endpoint not found"}}`), "gpt-5.4")

	require.True(t, handled)
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.4", repo.modelRateLimitCalls[0].scope)
}

func TestRateLimitService_HandleTempUnschedulable_PoolModeCustomPolicyUsesModelScope(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAIModelNotFoundTempProvider()
	provider.Credentials["pool_mode"] = true
	provider.Credentials["custom_error_codes_enabled"] = true
	provider.Credentials["custom_error_codes"] = []any{float64(http.StatusNotFound)}

	handled := svc.Core.HandleTempUnschedulable(context.Background(), provider, http.StatusNotFound, []byte(`{"error":{"message":"endpoint not found"}}`), "gpt-5.4")

	require.True(t, handled)
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.4", repo.modelRateLimitCalls[0].scope)
}

func TestRateLimitService_HandleUpstreamError_CustomPolicyExclusionSkipsAllState(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAIModelNotFoundTempProvider()
	provider.Credentials["custom_error_codes_enabled"] = true
	provider.Credentials["custom_error_codes"] = []any{float64(http.StatusServiceUnavailable)}

	handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`), "gpt-5.4")).StopScheduling

	require.False(t, handled)
	require.Zero(t, repo.tempCalls)
	require.Empty(t, repo.modelRateLimitCalls)
}

func TestRateLimitService_HandleTempUnschedulable_AuthenticationFailureStaysProviderScoped(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAIModelNotFoundTempProvider()
	provider.TempUnschedulableReason = "legacy non-JSON reason"
	provider.Credentials["temp_unschedulable_rules"] = []any{
		map[string]any{
			"error_code":       float64(http.StatusUnauthorized),
			"keywords":         []any{"unauthorized"},
			"duration_minutes": float64(10),
		},
	}

	handled := svc.Core.HandleTempUnschedulable(context.Background(), provider, http.StatusUnauthorized, []byte(`{"error":{"message":"unauthorized"}}`), "gpt-5.4")

	require.True(t, handled)
	require.Equal(t, 1, repo.tempCalls)
	require.Empty(t, repo.modelRateLimitCalls)
}

func TestRateLimitService_HandleUpstreamError_CodexPlanGatedModelUsesModelRateLimit(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAICodexPlanGatedOAuthProvider()

	handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusBadRequest, http.Header{}, []byte(`{"detail":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT provider."}`), "gpt-5.6-sol")).StopScheduling

	require.True(t, handled)
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
	call := repo.modelRateLimitCalls[0]
	require.Equal(t, provider.ID, call.providerID)
	require.Equal(t, "gpt-5.6-sol", call.scope)
	require.Equal(t, providercore.CodexPlanGatedModelReason, call.reason)
	require.WithinDuration(t, time.Now().Add(providercore.CodexPlanGatedModelCooldown), call.resetAt, 5*time.Second)
}

func TestRateLimitService_HandleUpstreamError_CodexPlanGatedModelUsesFinalUpstreamModel(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAICodexPlanGatedOAuthProvider()
	// 状态入口接收实际发送的最终上游 ID，即使该 ID 也是映射键也不得再次映射。
	provider.Credentials["model_mapping"] = map[string]any{
		"gpt-5.6-sol":       "external-model-v1",
		"external-model-v1": "unexpected-remap",
	}

	handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusBadRequest, http.Header{}, []byte(`{"detail":"The 'external-model-v1' model is not supported when using Codex with a ChatGPT provider."}`), "external-model-v1")).StopScheduling

	require.True(t, handled)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "external-model-v1", repo.modelRateLimitCalls[0].scope)
}

func TestRateLimitService_HandleUpstreamError_CodexPlanGatedModelIgnoresAPIKeyProvider(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAICodexPlanGatedOAuthProvider()
	provider.Type = capability.ProviderTypeAPIKey

	handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusBadRequest, http.Header{}, []byte(`{"detail":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT provider."}`), "gpt-5.6-sol")).StopScheduling

	require.False(t, handled)
	require.Empty(t, repo.modelRateLimitCalls)
}

func TestRateLimitService_HandleUpstreamError_CodexPlanGatedImageModelSkipsCooldown(t *testing.T) {
	for _, model := range []string{"gpt-image-1", "gpt-image-1.5", "gpt-image-2"} {
		t.Run(model, func(t *testing.T) {
			repo := &modelNotFoundProviderRepoStub{}
			svc := newModelHealthObserver(repo)
			provider := openAICodexPlanGatedOAuthProvider()

			handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusBadRequest, http.Header{}, []byte(`{"detail":"The '`+model+`' model is not supported when using Codex with a ChatGPT provider."}`), model)).StopScheduling

			require.True(t, handled, "当前文本端点尝试仍应切换提供商")
			require.Empty(t, repo.modelRateLimitCalls, "文本端点错配不应冷却提供商的图片模型")
			require.Zero(t, repo.tempCalls)
		})
	}
}

func TestRateLimitService_HandleUpstreamError_CodexPlanGatedTextModelStillCoolsDown(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAICodexPlanGatedOAuthProvider()

	handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusBadRequest, http.Header{}, []byte(`{"detail":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT provider."}`), "gpt-5.6-sol")).StopScheduling

	require.True(t, handled)
	require.Len(t, repo.modelRateLimitCalls, 1, "非图片套餐门控模型应保留原有冷却")
	require.Equal(t, providercore.CodexPlanGatedModelReason, repo.modelRateLimitCalls[0].reason)
}

func TestRateLimitService_HandleUpstreamError_CodexPlanGatedImageModelKeepsCooldownOnImagesEndpoint(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAICodexPlanGatedOAuthProvider()

	handled := svc.ApplyUpstreamError(requeststate.WithOpenAIImagesEndpoint(context.Background()), provider, healthTestObservation(requeststate.WithOpenAIImagesEndpoint(context.Background()), http.StatusBadRequest, http.Header{}, []byte(`{"detail":"The 'gpt-image-2' model is not supported when using Codex with a ChatGPT provider."}`), "gpt-image-2")).StopScheduling

	require.True(t, handled)
	require.Len(t, repo.modelRateLimitCalls, 1, "专用 Images 端点上的能力拒绝必须保留冷却")
	require.Equal(t, "gpt-image-2", repo.modelRateLimitCalls[0].scope)
	require.Equal(t, providercore.CodexPlanGatedModelReason, repo.modelRateLimitCalls[0].reason)
}

func TestRateLimitService_HandleUpstreamError_CodexPlanGatedImageModelSkipsCooldownOnIntentOnly(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAICodexPlanGatedOAuthProvider()

	handled := svc.ApplyUpstreamError(requeststate.WithOpenAIImageGenerationIntent(context.Background()), provider, healthTestObservation(requeststate.WithOpenAIImageGenerationIntent(context.Background()), http.StatusBadRequest, http.Header{}, []byte(`{"detail":"The 'gpt-image-2' model is not supported when using Codex with a ChatGPT provider."}`), "gpt-image-2")).StopScheduling

	require.True(t, handled)
	require.Empty(t, repo.modelRateLimitCalls, "生图意图不等于专用 Images 入口")
}

func TestRateLimitService_HandleUpstreamError_ModelNotFoundImageModelStillCoolsDown(t *testing.T) {
	repo := &modelNotFoundProviderRepoStub{}
	svc := newModelHealthObserver(repo)
	provider := openAICodexPlanGatedOAuthProvider()

	handled := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"The model 'gpt-image-2' does not exist","code":"model_not_found"}}`), "gpt-image-2")).StopScheduling

	require.True(t, handled)
	require.Len(t, repo.modelRateLimitCalls, 1, "图片模型的 404 model_not_found 仍应冷却")
	require.Equal(t, providercore.ModelNotFoundReason, repo.modelRateLimitCalls[0].reason)
}

func TestRateLimitService_HandleUpstreamError_403PreservesOriginalUpstreamMessage(t *testing.T) {
	repo := &unauthorizedHealthStore{}
	svc := newUnauthorizedObserver(repo, nil)
	provider := &providercore.Record{
		ID:       201,
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeOAuth,
	}

	shouldDisable := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), 403, http.Header{}, []byte(`{"error":{"message":"workspace forbidden by policy","type":"invalid_request_error"}}`))).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Contains(t, repo.lastErrorMsg, "workspace forbidden by policy")
	require.NotContains(t, repo.lastErrorMsg, "provider may be suspended or lack permissions")
}

func TestRateLimitService_HandleUpstreamError_403FallsBackToRawBody(t *testing.T) {
	repo := &unauthorizedHealthStore{}
	svc := newUnauthorizedObserver(repo, nil)
	provider := &providercore.Record{
		ID:       202,
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeOAuth,
	}

	shouldDisable := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), 403, http.Header{}, []byte(`{"error":{"type":"access_denied","details":{"reason":"ip_blocked"}}}`))).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Contains(t, repo.lastErrorMsg, `"access_denied"`)
	require.Contains(t, repo.lastErrorMsg, `"ip_blocked"`)
	require.NotContains(t, repo.lastErrorMsg, "provider may be suspended or lack permissions")
}

func TestHandle529_EnabledFromDB_PausesProvider(t *testing.T) {
	providerRepo := &overloadProviderRepoStub{}
	settingRepo := newCooldownSettingsStore()
	data, err := json.Marshal(providercore.OverloadCooldownSettings{Enabled: true, CooldownMinutes: 15})
	require.NoError(t, err)
	settingRepo.data[providercore.SettingKeyOverloadCooldownSettings] = string(data)

	settingSvc := providercore.NewRuntimeSettings(settingRepo, errCooldownSettingMissing)
	svc := newOverloadObserver(providerRepo, providercore.HealthOptions{OverloadSettings: settingSvc.GetOverloadCooldownSettings})

	provider := &providercore.Record{ID: 42, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth}
	before := time.Now()
	svc.Core.ApplyOverload(context.Background(), provider)

	require.Equal(t, 1, providerRepo.overloadCalls)
	require.Equal(t, int64(42), providerRepo.lastOverloadID)
	require.WithinDuration(t, before.Add(15*time.Minute), providerRepo.lastOverloadEnd, 2*time.Second)
}

func TestHandle529_DisabledFromDB_SkipsProvider(t *testing.T) {
	providerRepo := &overloadProviderRepoStub{}
	settingRepo := newCooldownSettingsStore()
	data, err := json.Marshal(providercore.OverloadCooldownSettings{Enabled: false, CooldownMinutes: 15})
	require.NoError(t, err)
	settingRepo.data[providercore.SettingKeyOverloadCooldownSettings] = string(data)

	settingSvc := providercore.NewRuntimeSettings(settingRepo, errCooldownSettingMissing)
	svc := newOverloadObserver(providerRepo, providercore.HealthOptions{OverloadSettings: settingSvc.GetOverloadCooldownSettings})

	provider := &providercore.Record{ID: 42, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth}
	svc.Core.ApplyOverload(context.Background(), provider)

	require.Equal(t, 0, providerRepo.overloadCalls, "should NOT pause when disabled")
}

func TestHandle529_NilSettingService_FallsBackToConfig(t *testing.T) {
	providerRepo := &overloadProviderRepoStub{}
	minutes := 20
	svc := newOverloadObserver(providerRepo, providercore.HealthOptions{OverloadMinutes: minutes})
	// 设置读取器留空。

	provider := &providercore.Record{ID: 77, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth}
	before := time.Now()
	svc.Core.ApplyOverload(context.Background(), provider)

	require.Equal(t, 1, providerRepo.overloadCalls)
	require.WithinDuration(t, before.Add(20*time.Minute), providerRepo.lastOverloadEnd, 2*time.Second)
}

func TestHandle529_NilSettingService_ZeroConfig_DefaultsTen(t *testing.T) {
	providerRepo := &overloadProviderRepoStub{}
	svc := newOverloadObserver(providerRepo, providercore.HealthOptions{})

	provider := &providercore.Record{ID: 88, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth}
	before := time.Now()
	svc.Core.ApplyOverload(context.Background(), provider)

	require.Equal(t, 1, providerRepo.overloadCalls)
	require.WithinDuration(t, before.Add(10*time.Minute), providerRepo.lastOverloadEnd, 2*time.Second)
}

func TestHandle529_DBReadError_FallsBackToConfig(t *testing.T) {
	providerRepo := &overloadProviderRepoStub{}
	errRepo := &errSettingRepo{readErr: context.DeadlineExceeded}
	errRepo.data = make(map[string]string)

	minutes := 7
	settingSvc := providercore.NewRuntimeSettings(errRepo, errCooldownSettingMissing)
	svc := newOverloadObserver(providerRepo, providercore.HealthOptions{OverloadMinutes: minutes, OverloadSettings: settingSvc.GetOverloadCooldownSettings})

	provider := &providercore.Record{ID: 99, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth}
	before := time.Now()
	svc.Core.ApplyOverload(context.Background(), provider)

	require.Equal(t, 1, providerRepo.overloadCalls)
	require.WithinDuration(t, before.Add(7*time.Minute), providerRepo.lastOverloadEnd, 2*time.Second)
}

func TestHandleUpstreamError_529RespectsProviderPolicies(t *testing.T) {
	tests := []struct {
		name        string
		credentials map[string]any
	}{
		{
			name:        "pool mode",
			credentials: map[string]any{"pool_mode": true},
		},
		{
			name: "custom code filter excludes 529",
			credentials: map[string]any{
				"custom_error_codes_enabled": true,
				"custom_error_codes":         []any{float64(429)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &overloadProviderRepoStub{}
			svc := newOverloadObserver(repo, providercore.HealthOptions{})
			provider := &providercore.Record{
				ID:          101,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Credentials: tt.credentials,
			}

			shouldDisable := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), 529, nil, []byte(`{"error":{"message":"overloaded"}}`))).StopScheduling

			require.False(t, shouldDisable)
			require.Zero(t, repo.overloadCalls)
			require.Zero(t, repo.errorCalls)
		})
	}
}

func TestHandleUpstreamError_529CustomCodeDisablesInsteadOfOverloadCooldown(t *testing.T) {
	repo := &overloadProviderRepoStub{}
	svc := newOverloadObserver(repo, providercore.HealthOptions{})
	provider := &providercore.Record{
		ID:       102,
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeAPIKey,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(529)},
		},
	}

	shouldDisable := svc.ApplyUpstreamError(context.Background(), provider, healthTestObservation(context.Background(), 529, nil, []byte(`{"error":{"message":"overloaded"}}`))).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.errorCalls)
	require.Zero(t, repo.overloadCalls)
}

func TestUpdateSessionWindow_UsesResetHeader(t *testing.T) {
	// The reset header provides the real window end as a Unix timestamp.
	// UpdateSessionWindow should use it instead of the hour-truncated prediction.
	resetUnix := time.Now().Add(3 * time.Hour).Unix()
	wantEnd := time.Unix(resetUnix, 0)
	wantStart := wantEnd.Add(-5 * time.Hour)

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	provider := &providercore.Record{ID: 42} // no existing window → needInitWindow=true
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", resetUnix))

	svc.UpdateSessionWindow(context.Background(), provider, SessionWindowObservation(headers))

	if len(repo.sessionWindowCalls) != 1 {
		t.Fatalf("expected 1 UpdateSessionWindow call, got %d", len(repo.sessionWindowCalls))
	}

	call := repo.sessionWindowCalls[0]
	if call.ID != 42 {
		t.Errorf("expected provider ID 42, got %d", call.ID)
	}
	if call.End == nil || !call.End.Equal(wantEnd) {
		t.Errorf("expected window end %v, got %v", wantEnd, call.End)
	}
	if call.Start == nil || !call.Start.Equal(wantStart) {
		t.Errorf("expected window start %v, got %v", wantStart, call.Start)
	}
	if call.Status != "allowed" {
		t.Errorf("expected status 'allowed', got %q", call.Status)
	}
}

func TestUpdateSessionWindow_FallbackPredictionWhenNoResetHeader(t *testing.T) {
	// When the reset header is absent, should fall back to hour-truncated prediction.
	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	provider := &providercore.Record{ID: 10} // no existing window
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed_warning")
	// No anthropic-ratelimit-unified-5h-reset header

	// Capture now before the call to avoid hour-boundary races
	now := time.Now()
	expectedStart := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
	expectedEnd := expectedStart.Add(5 * time.Hour)

	svc.UpdateSessionWindow(context.Background(), provider, SessionWindowObservation(headers))

	if len(repo.sessionWindowCalls) != 1 {
		t.Fatalf("expected 1 UpdateSessionWindow call, got %d", len(repo.sessionWindowCalls))
	}

	call := repo.sessionWindowCalls[0]
	if call.End == nil {
		t.Fatal("expected window end to be set (fallback prediction)")
	}
	// Fallback: start = current hour truncated, end = start + 5h

	if !call.End.Equal(expectedEnd) {
		t.Errorf("expected fallback end %v, got %v", expectedEnd, *call.End)
	}
	if call.Start == nil || !call.Start.Equal(expectedStart) {
		t.Errorf("expected fallback start %v, got %v", expectedStart, call.Start)
	}
}

func TestUpdateSessionWindow_CorrectsStalePrediction(t *testing.T) {
	// When the stored SessionWindowEnd is wrong (from a previous prediction),
	// and the reset header provides the real time, it should update the window.
	staleEnd := time.Now().Add(2 * time.Hour)             // existing prediction: 2h from now
	realResetUnix := time.Now().Add(4 * time.Hour).Unix() // real reset: 4h from now
	wantEnd := time.Unix(realResetUnix, 0)

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	provider := &providercore.Record{
		ID:               55,
		SessionWindowEnd: &staleEnd,
	}
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", realResetUnix))

	svc.UpdateSessionWindow(context.Background(), provider, SessionWindowObservation(headers))

	if len(repo.sessionWindowCalls) != 1 {
		t.Fatalf("expected 1 UpdateSessionWindow call, got %d", len(repo.sessionWindowCalls))
	}

	call := repo.sessionWindowCalls[0]
	if call.End == nil || !call.End.Equal(wantEnd) {
		t.Errorf("expected corrected end %v, got %v", wantEnd, call.End)
	}
}

func TestUpdateSessionWindow_NoUpdateWhenHeaderMatchesStored(t *testing.T) {
	// If the reset header matches the stored SessionWindowEnd, no window update needed.
	futureUnix := time.Now().Add(3 * time.Hour).Unix()
	existingEnd := time.Unix(futureUnix, 0)

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	provider := &providercore.Record{
		ID:               77,
		SessionWindowEnd: &existingEnd,
	}
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", futureUnix)) // same as stored

	svc.UpdateSessionWindow(context.Background(), provider, SessionWindowObservation(headers))

	if len(repo.sessionWindowCalls) != 1 {
		t.Fatalf("expected 1 UpdateSessionWindow call, got %d", len(repo.sessionWindowCalls))
	}

	call := repo.sessionWindowCalls[0]
	// windowStart and windowEnd should be nil (no update needed)
	if call.Start != nil || call.End != nil {
		t.Errorf("expected nil start/end (no window change needed), got start=%v end=%v", call.Start, call.End)
	}
	// Status is still updated
	if call.Status != "allowed" {
		t.Errorf("expected status 'allowed', got %q", call.Status)
	}
}

func TestUpdateSessionWindow_ClearsUtilizationOnWindowReset(t *testing.T) {
	// When needInitWindow=true and window is set, utilization should be cleared.
	resetUnix := time.Now().Add(3 * time.Hour).Unix()

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	provider := &providercore.Record{ID: 33} // no existing window → needInitWindow=true
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", resetUnix))
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "0.15")

	svc.UpdateSessionWindow(context.Background(), provider, SessionWindowObservation(headers))

	// Should have 2 UpdateExtra calls: one to clear utilization, one to store new utilization
	if len(repo.updateExtraCalls) != 2 {
		t.Fatalf("expected 2 UpdateExtra calls, got %d", len(repo.updateExtraCalls))
	}

	// First call: clear utilization (nil value)
	clearCall := repo.updateExtraCalls[0]
	if clearCall.Updates["session_window_utilization"] != nil {
		t.Errorf("expected utilization cleared to nil, got %v", clearCall.Updates["session_window_utilization"])
	}

	// Second call: store new utilization
	storeCall := repo.updateExtraCalls[1]
	if val, ok := storeCall.Updates["session_window_utilization"].(float64); !ok || val != 0.15 {
		t.Errorf("expected utilization stored as 0.15, got %v", storeCall.Updates["session_window_utilization"])
	}
}

func TestUpdateSessionWindow_NoClearUtilizationOnCorrection(t *testing.T) {
	// When correcting a stale prediction (needInitWindow=false), utilization should NOT be cleared.
	staleEnd := time.Now().Add(2 * time.Hour)
	realResetUnix := time.Now().Add(4 * time.Hour).Unix()

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	provider := &providercore.Record{
		ID:               66,
		SessionWindowEnd: &staleEnd,
	}
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", realResetUnix))
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "0.30")

	svc.UpdateSessionWindow(context.Background(), provider, SessionWindowObservation(headers))

	// Only 1 UpdateExtra call (store utilization), no clear call
	if len(repo.updateExtraCalls) != 1 {
		t.Fatalf("expected 1 UpdateExtra call (no clear), got %d", len(repo.updateExtraCalls))
	}

	if val, ok := repo.updateExtraCalls[0].Updates["session_window_utilization"].(float64); !ok || val != 0.30 {
		t.Errorf("expected utilization 0.30, got %v", repo.updateExtraCalls[0].Updates["session_window_utilization"])
	}
}

func TestUpdateSessionWindow_SamplesFable7dOiHeaders(t *testing.T) {
	// 被动采样应收集 7d_oi（Fable 专属 7d 窗口）的 utilization 和 reset。
	existingEnd := time.Now().Add(3 * time.Hour)
	resetOIUnix := time.Now().Add(80 * time.Hour).Unix()

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	provider := &providercore.Record{ID: 90, SessionWindowEnd: &existingEnd} // 复用现有窗口，不触发初始化
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-7d_oi-utilization", "0.87")
	headers.Set("anthropic-ratelimit-unified-7d_oi-reset", fmt.Sprintf("%d", resetOIUnix))

	svc.UpdateSessionWindow(context.Background(), provider, SessionWindowObservation(headers))

	if len(repo.updateExtraCalls) != 1 {
		t.Fatalf("expected 1 UpdateExtra call, got %d", len(repo.updateExtraCalls))
	}
	updates := repo.updateExtraCalls[0].Updates
	if val, ok := updates["passive_usage_7d_oi_utilization"].(float64); !ok || val != 0.87 {
		t.Errorf("expected passive_usage_7d_oi_utilization=0.87, got %v", updates["passive_usage_7d_oi_utilization"])
	}
	if val, ok := updates["passive_usage_7d_oi_reset"].(int64); !ok || val != resetOIUnix {
		t.Errorf("expected passive_usage_7d_oi_reset=%d, got %v", resetOIUnix, updates["passive_usage_7d_oi_reset"])
	}
}

func TestUpdateSessionWindow_ClearsFable7dOiOnWindowReset(t *testing.T) {
	// 5h 窗口重置时应连同清除 7d_oi 被动采样数据，与 7d 行为一致。
	resetUnix := time.Now().Add(3 * time.Hour).Unix()

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	provider := &providercore.Record{ID: 91} // 无现有窗口，需要执行初始化
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", resetUnix))

	svc.UpdateSessionWindow(context.Background(), provider, SessionWindowObservation(headers))

	if len(repo.updateExtraCalls) != 1 {
		t.Fatalf("expected 1 UpdateExtra (clear) call, got %d", len(repo.updateExtraCalls))
	}
	clearUpdates := repo.updateExtraCalls[0].Updates
	for _, key := range []string{"passive_usage_7d_oi_utilization", "passive_usage_7d_oi_reset"} {
		if val, present := clearUpdates[key]; !present || val != nil {
			t.Errorf("expected %s cleared to nil on window reset, got present=%v val=%v", key, present, val)
		}
	}
}

func TestUpdateSessionWindow_NoStatusHeader(t *testing.T) {
	// Should return immediately if no status header.
	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	provider := &providercore.Record{ID: 1}

	svc.UpdateSessionWindow(context.Background(), provider, SessionWindowObservation(http.Header{}))

	if len(repo.sessionWindowCalls) != 0 {
		t.Errorf("expected no calls when status header absent, got %d", len(repo.sessionWindowCalls))
	}
}

func TestRateLimitService_HandleUpstreamError_OAuth401SetsTempUnschedulable(t *testing.T) {
	t.Run("gemini", func(t *testing.T) {
		repo := &unauthorizedHealthStore{}
		invalidator := &unauthorizedTokenRecorder{}
		service := newUnauthorizedObserver(repo, invalidator)
		provider := &providercore.Record{
			ID:       100,
			Platform: capability.PlatformGemini,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				"refresh_token":              "rt-100",
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{
					map[string]any{
						"error_code":       401,
						"keywords":         []any{"unauthorized"},
						"duration_minutes": 30,
						"description":      "custom rule",
					},
				},
			},
		}

		shouldDisable := service.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: 401, Body: []byte("unauthorized")}).StopScheduling

		require.True(t, shouldDisable)
		require.Equal(t, 0, repo.setErrorCalls)
		require.Equal(t, 1, repo.tempCalls)
		require.Len(t, invalidator.providers, 1)
	})

	t.Run("antigravity_401_sets_temp_unschedulable", func(t *testing.T) {
		repo := &unauthorizedHealthStore{}
		invalidator := &unauthorizedTokenRecorder{}
		service := newUnauthorizedObserver(repo, invalidator)
		provider := &providercore.Record{
			ID:       100,
			Platform: capability.PlatformAntigravity,
			Type:     capability.ProviderTypeOAuth,
			Status:   billing.StatusActive,
			Credentials: map[string]any{
				"access_token":  "expired-at",
				"refresh_token": "rt-100",
			},
		}

		shouldDisable := service.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: 401, Body: []byte("unauthorized")}).StopScheduling

		require.True(t, shouldDisable)
		require.Equal(t, 0, repo.setErrorCalls, "Antigravity OAuth 401 must keep status=active so refresh worker can recover it")
		require.Equal(t, 1, repo.tempCalls)
		require.Equal(t, int64(100), repo.lastTempID)
		require.Contains(t, repo.lastTempReason, "invalid or expired credentials")
		require.Equal(t, 1, repo.updateExtraCalls)
		require.Equal(t, true, repo.lastExtraUpdates[providercore.AntigravityForceTokenRefreshExtraKey])
		require.Equal(t, "401_invalid", repo.lastExtraUpdates[providercore.AntigravityForceTokenRefreshReasonExtraKey])
		require.Equal(t, true, provider.Extra[providercore.AntigravityForceTokenRefreshExtraKey])
		require.Len(t, invalidator.providers, 1)
		require.Equal(t, int64(100), invalidator.providers[0].ID)
	})
}

// TestRateLimitService_HandleUpstreamError_SparkShadow401RedirectsToParent 检查影子的 401 按母提供商处理。
// 母提供商临时停调并清除 token 缓存，影子保持启用，等待母提供商凭据恢复。
func TestRateLimitService_HandleUpstreamError_SparkShadow401RedirectsToParent(t *testing.T) {
	repo := &unauthorizedHealthStore{}
	repo.providersByID = map[int64]*providercore.Record{}
	invalidator := &unauthorizedTokenRecorder{}
	service := newUnauthorizedObserver(repo, invalidator)

	const parentID = int64(500)
	mother := &providercore.Record{
		ID:          parentID,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Credentials: map[string]any{"refresh_token": "rt-mother"},
	}
	repo.providersByID[parentID] = mother

	shadowParent := parentID
	shadow := &providercore.Record{
		ID:               501,
		Platform:         capability.PlatformOpenAI,
		Type:             capability.ProviderTypeOAuth,
		ParentProviderID: &shadowParent,
		QuotaDimension:   providercore.QuotaDimensionSpark,
		// 影子不持凭据:GetCredential("refresh_token") == ""
	}

	shouldDisable := service.ApplyUpstreamError(context.Background(), shadow, HealthObservation{Status: 401, Body: []byte("unauthorized")}).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.setErrorCalls, "spark shadow must not be permanently disabled on a parent-token 401")
	require.Equal(t, 1, repo.tempCalls)
	require.Equal(t, parentID, repo.lastTempID, "temp-unschedulable must target the credential owner (parent)")
	require.Len(t, invalidator.providers, 1)
	require.Equal(t, parentID, invalidator.providers[0].ID, "token cache invalidation must target the parent")
}

// TestRateLimitService_HandleUpstreamError_OAuth401InvalidatorError 检查 token 缓存失效失败时仍临时停调。
// 401 处理保持数据库中的凭据不变，updateCredentialsCalls 为零。
func TestRateLimitService_HandleUpstreamError_OAuth401InvalidatorError(t *testing.T) {
	repo := &unauthorizedHealthStore{}
	invalidator := &unauthorizedTokenRecorder{err: errors.New("boom")}
	service := newUnauthorizedObserver(repo, invalidator)
	provider := &providercore.Record{
		ID:       101,
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt-101",
		},
	}

	shouldDisable := service.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: 401, Body: []byte("unauthorized")}).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, 1, repo.tempCalls)
	require.Equal(t, 0, repo.updateCredentialsCalls)
	require.Len(t, invalidator.providers, 1)
}

func TestRateLimitService_HandleUpstreamError_NonOAuth401(t *testing.T) {
	repo := &unauthorizedHealthStore{}
	invalidator := &unauthorizedTokenRecorder{}
	service := newUnauthorizedObserver(repo, invalidator)
	provider := &providercore.Record{
		ID:       102,
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeAPIKey,
	}

	shouldDisable := service.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: 401, Body: []byte("unauthorized")}).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Empty(t, invalidator.providers)
}

// TestRateLimitService_HandleUpstreamError_OAuth401DoesNotOverwriteCredentials 检查 401 处理保持并发刷新的凭据。
// 请求持有的凭据快照早于数据库中的 refresh_token，更新健康状态时仍保留数据库当前值。
func TestRateLimitService_HandleUpstreamError_OAuth401DoesNotOverwriteCredentials(t *testing.T) {
	repo := &unauthorizedHealthStore{}
	service := newUnauthorizedObserver(repo, nil)
	provider := &providercore.Record{
		ID:       103,
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "token",
			"refresh_token": "rt-103",
		},
	}

	shouldDisable := service.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: 401, Body: []byte("unauthorized")}).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.updateCredentialsCalls, "401 handler must not write credentials back from the request-start snapshot")
	require.Equal(t, 0, repo.updateExtraCalls, "OpenAI 401 must not set Antigravity force-refresh marker")
	require.Equal(t, 1, repo.tempCalls, "401 handler should still set temp-unschedulable cooldown")
	require.Nil(t, repo.lastCredentials, "no credentials should have been persisted")
}

// TestRateLimitService_HandleUpstreamError_OAuth401NoRefreshTokenSetsError 验证缺失 refresh_token 的 OAuth 提供商 401 后无法靠冷却窗口自愈，应直接标记 error。
func TestRateLimitService_HandleUpstreamError_OAuth401NoRefreshTokenSetsError(t *testing.T) {
	t.Run("openai_no_refresh_token", func(t *testing.T) {
		repo := &unauthorizedHealthStore{}
		invalidator := &unauthorizedTokenRecorder{}
		service := newUnauthorizedObserver(repo, invalidator)
		provider := &providercore.Record{
			ID:       2881,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				"access_token": "expired-at",
			},
		}

		shouldDisable := service.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: 401, Body: []byte("unauthorized")}).StopScheduling

		require.True(t, shouldDisable)
		require.Equal(t, 1, repo.setErrorCalls)
		require.Equal(t, 0, repo.tempCalls)
		require.Equal(t, 0, repo.updateCredentialsCalls)
		require.Contains(t, repo.lastErrorMsg, "refresh_token missing")
		require.Len(t, invalidator.providers, 1)
	})

	t.Run("blank_refresh_token_treated_as_missing", func(t *testing.T) {
		repo := &unauthorizedHealthStore{}
		service := newUnauthorizedObserver(repo, nil)
		provider := &providercore.Record{
			ID:       2882,
			Platform: capability.PlatformGemini,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				"access_token":  "expired-at",
				"refresh_token": "   ",
			},
		}

		shouldDisable := service.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: 401, Body: []byte("unauthorized")}).StopScheduling

		require.True(t, shouldDisable)
		require.Equal(t, 1, repo.setErrorCalls)
		require.Equal(t, 0, repo.tempCalls)
	})

	t.Run("antigravity_no_refresh_token_sets_error", func(t *testing.T) {
		repo := &unauthorizedHealthStore{}
		invalidator := &unauthorizedTokenRecorder{}
		service := newUnauthorizedObserver(repo, invalidator)
		provider := &providercore.Record{
			ID:       2883,
			Platform: capability.PlatformAntigravity,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				"access_token": "expired-at",
			},
		}

		shouldDisable := service.ApplyUpstreamError(context.Background(), provider, HealthObservation{Status: 401, Body: []byte("unauthorized")}).StopScheduling

		require.True(t, shouldDisable)
		require.Equal(t, 1, repo.setErrorCalls, "Antigravity OAuth without refresh_token cannot self-recover")
		require.Equal(t, 0, repo.tempCalls)
		require.Contains(t, repo.lastErrorMsg, "refresh_token missing")
		require.Len(t, invalidator.providers, 1)
	})
}

func (r *errorPolicyRepoStub) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

func (r *errorPolicyRepoStub) SetError(context.Context, int64, string) error {
	r.setErrCalls++
	return nil
}

func (r *errorPolicyRepoStub) SetModelRateLimit(_ context.Context, id int64, _ string, _ time.Time, _ ...string) error {
	r.modelRateLimitCalls = append(r.modelRateLimitCalls, id)
	return nil
}

func newErrorPolicyObserver(repo *errorPolicyRepoStub) *UpstreamHealth {
	return &UpstreamHealth{Core: providercore.NewHealthService(repo, nil, providercore.HealthOptions{})}
}

func (r *modelNotFoundProviderRepoStub) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.tempCalls++
	return nil
}

func (r *modelNotFoundProviderRepoStub) SetModelRateLimit(ctx context.Context, id int64, scope string, resetAt time.Time, reason ...string) error {
	call := modelNotFoundRateLimitCall{
		providerID: id,
		scope:      scope,
		resetAt:    resetAt,
	}
	if len(reason) > 0 {
		call.reason = reason[0]
	}
	r.modelRateLimitCalls = append(r.modelRateLimitCalls, call)
	return r.modelRateLimitErr
}

func openAIModelNotFoundTempProvider() *providercore.Record {
	return &providercore.Record{
		ID:          101,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{
				map[string]any{
					"error_code":       float64(http.StatusNotFound),
					"keywords":         []any{"not found"},
					"duration_minutes": float64(10),
				},
			},
		},
	}
}

func openAICodexPlanGatedOAuthProvider() *providercore.Record {
	return &providercore.Record{
		ID:          202,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{},
	}
}

// newModelHealthObserver 为生产观测入口绑定测试依赖和请求输入。
func newModelHealthObserver(repo *modelNotFoundProviderRepoStub) *UpstreamHealth {
	core := providercore.NewHealthService(repo, nil, providercore.HealthOptions{})
	models := &ModelHealth{Health: core, IsImageModel: media.IsGPTImageGenerationModel}
	return &UpstreamHealth{Core: core, Models: models, Limits: &RateLimitObserver{Health: core}}
}

// healthTestObservation 将测试请求的模型意图转换为观测字段。
func healthTestObservation(ctx context.Context, status int, headers http.Header, body []byte, models ...string) HealthObservation {
	input := HealthObservation{Status: status, Headers: headers, Body: body, ModelProvided: len(models) > 0, ImagesEndpoint: requeststate.OpenAIImagesEndpointFromContext(ctx)}
	if len(models) > 0 {
		input.Model = models[0]
		input.EffectiveModel = strings.TrimSpace(models[0])
	}
	if thinking, ok := requeststate.ThinkingEnabledFromContext(ctx); ok {
		input.Thinking = &thinking
	}
	return input
}

func (r *overloadProviderRepoStub) SetError(_ context.Context, _ int64, _ string) error {
	r.errorCalls++
	return nil
}

func (r *overloadProviderRepoStub) SetOverloaded(_ context.Context, id int64, until time.Time) error {
	r.overloadCalls++
	r.lastOverloadID = id
	r.lastOverloadEnd = until
	return nil
}

// newOverloadObserver 为过载测试绑定健康状态组件。
func newOverloadObserver(repo providercore.HealthStore, options providercore.HealthOptions) *UpstreamHealth {
	return &UpstreamHealth{Core: providercore.NewHealthService(repo, nil, options)}
}

func (s *errSettingRepo) GetValue(context.Context, string) (string, error) { return "", s.readErr }

// newSessionWindowService 为会话窗口测试装配存储和限流恢复组件。
func newSessionWindowService(repo *sessionWindowMockRepo) *providercore.HealthService {
	recovery := providercore.NewRecoveryService(repo, nil, providercore.RecoveryOptions{})
	return providercore.NewHealthService(repo, nil, providercore.HealthOptions{SessionWindows: repo, ClearWindowRateLimit: recovery.ClearRateLimit})
}

// newUnauthorizedObserver 为 401 观测入口绑定健康状态接口。
func newUnauthorizedObserver(repo *unauthorizedHealthStore, invalidator *unauthorizedTokenRecorder) *UpstreamHealth {
	options := providercore.HealthOptions{SessionWindows: repo}
	if invalidator != nil {
		options.InvalidateUnauthorizedToken = invalidator.InvalidateToken
	}
	return &UpstreamHealth{Core: providercore.NewHealthService(repo, nil, options)}
}

func (s *unauthorizedHealthStore) GetByID(_ context.Context, id int64) (*providercore.Record, error) {
	return providercore.CloneRecord(s.providersByID[id]), nil
}

func (s *unauthorizedHealthStore) SetError(_ context.Context, id int64, message string) error {
	s.setErrorCalls++
	s.lastErrorID = id
	s.lastErrorMsg = message
	return nil
}

func (s *unauthorizedHealthStore) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	s.tempCalls++
	s.lastTempID = id
	s.lastTempUntil = until
	s.lastTempReason = reason
	return nil
}

func (s *unauthorizedHealthStore) UpdateExtra(_ context.Context, _ int64, fields map[string]any) error {
	s.updateExtraCalls++
	s.lastExtraUpdates = maps.Clone(fields)
	return nil
}

func (s *unauthorizedHealthStore) UpdateCredentials(_ context.Context, _ int64, fields map[string]any) error {
	s.updateCredentialsCalls++
	s.lastCredentials = maps.Clone(fields)
	return nil
}

func (*unauthorizedHealthStore) UpdateSessionWindow(context.Context, int64, *time.Time, *time.Time, string) error {
	panic("unexpected window update")
}

func (s *unauthorizedTokenRecorder) InvalidateToken(_ context.Context, value *providercore.Record) error {
	s.providers = append(s.providers, value)
	return s.err
}
