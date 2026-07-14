package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/claude"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// apiKeyFastModePolicyFromContext 只读取鉴权中间件写入的可信策略。
func apiKeyFastModePolicyFromContext(ctx context.Context) string {
	if ctx == nil {
		return APIKeyFastModePolicyFollowRequest
	}
	raw, _ := ctx.Value(ctxkey.APIKeyFastModePolicy).(string)
	policy, ok := NormalizeAPIKeyFastModePolicy(strings.TrimSpace(raw))
	if !ok {
		return APIKeyFastModePolicyFollowRequest
	}
	return policy
}

// apiKeyFastModePricingModel 优先使用入口记录的用户可见模型，确保能力判断与分组定价一致。
func apiKeyFastModePricingModel(ctx context.Context, fallback string) string {
	if ctx != nil {
		if model, ok := ctx.Value(ctxkey.Model).(string); ok && strings.TrimSpace(model) != "" {
			return strings.TrimSpace(model)
		}
	}
	return strings.TrimSpace(fallback)
}

// apiKeyFastModeSupported 按当前有效分组和模型定价判断 Fast 能力。
// 缺少分组、解析器或定价结果时按不支持处理，避免 Key 配置误改上游请求。
func apiKeyFastModeSupported(ctx context.Context, resolver *ModelPricingResolver, model string) bool {
	if ctx == nil || resolver == nil || resolver.billingService == nil {
		return false
	}
	group, ok := ctx.Value(ctxkey.Group).(*Group)
	if !ok || group == nil || group.ID <= 0 {
		return false
	}
	groupID := group.ID
	resolved := resolver.Resolve(ctx, PricingInput{
		Model:   apiKeyFastModePricingModel(ctx, model),
		GroupID: &groupID,
	})
	return resolved != nil && resolved.SupportsServiceTier
}

// openAIAPIKeyFastModeSupported 将单 Key 策略限制到 OpenAI 原生适配器。
func (s *OpenAIGatewayService) openAIAPIKeyFastModeSupported(ctx context.Context, account *Account, model string) bool {
	return account != nil && account.IsOpenAI() && apiKeyFastModeSupported(ctx, s.resolver, model)
}

// claudeAPIKeyFastModeSupported 将 Claude Fast 限制到 Anthropic API Key 直连适配器。
// Bedrock、Vertex 和 OAuth/Setup Token 路径不会应用单 Key Fast 策略。
func (s *GatewayService) claudeAPIKeyFastModeSupported(ctx context.Context, account *Account, model string) bool {
	return account != nil && account.IsAnthropic() && account.Type == AccountTypeAPIKey &&
		apiKeyFastModeSupported(ctx, s.resolver, model)
}

// addAnthropicBetaToken 在保留其它 beta 的同时补齐指定 token。
func addAnthropicBetaToken(header, token string) string {
	if containsBetaToken(header, token) {
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
func (s *GatewayService) applyClaudeAPIKeyFastMode(
	ctx context.Context,
	account *Account,
	model string,
	body []byte,
	headers http.Header,
) ([]byte, http.Header, error) {
	if !s.claudeAPIKeyFastModeSupported(ctx, account, model) {
		return body, headers, nil
	}
	if headers == nil {
		headers = make(http.Header)
	}

	policy := apiKeyFastModePolicyFromContext(ctx)
	fastRequested := strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "speed").String()), "fast") ||
		containsBetaToken(getHeaderRaw(headers, "anthropic-beta"), claude.BetaFastMode)
	shouldFast := fastRequested
	switch policy {
	case APIKeyFastModePolicyForceOn:
		shouldFast = true
	case APIKeyFastModePolicyForceOff:
		shouldFast = false
	}

	if shouldFast {
		updated, err := sjson.SetBytes(body, "speed", "fast")
		if err != nil {
			return body, headers, fmt.Errorf("set Claude fast speed: %w", err)
		}
		cloned := headers.Clone()
		setHeaderRaw(cloned, "anthropic-beta", addAnthropicBetaToken(getHeaderRaw(cloned, "anthropic-beta"), claude.BetaFastMode))
		return updated, cloned, nil
	}

	if policy != APIKeyFastModePolicyForceOff {
		return body, headers, nil
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
	setHeaderRaw(cloned, "anthropic-beta", stripBetaTokensWithSet(getHeaderRaw(cloned, "anthropic-beta"), fastOnly))
	return updated, cloned, nil
}
