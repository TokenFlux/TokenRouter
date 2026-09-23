package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// countExecution 仅连接尚未清零的选择与计数执行原语，不拥有循环、缓存或规则。
type countExecution struct {
	*service.GatewayService
	choices *selection.Generic
}

func (p countExecution) SelectCountTarget(ctx context.Context, id *int64, hash, model string, excluded map[int64]struct{}) (gatewayhttp.CountTarget, error) {
	value, err := p.choices.SelectAccountForModelWithExclusions(ctx, id, hash, model, excluded)
	if err != nil {
		return nil, err
	}
	return countTarget{gateway: p.GatewayService, account: value, choices: p.choices}, nil
}
func (p countExecution) PlanCountRoute(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	var id *int64
	if key != nil {
		id = key.GroupID
	}
	return p.PlanRoute(ctx, service.APIKeyRouteGroup(key), id, model)
}

// countTarget 将已经取得的账号保持在受控调用内，不把凭据暴露给 HTTP。
type countTarget struct {
	choices *selection.Generic
	gateway *service.GatewayService
	account *gatewayprovider.ExecutionAccount
}

func (t countTarget) Snapshot() account.AccountSnapshot {
	return gatewayprovider.ExecutionSnapshot(t.account)
}
func (t countTarget) RetryLimit() int { return t.account.View().GetPoolModeRetryCount() }
func (t countTarget) ForwardCountTokens(ctx context.Context, c *gin.Context, parsed *requeststate.ParsedRequest) error {
	return t.gateway.ForwardCountTokens(ctx, c, t.account, parsed)
}
func (t countTarget) ReleaseSession(ctx context.Context, hash string) {
	t.choices.ReleaseAccountSession(ctx, t.account, hash)
}

// provideCountTokensHTTP 直接装配原生 HTTP，固定依赖不经旧 Handler 工厂。
func provideCountTokensHTTP(source *service.GatewayService, openAI *service.OpenAIGatewayService, funding *admission.FundingAdmission, rules *errorpolicy.ErrorPassthroughService, cfg *config.Config, activity *gatewayRequestActivity, prompts *promptpolicy.Service, availability *gatewayModelAvailability, choices *selection.Generic) *gatewayhttp.CountTokensHandler {
	limit := int64(0)
	switches := 10
	if cfg != nil {
		limit = cfg.Gateway.MaxBodySize
		if cfg.Gateway.MaxAccountSwitches > 0 {
			switches = cfg.Gateway.MaxAccountSwitches
		}
	}
	var matcher gatewayhttp.ErrorRuleMatcher
	if rules != nil {
		matcher = rules
	}
	ports := gatewayhttp.CountHTTPPorts{
		Executor: countExecution{source, choices}, Funding: funding, ReadAccess: keyhttp.GetAPIKeyFromContext,
		ObserveCompatibility: func(log *zap.Logger) {
			gatewayhttp.LogCompatibilityFallback(log, func() gatewayhttp.CompatibilityLogSnapshot {
				value := openAI.SnapshotOpenAICompatibilityFallbackMetrics()
				return gatewayhttp.CompatibilityLogSnapshot{ReadTotal: value.SessionHashLegacyReadFallbackTotal, ReadHit: value.SessionHashLegacyReadFallbackHit, DualWrite: value.SessionHashLegacyDualWriteTotal, ReadHitRate: value.SessionHashLegacyReadHitRate, MetadataTotal: value.MetadataLegacyFallbackTotal}
			})
		},
		BusinessError: func(c *gin.Context, err error, started bool, write func(int, string, string, bool)) bool {
			return gatewayhttp.WriteGroupSelectionBusinessError(c, err, started, keyhttp.GetAPIKeyFromContext, gatewayprovider.ModelDisplayCatalogue{}, write)
		},
		Failure: func(c *gin.Context, failure *forwardcore.UpstreamFailoverError, platform string, started bool) {
			gatewayhttp.WriteAnthropicFailover(c, failure, platform, started, matcher, forwardcore.IsOpenAISilentRefusalErrorBody, forwardcore.OpenAISilentRefusalClientMessage())
		},
	}
	if availability != nil {
		ports.Diagnoser = availability.Messages
	}
	result := gatewayhttp.NewCountTokensHandler(limit, switches, ports, prompts)
	if activity != nil {
		result.BindRequestActivity(activity.Enter)
	}
	return result
}
