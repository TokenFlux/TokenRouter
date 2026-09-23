package app

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/textattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"context"
	slog "log/slog"
	time "time"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/service"
)

// 剩余转发测试的构造夹具；不再用于模型目录 HTTP。
type gatewayExecutionAccountRows struct {
	gatewayprovider.ExecutionAccountStore

	byGroup map[int64][]gatewayprovider.ExecutionAccount
}

type gatewayExecutionChannelRows struct {
	routing.ChannelRepository

	channels       []routing.Channel
	groupPlatforms map[int64]string
}

func (s *gatewayExecutionChannelRows) ListAll(ctx context.Context) ([]routing.Channel, error) {
	channels := make([]routing.Channel, len(s.channels))
	copy(channels, s.channels)
	return channels, nil
}

func (s *gatewayExecutionChannelRows) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	platforms := make(map[int64]string, len(groupIDs))
	for _, groupID := range groupIDs {
		if platform, ok := s.groupPlatforms[groupID]; ok {
			platforms[groupID] = platform
		}
	}
	return platforms, nil
}

func (s *gatewayExecutionAccountRows) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]gatewayprovider.ExecutionAccount, error) {
	accounts, ok := s.byGroup[groupID]
	if !ok {
		return nil, nil
	}
	out := make([]gatewayprovider.ExecutionAccount, len(accounts))
	copy(out, accounts)
	return out, nil
}

func newGatewayExecutionHandlerForTest(repo gatewayprovider.ExecutionAccountStore) *messageEndpointsFixture {
	return newGatewayExecutionHandlerWithChannelForTest(repo, nil)
}

func newGatewayExecutionHandlerWithChannelForTest(repo gatewayprovider.ExecutionAccountStore, channelService *routing.ChannelService) *messageEndpointsFixture {
	return newMessageEndpointsFixture(service.NewGatewayService(
		repo,
		nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, channelService, nil, responseHeaderFilterForTest(nil),
	), nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0})
}

func newGatewayExecutionChannelServiceForTest(groupID int64, platform string, channel routing.Channel) *routing.ChannelService {
	channel.GroupIDs = []int64{groupID}
	repo := &gatewayExecutionChannelRows{
		channels:       []routing.Channel{channel},
		groupPlatforms: map[int64]string{groupID: platform},
	}
	return routing.NewChannelService(repo, nil, routing.ChannelOptions{Warn: slog.Warn,
		Now: time.Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	},
	)
}

// messageEndpointsFixture 只保存原生处理函数，不复制旧 Handler/Service 的实现或状态。
type messageEndpointsFixture struct {
	Messages                     gin.HandlerFunc
	Responses                    gin.HandlerFunc
	ChatCompletions              gin.HandlerFunc
	prepareGatewayAttemptRequest func(context.Context, *requeststate.ParsedRequest, []byte, *apikey.APIKey, string) (*requeststate.ParsedRequest, routing.ChannelMappingResult, error)
}

// newMessageEndpointsFixture 将被验证的真实单次能力接入原生运行时；观测使用无状态替身。
func newMessageEndpointsFixture(source *service.GatewayService, funding *admission.FundingAdmission, concurrency *gatewayhttp.ConcurrencyHelper, options gatewayhttp.MessagesHTTPOptions) *messageEndpointsFixture {
	plan := func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
		var group *routing.Group
		var id *int64
		if key != nil {
			group, id = key.Group, key.GroupID
		}
		return source.PlanRoute(ctx, group, id, model)
	}
	b := textattempt.Bindings{
		PlanRoute: plan, Diagnoser: source, Concurrency: concurrency,
		Submission: gatewayhttp.NewCompletionSubmission(nil, false),
	}
	if source != nil {
		b.Recorder = source.CompletionRecorder()
		b.Selection = textattempt.SelectionPorts{
			SelectAccount:      source.SelectAccountWithLoadAwareness,
			TrackSession:       source.TrackSessionAttempt,
			NewSessionAttempts: source.NewSessionAttempts,
			SingleAccountGroup: source.IsSingleAntigravityAccountGroup,
			ReportSchedule:     source.ReportAdvancedAccountScheduleResult,
			IncrementRPM:       source.IncrementAccountRPM,
			BindSticky:         source.BindStickySession,
			ResolveGroup:       source.ResolveGroupByID,
			AccountSwitched:    source.RecordAdvancedAccountSwitch,
			TempUnschedule:     source.TempUnscheduleRetryableError,
		}
		b.Forward = textattempt.ForwardPorts{
			BedrockCompat:     source.ApplyBedrockCCCompat,
			ForwardMessages:   source.Forward,
			ForwardResponses:  source.ForwardAsResponses,
			ForwardChat:       source.ForwardAsChatCompletions,
			SaveGeminiSession: source.SaveGeminiSession,
			ReplaceModel:      source.ReplaceModelInBody,
		}
	}
	if funding != nil {
		b.CheckFunding = funding.CheckKey
	}
	var settings *gateway.RuntimeSettings
	bindings := gatewayhttp.MessagesBindings{
		PlanRoute: plan, ClientVersions: settings.GetClaudeCodeVersionBounds, Funding: funding,
		ObserveCompatibility: func(log *zap.Logger) {
			gatewayhttp.LogCompatibilityFallback(log, func() gatewayhttp.CompatibilityLogSnapshot { return gatewayhttp.CompatibilityLogSnapshot{} })
		},
	}
	if source != nil {
		bindings.IsolateSession = source.EnsureSessionIsolation
		bindings.CachedSession = source.GetCachedSessionAccountID
	}
	runtime := textattempt.New(b)
	var prompts *promptpolicy.Service
	messages := gatewayhttp.NewBoundMessagesHandler(options, bindings, prompts, concurrency, textflow.NewMessagesExecutor(runtime, textflow.MessageOptions{MaxSwitches: options.MaxSwitches, CompletePartialFailure: true, Observe: telemetry.Failover}, textflow.MessageOptions{MaxSwitches: options.MaxGeminiSwitches, Observe: telemetry.Failover}))
	compatible := gatewayhttp.NewBoundCompatibleTextHandler(options, bindings, b.Forward.ReplaceModel, prompts, concurrency, textflow.NewMessagesExecutor(runtime, textflow.MessageOptions{MaxSwitches: options.MaxSwitches, StopOnCanceledContext: true, Observe: telemetry.Failover}, textflow.MessageOptions{MaxSwitches: options.MaxGeminiSwitches, StopOnCanceledContext: true, Observe: telemetry.Failover}))
	return &messageEndpointsFixture{
		Messages: messages.Messages, Responses: compatible.Responses, ChatCompletions: compatible.ChatCompletions,
		prepareGatewayAttemptRequest: func(ctx context.Context, parsed *requeststate.ParsedRequest, body []byte, key *apikey.APIKey, model string) (*requeststate.ParsedRequest, routing.ChannelMappingResult, error) {
			return gatewayhttp.PrepareChannelAttempt(ctx, parsed, body, key, model, plan)
		},
	}
}
