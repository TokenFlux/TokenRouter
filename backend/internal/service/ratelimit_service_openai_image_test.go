//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	settingstestkit "github.com/TokenFlux/TokenRouter/internal/settings/testkit"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayService_HandleOpenAIAccountUpstreamError_ImageRateLimitDoesNotBlockWholeAccount(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 203, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	body := []byte(`{"error":{"type":"rate_limit_exceeded","message":"Rate limit reached for gpt-image-2-codex (for limit gpt-image) on input-images per min. Please try again in 1s."}}`)

	disabled := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusTooManyRequests, http.Header{}, body, false, "gpt-image-2").StopScheduling

	require.False(t, disabled)
	require.Len(t, repo.ModelRateLimitCalls, 1)
	require.Equal(t, accountcore.OpenAIImageGenerationRateLimitKey, repo.ModelRateLimitCalls[0].Scope)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIGatewayServiceForwardImages_ImageRateLimitReturnsFailoverAndCoolsCapability(t *testing.T) {

	repo := &gatewaytestkit.ModelHealthStore{}
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat"}`)
	errorBody := `{"error":{"type":"rate_limit_exceeded","message":"Rate limit reached for gpt-image-2-codex (for limit gpt-image) in organization org on input-images per min: Limit 4000, Used 4000. Please try again in 1s."}}`

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil),

		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"X-Request-Id": []string{"req_img_rate_limited"}},
				Body:       io.NopCloser(strings.NewReader(errorBody)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 204,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "input-images per min")
	require.Len(t, repo.ModelRateLimitCalls, 1)
	require.Equal(t, accountcore.OpenAIImageGenerationRateLimitKey, repo.ModelRateLimitCalls[0].Scope)
}

// issue #6171：上游"回文字没回图"是**这一轮**的结果（模型选择了说话），不是账号能力
// 失效。它同时被判为可重试（502）并驱动 failover，若还写 30 分钟账号级冷却，一次闲聊
// 回复就会沿号池把每个被重试到的账号依次冷却掉。冷却仍保留给结构化上游证据，见
// TestOpenAIGatewayServiceForwardImages_StructuredUnavailableCoolsImageCapability。
func TestOpenAIGatewayServiceForwardImages_TextFallbackDoesNotCoolImageCapability(t *testing.T) {

	repo := &gatewaytestkit.ModelHealthStore{}
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat"}`)
	upstreamSSE := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"model\":\"gpt-5.4-mini\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"Here's a polished image prompt for your request.\"}]}]}}\n\n"

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: repo,
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
			},
		},
	}))
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 205,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.False(t, failoverErr.RetryableOnSameAccount)
	// 换号行为不变：该判据仍足以放弃本账号重试这一次请求……
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	// ……但不再写任何账号级状态，否则重试会把冷却一路刷到整个号池。
	require.Empty(t, repo.ModelRateLimitCalls,
		"模型回文字只说明这一轮没出图，不构成账号 30 分钟不可用的证据")
}

// 对照不变式：上游 error 帧点名 image_generation_unavailable 时仍写冷却，
// 保证 #6171 的修复没有把这项能力保护整个废掉。
func TestOpenAIGatewayServiceForwardImages_StructuredUnavailableCoolsImageCapability(t *testing.T) {

	repo := &gatewaytestkit.ModelHealthStore{}
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat"}`)
	upstreamSSE := "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"r\",\"error\":" +
		"{\"type\":\"upstream_error\",\"code\":\"image_generation_unavailable\"," +
		"\"message\":\"image generation tool is not available for this account\"}}}\n\n"

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: repo,
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
			},
		},
	}))
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 206,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	before := time.Now()
	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.Nil(t, result)
	require.Error(t, err)
	require.Len(t, repo.ModelRateLimitCalls, 1)
	call := repo.ModelRateLimitCalls[0]
	require.Equal(t, account.Record.ID, call.AccountID)
	require.Equal(t, accountcore.OpenAIImageGenerationRateLimitKey, call.Scope)
	require.Equal(t, openai.OpenAIImagesOAuthUnavailableReason, call.Reason)
	require.WithinDuration(t, before.Add(openai.OpenAIImagesOAuthUnavailableDefaultCooldown), call.ResetAt, time.Second)
}

func TestOpenAIGatewayService_CoolOpenAIImagesOAuthToolUsesConfiguredCooldown(t *testing.T) {
	accountRepo := &gatewaytestkit.ModelHealthStore{}
	settingRepo := settingstestkit.NewMemory()
	settingRepo.Data[accountcore.SettingKeyOpenAIImagesOAuthUnavailableCooldownSettings] = `{"cooldown_minutes":7}`
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    accountRepo,
		settingService: newExecutionReadersFixture(settingRepo, &config.Config{}),
	}))

	before := time.Now()
	svc.coolOpenAIImagesOAuthTool(context.Background(), &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 206, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}})

	require.Len(t, accountRepo.ModelRateLimitCalls, 1)
	require.WithinDuration(t, before.Add(7*time.Minute), accountRepo.ModelRateLimitCalls[0].ResetAt, time.Second)
}

func TestOpenAIGatewayServiceForwardImages_CapabilityLossCoolsImageScope(t *testing.T) {

	repo := &gatewaytestkit.ModelHealthStore{}
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat"}`)
	errorBody := `{"error":{"message":"Tool choice 'image_generation' not found in 'tools' parameter.","param":"tool_choice","type":"invalid_request_error"}}`

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil),

		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"X-Request-Id": []string{"req_img_capability_lost"}},
				Body:       io.NopCloser(strings.NewReader(errorBody)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 205,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	before := time.Now()
	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.Nil(t, result)
	require.Error(t, err)
	require.Len(t, repo.ModelRateLimitCalls, 1)
	call := repo.ModelRateLimitCalls[0]
	require.Equal(t, account.Record.ID, call.AccountID)
	require.Equal(t, accountcore.OpenAIImageGenerationRateLimitKey, call.Scope)
	require.Equal(t, accountcore.OpenAIImageCapabilityLossReason, call.Reason)
	require.WithinDuration(t, before.Add(accountcore.OpenAIImageCapabilityLossCooldown), call.ResetAt, time.Second)
}

func TestOpenAIGatewayServiceHandleUpstreamError_PassthroughCapabilityLossDoesNotCool(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 206, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	body := []byte(`{"error":{"message":"Tool choice 'image_generation' not found in 'tools' parameter.","param":"tool_choice","type":"invalid_request_error"}}`)

	disabled := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusBadRequest, http.Header{}, body, false, "gpt-5.5").StopScheduling

	require.False(t, disabled)
	require.Empty(t, repo.ModelRateLimitCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}
