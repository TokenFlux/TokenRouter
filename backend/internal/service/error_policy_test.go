//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TestCheckErrorPolicy — 6 table-driven cases for the pure logic function
// ---------------------------------------------------------------------------

func TestCheckErrorPolicy(t *testing.T) {
	tests := []struct {
		name       string
		account    *Account
		statusCode int
		body       []byte
		expected   ErrorPolicyResult
	}{
		{
			name: "no_policy_oauth_returns_none",
			account: &Account{
				ID:       1,
				Type:     AccountTypeOAuth,
				Platform: PlatformAntigravity,
				// no custom error codes, no temp rules
			},
			statusCode: 500,
			body:       []byte(`"error"`),
			expected:   ErrorPolicyNone,
		},
		{
			name: "custom_error_codes_hit_returns_matched",
			account: &Account{
				ID:       2,
				Type:     AccountTypeAPIKey,
				Platform: PlatformAntigravity,
				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(429), float64(500)},
				},
			},
			statusCode: 500,
			body:       []byte(`"error"`),
			expected:   ErrorPolicyCustomMatched,
		},
		{
			name: "custom_error_codes_miss_returns_skipped",
			account: &Account{
				ID:       3,
				Type:     AccountTypeAPIKey,
				Platform: PlatformAntigravity,
				Credentials: map[string]any{
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(429), float64(500)},
				},
			},
			statusCode: 503,
			body:       []byte(`"error"`),
			expected:   ErrorPolicyCustomSkipped,
		},
		{
			name: "temp_unschedulable_hit_returns_temp_unscheduled",
			account: &Account{
				ID:       4,
				Type:     AccountTypeOAuth,
				Platform: PlatformAntigravity,
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
			expected:   ErrorPolicyTempUnscheduled,
		},
		{
			name: "temp_unschedulable_401_first_hit_returns_temp_unscheduled",
			account: &Account{
				ID:       14,
				Type:     AccountTypeOAuth,
				Platform: PlatformAntigravity,
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
			expected:   ErrorPolicyTempUnscheduled,
		},
		{
			// Antigravity 401 不走升级逻辑（由 applyErrorPolicy 的 temp_unschedulable_rules 自行控制），
			// second hit 仍然返回 TempUnscheduled。
			name: "temp_unschedulable_401_second_hit_antigravity_stays_temp",
			account: &Account{
				ID:                      15,
				Type:                    AccountTypeOAuth,
				Platform:                PlatformAntigravity,
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
			expected:   ErrorPolicyTempUnscheduled,
		},
		{
			name: "temp_unschedulable_body_miss_returns_none",
			account: &Account{
				ID:       5,
				Type:     AccountTypeOAuth,
				Platform: PlatformAntigravity,
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
			expected:   ErrorPolicyNone,
		},
		{
			name: "custom_error_codes_override_temp_unschedulable",
			account: &Account{
				ID:       6,
				Type:     AccountTypeAPIKey,
				Platform: PlatformAntigravity,
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
			expected:   ErrorPolicyCustomMatched, // custom codes take precedence
		},
		{
			name: "pool_mode_custom_error_codes_hit_returns_matched",
			account: &Account{
				ID:       7,
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":                  true,
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(401), float64(403)},
				},
			},
			statusCode: 401,
			body:       []byte(`unauthorized`),
			expected:   ErrorPolicyCustomMatched,
		},
		{
			name: "pool_mode_without_custom_error_codes_returns_bypassed",
			account: &Account{
				ID:       8,
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode": true,
				},
			},
			statusCode: 401,
			body:       []byte(`unauthorized`),
			expected:   ErrorPolicyPoolBypassed,
		},
		{
			name: "pool_mode_temp_unschedulable_hit_returns_temp_unscheduled",
			account: &Account{
				ID:       9,
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
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
			expected:   ErrorPolicyTempUnscheduled,
		},
		{
			name: "pool_mode_repeated_401_explicit_rule_stays_temp_unscheduled",
			account: &Account{
				ID:                      11,
				Type:                    AccountTypeAPIKey,
				Platform:                PlatformOpenAI,
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
			expected:   ErrorPolicyTempUnscheduled,
		},
		{
			name: "pool_mode_temp_unschedulable_miss_returns_bypassed",
			account: &Account{
				ID:       10,
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
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
			expected:   ErrorPolicyPoolBypassed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &errorPolicyRepoStub{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)

			result := svc.CheckErrorPolicy(context.Background(), tt.account, tt.statusCode, tt.body)
			require.Equal(t, tt.expected, result, "unexpected ErrorPolicyResult")
		})
	}
}

func TestHandleUpstreamError_PoolModePolicies(t *testing.T) {
	t.Run("pool_mode_without_custom_error_codes_still_skips", func(t *testing.T) {
		repo := &errorPolicyRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		account := &Account{
			ID:       30,
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
			Credentials: map[string]any{
				"pool_mode": true,
			},
		}

		shouldDisable := svc.HandleUpstreamError(context.Background(), account, 401, http.Header{}, []byte("unauthorized"))

		require.False(t, shouldDisable)
		require.Equal(t, 0, repo.setErrCalls)
		require.Equal(t, 0, repo.tempCalls)
	})

	t.Run("pool_mode_with_custom_error_codes_uses_local_error_policy", func(t *testing.T) {
		repo := &errorPolicyRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		account := &Account{
			ID:       31,
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
			Credentials: map[string]any{
				"pool_mode":                  true,
				"custom_error_codes_enabled": true,
				"custom_error_codes":         []any{float64(401)},
			},
		}

		shouldDisable := svc.HandleUpstreamError(context.Background(), account, 401, http.Header{}, []byte("unauthorized"))

		require.True(t, shouldDisable)
		require.Equal(t, 1, repo.setErrCalls)
		require.Equal(t, 0, repo.tempCalls)
	})

	t.Run("pool_mode_explicit_temp_rule_stops_scheduling", func(t *testing.T) {
		repo := &errorPolicyRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		account := &Account{
			ID:       32,
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
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

		shouldDisable := svc.HandleUpstreamError(
			context.Background(),
			account,
			http.StatusServiceUnavailable,
			http.Header{},
			[]byte("Service temporarily unavailable"),
		)

		require.True(t, shouldDisable)
		require.Equal(t, 0, repo.setErrCalls)
		require.Equal(t, 1, repo.tempCalls)
	})

	t.Run("pool_mode_temp_rule_miss_still_skips", func(t *testing.T) {
		repo := &errorPolicyRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		account := &Account{
			ID:       33,
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
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

		shouldDisable := svc.HandleUpstreamError(
			context.Background(),
			account,
			http.StatusServiceUnavailable,
			http.Header{},
			[]byte("Service temporarily unavailable"),
		)

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
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			account := &Account{
				ID:       int64(1000 + tt.statusCode),
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":                  true,
					"custom_error_codes_enabled": true,
					"custom_error_codes":         []any{float64(tt.statusCode)},
				},
			}

			decision := svc.ApplyUpstreamError(
				context.Background(), account, tt.statusCode, http.Header{}, []byte(`{"error":{"message":"configured failure"}}`),
			)

			require.Equal(t, ErrorPolicyCustomMatched, decision.Policy)
			require.True(t, decision.StopScheduling)
			require.False(t, decision.RetryableOnSameAccount(account, tt.statusCode))
			require.Equal(t, 1, repo.setErrCalls)
			require.Equal(t, 0, repo.tempCalls)
		})
	}
}

// TestUpstreamErrorDecision_PoolRetryStatusPromotesFailover 验证管理员配置的池模式
// 重试状态码可以提升非默认故障转移错误，同时不会写入本地状态。
func TestUpstreamErrorDecision_PoolRetryStatusPromotesFailover(t *testing.T) {
	repo := &errorPolicyRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       20422,
		Type:     AccountTypeAPIKey,
		Platform: PlatformOpenAI,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(http.StatusUnprocessableEntity)},
		},
	}

	decision := svc.ApplyUpstreamError(
		context.Background(), account, http.StatusUnprocessableEntity, http.Header{}, []byte(`{"error":{"message":"unprocessable"}}`),
	)

	require.Equal(t, ErrorPolicyPoolBypassed, decision.Policy)
	require.True(t, decision.ShouldFailover(account, http.StatusUnprocessableEntity, false))
	require.True(t, decision.RetryableOnSameAccount(account, http.StatusUnprocessableEntity))
	require.Zero(t, repo.setErrCalls)
	require.Zero(t, repo.tempCalls)
	require.Empty(t, repo.modelRateLimitCalls)
}

// TestUpstreamErrorDecision_UsesSeparateEntryDefaults 验证普通账号保持入口旧行为，
// 池模式则使用平台错误分类，并且显式策略始终覆盖两者。
func TestUpstreamErrorDecision_UsesSeparateEntryDefaults(t *testing.T) {
	account := &Account{
		ID:          20423,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"pool_mode": true},
	}

	require.False(t, (UpstreamErrorDecision{Policy: ErrorPolicyNone}).ShouldFailoverWithDefaults(account, http.StatusBadGateway, false, true))
	require.True(t, (UpstreamErrorDecision{Policy: ErrorPolicyPoolBypassed}).ShouldFailoverWithDefaults(account, http.StatusBadGateway, false, true))
	require.True(t, (UpstreamErrorDecision{Policy: ErrorPolicyCustomMatched}).ShouldFailoverWithDefaults(account, http.StatusUnprocessableEntity, false, false))
	require.False(t, (UpstreamErrorDecision{Policy: ErrorPolicyCustomSkipped}).ShouldFailoverWithDefaults(account, http.StatusBadGateway, true, true))
}

// TestGatewayFailoverSideEffects_BedrockUsesMappedModel 验证 Bedrock 显式临时规则
// 使用实际上游模型，并禁止池模式同账号重试。
func TestGatewayFailoverSideEffects_BedrockUsesMappedModel(t *testing.T) {
	repo := &errorPolicyRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc := &GatewayService{rateLimitService: rateLimitService}
	account := &Account{
		ID:       20503,
		Type:     AccountTypeBedrock,
		Platform: PlatformAnthropic,
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
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"maintenance"}}`)),
	}

	decision := svc.handleFailoverSideEffects(context.Background(), resp, account, "anthropic.claude-mapped")

	require.Equal(t, ErrorPolicyTempUnscheduled, decision.Policy)
	require.True(t, decision.StopScheduling)
	require.False(t, decision.RetryableOnSameAccount(account, http.StatusServiceUnavailable))
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "anthropic.claude-mapped", repo.modelRateLimitCalls[0].scope)
	require.Zero(t, repo.tempCalls)
}

// ---------------------------------------------------------------------------
// TestApplyErrorPolicy — 4 table-driven cases for the wrapper method
// ---------------------------------------------------------------------------

func TestApplyErrorPolicy(t *testing.T) {
	tests := []struct {
		name              string
		account           *Account
		statusCode        int
		body              []byte
		expectedHandled   bool
		expectedStatus    int  // expected outStatus
		expectedSwitchErr bool // expect *AntigravityAccountSwitchError
		handleErrorCalls  int
	}{
		{
			name: "none_not_handled",
			account: &Account{
				ID:       10,
				Type:     AccountTypeOAuth,
				Platform: PlatformAntigravity,
			},
			statusCode:       500,
			body:             []byte(`"error"`),
			expectedHandled:  false,
			expectedStatus:   500, // passthrough
			handleErrorCalls: 0,
		},
		{
			name: "skipped_handled_no_handleError",
			account: &Account{
				ID:       11,
				Type:     AccountTypeAPIKey,
				Platform: PlatformAntigravity,
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
			account: &Account{
				ID:       12,
				Type:     AccountTypeAPIKey,
				Platform: PlatformAntigravity,
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
			account: &Account{
				ID:       13,
				Type:     AccountTypeOAuth,
				Platform: PlatformAntigravity,
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
			repo := &errorPolicyRepoStub{}
			rlSvc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc := &AntigravityGatewayService{
				rateLimitService: rlSvc,
			}

			var handleErrorCount int
			p := antigravityRetryLoopParams{
				ctx:            context.Background(),
				prefix:         "[test]",
				account:        tt.account,
				requestedModel: "claude-sonnet-4-5",
				handleError: func(ctx context.Context, prefix string, account *Account, statusCode int, headers http.Header, body []byte, requestedModel string, groupID int64, sessionHash string, isStickySession bool) *handleModelRateLimitResult {
					handleErrorCount++
					return nil
				},
				isStickySession: true,
			}

			handled, outStatus, retErr := svc.applyErrorPolicy(p, tt.statusCode, http.Header{}, tt.body)

			require.Equal(t, tt.expectedHandled, handled, "handled mismatch")
			require.Equal(t, tt.expectedStatus, outStatus, "outStatus mismatch")
			require.Equal(t, tt.handleErrorCalls, handleErrorCount, "handleError call count mismatch")

			if tt.expectedSwitchErr {
				var switchErr *AntigravityAccountSwitchError
				require.ErrorAs(t, retErr, &switchErr)
				require.Equal(t, tt.account.ID, switchErr.OriginalAccountID)
				require.Zero(t, repo.tempCalls)
				require.Len(t, repo.modelRateLimitCalls, 1)
				require.Equal(t, "claude-sonnet-4-5", repo.modelRateLimitCalls[0].scope)
			} else {
				require.NoError(t, retErr)
			}
		})
	}
}

func TestApplyErrorPolicy_GeminiRateLimitBypassesCustomSkip(t *testing.T) {
	repo := &stubAntigravityAccountRepo{}
	cache := &stubSmartRetryCache{}
	rlSvc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc := &AntigravityGatewayService{
		rateLimitService: rlSvc,
		accountRepo:      repo,
		cache:            cache,
	}

	account := &Account{
		ID:       31,
		Type:     AccountTypeAPIKey,
		Platform: PlatformAntigravity,
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
	p := antigravityRetryLoopParams{
		ctx:         context.Background(),
		prefix:      "[test]",
		account:     account,
		accountRepo: repo,
		groupID:     42,
		sessionHash: "gemini:sticky",
		handleError: func(context.Context, string, *Account, int, http.Header, []byte, string, int64, string, bool) *handleModelRateLimitResult {
			t.Fatal("model rate limit should be handled before custom error fallback")
			return nil
		},
	}

	handled, outStatus, retErr := svc.applyErrorPolicy(p, http.StatusTooManyRequests, http.Header{}, body)

	require.True(t, handled)
	require.Equal(t, http.StatusTooManyRequests, outStatus)
	require.NoError(t, retErr)
	require.Len(t, repo.modelRateLimitCalls, 2)
	require.Equal(t, "gemini-3-flash", repo.modelRateLimitCalls[0].modelKey)
	require.Equal(t, antigravityGeminiModelRateLimitKey, repo.modelRateLimitCalls[1].modelKey)
	require.Len(t, cache.deleteCalls, 1)
	require.Equal(t, int64(42), cache.deleteCalls[0].groupID)
	require.Equal(t, "gemini:sticky", cache.deleteCalls[0].sessionHash)
}

// ---------------------------------------------------------------------------
// errorPolicyRepoStub — minimal AccountRepository stub for error policy tests
// ---------------------------------------------------------------------------

type errorPolicyRepoStub struct {
	mockAccountRepoForGemini
	tempCalls           int
	setErrCalls         int
	lastErrorMsg        string
	modelRateLimitCalls []modelNotFoundRateLimitCall
}

// retryExhaustedCooldownRepoStub 记录同账号重试耗尽后的本地冷却写入。
type retryExhaustedCooldownRepoStub struct {
	AccountRepository
	account   *Account
	tempCalls int
}

func (r *retryExhaustedCooldownRepoStub) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *retryExhaustedCooldownRepoStub) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

// TestTempUnscheduleRetryableError_PoolModeSkipsLegacyCooldown 验证池模式的
// 同账号重试耗尽后只切号，不复用旧版 400/502 一分钟冷却。
func TestTempUnscheduleRetryableError_PoolModeSkipsLegacyCooldown(t *testing.T) {
	poolAccount := &Account{
		ID:       81,
		Type:     AccountTypeAPIKey,
		Platform: PlatformAnthropic,
		Credentials: map[string]any{
			"pool_mode": true,
		},
	}
	repo := &retryExhaustedCooldownRepoStub{account: poolAccount}
	svc := &GatewayService{accountRepo: repo}

	svc.TempUnscheduleRetryableError(context.Background(), poolAccount.ID, &UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		RetryableOnSameAccount: true,
	})

	require.Zero(t, repo.tempCalls)

	// 非池账号继续保留旧版特殊错误的冷却行为。
	repo.account = &Account{ID: 82, Type: AccountTypeOAuth, Platform: PlatformAntigravity}
	svc.TempUnscheduleRetryableError(context.Background(), repo.account.ID, &UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		RetryableOnSameAccount: true,
	})
	require.Equal(t, 1, repo.tempCalls)
}

func (r *errorPolicyRepoStub) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.tempCalls++
	return nil
}

func (r *errorPolicyRepoStub) SetError(ctx context.Context, id int64, errorMsg string) error {
	r.setErrCalls++
	r.lastErrorMsg = errorMsg
	return nil
}

func (r *errorPolicyRepoStub) SetModelRateLimit(_ context.Context, id int64, scope string, resetAt time.Time, reason ...string) error {
	call := modelNotFoundRateLimitCall{accountID: id, scope: scope, resetAt: resetAt}
	if len(reason) > 0 {
		call.reason = reason[0]
	}
	r.modelRateLimitCalls = append(r.modelRateLimitCalls, call)
	return nil
}
