package provider

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// claude_cache_policy.go resolves per-Key forced one-hour Claude prompt-cache
// policy for gateway forwarding. An API Key can request a one-hour lifetime on
// existing ephemeral cache markers across native Messages, passthrough,
// Bedrock, Vertex, and OAuth-mimicked OpenAI conversions without changing the
// global OAuth billing override.
//
// Anthropic-compatible endpoints can also host passback model families such as
// GLM, Kimi, DeepSeek, MiniMax, and Qwen thinking models. Eligibility therefore
// combines the request's authenticated Key snapshot with provider platform,
// credential kind, and final upstream model classification so non-Claude models
// keep their incoming cache controls untouched.

// APIKeyForceClaude1hCache reports whether the authenticated access snapshot in
// ctx enables forced one-hour Claude prompt caching. Nil contexts and requests
// without an access snapshot return false so client cache lifetimes stay unchanged.
func APIKeyForceClaude1hCache(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	access, _ := apikey.AccessSnapshotFromContext(ctx)
	return access.ForceClaude1hCache()
}

// WithAPIKeyForceClaude1hCache binds enabled to a copied access snapshot in a
// derived context. A nil context becomes context.Background before storing the
// per-Key Claude cache-lifetime choice for downstream forwarding.
func WithAPIKeyForceClaude1hCache(ctx context.Context, enabled bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return apikey.WithForceClaude1hCache(ctx, enabled)
}

// ShouldForceClaude1hCache reports whether ctx, target, and the ordered model
// candidates qualify for forced one-hour ephemeral cache markers. Callers pass
// the final mapped upstream model first so alias redirects toward non-Claude
// models are evaluated against the actual destination model.
//
// Only Anthropic, OpenAI, and OpenCode provider platforms can carry Claude
// Messages traffic; Kimi, Zhipu, DeepSeek, and other platforms return false.
// Passback-required model families always return false even on Anthropic API
// Key relays, while strict Anthropic models, Claude-named deployments, and
// native Anthropic OAuth, Bedrock, or Vertex service-account targets qualify.
// @project-doc docs/interfaces/anthropic_upstream.md#claude_cache_ttl_policy
func ShouldForceClaude1hCache(ctx context.Context, target *ExecutionProvider, models ...string) bool {
	if !APIKeyForceClaude1hCache(ctx) || target == nil {
		return false
	}
	switch target.Record.Platform {
	case capability.PlatformAnthropic, capability.PlatformOpenAI, capability.PlatformOpenCode:
	default:
		return false
	}

	primaryModel := ""
	for _, candidate := range models {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			primaryModel = trimmed
			break
		}
	}
	if primaryModel != "" {
		switch modelidentity.ResolveThinkingProtocol(primaryModel) {
		case modelidentity.ThinkingProtocolAnthropicStrict:
			return true
		case modelidentity.ThinkingProtocolPassbackRequired:
			return false
		case modelidentity.ThinkingProtocolUnknown:
		}
		if strings.Contains(strings.ToLower(primaryModel), "claude") {
			return true
		}
	}

	// Unrecognized or custom deployment IDs still qualify on Anthropic-owned
	// OAuth, Bedrock, and Vertex credentials, while generic API Key relays
	// require an explicit Claude model match above because compatible non-Claude
	// endpoints share PlatformAnthropic.
	return target.View().IsAnthropicOAuthOrSetupToken() ||
		target.View().IsBedrock() ||
		(target.View().IsAnthropic() && target.Record.Type == capability.ProviderTypeServiceAccount)
}
