package mediaentry

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Options 保留原静态切号与非流图片心跳预算。
type Options struct {
	MaxSwitches    int
	ImageKeepalive time.Duration
}

// PlatformPorts 只执行一次已选账号的交换，循环与完成资格归 media。
type PlatformPorts struct {
	SelectImages  func(context.Context, *int64, string, string, map[int64]struct{}, account.OpenAIImagesCapability) (*provider.SelectionResult, scheduler.PlatformDecision, error)
	Images        func(context.Context, *gin.Context, *provider.ExecutionAccount, []byte, *media.ImageRequest, string, ...egress.TLSFingerprintRouterMatchResult) (*forward.OpenAIResult, error)
	GrokMedia     func(context.Context, *gin.Context, *provider.ExecutionAccount, grok.GrokMediaEndpoint, string, []byte, string) (*forward.OpenAIResult, error)
	Embeddings    func(context.Context, *gin.Context, *provider.ExecutionAccount, []byte, string) (*forward.OpenAIResult, error)
	AlphaSearch   func(context.Context, *gin.Context, *provider.ExecutionAccount, []byte, ...egress.TLSFingerprintRouterMatchResult) (*forward.OpenAIResult, error)
	Voice         func(context.Context, *gin.Context, *provider.ExecutionAccount, string, []byte, string) (*forward.OpenAIResult, error)
	OpenRealtime  func(context.Context, *provider.ExecutionAccount, string, string) (upstream.FrameConn, error)
	RealtimeError func(context.Context, *provider.ExecutionAccount, int, []byte)
	RelayRealtime func(context.Context, upstream.FrameConn, upstream.FrameConn) (bool, error)
	Credential    func(context.Context, *gin.Context, *provider.ExecutionAccount) (string, string, error)
	Stop429       func(*provider.ExecutionAccount, int, int, *failover.OAuth429State) bool
	ReportSwitch  func()
}

// Bindings 不读取配置或创建共享实例，按原入口保持可选额度端口的存在性。
type Bindings struct {
	Common            openaiattempt.Bindings
	Platform          PlatformPorts
	Resources         *gatewayhttp.OpenAIHTTPResources
	Dependencies      gatewayhttp.OpenAIDependencies
	Options           Options
	Quota             provider.QuotaUpdater
	CheckFunding      func(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error
	PlanRoute         func(context.Context, *apikey.APIKey, string) routing.RoutePlan
	Isolate           func(context.Context, *apikey.APIKey, int64, string, string) error
	VideoTasks        func() *media.VideoTasks
	EligibilityProber provider.GrokMediaEligibilityProber
}

// Runtime 仅持有构造期固定的端口，每次 HTTP 调用建立独立请求适配。
type Runtime struct{ bindings Bindings }

func New(b Bindings) *Runtime { return &Runtime{bindings: b} }
func (h *Runtime) submitOpenAIUsageRecordTask(c *gin.Context, result *forward.OpenAIResult, task completion.UsageRecordTask) {
	images := 0
	if result != nil {
		images = result.ImageCount
	}
	h.bindings.Common.Support.Submission.SubmitImages(c, images, task)
}
func (h *Runtime) checkContentModeration(c *gin.Context, log *zap.Logger, key *apikey.APIKey, subject authctx.AuthSubject, protocol, model string, body []byte) *moderation.Decision {
	return gatewayhttp.RunContentModeration(gatewayhttp.GatewayModerationEndpoints{}, c, log, h.bindings.Common.Support.Moderation, apikey.CopyAPIKey(key), subject, protocol, model, body)
}
func (h *Runtime) handleOpenAISessionIsolationError(c *gin.Context, err error, started bool) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, session.ErrSessionIsolationConflict) {
		gatewayhttp.DefaultOpenAIErrorOutput().StreamError(c, http.StatusForbidden, "permission_error", session.SessionIsolationConflictMessage, started)
		return true
	}
	gatewayhttp.DefaultOpenAIErrorOutput().StreamError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", started)
	return true
}
func (h *Runtime) ensureGrokMediaAccountEligibility(ctx context.Context, value *provider.ExecutionAccount) (bool, string, error) {
	var probe provider.GrokMediaEligibilityProber
	if h != nil {
		probe = h.bindings.EligibilityProber
	}
	return provider.CheckGrokMediaEligibility(ctx, value, probe)
}
