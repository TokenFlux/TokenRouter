package selection

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// Generic 只组合通用选择所需的读取、资格和资源；尝试切换与平台转发不属于本对象。
type Generic struct {
	options                 Options
	accountRepo             Accounts
	groupRepo               Groups
	schedulerSnapshot       Snapshots
	cache                   schedulercore.StickyCache
	concurrencyService      *schedulercore.ConcurrencyService
	healthObserver          *accountprovider.UpstreamHealth
	channelService          *routing.ChannelService
	schedulerParameters     *schedulercore.Parameters
	advancedAccountStats    *schedulercore.RuntimeStats
	freeQuotaGate           *account.FreeQuotaGate
	window                  *billing.WindowCostGuard
	windowPrefetchAvailable bool
	rpmCache                schedulercore.RPMCache
	sessionLimitCache       schedulercore.SessionLimitCache
	setAccountError         func(context.Context, int64, string) error
}

// NewGeneric 绑定唯一状态拥有者，执行凭据只留在单次选择的适配作用域。
// @project-doc docs/architecture/account_scheduling_and_cache.md#advanced_scheduler_selection
func NewGeneric(deps GenericDependencies, options Options) *Generic {
	if deps.Window == nil {
		deps.Window = defaultWindowCostGuard()
	}
	return &Generic{
		options:           options,
		accountRepo:       deps.Accounts,
		groupRepo:         deps.Groups,
		schedulerSnapshot: deps.Snapshot,
		cache:             deps.Cache,

		concurrencyService:  deps.Concurrency,
		healthObserver:      deps.Health,
		channelService:      deps.Channels,
		schedulerParameters: deps.Parameters,

		advancedAccountStats:    deps.Feedback,
		freeQuotaGate:           deps.FreeQuota,
		window:                  deps.Window,
		windowPrefetchAvailable: deps.WindowPrefetchAvailable,

		rpmCache:          deps.RPM,
		sessionLimitCache: deps.Sessions,
		setAccountError:   deps.SetAccountError,
	}
}

// Compatible 在同一原生调度器上连接 OpenAI/Grok 资格，借用共享状态而不拥有供应商执行。
type Compatible struct {
	options             Options
	accountRepo         Accounts
	schedulerSnapshot   Snapshots
	schedulingGroups    func(context.Context, int64) (*routing.Group, error)
	cache               schedulercore.StickyCache
	concurrencyService  *schedulercore.ConcurrencyService
	healthObserver      *accountprovider.UpstreamHealth
	channelService      *routing.ChannelService
	schedulerParameters *schedulercore.Parameters
	openaiAccountStats  *schedulercore.RuntimeStats
	quotaSettings       *account.QuotaSettingsCache
	freeQuotaGate       *account.FreeQuotaGate
	newFreeQuotaGate    func() *account.FreeQuotaGate
	runtime             *account.RuntimeBlockState
	modelTransient      *account.ModelTransientState
	proxyCircuit        *egress.ProxyStreamCircuit
	proxyFailOpenLogAt  atomic.Int64
	responseState       session.OpenAIWSStateStore
	stickyMetrics       *schedulercore.StickyStats
	pickerOnce          sync.Once
	picker              pickerEngine
}

func NewCompatible(deps CompatibleDependencies, options Options) *Compatible {
	if deps.RuntimeBlocks == nil {
		deps.RuntimeBlocks = account.NewRuntimeBlockState(time.Now)
	}
	if deps.ModelTransient == nil {
		deps.ModelTransient = account.NewModelTransientState(0)
	}
	if deps.ProxyCircuit == nil {
		deps.ProxyCircuit = egress.NewProxyStreamCircuit(egress.DefaultProxyStreamCircuitSettings())
	}
	if deps.StickyStats == nil {
		deps.StickyStats = &schedulercore.StickyStats{}
	}
	var groups func(context.Context, int64) (*routing.Group, error)
	if deps.Groups != nil {
		groups = deps.Groups.GetByID
	}
	return &Compatible{
		options:           options,
		accountRepo:       deps.Accounts,
		schedulerSnapshot: deps.Snapshot,
		schedulingGroups:  groups,
		cache:             deps.Cache,

		concurrencyService:  deps.Concurrency,
		healthObserver:      deps.Health,
		channelService:      deps.Channels,
		schedulerParameters: deps.Parameters,

		openaiAccountStats: deps.Feedback,
		quotaSettings:      deps.QuotaSettings,
		freeQuotaGate:      deps.FreeQuota,
		newFreeQuotaGate:   deps.NewAdvancedFreeQuota,

		runtime:        deps.RuntimeBlocks,
		modelTransient: deps.ModelTransient,
		proxyCircuit:   deps.ProxyCircuit,
		responseState:  deps.Responses,
		stickyMetrics:  deps.StickyStats,
	}
}

// Gemini 仅持有 Gemini/混合池的无槽选择依赖，凭据和报文执行留在各自拥有者。
type Gemini struct {
	options              Options
	accountRepo          Accounts
	groupRepo            Groups
	schedulerSnapshot    Snapshots
	cache                schedulercore.StickyCache
	schedulerParameters  *schedulercore.Parameters
	advancedAccountStats *schedulercore.RuntimeStats
	quotaPrecheck        *account.GeminiPrecheck
}

func NewGemini(deps GeminiDependencies, options Options) *Gemini {
	return &Gemini{
		options:           options,
		accountRepo:       deps.Accounts,
		groupRepo:         deps.Groups,
		schedulerSnapshot: deps.Snapshot,
		cache:             deps.Cache,

		schedulerParameters:  deps.Parameters,
		advancedAccountStats: deps.Feedback,
		quotaPrecheck:        deps.QuotaPrecheck,
	}
}

// DiagnosticSource 只允许原诊断读取；安全 DTO 仍由 scheduler 核心产生。
type DiagnosticSource interface {
	GetAccount(context.Context, int64) (*provider.ExecutionAccount, error)
	GetGroup(context.Context, int64) (*routing.Group, error)
	ListAccountsForSchedulerScoreFilter(context.Context, string, string, string, string, int64, string) ([]provider.ExecutionAccount, error)
	ListSchedulableAccountsForAdvancedSchedulerScore(context.Context, *int64, string) ([]provider.ExecutionAccount, error)
}

// Diagnostics 与真实选择共用参数、反馈和资格实例，不抢槽或写入粘性。
type Diagnostics struct {
	source              DiagnosticSource
	concurrencyService  *schedulercore.ConcurrencyService
	feedback            *schedulercore.RuntimeStats
	schedulerParameters *schedulercore.Parameters
	gatewayService      *Generic
	openAIGateway       *Compatible
}

func NewDiagnostics(source DiagnosticSource, shared Shared, generic *Generic, compatible *Compatible) *Diagnostics {
	return &Diagnostics{
		source:              source,
		concurrencyService:  shared.Concurrency,
		feedback:            shared.Feedback,
		schedulerParameters: shared.Parameters,

		gatewayService: generic,
		openAIGateway:  compatible,
	}
}
