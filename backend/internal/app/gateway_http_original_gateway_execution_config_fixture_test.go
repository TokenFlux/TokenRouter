package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/textattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	openaiwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// 剩余转发测试的构造夹具；不再用于模型目录 HTTP。
type gatewayExecutionAccountRows struct {
	gatewayprovider.ExecutionAccountStore

	byGroup map[int64][]gatewayprovider.ExecutionAccount
}

type gatewayExecutionPricingConfigRows struct {
	routing.PricingConfigRepository

	modelConfigs   []testkit.Configuration
	groupPlatforms map[int64]string
}

func (s *gatewayExecutionPricingConfigRows) ListAll(ctx context.Context) ([]testkit.Configuration, error) {
	modelConfigs := make([]testkit.Configuration, len(s.modelConfigs))
	copy(modelConfigs, s.modelConfigs)
	return modelConfigs, nil
}

func (s *gatewayExecutionPricingConfigRows) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
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
	return newGatewayExecutionHandlerWithPricingConfigForTest(repo, nil)
}

func newGatewayExecutionHandlerWithPricingConfigForTest(repo gatewayprovider.ExecutionAccountStore, pricingConfigService *routing.PricingConfigService) *messageEndpointsFixture {
	source, choices, messages := newGenericExecutionAndSelectionFixture(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, pricingConfigService, nil, responseHeaderFilterForTest(nil))
	return newMessageEndpointsFixture(source, messages, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, newExecutionAvailabilityForTest(repo, pricingConfigService, nil), choices)
}

func newGatewayExecutionPricingConfigServiceForTest(groupID int64, platform string, pricingConfig testkit.Configuration) *routing.PricingConfigService {
	pricingConfig.GroupIDs = []int64{groupID}
	repo := &gatewayExecutionPricingConfigRows{
		modelConfigs:   []testkit.Configuration{pricingConfig},
		groupPlatforms: map[int64]string{groupID: platform},
	}
	return testkit.NewPricingConfigService(repo, nil, routing.PricingConfigOptions{
		Warn: slog.Warn,
		Now:  time.Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	},
	)
}

// messageEndpointsFixture 只保存原生处理函数，不复制旧 Handler/Service 的实现或状态。
type messageEndpointsFixture struct {
	Messages                     gin.HandlerFunc
	Responses                    gin.HandlerFunc
	ChatCompletions              gin.HandlerFunc
	prepareGatewayAttemptRequest func(context.Context, *requeststate.ParsedRequest, []byte, *apikey.APIKey, string) (*requeststate.ParsedRequest, routing.GroupMappingResult, error)
}

// newMessageEndpointsFixture 将被验证的真实单次能力接入原生运行时；观测使用无状态替身。
func newMessageEndpointsFixture(source *messageExecutionFixture, messages *gatewayhttp.MessagesExecutor, funding *admission.FundingAdmission, concurrency *gatewayhttp.ConcurrencyHelper, options gatewayhttp.MessagesHTTPOptions, availability *gatewayModelAvailability, choices *selection.Generic) *messageEndpointsFixture {
	plan := func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
		var group *routing.Group
		var id *int64
		if key != nil {
			group, id = key.Group, key.GroupID
		}
		return source.Routes.PlanRoute(ctx, group, id, model)
	}
	b := textattempt.Bindings{
		PlanRoute: plan, Concurrency: concurrency,
		Submission: gatewayhttp.NewCompletionSubmission(nil, false),
	}
	if availability != nil {
		b.Diagnoser = availability.Messages
	}
	if source != nil {
		b.Recorder = source.Recorder
		b.Selection = textattempt.SelectionPorts{
			SelectAccount:      choices.SelectAccountWithLoadAwareness,
			TrackSession:       choices.TrackSessionAttempt,
			NewSessionAttempts: choices.NewSessionAttempts,
			SingleAccountGroup: choices.IsSingleAntigravityAccountGroup,
			ReportSchedule:     choices.ReportAdvancedAccountScheduleResult,
			IncrementRPM:       choices.IncrementAccountRPM,
			BindSticky:         choices.BindStickySession,
			ResolveGroup:       choices.ResolveGroupByID,
			AccountSwitched:    choices.RecordAdvancedAccountSwitch,
			TempUnschedule:     messageRetryCooldown(source.Cooldown),
		}
		b.Forward = textattempt.ForwardPorts{
			SaveGeminiSession: messageDigestSave(source.Digest),
			ReplaceModel:      openaiwire.ReplaceModelInBody,
		}
	}
	if messages != nil {
		b.Forward.BedrockCompat = messages.ApplyBedrockCCCompat
		b.Forward.ForwardMessages = messages.Forward
		b.Forward.ForwardResponses = messages.ForwardAsResponses
		b.Forward.ForwardChat = messages.ForwardAsChatCompletions
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
		bindings.IsolateSession = messageSessionIsolation(source.Cache)
		bindings.CachedSession = choices.GetCachedSessionAccountID
	}
	runtime := textattempt.New(b)
	var prompts *promptpolicy.Service
	messageHandler := gatewayhttp.NewBoundMessagesHandler(options, bindings, prompts, concurrency, textflow.NewMessagesExecutor(runtime, textflow.MessageOptions{MaxSwitches: options.MaxSwitches, CompletePartialFailure: true, Observe: telemetry.Failover}, textflow.MessageOptions{MaxSwitches: options.MaxGeminiSwitches, Observe: telemetry.Failover}))
	compatible := gatewayhttp.NewBoundCompatibleTextHandler(options, bindings, b.Forward.ReplaceModel, prompts, concurrency, textflow.NewMessagesExecutor(runtime, textflow.MessageOptions{MaxSwitches: options.MaxSwitches, StopOnCanceledContext: true, Observe: telemetry.Failover}, textflow.MessageOptions{MaxSwitches: options.MaxGeminiSwitches, StopOnCanceledContext: true, Observe: telemetry.Failover}))
	return &messageEndpointsFixture{
		Messages: messageHandler.Messages, Responses: compatible.Responses, ChatCompletions: compatible.ChatCompletions,
		prepareGatewayAttemptRequest: func(ctx context.Context, parsed *requeststate.ParsedRequest, body []byte, key *apikey.APIKey, model string) (*requeststate.ParsedRequest, routing.GroupMappingResult, error) {
			return gatewayhttp.PrepareGroupAttempt(ctx, parsed, body, key, model, plan)
		},
	}
}
