//go:build unit

package service

import (
	"context"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	scheduler "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayService_SelectAccountWithScheduler_UsesWSPassthroughSnapshotFlags(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10105)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 35001,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 10,
		GroupIDs:    []int64{groupID},
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_mode": accountcore.OpenAIWSIngressModePassthrough,
		}},
	}

	snapshotCache := &openAISnapshotCacheStub{
		snapshotAccounts: []*gatewayprovider.ExecutionAccount{account},
		accountsByID:     map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = accountcore.OpenAIWSIngressModeCtxPool

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    schedulerTestOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{*account}},
		cache:          &schedulerTestGatewayCache{},
		cfg:            cfg,
		healthObserver: newUpstreamHealthForTest(nil, nil, nil, accountcore.HealthOptions{}, nil),
		schedulerParameters: newAdvancedSchedulerParametersForTest(cfg,
			"true"),
		schedulerSnapshot: NewSchedulerSnapshotService(snapshotCache, nil, nil, nil, nil),
		concurrencyService: scheduler.NewConcurrencyService(schedulerTestConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
			Event: logging.Event,
		},
		),
	}))

	selection, decision, err := svc.SelectAccountWithScheduler(
		ctx,
		&groupID,
		"",
		"session_hash_ws_passthrough",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportResponsesWebsocketV2, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, account.Record.ID, selection.Account.Record.ID)
	require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
}
