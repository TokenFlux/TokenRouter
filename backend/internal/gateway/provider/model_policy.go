package provider

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

// ModelPolicy 只组合账号的模型配置与当次不可变路线，不持有查询、缓存或执行资源。
// Route 独立于 Record，不能进入持久化账号或调度快照。
type ModelPolicy struct {
	Record *account.Record
	Route  requeststate.AttemptRoute
}

func (p ModelPolicy) protocolTarget() account.ProtocolTarget {
	return account.ProtocolTarget{Record: p.Record, Protocol: p.Route.Protocol()}
}

// Mapped 保留原调用点读取模型配置以及一跳映射。
func (p ModelPolicy) Mapped(model string) string {
	if p.Record == nil {
		return model
	}
	mapped, _ := p.ResolveMapped(model)
	return mapped
}

// ResolveMapped 保留单步映射命中信息，并在当次尝试的匹配时点读取账号配置。
func (p ModelPolicy) ResolveMapped(model string) (string, bool) {
	return p.Route.ResolveModel(p.Record.ID, p.Record.Platform, account.ResolveModelMapping(p.Record, accountprovider.ModelDefaults()), model)
}

func (p ModelPolicy) ForwardModel(requested, dispatchMapped string) string {
	model := requested
	if dispatchMapped = strings.TrimSpace(dispatchMapped); dispatchMapped != "" {
		model = dispatchMapped
	}
	return p.Mapped(model)
}
func (p ModelPolicy) NormalizeOpenAI(model string) string {
	if p.Record == nil {
		return strings.TrimSpace(model)
	}
	if p.Record.IsGrok() {
		return grok.NormalizeModelID(model)
	}
	if p.Record.UsesOpenAICodexProtocol() {
		return NormalizeCodexModel(model)
	}
	return strings.TrimSpace(model)
}

// RawChat 先使用已解析协议，再保留各平台原协议缺省。
func (p ModelPolicy) RawChat() bool {
	if p.Record != nil && p.Route.Protocol() != "" {
		return p.Route.Protocol() == protocol.ProtocolOpenAIChatCompletions
	}
	if p.Record == nil || p.Record.Type != capability.AccountTypeAPIKey {
		return false
	}
	if p.Record.IsCNProvider() {
		switch p.protocolTarget().GetAPIProtocol() {
		case account.APIProtocolChatCompletions:
			return true
		case account.APIProtocolAdaptive:
			return !p.Record.SupportsNativeCNResponses()
		default:
			return false
		}
	}
	return account.ResolveUpstreamTextProtocol(p.Record.Extra, account.TextProtocolResponses) == account.TextProtocolChatCompletions
}

// OpenAIUpstream 保留压缩映射、透传和普通映射的原优先级。
func (p ModelPolicy) OpenAIUpstream(requested string, compact, allowHTTPPassthrough bool) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return ""
	}
	if p.RawChat() {
		return p.NormalizeOpenAI(p.ForwardModel(requested, ""))
	}
	if p.Record != nil && p.Record.IsOpenAIPassthroughEnabled() {
		if compact {
			return account.ResolveCompactForwardModel(p.Record, requested)
		}
		return requested
	}
	if allowHTTPPassthrough && p.Record != nil && p.Record.IsOpenAIPassthroughEnabled() {
		return requested
	}
	if compact && p.Record != nil {
		if model, matched := p.Record.ResolveCompactMappedModel(requested); matched {
			if model = strings.TrimSpace(model); model != "" {
				return model
			}
		}
	}
	model := strings.TrimSpace(p.ForwardModel(requested, ""))
	if model == "" {
		return ""
	}
	if compact {
		compactModel := strings.TrimSpace(account.ResolveCompactForwardModel(p.Record, model))
		if compactModel != "" && compactModel != model {
			return compactModel
		}
	}
	return strings.TrimSpace(p.NormalizeOpenAI(model))
}
func (p ModelPolicy) CanonicalSchedulingModel(requested string) string {
	model := strings.TrimSpace(requested)
	if p.Record == nil || model == "" {
		return model
	}
	if p.Record.IsOpenAI() {
		return p.OpenAIUpstream(model, false, false)
	}
	if mapped := strings.TrimSpace(p.Mapped(model)); mapped != "" {
		model = mapped
	}
	if p.Record.IsOpenAICompatible() {
		return p.NormalizeOpenAI(model)
	}
	return model
}
func (p ModelPolicy) bedrockInput(model string) *bedrock.RouteInput {
	if p.Record == nil {
		return nil
	}
	return &bedrock.RouteInput{Region: p.Record.GetCredential("aws_region"), ForceGlobal: p.Record.GetCredential("aws_force_global") == "true", Model: p.Mapped(model)}
}
func (p ModelPolicy) Bedrock(model string) (string, bool) {
	return bedrock.ResolveBedrockModelID(p.bedrockInput(model), model)
}

// BedrockRoute 复用账号单步映射和平台区域规则，不改变资格或来源区域。
func (p ModelPolicy) BedrockRoute(model string) (bedrock.BedrockModelRoute, error) {
	return bedrock.ResolveBedrockModelRoute(p.bedrockInput(model), model)
}

func (p ModelPolicy) AnthropicUpstream(mapped string) string {
	if p.Record == nil {
		return ""
	}
	mapped = strings.TrimSpace(mapped)
	if mapped == "" || p.Record.Platform != capability.PlatformAnthropic || p.Record.Type == capability.AccountTypeAPIKey || p.Record.IsBedrock() {
		return mapped
	}
	normalized := anthropic.NormalizeModelID(mapped)
	if p.Record.Type == capability.AccountTypeServiceAccount {
		return vertex.NormalizeVertexAnthropicModelID(normalized)
	}
	return normalized
}
func modelThinking(ctx context.Context) *bool {
	if value, ok := requeststate.ThinkingEnabledFromContext(ctx); ok {
		return &value
	}
	return nil
}

// Supports 只检查已经过渠道映射的模型，不再执行渠道映射。
func (p ModelPolicy) Supports(ctx context.Context, model string) bool {
	value := p.Record
	if value == nil {
		return false
	}
	if value.Platform == capability.PlatformAntigravity {
		if strings.TrimSpace(model) == "" {
			return true
		}
		mapped := accountprovider.MapAntigravityModel(value, model)
		if mapped == "" {
			return false
		}
		if thinking := modelThinking(ctx); thinking != nil {
			final := antigravity.ApplyThinkingModelSuffix(mapped, *thinking)
			if final == mapped {
				return true
			}
			return value.IsModelSupported(final, accountprovider.ModelDefaults(), accountprovider.ModelRules(value))
		}
		return true
	}
	if value.IsBedrock() {
		_, ok := p.Bedrock(model)
		return ok
	}
	if value.Platform == capability.PlatformOpenAI && value.IsOpenAIPassthroughEnabled() {
		return true
	}
	if value.Platform == capability.PlatformAnthropic && value.Type != capability.AccountTypeAPIKey {
		mapped := account.ResolveForwardMappedModel(value, model, accountprovider.ModelDefaults())
		return value.FinalModelWhitelisted(p.AnthropicUpstream(mapped), accountprovider.ModelDefaults(), accountprovider.ModelRules(value))
	}
	return value.IsModelSupported(model, accountprovider.ModelDefaults(), accountprovider.ModelRules(value))
}

// UpstreamModel 保留最终模型登记时点；目录和执行共用平台规则。
func (p ModelPolicy) UpstreamModel(ctx context.Context, requested string) string {
	value := p.Record
	if value == nil {
		return ""
	}
	var model string
	if value.IsBedrock() {
		mapped, ok := p.Bedrock(requested)
		if !ok {
			return ""
		}
		model = mapped
	} else if value.Platform == capability.PlatformAntigravity {
		model = accountprovider.FinalAntigravityModel(value, requested, modelThinking(ctx))
	} else if value.Platform == capability.PlatformOpenAI || value.Platform == capability.PlatformGrok {
		model = p.OpenAIUpstream(requested, false, true)
	} else {
		mapped := account.ResolveForwardMappedModel(value, requested, accountprovider.ModelDefaults())
		if value.Platform == capability.PlatformQoder {
			site, err := qoder.ParseSite(value.GetCredential("site"))
			if err != nil {
				return ""
			}
			model = qoder.ResolveQoderModelForSite(site, mapped).Key
		} else {
			model = p.AnthropicUpstream(mapped)
		}
	}
	model = strings.TrimSpace(model)
	modeltrace.RegisterStage(ctx, model)
	return model
}

// LimitKeys 保留平台模型别名及图片、Fable、Gemini 共享窗口的原顺序。
func (p ModelPolicy) LimitKeys(ctx context.Context, requested string) []string {
	value := p.Record
	if value == nil {
		return nil
	}
	key := p.Mapped(requested)
	switch value.Platform {
	case capability.PlatformOpenAI, capability.PlatformGrok:
		key = p.CanonicalSchedulingModel(requested)
	case capability.PlatformAntigravity:
		key = accountprovider.FinalAntigravityModel(value, requested, modelThinking(ctx))
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	keys := []string{key}
	switch value.Platform {
	case capability.PlatformAntigravity:
		if strings.HasPrefix(antigravity.NormalizeAntigravityModelName(key), "gemini-") && key != "antigravity:gemini" {
			keys = append(keys, "antigravity:gemini")
		}
	case capability.PlatformOpenAI:
		images := media.IsImageGenerationModel(requested) || media.IsImageGenerationModel(key) || requeststate.OpenAIImageGenerationIntentFromContext(ctx)
		if images && key != account.OpenAIImageGenerationRateLimitKey {
			keys = append(keys, account.OpenAIImageGenerationRateLimitKey)
		}
	case capability.PlatformAnthropic:
		if anthropic.IsAnthropicFableModel(key) && key != account.AnthropicFableRateLimitKey {
			keys = append(keys, account.AnthropicFableRateLimitKey)
		}
	}
	return keys
}
func (p ModelPolicy) AllowsModel(ctx context.Context, model string) bool {
	return p.Record.ModelRateLimitAllows(p.LimitKeys(ctx, model))
}

// ListingModels 保留 Antigravity 普通与 thinking 两次独立资格检查及稳定去重。
func (p ModelPolicy) ListingModels(ctx context.Context, requested string) []string {
	models := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		key := strings.ToLower(model)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		models = append(models, model)
	}
	if p.Record != nil && p.Record.Platform == capability.PlatformAntigravity {
		plain := requeststate.WithThinkingEnabled(ctx, false)
		if p.AllowsModel(plain, requested) && p.Supports(plain, requested) {
			add(p.UpstreamModel(plain, requested))
		}
		thinking := requeststate.WithThinkingEnabled(ctx, true)
		if p.AllowsModel(thinking, requested) && p.Supports(thinking, requested) {
			add(p.UpstreamModel(thinking, requested))
		}
		return models
	}
	if p.Record == nil || !p.AllowsModel(ctx, requested) {
		return models
	}
	add(p.UpstreamModel(ctx, requested))
	return models
}

// ForwardMappedModels 保留计费模型与最终上游模型的独立解析和 Compact 优先级。
func (p ModelPolicy) ForwardMappedModels(requested string, compact bool) (billingModel, upstreamModel string) {
	requested = strings.TrimSpace(requested)
	if p.Record != nil && p.Record.IsOpenAIPassthroughEnabled() {
		billingModel = requested
	} else if p.Record != nil {
		billingModel = strings.TrimSpace(p.Mapped(requested))
	}
	if billingModel == "" {
		billingModel = requested
	}
	upstreamModel = p.OpenAIUpstream(requested, compact, false)
	if strings.TrimSpace(upstreamModel) == "" {
		upstreamModel = billingModel
	}
	return billingModel, upstreamModel
}

// ErrorSchedulingModel 只使用已经观测的型号，缺失时才回退计费型号。
func ErrorSchedulingModel(billingModel, upstreamModel string) string {
	if upstream := strings.TrimSpace(upstreamModel); upstream != "" {
		return upstream
	}
	return strings.TrimSpace(billingModel)
}
