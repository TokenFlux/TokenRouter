package modelrules

import (
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// encoding.go selects model payload constraints from a model's author identity
// and the request protocol. An OpenCode offering can use a different outbound
// name from its canonical model, so its retained AttributeModel supplies the
// author and model used here. Missing or unrecognized author qualification
// leaves those model rules unavailable. Native services retain their existing
// service recognition and complete model normalization.
//
// Sampling conditions, thinking representations, and prohibited request fields
// accompany the model across supported protocols. Authentication restrictions,
// service output limits, site-specific behavior, and media policies remain in
// platform.go. Request-state defaults use the same identity as the encoding
// rules so an alias with omitted thinking controls behaves like its author
// model. The original Target continues to identify the service and outbound name.

// applyModelEncoding adds model payload rules before service and administrator
// patches. OpenCode requires a qualified canonical author and a supported text
// protocol; other routes retain their native service's model rules, including
// metadata queries whose protocol has not been selected yet.
//
// Conditions describe display capabilities until ForRequest evaluates a request.
// This method keeps authentication and endpoint policies outside the model rule
// selection, so a canonical OpenAI identity cannot acquire Codex OAuth field
// removal or Compact effort aliases through an intermediary.
func (r *Resolution) applyModelEncoding(patch *modelcatalog.Attributes, conditions modelcatalog.ParameterConditions) {
	t := r.Target
	author, model := t.modelEncodingIdentity()
	// Intermediary encodings are selected explicitly; model constraints cannot be
	// inferred for a different wire shape or a route with no selected protocol.
	if t.usesCanonicalIdentity() && !slices.Contains([]string{"openai_chat_completions", "openai_responses", "anthropic_messages"}, t.UpstreamProtocol) {
		return
	}
	switch author {
	case "openai":
		r.applyOpenAIEncoding(model, patch, conditions)
	case "grok":
		r.applyGrokEncoding(model, patch)
	case "deepseek":
		r.applyDeepSeekEncoding(model, patch, conditions)
	}
}

// applyMessagesEncoding adds signatures and thinking representations after the
// service-specific rules. OpenCode requires the declared canonical author to
// own the signature family, and other platforms retain their Messages model
// recognition for compatible services and hosted variants. The model name
// remains the canonical name throughout these checks, so an intermediary alias
// cannot lose the author model's adaptive mode. Service rules run first because
// compatible Messages encoding may refine their default thinking representation.
func (r *Resolution) applyMessagesEncoding(conditions modelcatalog.ParameterConditions) {
	t := r.Target
	if t.UpstreamProtocol != "anthropic_messages" {
		return
	}
	author, model := t.modelEncodingIdentity()
	signature := thinkingSignatureMode(model)
	if t.usesCanonicalIdentity() {
		switch author {
		case "anthropic":
			if signature != "anthropic" {
				return
			}
		case "deepseek", "kimi", "zhipu", "minimax", "alibaba":
			if signature != "passback" {
				return
			}
		default:
			return
		}
	}
	// Claude sampling and adaptive thinking belong to its Messages encoding;
	// intermediaries carrying that model need the same payload constraints as author endpoints.
	if strings.HasPrefix(model, "claude-") {
		conditions["temperature"] = modelcatalog.ParameterCondition{Thinking: new(false)}
		r.Rules.ThinkingMode = claudeThinkingMode(model, r.Attributes)
	}
	if signature != "" {
		r.Rules.SignatureMode = signature
	}
	// MiniMax Messages uses adaptive thinking while its catalog attributes retain
	// the supported controls available to conversion and request parameter validation.
	if strings.HasPrefix(model, "minimax-m") {
		r.Rules.ThinkingMode = "adaptive"
	}
}

// modelEncodingIdentity returns the author family and model used for payload
// rules and request defaults. Targets whose rules come from canonical references
// use the qualified AttributeModel; unknown authors and missing qualifications
// empty values.
//
// Native routes retain service-based selection, Google resource normalization,
// and service-prefix removal. Qualification is interpreted locally and never
// changes Target.Platform, catalog service identity, or the outbound Model.
// These returned names select model rules only, so callers must continue using
// the original target for authentication, quotas, addresses, and media policy.
func (t Target) modelEncodingIdentity() (string, string) {
	model := normalize(t.Model)
	if t.usesCanonicalIdentity() {
		// A qualified reference supplies author evidence independently of the
		// service alias. Reject incomplete and nested references before choosing rules.
		author, name, qualified := strings.Cut(normalize(t.AttributeModel), "/")
		family := serviceRuleFamily(author)
		if !qualified || name == "" || strings.Contains(name, "/") || family == "" {
			return "", ""
		}
		return family, name
	}
	if t.Platform == "gemini" || t.Platform == "antigravity" {
		model = googleResourceModel(model)
	}
	if t.ServiceID() != "" {
		model = strings.TrimPrefix(model, t.ServiceID()+"/")
	}
	return t.servicePlatform(), model
}

// thinkingSignatureMode selects signature handling for registered Messages
// model families. Passback families retain their thinking payloads, Claude
// names use Anthropic signatures, and unrecognized identities return no
// protocol-specific choice.
func thinkingSignatureMode(model string) string {
	switch {
	case strings.HasPrefix(model, "deepseek-"), strings.HasPrefix(model, "kimi-"), strings.HasPrefix(model, "moonshot-"), strings.HasPrefix(model, "glm-"), model == "k3", model == "k3-256k", strings.HasPrefix(model, "minimax-m"):
		return "passback"
	case strings.Contains(model, "-thinking") && (strings.HasPrefix(model, "qwen-") || strings.HasPrefix(model, "qwen2-") || strings.HasPrefix(model, "qwen3-") || strings.HasPrefix(model, "qwen4-")):
		return "passback"
	case strings.HasPrefix(model, "claude-"), strings.HasPrefix(model, "opus-"), strings.HasPrefix(model, "sonnet-"), strings.HasPrefix(model, "haiku-"):
		return "anthropic"
	default:
		return ""
	}
}
