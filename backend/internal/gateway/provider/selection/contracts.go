// Package selection 将执行账号投影和平台资格接入 scheduler 的唯一选择算法。
// 原生核心只取得无凭据候选；完整执行目标保持在本次选择的适配作用域内。
package selection

import (
	"context"
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

// Accounts 限定原候选查询和完整目标读取，不向选择器开放凭据刷新、CRUD 或资金写入。
type Accounts interface {
	GetByID(context.Context, int64) (*provider.ExecutionAccount, error)
	ListSchedulableByPlatform(context.Context, string) ([]provider.ExecutionAccount, error)
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]provider.ExecutionAccount, error)
	ListSchedulableUngroupedByPlatform(context.Context, string) ([]provider.ExecutionAccount, error)
	ListSchedulableByPlatforms(context.Context, []string) ([]provider.ExecutionAccount, error)
	ListSchedulableByGroupIDAndPlatforms(context.Context, int64, []string) ([]provider.ExecutionAccount, error)
	ListSchedulableUngroupedByPlatforms(context.Context, []string) ([]provider.ExecutionAccount, error)
}

// Groups 保留轻量分组与完整设置的不同读取时点。
type Groups interface {
	GetByID(context.Context, int64) (*routing.Group, error)
	GetByIDLite(context.Context, int64) (*routing.Group, error)
}

// Snapshots 是受控读取端口；具体缓存解码仍由所属 Adapter 提供。
type Snapshots interface {
	GetAccount(context.Context, int64) (*account.Record, error)
	ListAccounts(context.Context, *int64, string, bool) ([]account.Record, bool, error)
}

// Reads 只持有现有数据来源，不创建仓储、缓存或回源预算。
type Reads struct {
	Accounts Accounts
	Groups   Groups
	Snapshot Snapshots
}

// Shared 接收 app 已构造的原生拥有者；普通和高级选择不得复制反馈、计数或共享价格配置缓存。
type Shared struct {
	Cache         schedulercore.StickyCache
	Concurrency   *schedulercore.ConcurrencyService
	Health        *accountprovider.UpstreamHealth
	GroupPolicies *routing.PricingConfigService
	Parameters    *schedulercore.Parameters
	Feedback      *schedulercore.RuntimeStats
}

// GenericDependencies 保留窗口费用、RPM 与会话各自的作用域。
type GenericDependencies struct {
	Reads
	Shared
	Window                  *billing.WindowCostGuard
	WindowPrefetchAvailable bool
	RPM                     schedulercore.RPMCache
	Sessions                schedulercore.SessionLimitCache
	FreeQuota               *account.FreeQuotaGate
	SetAccountError         func(context.Context, int64, string) error
}

// CompatibleDependencies 只借用同一响应归属、健康状态和配额观测，不能执行供应商交换。
type CompatibleDependencies struct {
	Reads
	Shared
	Responses            session.OpenAIWSStateStore
	QuotaSettings        *account.QuotaSettingsCache
	RuntimeBlocks        *account.RuntimeBlockState
	ModelTransient       *account.ModelTransientState
	ProxyCircuit         *egress.ProxyStreamCircuit
	StickyStats          *schedulercore.StickyStats
	FreeQuota            *account.FreeQuotaGate
	NewAdvancedFreeQuota func() *account.FreeQuotaGate
}

// GeminiDependencies 复用既有配额批量预检，不合并普通与混合池的选择顺序。
type GeminiDependencies struct {
	Reads
	Shared
	QuotaPrecheck *account.GeminiPrecheck
}

// Options 是启动配置的显式投影；零值与配置缺省由 app 区分。
type Options struct {
	Simple            bool
	Scheduling        schedulercore.FlowOptions
	DebugRouting      bool
	StickyTTL         time.Duration
	ResponseTTL       time.Duration
	ReadLegacySticky  bool
	WriteLegacySticky bool
	WS                *egress.OpenAIWSOptions
	WSIngressMode     string
}

// DefaultOptions 对应原未提供进程配置的缺省值，不覆写已配置的显式零值。
func DefaultOptions() Options {
	return Options{
		Scheduling: schedulercore.FlowOptions{StickySessionMaxWaiting: 3, StickySessionWaitTimeout: 45 * time.Second, FallbackWaitTimeout: 30 * time.Second, FallbackMaxWaiting: 100, LoadBatchEnabled: true},

		StickyTTL:         time.Hour,
		ResponseTTL:       time.Hour,
		ReadLegacySticky:  true,
		WriteLegacySticky: true,
	}
}
