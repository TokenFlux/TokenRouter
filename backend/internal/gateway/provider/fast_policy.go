package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// apiKeyFastModePolicyFromContext 只读取鉴权中间件写入的可信策略。
func APIKeyFastModePolicy(ctx context.Context) string {
	if ctx == nil {
		return apikey.APIKeyFastModePolicyFollowRequest
	}
	access, _ := apikey.AccessSnapshotFromContext(ctx)
	raw := access.FastModePolicy()
	policy, ok := apikey.NormalizeAPIKeyFastModePolicy(strings.TrimSpace(raw))
	if !ok {
		return apikey.APIKeyFastModePolicyFollowRequest
	}
	return policy
}

// withAPIKeyFastModePolicy 将刷新后的单 Key 策略覆盖到派生请求上下文中。
func WithAPIKeyFastModePolicy(ctx context.Context, policy string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, ok := apikey.NormalizeAPIKeyFastModePolicy(strings.TrimSpace(policy))
	if !ok {
		normalized = apikey.APIKeyFastModePolicyFollowRequest
	}
	return apikey.WithFastModePolicy(ctx, normalized)
}

// apiKeyFastModePricingModel 优先使用入口记录的用户可见模型，确保能力判断与分组定价一致。
func FastModePricingModel(ctx context.Context, fallback string) string {
	if ctx != nil {
		if model, ok := ctx.Value(telemetry.Model).(string); ok && strings.TrimSpace(model) != "" {
			return strings.TrimSpace(model)
		}
	}
	return strings.TrimSpace(fallback)
}

// apiKeyFastModeForceOnSupported 按当前有效分组和模型定价判断 Fast 强制开启能力。
// 缺少分组、解析器或定价结果时按不支持处理，避免 Key 配置误向上游注入 Fast。
func SupportsFastMode(ctx context.Context, resolver *billing.PriceResolver, model string) bool {
	if ctx == nil || resolver == nil {
		return false
	}
	group, ok := requeststate.GroupFromContext(ctx)
	if !ok || group == nil || group.ID <= 0 {
		return false
	}
	groupID := group.ID
	resolved := resolver.Resolve(ctx, billing.PricingInput{
		Model:   FastModePricingModel(ctx, model),
		GroupID: &groupID,
	})
	return resolved != nil && resolved.SupportsServiceTier
}

// claudeAPIKeyFastModeForceOnSupported 将 Claude Fast 强制开启限制到 Anthropic API Key 直连适配器。
// Bedrock、Vertex 和 OAuth/Setup Token 路径不会由单 Key 策略注入 Fast。
func SupportsAnthropicFastMode(ctx context.Context, resolver *billing.PriceResolver,

	account *ExecutionAccount, model string) bool {
	return account != nil && account.View().IsAnthropic() && account.Record.Type == capability.AccountTypeAPIKey &&
		SupportsFastMode(ctx, resolver, model)
}

// addAnthropicBetaToken 在保留其它 beta 的同时补齐指定 token。
func addAnthropicBetaToken(header, token string) string {
	if claude.ContainsBetaToken(header, token) {
		return header
	}
	header = strings.TrimSpace(header)
	if header == "" {
		return token
	}
	return header + "," + token
}

// applyClaudeAPIKeyFastMode 将单 Key 策略编码为 Claude 官方 Fast wire 格式。
// 这里只改写候选请求，最终 beta filter/block 仍由系统策略执行。
func ApplyAnthropicFastMode(
	ctx context.Context, resolver *billing.PriceResolver,

	account *ExecutionAccount,
	model string,
	body []byte,
	headers http.Header,
) ([]byte, http.Header, error) {
	policy := APIKeyFastModePolicy(ctx)

	if policy == apikey.APIKeyFastModePolicyForceOff {
		if account == nil || !account.View().IsAnthropic() {
			return body, headers, nil
		}
		if headers == nil {
			headers = make(http.Header)
		}
		updated := body
		if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "speed").String()), "fast") {
			var err error
			updated, err = sjson.DeleteBytes(body, "speed")
			if err != nil {
				return body, headers, fmt.Errorf("remove Claude fast speed: %w", err)
			}
		}
		cloned := headers.Clone()
		fastOnly := map[string]struct{}{claude.BetaFastMode: {}}
		claude.SetHeaderRaw(cloned, "anthropic-beta", claude.StripBetaTokensWithSet(claude.GetHeaderRaw(cloned, "anthropic-beta"), fastOnly))
		return updated, cloned, nil
	}

	if !SupportsAnthropicFastMode(ctx, resolver, account, model) {
		return body, headers, nil
	}
	if headers == nil {
		headers = make(http.Header)
	}

	fastRequested := strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "speed").String()), "fast") ||
		claude.ContainsBetaToken(claude.GetHeaderRaw(headers, "anthropic-beta"), claude.BetaFastMode)
	shouldFast := fastRequested
	if policy == apikey.APIKeyFastModePolicyForceOn {
		shouldFast = true
	}

	if shouldFast {
		updated, err := sjson.SetBytes(body, "speed", "fast")
		if err != nil {
			return body, headers, fmt.Errorf("set Claude fast speed: %w", err)
		}
		cloned := headers.Clone()
		claude.SetHeaderRaw(cloned, "anthropic-beta", addAnthropicBetaToken(claude.GetHeaderRaw(cloned, "anthropic-beta"), claude.BetaFastMode))
		return updated, cloned, nil
	}

	return body, headers, nil
}
