package modelrules

import (
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// openai.go defines OpenAI model and authentication rules for Chat, Responses,
// Compact and Images. Author-level sampling controls also apply through
// canonical model references, while OAuth restrictions require the selected
// OpenAI account route. Native first-party Chat selection uses the exact model
// restriction for ordinary function tools on GPT-6.2.

// applyOpenAIEncoding selects payload controls for the final OpenAI author identity.
func (r *Resolution) applyOpenAIEncoding(model string, patch *modelcatalog.Attributes, conditions modelcatalog.ParameterConditions) {
	t := r.Target
	// OpenAI rules describe Chat and Responses payloads. A Messages route has
	// no registered representation for these model controls.
	if t.UpstreamProtocol == "anthropic_messages" {
		return
	}
	switch model {
	case "gpt-3.5-turbo", "gpt-4", "gpt-4-turbo", "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-4o", "gpt-4o-2024-05-13", "gpt-4o-2024-08-06", "gpt-4o-2024-11-20", "gpt-4o-mini", "gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-mini", "gpt-5.1-codex-max", "gpt-5.2", "gpt-5.2-chat-latest", "gpt-5.2-pro", "gpt-5.2-codex":
		// These registered identities declare verbosity unavailable. The
		// OpenAI forwarding transformer reads this flag when removing
		// text.verbosity from their request bodies.
		r.Rules.Verbosity = new(false)
	}
	if (t.ServiceID() == "openai" || t.usesCanonicalIdentity()) && r.Attributes.Temperature != nil && !*r.Attributes.Temperature {
		// OpenAI model metadata with unavailable temperature also restricts
		// top_p. Both author routes and qualified intermediary identities
		// therefore remove the same sampling fields from their requests.
		patch.TopP = new(false)
	}
	if (t.ServiceID() == "openai" || t.usesCanonicalIdentity()) && slices.Contains([]string{"gpt-5.2", "gpt-5.4"}, model) {
		// These registered sampling controls depend on none effort. Keep
		// their request condition so ForRequest evaluates the same model
		// with each supplied reasoning state.
		conditions["temperature"] = modelcatalog.ParameterCondition{Efforts: []string{"none"}}
		conditions["top_p"] = modelcatalog.ParameterCondition{Efforts: []string{"none"}}
		patch.TopP = new(true)
	}
	if t.UpstreamProtocol == "openai_chat_completions" && (t.ServiceID() == "openai" || t.usesCanonicalIdentity()) {
		// Tool capability can differ between Chat and Responses for one
		// model. Apply the registered Chat restriction here so route
		// attributes retain that protocol distinction.
		switch model {
		case "gpt-6-astra", "gpt-6.1-sol":
			patch.ToolCall = new(false)
		case "gpt-6-sol", "gpt-6-luna":
			conditions["tool_call"] = modelcatalog.ParameterCondition{Efforts: []string{"none"}}
		}
	}
}

// OpenAIChatFunctionToolsUnsupported identifies models whose first-party Chat
// endpoint cannot accept ordinary function tools. Callers resolve the outgoing
// model and confirm the first-party endpoint before applying the restriction.
func OpenAIChatFunctionToolsUnsupported(model string) bool {
	switch normalize(model) {
	case "gpt-6.2-astra", "gpt-6.2-sol":
		return true
	default:
		return false
	}
}

// applyOpenAIAccess applies OAuth and Compact restrictions for the selected account route.
func (r *Resolution) applyOpenAIAccess(model string, patch *modelcatalog.Attributes) {
	t := r.Target
	if t.ProviderType == "oauth" || t.ProviderType == "setup-token" {
		// OAuth route encoders use their own output and sampling controls.
		// Mark unsupported attributes and dropped fields so forwarding
		// uses the selected authentication variant.
		patch.Temperature, patch.TopP = new(false), new(false)
		r.Rules.OutputParameter = new(false)
		r.Rules.DropFields = []string{"max_output_tokens", "max_completion_tokens", "frequency_penalty", "presence_penalty"}
		if model == "gpt-5.3-codex-spark" {
			patch.InputModalities = new([]string{"text"})
			patch.Attachment = new(false)
			r.Rules.ImageGenerationTool = new(false)
		}
		if t.Endpoint == "compact" && slices.Contains([]string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"}, model) {
			// These registered Compact routes encode max effort as xhigh.
			// Remove max from display choices and retain its request
			// rewrite in EffortAliases.
			if r.Attributes.ReasoningOptions != nil {
				patch.ReasoningOptions = new(removeEffort(r.Attributes.ReasoningOptions, "max"))
			}
			r.Rules.EffortAliases = map[string]string{"max": "xhigh"}
		}
	}
}

// removeEffort copies the control list while omitting one effort string from
// effort options. Nil inputs produce an empty list; retained pointer values
// and budget endpoints remain shared until attribute merging clones them.
func removeEffort(options *[]modelcatalog.ReasoningOption, effort string) []modelcatalog.ReasoningOption {
	result := []modelcatalog.ReasoningOption{}
	if options == nil {
		return result
	}
	for _, option := range *options {
		if option.Type == "effort" {
			option.Values = slices.DeleteFunc(slices.Clone(option.Values), func(value *string) bool { return value != nil && *value == effort })
		}
		result = append(result, option)
	}
	return result
}

// openaiMedia returns supported media parameters for the exact provider model.
func openaiMedia(model string) *modelcatalog.MediaCapabilities {
	result := &modelcatalog.MediaCapabilities{}
	if !slices.Contains([]string{"gpt-image-1", "gpt-image-1-mini", "gpt-image-1.5"}, model) && !strings.HasPrefix(model, "gpt-image-2") {
		return nil
	}
	result.ImageSizes = []string{"1K", "2K"}
	result.AspectRatios = []string{"1:1", "4:3", "3:4", "16:9", "9:16"}
	result.Qualities = []string{"low", "medium", "high", "auto"}
	result.BackgroundOptions = []string{"auto", "opaque"}
	result.MaxReferenceImages = new(16)
	result.InputFidelity = new(true)
	// GPT Image 2 registration changes supported sizes and background options.
	// Its false input fidelity flag makes PrepareModelAttributeParameters
	// remove that tool field.
	if strings.HasPrefix(model, "gpt-image-2") {
		result.ImageSizes = append(result.ImageSizes, "4K")
		result.BackgroundOptions = append(result.BackgroundOptions, "transparent")
		result.InputFidelity = new(false)
	}
	if model == "gpt-image-2.6-sunburst" || model == "gpt-image-2.6-flare" {
		result.Qualities = []string{"low", "medium", "high", "xhigh", "max", "auto"}
		result.OutputFormats = []string{"png", "jpeg", "webp", "gif"}
	}
	return result
}
