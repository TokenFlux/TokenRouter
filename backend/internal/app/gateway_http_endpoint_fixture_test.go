package app

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/mediaentry"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/wsentry"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// gatewayHTTPFixtureInput 只描述测试提供的原生依赖和显式预算，不持有业务规则。
type gatewayHTTPFixtureInput struct {
	Credentials  *gatewayhttp.RequestCredentialExecutor
	Availability *gatewayModelAvailability
	Choices      *selection.Compatible
	Source       *service.OpenAIGatewayService
	Funding      *admission.FundingAdmission
	Keys         *apikey.APIKeyService
	Worker       *completion.UsageRecordWorkerPool
	Rules        *errorpolicy.ErrorPassthroughService
	Moderator    *moderation.ContentModerationService
	Ops          *ops.OpsService
	Queue        gatewayhttp.OpsErrorLogQueue
	Config       *config.Config
	Prompts      *promptpolicy.Service
	Concurrency  *gatewayhttp.ConcurrencyHelper
	Images       *scheduler.ImageConcurrencyLimiter
	MaxSwitches  int
	Recorder     *completion.Recorder
}

// gatewayHTTPEndpointsFixture 仅保存函数句柄；执行、输出、计费和资源状态均使用原生实现。
type gatewayHTTPEndpointsFixture struct {
	Input                              *gatewayHTTPFixtureInput
	Responses                          gin.HandlerFunc
	Messages                           gin.HandlerFunc
	ChatCompletions                    gin.HandlerFunc
	ResponsesWebSocket                 gin.HandlerFunc
	Images                             gin.HandlerFunc
	GrokVideoGeneration                gin.HandlerFunc
	GrokVideoStatus                    gin.HandlerFunc
	httpResources                      func() *gatewayhttp.OpenAIHTTPResources
	openAIAttemptSupport               func() *openaiattempt.Support
	rejectIfCyberSessionBlocked        func(*gin.Context, *apikey.APIKey, []byte, string, gatewayhttp.CyberBlockFormat) bool
	enqueueCyberSessionBlockedOpsEntry func(*gin.Context, *apikey.APIKey, string, string)
}

type fixtureCyberTasks struct{ source *service.OpenAIGatewayService }

func (t fixtureCyberTasks) Go(name string, fn func()) bool {
	return t.source.RunBackgroundTask(name, fn)
}

type fixtureCyberOps struct {
	service *ops.OpsService
	queue   gatewayhttp.OpsErrorLogQueue
}

func (w fixtureCyberOps) Enqueue(value *ops.OpsInsertErrorLogInput) {
	w.queue.Enqueue(w.service, value)
}

// newGatewayHTTPEndpoints 在调用前投影测试可变输入，复用真实 app 绑定函数；资源指针始终相同。
func newGatewayHTTPEndpoints(input gatewayHTTPFixtureInput) *gatewayHTTPEndpointsFixture {
	f := &gatewayHTTPEndpointsFixture{Input: &input}
	resources := func() *gatewayhttp.OpenAIHTTPResources {
		return &gatewayhttp.OpenAIHTTPResources{Concurrency: input.Concurrency, Images: input.Images, ImageOptions: openAIImageAdmissionOptions(input.Config)}
	}
	base := func() (openaiattempt.Bindings, *gatewayhttp.CyberHandler, *session.CyberBlocks) {
		recorder := input.Recorder
		var blocks *session.CyberBlocks
		if input.Source != nil {
			if recorder == nil {
				recorder = input.Source.CompletionRecorder()
			}
			blocks = input.Source.CyberBlocks()
		}
		var moderator gatewayhttp.ModerationPort
		if input.Moderator != nil {
			moderator = input.Moderator
		}
		runtime := moderationflow.Runtime{Recorder: recorder, Blocks: blocks, Tasks: fixtureCyberTasks{input.Source}}
		if input.Ops != nil && input.Queue != nil {
			runtime.Ops = fixtureCyberOps{input.Ops, input.Queue}
		}
		cyber := gatewayhttp.NewBoundCyberHandler(blocks, moderator, runtime)
		bindings := provideOpenAIAttemptBindings(input.Source, input.Keys, resources(), cyber, input.Rules, input.Moderator, GatewayCompletionRecorders{OpenAI: recorder}, input.Worker, input.Availability, input.Choices)
		return bindings, cyber, blocks
	}
	text := func() *gatewayhttp.OpenAITextHandler {
		common, cyber, _ := base()
		options := openAITextOptions(input.Config)
		options.MaxSwitches = input.MaxSwitches
		bindings := openAITextBindings(input.Source, input.Funding, input.Keys, resources(), cyber, input.Rules, input.Moderator)
		executor := textflow.NewResponsesExecutor(provideOpenAITextAttemptRuntime(common), textflow.ResponseOptions{MaxSwitches: input.MaxSwitches}, textflow.ResponseOptions{MaxSwitches: input.MaxSwitches, FirstOutputBudget: true})
		return gatewayhttp.NewBoundOpenAITextHandler(options, bindings, input.Prompts, executor)
	}
	ws := func() *gatewayhttp.ResponsesWSHandler {
		common, _, blocks := base()
		options := responsesWSOptions(input.Config)
		options.MaxAccountSwitches = input.MaxSwitches
		return wsentry.New(options, responsesWSBindings(input.Source, input.Credentials, input.Funding, input.Keys, common, input.Prompts, blocks))
	}
	media := func() *mediaentry.Runtime {
		common, _, _ := base()
		bindings := mediaBindings(input.Source, input.Credentials, input.Keys, input.Funding, common, resources(), nil, input.Config, input.Source.Grok, provideGrokVideoTasks(nil, input.Config))
		bindings.Options.MaxSwitches = input.MaxSwitches
		bindings.EligibilityProber = nil
		return mediaentry.New(bindings)
	}
	f.Responses = func(c *gin.Context) { text().Responses(c) }
	f.Messages = func(c *gin.Context) { text().Messages(c) }
	f.ChatCompletions = func(c *gin.Context) { text().ChatCompletions(c) }
	f.ResponsesWebSocket = func(c *gin.Context) { ws().ResponsesWebSocket(c) }
	f.Images = func(c *gin.Context) { media().MediaHTTPHandler().Images(c) }
	f.GrokVideoGeneration = func(c *gin.Context) { media().MediaHTTPHandler().GrokVideoGeneration(c) }
	f.GrokVideoStatus = func(c *gin.Context) { media().MediaHTTPHandler().GrokVideoStatus(c) }
	f.httpResources = resources
	f.openAIAttemptSupport = func() *openaiattempt.Support { common, _, _ := base(); return common.Support }
	f.rejectIfCyberSessionBlocked = func(c *gin.Context, key *apikey.APIKey, body []byte, model string, format gatewayhttp.CyberBlockFormat) bool {
		_, cyber, _ := base()
		return cyber.RejectSession(c, apikey.CopyAPIKey(key), body, model, format)
	}
	f.enqueueCyberSessionBlockedOpsEntry = func(c *gin.Context, key *apikey.APIKey, model, block string) {
		_, cyber, _ := base()
		cyber.EnqueueBlocked(c, apikey.CopyAPIKey(key), model, block)
	}
	return f
}

// newGatewayHTTPEndpointsFromDeps 保留原测试参数输入，共享资源由真实 app provider 构造。
func newGatewayHTTPEndpointsFromDeps(source *service.OpenAIGatewayService, credentials *gatewayhttp.RequestCredentialExecutor, concurrency *scheduler.ConcurrencyService, funding *admission.FundingAdmission, keys *apikey.APIKeyService, worker *completion.UsageRecordWorkerPool, rules *errorpolicy.ErrorPassthroughService, moderator *moderation.ContentModerationService, opsService *ops.OpsService, cfg *config.Config, prompts *promptpolicy.Service, availability *gatewayModelAvailability, choices *selection.Compatible, provided ...*gatewayhttp.OpenAIHTTPResources) *gatewayHTTPEndpointsFixture {
	var resources *gatewayhttp.OpenAIHTTPResources
	if len(provided) > 0 {
		resources = provided[0]
	}
	if resources == nil {
		resources = provideOpenAIHTTPResources(concurrency, cfg)
	}
	return newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Source: source, Credentials: credentials, Availability: availability, Choices: choices, Funding: funding, Keys: keys, Worker: worker, Rules: rules, Moderator: moderator, Ops: opsService, Config: cfg, Prompts: prompts, Concurrency: resources.Concurrency, Images: resources.Images, MaxSwitches: openAITextOptions(cfg).MaxSwitches})
}

// 类型断言约束原测试后台端口，不增加第二套生命周期或队列。
var _ moderationflow.Tasks = fixtureCyberTasks{}
