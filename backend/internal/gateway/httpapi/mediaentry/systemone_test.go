package mediaentry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

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
