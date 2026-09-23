package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	s09wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

func (s *OpenAIGatewayService) validateUpstreamBaseURL(raw string) (string, error) {
	normalized, err := s.validateOutboundURL(raw)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return normalized, nil
}

// validateOutboundURL 按安全配置校验网关主动连接的 URL。
func (s *OpenAIGatewayService) validateOutboundURL(raw string) (string, error) {
	if s == nil || s.cfg == nil {
		return egress.ValidateURLFormat(raw, false)
	}
	if !s.cfg.Security.URLAllowlist.Enabled {
		return egress.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
	}
	return egress.ValidateHTTPSURL(raw, egress.ValidationOptions{
		AllowedHosts:     s.cfg.Security.URLAllowlist.UpstreamHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
}

func (s *OpenAIGatewayService) replaceModelInResponseBody(body []byte, fromModel, toModel string) []byte {
	return s09wire.ReplaceModelInResponseBody(body, fromModel, toModel)
}

// evaluateOpenAIFastPolicy 返回指定账号、模型和 service_tier 应执行的动作及错误消息。
// 策略服务不可用或没有规则命中时返回 pass，调用方可安全地直接放行。
//
// 匹配规则：
//   - Scope 按账号类型过滤（all / oauth / apikey / bedrock）
//   - UserIDs 非空时按 API Key 所属的可信用户 ID 过滤
//   - ServiceTier 必须为空、all 或等于归一化后的 tier
//   - ModelWhitelist 将规则限制到指定模型，FallbackAction 处理未匹配模型
//   - 用户专属规则优先于全局规则，两组内部均保持配置顺序并首条命中
//
// 与 Claude BetaPolicy 的差异（保留首条匹配 short-circuit）：
//   - BetaPolicy 处理的是 anthropic-beta header 中的 token 集合，不同
//     规则可能针对不同 token，filter 需要累加成 set；block 则 first-match。
//   - OpenAI fast policy 操作的是单个字段 service_tier：filter 即删字段，
//     没有可累加的对象。一次请求只携带一个 service_tier，规则的 tier
//     维度天然互斥；同一 (scope, tier) 下若多条规则的 model whitelist
//     发生重叠，admin 可通过规则顺序明确意图。因此采用 first-match 而
//     非 BetaPolicy 那样的"block 覆盖 filter 覆盖 pass"语义。
func (s *OpenAIGatewayService) evaluateOpenAIFastPolicy(ctx context.Context, account *gatewayprovider.ExecutionAccount, model, serviceTier string) (action, errMsg string) {
	if s == nil || s.settingService == nil {
		return anthropic.BetaPolicyActionPass, ""
	}
	tier := strings.ToLower(strings.TrimSpace(serviceTier))
	if tier == "" {
		return anthropic.BetaPolicyActionPass, ""
	}
	settings := openAIFastPolicySettingsFromContext(ctx)
	if settings == nil {
		fetched, err := s.settingService.Gateway.GetOpenAIFastPolicySettings(ctx)
		if err != nil || fetched == nil {
			return anthropic.BetaPolicyActionPass, ""
		}
		settings = fetched
	}
	return tierpolicy.Evaluate(settings, openAIFastPolicyUserID(ctx), account != nil && account.View().IsOAuth(), account != nil && account.View().IsBedrock(), model, tier)
}

// openAIFastPolicyUserID 从可信请求上下文读取 API Key 所属用户 ID。
func openAIFastPolicyUserID(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	access, _ := apikey.AccessSnapshotFromContext(ctx)
	userID := access.PayerUserID
	if userID <= 0 {
		return 0
	}
	return userID
}

// openAIFastPolicyCtxKey 是 context 中预取的 OpenAIFastPolicySettings 缓存
// 键，仅用于 WebSocket 长会话内多帧复用同一份策略快照，避免每帧 DB 命中。
//
// Trade-off：策略变更不会影响当前 WS session（只影响新 session）。这是
// 有意为之 —— 对长会话来说，"策略一致性"比"立刻生效"更重要，且 Claude
// BetaPolicy 的 gin.Context 缓存也是同样取舍。需要 hot-reload 时管理员
// 可以通过踢断 session 强制刷新。
type openAIFastPolicyCtxKeyType struct{}

var openAIFastPolicyCtxKey = openAIFastPolicyCtxKeyType{}

// withOpenAIFastPolicyContext 将一份 settings 快照绑定到 context，供该 ctx
// 衍生 goroutine 中的 evaluateOpenAIFastPolicy 复用。
func withOpenAIFastPolicyContext(ctx context.Context, settings *tierpolicy.OpenAIFastPolicySettings) context.Context {
	if ctx == nil || settings == nil {
		return ctx
	}
	return context.WithValue(ctx, openAIFastPolicyCtxKey, settings)
}

func openAIFastPolicySettingsFromContext(ctx context.Context) *tierpolicy.OpenAIFastPolicySettings {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(openAIFastPolicyCtxKey).(*tierpolicy.OpenAIFastPolicySettings); ok {
		return v
	}
	return nil
}

// openAIGroupFastPolicy 只信任认证链路完整加载的分组，并限于 OpenAI 账号。
func openAIGroupFastPolicy(ctx context.Context, account *gatewayprovider.ExecutionAccount) string {
	if ctx == nil || account == nil || !account.View().IsOpenAI() {
		return routing.GroupOpenAIFastPolicyFollowRequest
	}
	group, _ := requeststate.GroupFromContext(ctx)
	if !routing.IsGroupContextValid(group) || !routing.GroupSupportsOpenAIFast(group.Platform) {
		return routing.GroupOpenAIFastPolicyFollowRequest
	}
	return group.EffectiveOpenAIFastPolicy()
}

func appendOpenAIResponsesRequestPathSuffix(base, suffix string) string {
	return openai.AppendResponsesPathSuffix(base, suffix)
}
