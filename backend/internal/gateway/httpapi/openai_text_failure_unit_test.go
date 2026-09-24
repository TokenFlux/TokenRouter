//go:build unit

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
)

func TestGrokContentPolicy403DoesNotMutateOrFailover(t *testing.T) {
	repo := &textFailureStore{}
	svc := textFailureFixture(repo, false)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4715, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	body := []byte(`{"error":{"code":"new_sensitive","message":"text is sensitive"}}`)

	gatewayprovider.ApplyGrokExecutionHealth(context.Background(), svc.Grok.Health, account, http.StatusForbidden, nil, body, "")

	require.Zero(t, repo.tempUnschedCalls)
	require.Zero(t, repo.rateLimitedCalls)
	require.Zero(t, repo.updateCalls)
	require.False(t, svc.Output.Health.Runtime.Blocked(account.Record.ID, nil))
	require.False(t, gatewayprovider.ShouldFailoverGrokResponse(http.StatusForbidden, body))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}
	got := svc.httpFailover(context.Background(), c, account, resp, body, "text is sensitive", "grok-4.5")
	require.Nil(t, got)
	require.Zero(t, repo.tempUnschedCalls)
}
func TestGrokNonFailoverDoesNotApplyGenericTempUnschedulablePolicy(t *testing.T) {

	repo := &textFailureStore{}
	svc := textFailureFixture(repo, true)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5099,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{map[string]any{
				"error_code":       float64(http.StatusForbidden),
				"keywords":         []any{"text is sensitive"},
				"duration_minutes": float64(1),
			}},
		}},
	}
	body := []byte(`{"error":{"code":"new_sensitive","message":"text is sensitive"}}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}

	got := svc.httpFailover(
		context.Background(), c, account, resp, body, "text is sensitive", "",
	)

	require.Nil(t, got)
	require.Zero(t, repo.tempUnschedCalls)
	require.Zero(t, repo.rateLimitedCalls)
	require.Zero(t, repo.updateCalls)
	require.False(t, svc.Output.Health.Runtime.Blocked(account.Record.ID, nil))
}
func TestFailoverOpenAIUpstreamHTTPErrorUsesOnlyGrokRateLimitPolicy(t *testing.T) {

	repo := &textFailureStore{}
	svc := textFailureFixture(repo, false)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 70, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"45"}},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	failoverErr := svc.httpFailover(
		context.Background(), c, account, resp,
		[]byte(`{"error":{"message":"rate limited"}}`), "rate limited", "grok-4.3",
	)

	require.NotNil(t, failoverErr)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Zero(t, repo.tempUnschedCalls)
}

// 自 #4547（issue 4527 第4点）起，临时不可调度规则命中已知模型时按模型隔离：
// 只封 (账号, 模型) 对，不再账号级一刀切；未知模型仍走账号级兜底
// （见 TestOpenAITempUnschedulable_UnknownModelKeepsAccountRuntimeBlock）。
// 池模式规则仍然生效（issue 4470）：停止同账号重试并对命中模型设临时封锁。
func TestOpenAIPoolModeTempRule_StopsSameAccountRetryAndIsolatesBlockToModel(t *testing.T) {
	repo := &gatewaytestkit.ErrorPolicyStore{}
	gateway := textFailureFixture(repo, true)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 46,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(http.StatusServiceUnavailable)},
			"temp_unschedulable_enabled":   true,
			"temp_unschedulable_rules": []any{
				map[string]any{
					"error_code":       float64(http.StatusServiceUnavailable),
					"keywords":         []any{"unavailable"},
					"duration_minutes": float64(30),
				},
			},
		}},
	}
	body := []byte(`{"error":{"message":"Service temporarily unavailable"}}`)
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
	}

	failoverErr := gateway.httpFailover(
		context.Background(),
		nil,
		account,
		resp,
		body,
		"Service temporarily unavailable",
		"gpt-5.4",
	)

	require.NotNil(t, failoverErr)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Zero(t, repo.TempCalls)
	require.Equal(t, 0, repo.SetErrCalls)
	require.Equal(t, billing.StatusActive, account.Record.Status)
	require.Len(t, repo.ModelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.4", repo.ModelRateLimitCalls[0].Scope)
	require.False(t, gateway.Output.Health.Runtime.Blocked(account.Record.ID, nil))
	require.False(t, gateway.Output.Health.Runtime.Blocked(account.Record.ID, nil) || gateway.Output.Health.ModelTransient.IsBlocked(account.Record.ID, "gpt-5.5", time.Now()))
}
