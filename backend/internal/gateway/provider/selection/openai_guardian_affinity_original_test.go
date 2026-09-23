package selection

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	clientmeta "github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	scheduler "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func guardianAffinityTestContext(t *testing.T, model, subagent, parentHeader, metadata string) context.Context {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set(clientmeta.OpenAISubagentHeader, subagent)
	if parentHeader != "" {
		c.Request.Header.Set(clientmeta.CodexParentThreadIDHeader, parentHeader)
	}
	if metadata != "" {
		c.Request.Header.Set(clientmeta.CodexTurnMetadataHeader, metadata)
	}
	return gatewayhttp.WithOpenAIGuardianParentAffinity(context.Background(), c, nil, model)
}

func TestOpenAIAccountSchedulerGuardianAffinitySelectsParent(t *testing.T) {
	parentID := "22222222-2222-4222-8222-222222222222"
	parentHash, _ := scheduler.DeriveSessionHashes(parentID)
	groupID := int64(102001)
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 39001, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 10, GroupIDs: []int64{groupID}, Credentials: map[string]any{"access_token": "parent", "plan_type": "team"}}},
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 39002, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}, Credentials: map[string]any{"access_token": "fallback", "plan_type": "team"}}},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:" + parentHash: 39001}, deletedSessions: map[string]int{}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Accounts: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}},
		Shared: Shared{
			Cache: cache,
			Concurrency: scheduler.NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{39001: true, 39002: true}}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
				Event: logging.Event}),
		},
	}, newSchedulerTestOpenAIWSV2Config())

	ctx := guardianAffinityTestContext(t, clientmeta.CodexAutoReviewModel, "guardian", parentID, "")
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)

	selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "child-session", clientmeta.CodexAutoReviewModel, nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(39001), selection.Account.Record.ID)
	require.Equal(t, openAIAccountScheduleLayerGuardianParent, decision.Layer)
	require.Zero(t, cache.deletedSessions["openai:"+parentHash])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	// 子请求不能用自己的结果覆盖父线程绑定。
	require.NoError(t, svc.BindStickySession(ctx, &groupID, parentHash, 39002))
	require.Equal(t, int64(39001), cache.sessionBindings["openai:"+parentHash])
}
