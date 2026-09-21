//go:build unit

package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/stretchr/testify/require"
)

func TestIsCreditsExhausted_UsesAICreditsKey(t *testing.T) {
	t.Run("无 AICredits key 则积分可用", func(t *testing.T) {
		account := &accountcore.Record{
			ID:       1,
			Platform: capability.PlatformAntigravity,
			Extra: map[string]any{
				"allow_overages": true,
			},
		}
		_, input := newAntigravityRetryFixture().Bind(AntigravityRetryRequest{Account: account})
		require.False(t, input.CreditsExhausted())
	})

	t.Run("AICredits key 生效则积分耗尽", func(t *testing.T) {
		account := &accountcore.Record{
			ID:       2,
			Platform: capability.PlatformAntigravity,
			Extra: map[string]any{
				"allow_overages": true,
				"model_rate_limits": map[string]any{
					accountcore.CreditsExhaustedKey: map[string]any{
						"rate_limited_at":     time.Now().UTC().Format(time.RFC3339),
						"rate_limit_reset_at": time.Now().Add(5 * time.Hour).UTC().Format(time.RFC3339),
					},
				},
			},
		}
		_, input := newAntigravityRetryFixture().Bind(AntigravityRetryRequest{Account: account})
		require.True(t, input.CreditsExhausted())
	})

	t.Run("AICredits key 过期则积分可用", func(t *testing.T) {
		account := &accountcore.Record{
			ID:       3,
			Platform: capability.PlatformAntigravity,
			Extra: map[string]any{
				"allow_overages": true,
				"model_rate_limits": map[string]any{
					accountcore.CreditsExhaustedKey: map[string]any{
						"rate_limited_at":     time.Now().Add(-6 * time.Hour).UTC().Format(time.RFC3339),
						"rate_limit_reset_at": time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
					},
				},
			},
		}
		_, input := newAntigravityRetryFixture().Bind(AntigravityRetryRequest{Account: account})
		require.False(t, input.CreditsExhausted())
	})
}

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
	account := &accountcore.Record{
		ID:       101,
		Name:     "acc-101",
		Type:     capability.AccountTypeOAuth,
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
		Account:     account,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"model":"claude-opus-4-6","request":{}}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", account.ID, account.Concurrency)
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
	account := &accountcore.Record{
		ID:       102,
		Name:     "acc-102",
		Type:     capability.AccountTypeOAuth,
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
		Account:     account,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", account.ID, account.Concurrency)
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

func TestAntigravityRetryLoop_ModelRateLimited_InjectsCredits(t *testing.T) {
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
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
	// 模型已限流 + overages 启用 + 无 AICredits key → 应直接注入积分
	account := &accountcore.Record{
		ID:          103,
		Name:        "acc-103",
		Type:        capability.AccountTypeOAuth,
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
		Account:     account,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", account.ID, account.Concurrency)
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

func TestAntigravityRetryLoop_CreditsExhausted_DoesNotInject(t *testing.T) {
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-1.test"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	// 模型限流 + overages 启用 + AICredits key 生效 → 不应注入积分，应切号
	account := &accountcore.Record{
		ID:          104,
		Name:        "acc-104",
		Type:        capability.AccountTypeOAuth,
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
				accountcore.CreditsExhaustedKey: map[string]any{
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
		Account:        account,
		AccessToken:    "token",
		Action:         "generateContent",
		Body:           []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		RequestedModel: "claude-sonnet-4-5",
		HandleError: func(int, http.Header, []byte) {
		},
	})
	_, err := adapter.AntigravityRetryLoop(input)

	// 模型限流 + 积分耗尽 → 应触发切号错误
	require.Error(t, err)
	var switchErr *antigravity.AntigravityAccountSwitchError
	require.ErrorAs(t, err, &switchErr)
}

func TestAntigravityRetryLoop_CreditErrorMarksExhausted(t *testing.T) {
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
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
	// 模型限流 + overages 启用 + 积分可用 → 注入积分但上游返回积分不足
	account := &accountcore.Record{
		ID:          105,
		Name:        "acc-105",
		Type:        capability.AccountTypeOAuth,
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
		Account:     account,
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", account.ID, account.Concurrency)
		},
		ModelStore:     repo,
		RequestedModel: "claude-sonnet-4-5",
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.NoError(t, err)
	require.NotNil(t, result)
	// 验证 AICredits key 已通过 SetModelRateLimit 写入数据库
	require.Len(t, repo.modelRateLimitCalls, 1, "应通过 SetModelRateLimit 写入 AICredits key")
	require.Equal(t, accountcore.CreditsExhaustedKey, repo.modelRateLimitCalls[0].modelKey)
}
