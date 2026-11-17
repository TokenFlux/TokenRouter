package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// model_attributes.go builds model catalogue targets from provider identity and
// the selected upstream protocol. Model attributes depend on the serving
// provider identity as well as the model name. A Kimi, Zhipu, and DeepSeek
// provider's protocol-specific endpoint, an enabled Anthropic relay URL, and
// Qoder's default site can each select a different catalogue identity for the
// same requested model.
//
// The record is translated into a modelcatalog.Target for the actual upstream
// protocol, then WithServiceIdentity resolves its service and endpoint context.
// OpenAI API Key Chat routes additionally recognize Ollama through their
// address or saved usage markers, and attach the provider's output cap for
// execution rules. Responses routes omit that Ollama interpretation. The helper
// reads configuration in memory and leaves the record unchanged so catalogue
// lookup can reuse it safely within the caller's snapshot.

// ModelAttributeTarget derives the catalogue lookup target for a model and
// upstream protocol. Nil records retain just those supplied route fields;
// nonnil records add platform, authentication type, endpoint, and site.
//
// Kimi, Zhipu, and DeepSeek endpoint selection follows the upstream protocol
// and provider mode, while an enabled Anthropic custom URL replaces its normal
// endpoint. Qoder defaults an empty site to global. OpenAI API Key Chat routes
// recognize Ollama by endpoint or saved usage markers and attach its output
// cap; with no supplied protocol, the legacy text-route selection determines
// whether this Chat handling applies.
//
// WithServiceIdentity normalizes the resulting catalogue service context. The
// helper performs no lookup or persistence and callers keep the input record
// stable during these reads.
func (r *Record) ModelAttributeTarget(model, upstreamProtocol string) modelcatalog.Target {
	target := modelcatalog.Target{Model: model, UpstreamProtocol: upstreamProtocol}
	if r == nil {
		return target
	}
	target.Platform, target.ProviderType = r.Platform, r.Type
	target.ProviderMode, target.Site = r.GetCredential("provider_mode"), r.GetCredential("site")
	target.BaseURL = r.GetCredential("base_url")
	// Catalogue identity must follow the endpoint used by this protocol. A
	// provider can configure separate Chat, Messages, and Responses addresses
	// whose service identities cannot be inferred from base_url alone.
	if r.HasProtocolEndpointDefaults() {
		target.ProviderMode = r.GetProviderMode()
		protocol := APIProtocolChatCompletions
		switch upstreamProtocol {
		case "anthropic_messages":
			protocol = APIProtocolAnthropic
		case "openai_responses":
			protocol = APIProtocolResponses
		}
		target.BaseURL = (ProtocolTarget{Record: r}).GetTextAPIKeyProtocolBaseURL(protocol)
	}
	if r.Platform == "anthropic" && r.IsCustomBaseURLEnabled() && r.GetCustomBaseURL() != "" {
		target.BaseURL = r.GetCustomBaseURL()
	}
	if target.Platform == "qoder" && target.Site == "" {
		target.Site = "global"
	}
	if target.Platform == "qoder" {
		target.QoderVersion = r.QoderCatalogVersion()
		if observed, found := r.QoderModel(model); found {
			target.QoderModel = &observed
			target.AttributeModel = observed.Identity
		}
	}
	chat := upstreamProtocol == "openai_chat_completions"
	// Callers without an upstream protocol still use the legacy text route
	// setting to choose Chat-specific interpretation; the default is Responses,
	// which leaves Ollama handling inactive for that route.
	if upstreamProtocol == "" {
		chat = ResolveUpstreamTextProtocol(r.Extra, TextProtocolResponses) == TextProtocolChatCompletions
	}
	if r.Platform == "openai" && r.Type == "apikey" && chat {
		target.Ollama = egress.IsOllamaCloudBaseURL(target.BaseURL)
		// Saved usage markers can identify an Ollama route whose configured address
		// is a relay. Their presence counts even when the marker value is empty,
		// matching the provider metadata used by its execution rules.
		for _, key := range []string{OllamaCloudUsageSessionExtraKey, OllamaCloudUsageAutoRefreshExtraKey, OllamaCloudUsageSnapshotExtraKey} {
			if _, found := r.Extra[key]; found {
				target.Ollama = true
			}
		}
		if target.Ollama {
			value, present := r.Extra[modelcatalog.OllamaOutputCapExtraKey]
			limit := int(modelcatalog.OllamaOutputCap(value, present))
			target.OllamaOutputCap = &limit
		}
	}
	return target.WithServiceIdentity()
}
