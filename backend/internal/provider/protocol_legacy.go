package provider

import (
	"maps"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// protocol_legacy.go translates legacy protocol settings and preserves endpoint
// selections during credential updates. Legacy protocol settings express text
// routing and workloads separately from native selections. Kimi, Zhipu, and
// DeepSeek adaptive mode enumerates available native protocols, while fixed
// variants produce one text identifier. OpenAI API-key workload and endpoint
// settings filter its native options before that selection is saved.
//
// A legacy edit to an already unified provider replaces text protocols and,
// when workload keys are supplied, embeddings. Independent media selections
// remain present. A credential replacement with no legacy keys carries forward
// omitted unified protocols and endpoint slots; supplied values, including nil,
// express caller intent. Endpoint migration copies fixed Kimi, Zhipu, and
// DeepSeek base addresses into missing slots before removing old fields, so the
// saved set and its addresses survive the representation change together.

// LegacyUpstreamProtocols returns a new selection derived from legacy settings
// for a nonnil record. Kimi, Zhipu, and DeepSeek modes use exact enum matching:
// adaptive uses all native options, recognized fixed modes choose one
// identifier, and other values use Chat. This read helper does not validate
// platform compatibility of fixed variants.
//
// OpenAI API-key workload flags can remove text-associated protocols or
// embeddings, while routing mode removes the disfavored Responses or Chat
// endpoint. Media generation and editing remain available through those
// workload filters. Other platforms return their native options, with save
// validation responsible for rejecting any unsupported selected identifier.
func (r *Record) LegacyUpstreamProtocols(legacyMode string) []capability.ProtocolID {
	options := r.NativeProtocolOptions()
	// Fixed Kimi, Zhipu, and DeepSeek legacy variants declare one text
	// protocol, while adaptive variants enumerate platform options. Save
	// validation separately rejects unsupported fixed choices.
	if r.HasProtocolEndpointDefaults() {
		switch legacyMode {
		case APIProtocolAdaptive:
			return options
		case APIProtocolAnthropic:
			return []capability.ProtocolID{capability.ProtocolAnthropicMessages}
		case APIProtocolResponses:
			return []capability.ProtocolID{capability.ProtocolOpenAIResponses}
		default:
			return []capability.ProtocolID{capability.ProtocolOpenAIChatCompletions}
		}
	}
	if r.IsOpenAIApiKey() {
		workloads, found := r.OpenAIWorkloadCapabilitySet()
		// Legacy text workload removal also removes WebSocket, Compact, and search
		// options, while image protocols and independently controlled embeddings remain available.
		if found && !workloads["text_generation"] {
			options = slices.DeleteFunc(options, func(p capability.ProtocolID) bool {
				return p != capability.ProtocolEmbeddings && p != capability.ProtocolImagesGenerations && p != capability.ProtocolImagesEdits
			})
		}
		if found && !workloads["embeddings"] {
			options = slices.DeleteFunc(options, func(p capability.ProtocolID) bool { return p == capability.ProtocolEmbeddings })
		}
		// Resolve both endpoint preferences so a forced mode excludes its opposite
		// endpoint, while preference-preserving mode retains both supported text options.
		mode := ResolveUpstreamTextProtocol(r.Extra, TextProtocolResponses)
		if mode == TextProtocolChatCompletions {
			options = slices.DeleteFunc(options, func(p capability.ProtocolID) bool { return p == capability.ProtocolOpenAIResponses })
		}
		if ResolveUpstreamTextProtocol(r.Extra, TextProtocolChatCompletions) == TextProtocolResponses {
			options = slices.DeleteFunc(options, func(p capability.ProtocolID) bool { return p == capability.ProtocolOpenAIChatCompletions })
		}
	}
	return options
}

// HasLegacyProtocolPatch reports whether credentials or Extra contain legacy
// protocol, workload, or text-route keys. Presence is enough, including nil values.
// Unified native-selection and endpoint-slot keys alone do not trigger legacy
// recomputation, and nil maps produce false.
func HasLegacyProtocolPatch(credentials, extra map[string]any) bool {
	for _, key := range []string{"api_protocol", OpenAIWorkloadCapabilitiesCredentialKey, LegacyOpenAICapabilitiesCredentialKey} {
		if _, ok := credentials[key]; ok {
			return true
		}
	}
	for _, key := range []string{ExtraKeyTextRouteMode, LegacyOpenAIResponsesModeExtraKey} {
		if _, ok := extra[key]; ok {
			return true
		}
	}
	return false
}

// ApplyLegacyProtocolPatch applies legacy selection intent to a nonnil provider
// whose candidate credentials and Extra already include the incoming values.
// An incoming unified selection wins, and a provider lacking a unified set is
// left for normal legacy default derivation. Patch maps determine key presence;
// their values are read from the prepared provider candidate.
//
// Text protocols are replaced from that candidate's legacy interpretation.
// Embeddings are replaced only when workload keys were supplied, leaving media
// and unrelated protocol selections intact. A temporary shallow credential copy
// removes the unified key for legacy evaluation, then the provider receives a
// separate top-level map with the rebuilt selection. Save normalization still
// validates the result before persistence.
func ApplyLegacyProtocolPatch(provider *Record, credentials, extra map[string]any) {
	if _, explicit := credentials[UpstreamProtocolsKey]; explicit || !HasLegacyProtocolPatch(credentials, extra) {
		return
	}
	if _, unified := provider.Credentials[UpstreamProtocolsKey]; !unified {
		return
	}
	// Remove the unified key from a temporary candidate so legacy evaluation reads
	// the merged workload and routing values rather than reusing the saved native selection.
	selected := provider.UpstreamProtocols()
	legacy := *provider
	legacy.Credentials = maps.Clone(provider.Credentials)
	delete(legacy.Credentials, UpstreamProtocolsKey)
	legacyProtocols := legacy.LegacyUpstreamProtocols(legacy.ConfiguredAPIProtocol())
	isText := func(p capability.ProtocolID) bool {
		return p == capability.ProtocolAnthropicMessages || p == capability.ProtocolOpenAIResponses || p == capability.ProtocolOpenAIChatCompletions
	}
	hasWorkload := false
	for _, key := range []string{OpenAIWorkloadCapabilitiesCredentialKey, LegacyOpenAICapabilitiesCredentialKey} {
		if _, ok := credentials[key]; ok {
			hasWorkload = true
		}
	}
	// Replace text and optionally embeddings as one patch-controlled subset.
	// Retain independently selected media protocols so older clients cannot erase them indirectly.
	selected = slices.DeleteFunc(selected, func(p capability.ProtocolID) bool {
		return isText(p) || (hasWorkload && p == capability.ProtocolEmbeddings)
	})
	for _, p := range legacyProtocols {
		if isText(p) || (hasWorkload && p == capability.ProtocolEmbeddings) {
			selected = append(selected, p)
		}
	}
	// Publish the rebuilt selection in a new top-level credential map. Existing
	// credential references keep their selected array, and later save validation
	// checks this candidate.
	provider.Credentials = maps.Clone(provider.Credentials)
	provider.Credentials[UpstreamProtocolsKey] = selected
}

// PreserveProtocolCredentials returns a nonnil shallow copy of incoming
// credentials with omitted unified protocol and endpoint fields retained when
// incoming has no legacy credential keys. Supplied fields win even when their
// values are nil, so omission and an explicit replacement remain distinguishable.
//
// Legacy key presence disables this preservation to let later normalization
// interpret that client's selection. Retained slices, endpoint maps, and nested
// incoming values remain shared. Normalization replaces a nonshadow candidate's
// protocol slice, while endpoint maps and other nested values can remain shared
// afterward; copy them before independent mutation. This helper examines
// credential keys; ApplyLegacyProtocolPatch handles Extra text-route intent
// with the complete incoming patch.
func PreserveProtocolCredentials(existing, incoming map[string]any) map[string]any {
	out := maps.Clone(incoming)
	if out == nil {
		out = map[string]any{}
	}
	// Secret-only credential rotations omit protocol fields. Carry those fields
	// forward unless legacy credential keys request a new interpretation of the selection.
	if !HasLegacyProtocolPatch(incoming, nil) {
		for _, key := range preservedProtocolCredentialKeys {
			if _, supplied := out[key]; !supplied {
				if value, exists := existing[key]; exists {
					out[key] = value
				}
			}
		}
	}
	return out
}

// MigrateLegacyProtocolCredentials transfers fixed Kimi, Zhipu, and DeepSeek
// endpoints and removes legacy selection fields from a nonnil candidate after
// its unified set has been validated and assigned. For nonempty legacy modes
// other than exact adaptive, a trimmed base URL fills a missing slot keyed by
// the stored legacy spelling.
//
// Existing slot presence wins, including an empty or nil value. The slot map
// and Extra are shallow-copied, while credential deletions mutate the
// candidate's current map, so callers prepare that map before migration. The
// base_url remains available for model and usage queries. Direct calls perform
// no protocol or URL validation; NormalizeProviderProtocols owns the ordered
// save path.
func MigrateLegacyProtocolCredentials(provider *Record) {
	// A fixed Kimi, Zhipu, and DeepSeek base belongs to its stored legacy
	// variant. Copy it into a missing protocol slot before deleting
	// api_protocol, preserving any explicitly present slot value.
	if provider.HasProtocolEndpointDefaults() {
		legacy := provider.GetCredential("api_protocol")
		if legacy != "" && legacy != APIProtocolAdaptive {
			// Saved endpoint slots use map[string]any. Other container types provide no
			// recognized slots here, so migration starts a new map for the fixed legacy base.
			urls, _ := provider.Credentials["api_base_urls"].(map[string]any)
			urls = maps.Clone(urls)
			if urls == nil {
				urls = map[string]any{}
			}
			if base := strings.TrimSpace(provider.GetCredential("base_url")); base != "" {
				if _, exists := urls[legacy]; !exists {
					urls[legacy] = base
				}
			}
			provider.Credentials["api_base_urls"] = urls
		}
	}
	// Protocol selection already resides in the unified array. Remove legacy keys
	// after address transfer so subsequent reads use the saved selection and its slots.
	delete(provider.Credentials, "api_protocol")
	delete(provider.Credentials, OpenAIWorkloadCapabilitiesCredentialKey)
	delete(provider.Credentials, LegacyOpenAICapabilitiesCredentialKey)
	provider.Extra = maps.Clone(provider.Extra)
	delete(provider.Extra, ExtraKeyTextRouteMode)
	delete(provider.Extra, LegacyOpenAIResponsesModeExtraKey)
}
