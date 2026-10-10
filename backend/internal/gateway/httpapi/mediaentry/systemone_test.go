package mediaentry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type systemOneFunds struct{}

type systemOneRPM struct {
	scheduler.UserRPMCache
	calls int
}

// TestSystemOneFailoverRules 检查重试耗尽时的平台匹配、监控开关和重试时间。
func TestSystemOneFailoverRules(t *testing.T) {
	for _, platform := range []string{"jev", "openai", ""} {
		t.Run(platform, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointSystemOne, nil)
			rule := gatewaytestkit.NonFailoverRule(503, "busy", 409, "Configured platform error")
			if platform != "" {
				rule.Platforms = []string{platform}
			}
			rule.SkipMonitoring = true
			rules := gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{rule})
			defer rules.Stop()
			runtime := New(Bindings{Common: openaiattempt.Bindings{Support: &openaiattempt.Support{Rules: rules}}})
			(mediaHTTPAdapter{runtime}).BindErrors(c)
			run := &systemOneRun{h: runtime, c: c}
			run.End(&forward.UpstreamFailoverError{StatusCode: 503, ResponseBody: []byte(`{"error":{"message":"busy"}}`), ResponseHeaders: http.Header{"Retry-After": []string{"17"}}})
			require.Equal(t, "17", recorder.Header().Get("Retry-After"))
			if platform == "openai" {
				require.Equal(t, 502, recorder.Code)
				require.NotContains(t, recorder.Body.String(), "Configured platform error")
				require.False(t, c.GetBool(gatewayhttp.OpsSkipPassthroughKey))
			} else {
				require.Equal(t, 409, recorder.Code)
				require.Contains(t, recorder.Body.String(), "Configured platform error")
				require.True(t, c.GetBool(gatewayhttp.OpsSkipPassthroughKey))
			}
		})
	}
}

// TestSystemOneMissingUsageRetainsSuccessfulRequest 检查用量告警的请求关联及成功响应。
func TestSystemOneMissingUsageRetainsSuccessfulRequest(t *testing.T) {
	var saved telemetry.RequestRecord
	ctx := telemetry.WithRequestCapture(context.Background(), telemetry.RequestRecord{RequestID: "local", State: "completed", Status: 200}, func(record telemetry.RequestRecord) { saved = record })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointSystemOne, nil).WithContext(ctx)
	c.Data(200, "application/json", []byte(`{"answers":{"available":{"type":"noul","noul":1}}}`))
	run := &systemOneRun{c: c, log: zap.NewNop(), selection: &gatewayprovider.SelectionResult{Provider: gatewayprovider.NewExecutionProvider(&provider.Record{ID: 42, Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey})}}
	run.MissingUsage(ctx, upstream.AttemptResult{Served: true, RequestID: "upstream"})
	require.Equal(t, 200, c.Writer.Status())
	require.Equal(t, "completed", saved.State)
	require.Len(t, saved.Attempts, 1)
	require.Equal(t, "usage_unknown", saved.Attempts[0].Outcome)
	require.Equal(t, "upstream", saved.Attempts[0].RequestID)
	events, present := c.Get(gatewayhttp.OpsUpstreamErrorsKey)
	require.True(t, present)
	typed, ok := events.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, typed, 1)
	require.Equal(t, "usage_invalid", typed[0].Kind)
}

func (systemOneFunds) Check(context.Context, billing.CheckInput) error { return nil }

func (r *systemOneRPM) IncrementUserRPM(context.Context, int64) (int, error) {
	r.calls++
	return r.calls, nil
}

// TestSystemOneConsumesOneRPM 检查首次准入后，多次上游尝试共用一次 RPM 计数。
func TestSystemOneConsumesOneRPM(t *testing.T) {
	cache := &systemOneRPM{}
	checker := admission.NewFundingAdmission(systemOneFunds{}, scheduler.NewRPMAdmission(cache, nil, scheduler.Diagnostics{}))
	called := 0
	runtime := New(Bindings{CheckFunding: checker.CheckKey, Platform: PlatformPorts{SystemOne: func(context.Context, *gin.Context, *gatewayprovider.ExecutionProvider, []byte) (upstream.AttemptResult, error) {
		called++
		return upstream.AttemptResult{Served: true, HasUsage: true}, nil
	}}})
	key := &apikey.APIKey{ID: 1, User: &apikey.User{ID: 1, RPMLimit: 1}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), key)
	require.Nil(t, (mediaHTTPAdapter{runtime}).Billing(c))
	run := &systemOneRun{h: runtime, c: c, key: key, selection: &gatewayprovider.SelectionResult{Provider: gatewayprovider.NewExecutionProvider(&provider.Record{ID: 1, Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey})}}
	for range 2 {
		result := run.Forward(c.Request.Context())
		require.NoError(t, result.Err)
	}
	require.Equal(t, 1, cache.calls)
	require.Equal(t, 2, called)
}
