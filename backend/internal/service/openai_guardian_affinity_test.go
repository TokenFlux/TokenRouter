package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

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
	c.Request.Header.Set(openAISubagentHeader, subagent)
	if parentHeader != "" {
		c.Request.Header.Set(codexParentThreadIDHeader, parentHeader)
	}
	if metadata != "" {
		c.Request.Header.Set(codexTurnMetadataHeader, metadata)
	}
	return WithOpenAIGuardianParentAffinity(context.Background(), c, nil, model)
}

func TestWithOpenAIGuardianParentAffinityRequiresUnambiguousReviewLineage(t *testing.T) {
	parentID := "11111111-1111-4111-8111-111111111111"
	wantHash := DeriveSessionHashFromSeed(parentID)

	for _, subagent := range []string{"guardian", "review", "GUARDIAN"} {
		t.Run(subagent, func(t *testing.T) {
			ctx := guardianAffinityTestContext(t, codexAutoReviewModel, subagent, parentID, `{"parent_thread_id":"`+parentID+`"}`)
			affinity, ok := openAIGuardianParentAffinityFromContext(ctx)
			require.True(t, ok)
			require.Equal(t, wantHash, affinity.currentSessionHash)
		})
	}

	t.Run("metadata only", func(t *testing.T) {
		ctx := guardianAffinityTestContext(t, codexAutoReviewModel, "guardian", "", `{"parent_thread_id":"`+parentID+`"}`)
		_, ok := openAIGuardianParentAffinityFromContext(ctx)
		require.True(t, ok)
	})

	t.Run("websocket envelope metadata", func(t *testing.T) {

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
		body := []byte(`{"type":"response.create","response":{"model":"codex-auto-review","client_metadata":{"x-codex-turn-metadata":"{\"parent_thread_id\":\"` + parentID + `\",\"subagent_kind\":\"guardian\"}"}}}`)
		ctx := WithOpenAIGuardianParentAffinity(context.Background(), c, body, codexAutoReviewModel)
		affinity, ok := openAIGuardianParentAffinityFromContext(ctx)
		require.True(t, ok)
		require.Equal(t, wantHash, affinity.currentSessionHash)
	})

	for name, ctx := range map[string]context.Context{
		"ordinary model":       guardianAffinityTestContext(t, "gpt-5.6-sol", "guardian", parentID, ""),
		"ordinary subagent":    guardianAffinityTestContext(t, codexAutoReviewModel, "collab_spawn", parentID, ""),
		"missing parent":       guardianAffinityTestContext(t, codexAutoReviewModel, "guardian", "", ""),
		"conflicting lineage":  guardianAffinityTestContext(t, codexAutoReviewModel, "guardian", parentID, `{"parent_thread_id":"different-parent"}`),
		"conflicting subagent": guardianAffinityTestContext(t, codexAutoReviewModel, "guardian", parentID, `{"parent_thread_id":"`+parentID+`","subagent_kind":"collab_spawn"}`),
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := openAIGuardianParentAffinityFromContext(ctx)
			require.False(t, ok)
		})
	}
}

func TestOpenAIAccountSchedulerGuardianAffinitySelectsParent(t *testing.T) {
	parentID := "22222222-2222-4222-8222-222222222222"
	parentHash := DeriveSessionHashFromSeed(parentID)
	groupID := int64(102001)
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 39001, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 10, GroupIDs: []int64{groupID}, Credentials: map[string]any{"access_token": "parent", "plan_type": "team"}}},
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 39002, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}, Credentials: map[string]any{"access_token": "fallback", "plan_type": "team"}}},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:" + parentHash: 39001}, deletedSessions: map[string]int{}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		cache:       cache,
		cfg:         newSchedulerTestOpenAIWSV2Config(),
		concurrencyService: scheduler.NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{39001: true, 39002: true}}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
			Event: logging.Event},
		),
	}))
	ctx := guardianAffinityTestContext(t, codexAutoReviewModel, "guardian", parentID, "")
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)

	selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "child-session", codexAutoReviewModel, nil, egress.OpenAIUpstreamTransportAny, false)
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
