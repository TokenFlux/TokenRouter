//go:build unit

package provider

import (
	"context"
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/stretchr/testify/require"
)

func TestApplyErrorPolicy(t *testing.T) {
	tests := []struct {
		name              string
		account           *accountcore.Record
		statusCode        int
		body              []byte
		expectedHandled   bool
		expectedStatus    int  // expected outStatus
		expectedSwitchErr bool // expect *AntigravityAccountSwitchError
		handleErrorCalls  int
	}{
		{
			name: "none_not_handled",
			account: &accountcore.Record{
				ID:       10,
				Type:     capability.AccountTypeOAuth,
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
			account: &accountcore.Record{
				ID:       11,
				Type:     capability.AccountTypeAPIKey,
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
			account: &accountcore.Record{
				ID:       12,
				Type:     capability.AccountTypeAPIKey,
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
			account: &accountcore.Record{
				ID:       13,
				Type:     capability.AccountTypeOAuth,
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
			rlSvc := accountcore.NewHealthService(repo, nil, accountcore.HealthOptions{})
			svc := newAntigravityPolicyFixture(repo, rlSvc)

			var handleErrorCount int
			p := AntigravityRetryRequest{
				Context:        context.Background(),
				Prefix:         "[test]",
				Account:        tt.account,
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
				var switchErr *antigravity.AntigravityAccountSwitchError
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
	repo := &antigravityPolicyStoreFixture{}
	var cleared []struct {
		groupID     int64
		sessionHash string
	}
	rlSvc := accountcore.NewHealthService(repo, nil, accountcore.HealthOptions{})
	svc := newAntigravityPolicyFixture(repo, rlSvc)

	account := &accountcore.Record{
		ID:       31,
		Type:     capability.AccountTypeAPIKey,
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
		Account:    account,
		ModelStore: repo,
		ClearSticky: func() {
			cleared = append(cleared, struct {
				groupID     int64
				sessionHash string
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

// 夹具只记录原健康端口写入；策略与供应商分类使用生产实现。
type antigravityPolicyStoreFixture struct {
	accountcore.HealthStore
	tempCalls           int
	modelRateLimitCalls []struct{ scope, modelKey string }
}

func (s *antigravityPolicyStoreFixture) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	s.tempCalls++
	return nil
}
func (s *antigravityPolicyStoreFixture) SetModelRateLimit(_ context.Context, _ int64, key string, _ time.Time, _ ...string) error {
	s.modelRateLimitCalls = append(s.modelRateLimitCalls, struct{ scope, modelKey string }{key, key})
	return nil
}
func (s *antigravityPolicyStoreFixture) UpdateExtra(context.Context, int64, map[string]any) error {
	return nil
}
func newAntigravityPolicyFixture(store *antigravityPolicyStoreFixture, policy *accountcore.HealthService) *AntigravityRetry {
	return &AntigravityRetry{Policy: policy, Health: &accountcore.AntigravityHealth{Store: store, ModelKeys: AntigravityModelLimitKeys, Info: func(string, ...any) {}, Logf: func(string, ...any) {}}}
}
