package messageforward

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// Dependencies 固定连接原生拥有者，不接受旧网关、配置聚合或 HTTP Context。
// Prepare 与执行共用这些实例，构造不启动任务或提前读取动态设置。
type Dependencies struct {
	Credentials  *account.MessageCredentialSource
	Fingerprint  *anthropic.RequestFingerprint
	Transport    httpclient.UpstreamTransport
	Health       *accountprovider.UpstreamHealth
	TLS          *egressprovider.TLSProfiles
	Settings     *gateway.RuntimeSettings
	Prices       *billing.PriceResolver
	Search       *searchtools.Emulator
	Enter        func() (func(), error)
	Debug        DebugObserver
	AccountState AccountState
	Deferred     *account.DeferredService
	Channels     BedrockChannels
}

// BedrockChannels 在原准备位置读取渠道开关，继续共享 routing 的缓存。
type BedrockChannels interface {
	GetChannelForGroup(context.Context, int64) (*routing.Channel, error)
}

// AccountState 只提供本条执行链原有的持久停调动作，不开放账号配置或资金写入。
type AccountState interface {
	SetTempUnschedulable(context.Context, int64, time.Time, string) error
}

// DebugObserver 只接收本次请求的调试快照；文件及日志资源由观察实现持有。
type DebugObserver interface {
	Snapshot(string, http.Header, []byte, map[string]string)
	Capture(*http.Request, []byte, *provider.ExecutionAccount, string, bool, bool) string
}

// Runtime 只持有 Messages 单次执行的固定依赖；状态、凭据及响应属于各次尝试。
type Runtime struct {
	dependencies Dependencies
	options      Options
}

func NewRuntime(dependencies Dependencies, options Options) *Runtime {
	options.URLValidation.AllowedHosts = slices.Clone(options.URLValidation.AllowedHosts)
	return &Runtime{dependencies: dependencies, options: options}
}

// checkBeta 在普通 Messages 的既有准备位置读取并保存结果，空集也代表已查询。
func (r *Runtime) checkBeta(ctx context.Context, state *AttemptState, target *provider.ExecutionAccount, header, model string) error {
	result := r.evaluateBeta(ctx, target, header, model)
	if result.BlockErr != nil {
		return result.BlockErr
	}
	state.BetaEvaluated = true
	state.BetaFilters = result.FilterSet
	if state.BetaFilters == nil {
		state.BetaFilters = map[string]struct{}{}
	}
	return nil
}

// betaFilters 保留计数入口按需读取的行为，不把一次未缓存查询变成请求级缓存。
func (r *Runtime) betaFilters(ctx context.Context, state *AttemptState, target *provider.ExecutionAccount, model string) map[string]struct{} {
	if state.BetaEvaluated {
		return state.BetaFilters
	}
	return r.evaluateBeta(ctx, target, "", model).FilterSet
}

func (r *Runtime) evaluateBeta(ctx context.Context, target *provider.ExecutionAccount, header, model string) anthropic.BetaPolicyResult {
	if r.dependencies.Settings == nil {
		return anthropic.BetaPolicyResult{}
	}
	settings, err := r.dependencies.Settings.GetBetaPolicySettings(ctx)
	if err != nil || settings == nil {
		return anthropic.BetaPolicyResult{}
	}
	return anthropic.EvaluateBetaPolicy(provider.AnthropicBetaPolicy(settings), header, target.View().IsOAuth(), target.View().IsBedrock(), model)
}

// validateBaseURL 保留格式检查与受允许列表约束两条路径及原错误前缀。
func (r *Runtime) validateBaseURL(raw string) (string, error) {
	var normalized string
	var err error
	if r.options.Configured && !r.options.URLAllowlistEnabled {
		normalized, err = egress.ValidateURLFormat(raw, r.options.AllowInsecureHTTP)
	} else {
		policy := r.options.URLValidation
		policy.RequireAllowlist = true
		normalized, err = egress.ValidateHTTPSURL(raw, policy)
	}
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return normalized, nil
}
