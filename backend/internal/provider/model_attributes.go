package provider

import (
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
)

// model_attributes.go builds model catalogue targets from provider identity and
// the selected upstream protocol. Model attributes depend on the serving
// provider identity as well as the model name. A Kimi, Zhipu, and DeepSeek
// provider's protocol-specific endpoint, an enabled Anthropic relay URL, and
// Qoder's default site can each select a different catalogue identity for the
// same requested model.
//
// The record is translated into a modelrules.Target for the actual upstream
// protocol, then WithServiceIdentity resolves its service and endpoint context.
// OpenAI API Key Chat routes additionally recognize Ollama through their
// address or saved usage markers, and attach the provider's output cap for
// execution rules. Responses routes omit that Ollama interpretation. The helper
// reads configuration in memory and leaves the record unchanged so catalogue
// lookup can reuse it safely within the caller's snapshot.

// ModelAttributeTarget builds an attribute route when no client protocol was selected.
// Protocol-aware consumers use ModelAttributeRoute so scoped group rules also
// receive the client's entry protocol before matching the final provider route.
func (r *Record) ModelAttributeTarget(model, upstreamProtocol string) modelrules.Target {
	return r.ModelAttributeRoute(model, "", upstreamProtocol)
}

// ModelAttributeRoute builds the shared lookup target for preview, probes and
// execution. The client protocol stays attached to the request, while the
// selected provider supplies its upstream protocol and final route key. This
// makes scoped rules match identically for raw names and account aliases.
func (r *Record) ModelAttributeRoute(model, clientProtocol, upstreamProtocol string) modelrules.Target {
	target := modelrules.Target{Model: model, ClientProtocol: clientProtocol, UpstreamProtocol: upstreamProtocol}
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
		index, err := r.QoderModelIndex()
		if err != nil {
			// Preview can describe configured custom routes before synchronization.
			// A blank index still applies Qoder's text converter constraints.
			index = qodermodel.NewCatalog(strconv.FormatInt(r.ID, 10)+":unavailable", nil)
		}
		target.Model = index.RouteKey(model)
		target.ProviderModels = index
		target.UpstreamProtocol = string(protocol.ProtocolQoderChat)
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
			value, present := r.Extra[modelrules.OllamaOutputCapExtraKey]
			limit := int(modelrules.OllamaOutputCap(value, present))
			target.OllamaOutputCap = &limit
		}
	}
	return target.WithServiceIdentity()
}
