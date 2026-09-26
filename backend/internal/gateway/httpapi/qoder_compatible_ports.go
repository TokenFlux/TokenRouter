package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// QoderCompatibleTarget 只允许执行、刷新和完成投影，不向 HTTP 暴露账号凭据。
type QoderCompatibleTarget interface {
	Snapshot() account.AccountSnapshot
	Forward(context.Context, *gin.Context, []byte, protocol.ProtocolID, string) (*forward.MessagesResult, error)
	Refresh(context.Context) (QoderCompatibleTarget, error)
	Completion(context.Context, QoderCompletionCapture) *completion.Input
}

// QoderCompatibleSelection 持有本次选择的槽位与反馈参数，不创建第二个选号循环。
type QoderCompatibleSelection interface {
	Target() QoderCompatibleTarget
	Acquired() bool
	ReleaseFunc() func()
	WaitPlan() *scheduler.AccountWaitPlan
	Report(int64, bool, *forward.MessagesResult)
	Switched()
}

// QoderCompatibleExecution 是固定的选择、路由和粘性端口。
type QoderCompatibleExecution interface {
	Select(context.Context, *int64, string, string, map[int64]struct{}, int64) (QoderCompatibleSelection, error)
	Plan(context.Context, *apikey.APIKey, string) routing.RoutePlan
	BindStickySession(context.Context, *int64, string, int64) error
}

// QoderCompletionCapture 在入队前取得原请求展示与资金参数，完成队列不持有 Gin。
type QoderCompletionCapture struct {
	Result                                                                             *forward.MessagesResult
	Key                                                                                *apikey.APIKey
	Subscription                                                                       *billing.UserSubscription
	QuotaPlatform, InboundEndpoint, UpstreamEndpoint, UserAgent, ClientIP, PayloadHash string
	Body                                                                               []byte
	Pricing                                                                            routing.PricingUsageFields
}

// QoderCompatibleOptions 注入唯一实例，不在 HTTP 构造时启动资源或复制缓存。
type QoderCompatibleOptions struct {
	Execution QoderCompatibleExecution
	Funding   interface {
		CheckKey(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error
	}
	Recorder interface {
		Record(context.Context, *completion.Input, bool) error
	}
	Pool              *completion.UsageRecordWorkerPool
	Slots             *ConcurrencyHelper
	Enter             func() (func(), error)
	ReadAccess        func(*gin.Context) (*apikey.APIKey, bool)
	Errors            QoderErrorPresenter
	Rules             *errorpolicy.ErrorPassthroughService
	PlatformAvailable bool
	MayRefresh        func(error) bool
	MaySwitch         func(error) bool
}

// QoderCompatibleRuntime 只拥有兼容入口的 HTTP 适配与完成输入冻结。
type QoderCompatibleRuntime struct {
	options           QoderCompatibleOptions
	concurrencyHelper *ConcurrencyHelper
}

func NewQoderCompatibleRuntime(options QoderCompatibleOptions) *QoderCompatibleRuntime {
	return &QoderCompatibleRuntime{options: options, concurrencyHelper: options.Slots}
}
