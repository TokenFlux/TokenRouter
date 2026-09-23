//go:build unit

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type countingGatewaySchedulerCache struct {
	*fakeSchedulerCache
	snapshotCalls atomic.Int64
}

func (c *countingGatewaySchedulerCache) GetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket) ([]scheduler.SnapshotAccount, bool, error) {
	c.snapshotCalls.Add(1)
	return c.fakeSchedulerCache.GetSnapshot(ctx, bucket)
}

func TestGatewayHandlerPreCancelledCompatibleRequestsDoNotSelectAccount(t *testing.T) {

	groupID := int64(9100)
	group := &routing.Group{ID: groupID, Hydrated: true, Platform: capability.PlatformAnthropic, Status: billing.StatusActive}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9101, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey,
		Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
		AccountGroups: []accountcore.GroupMembership{{AccountID: 9101, GroupID: groupID}}},
	}
	schedulerCache := &countingGatewaySchedulerCache{fakeSchedulerCache: &fakeSchedulerCache{accounts: []*gatewayprovider.ExecutionAccount{account}}}
	schedulerSnapshot := scheduler.NewSnapshotService(schedulerCache, nil, nil, nil, nil, scheduler.SnapshotBindings{})
	gatewayService := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, nil, nil, nil,
		schedulerSnapshot, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, responseHeaderFilterForTest(nil),
	)
	gatewayService.BindCompletionRecorder(newHTTPCompletionFixture(nil, nil,
		nil, nil, nil, nil, nil, false))

	cfg := &config.Config{RunMode: config.RunModeSimple}
	billingCacheService := newBillingEligibilityFixture(cfg)
	billingCacheService.Start()
	t.Cleanup(billingCacheService.Stop)
	h := &GatewayHandler{
		gatewayService:      gatewayService,
		billingCacheService: newFundingAdmissionFixture(billingCacheService, cfg),
		concurrencyHelper: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(&fakeConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
			Event: logging.Event,
		},
		), gatewayhttp.SSEPingFormatClaude, 0),
		maxAccountSwitches: 1,
		cfg:                cfg,
	}
	apiKey := &apikey.APIKey{
		ID: 9102, UserID: 9103, GroupID: &groupID, Group: group, Status: billing.StatusActive,
		User: &identity.User{ID: 9103, Concurrency: 10, Balance: 100},
	}

	tests := []struct {
		name string
		path string
		body string
		call func(*gin.Context)
	}{
		{
			name: "responses", path: "/v1/responses", body: `{"model":"claude-test","input":"hello","stream":false}`,
			call: h.Responses,
		},
		{
			name: "chat completions", path: "/v1/chat/completions", body: `{"model":"claude-test","messages":[{"role":"user","content":"hello"}],"stream":false}`,
			call: h.ChatCompletions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schedulerCache.snapshotCalls.Store(0)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			ctx = requeststate.WithGroup(ctx, group)
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			c.Request = req
			c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

			tt.call(c)

			require.Zero(t, schedulerCache.snapshotCalls.Load(), "a cancelled request must stop before the account selector")
			_, selected := c.Get(gatewayhttp.OpsAccountIDKey)
			require.False(t, selected)
		})
	}
}
