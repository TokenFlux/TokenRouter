package capability

import (
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// protocol.go defines the protocol catalogue and computes native options and
// conversion targets.
// Client entry protocols and provider upstream protocols share stable
// identifiers, but their permitted uses differ. Catalogue entries mark
// dedicated upstream operations separately so group admission can expose public
// entries while native provider selections include batch or Qoder execution.
//
// Platform adapter ranges determine the conversions available for each client
// source. Provider type and OpenAI authentication mode then narrow native
// options and special conversion cases; an enabled declaration alone cannot
// override those restrictions. Automatic fallback order combines the
// implemented platform targets deterministically, while callers can supply
// their own permitted ordering. Catalogue reads copy platform slices so
// administrative consumers can edit returned descriptions independently.

// Protocol aliases share the persisted protocol package identifiers across
// client admission, upstream capability declarations, and conversion lookup.
const (
	// Anthropic Messages entry identity.
	ProtocolAnthropicMessages = protocol.ProtocolAnthropicMessages
	// OpenAI Responses entry identity.
	ProtocolOpenAIResponses = protocol.ProtocolOpenAIResponses
	// Chat Completions entry identity.
	ProtocolOpenAIChatCompletions = protocol.ProtocolOpenAIChatCompletions
	// Gemini GenerateContent entry identity.
	ProtocolGeminiGenerateContent = protocol.ProtocolGeminiGenerateContent
	// SystemOne decision entry identity.
	ProtocolSystemOne = protocol.ProtocolSystemOne
	// OpenAI Embeddings entry identity.
	ProtocolEmbeddings = protocol.ProtocolEmbeddings
	// image generation entry identity.
	ProtocolImagesGenerations = protocol.ProtocolImagesGenerations
	// image editing entry identity.
	ProtocolImagesEdits = protocol.ProtocolImagesEdits
	// batch image job entry.
	ProtocolImageBatches = protocol.ProtocolImageBatches
	// video generation entry identity.
	ProtocolVideosGenerations = protocol.ProtocolVideosGenerations
	// video editing entry identity.
	ProtocolVideosEdits = protocol.ProtocolVideosEdits
	// video extension entry identity.
	ProtocolVideosExtensions = protocol.ProtocolVideosExtensions
	// text-to-speech entry identity.
	ProtocolTTS = protocol.ProtocolTTS
	// speech-to-text entry identity.
	ProtocolSTT = protocol.ProtocolSTT
	// custom voice resource entry.
	ProtocolCustomVoices = protocol.ProtocolCustomVoices
	// realtime voice entry identity.
	ProtocolVoiceRealtime = protocol.ProtocolVoiceRealtime
	// Responses WebSocket entry identity.
	ProtocolResponsesWebSocket = protocol.ProtocolResponsesWebSocket
	ProtocolLive               = protocol.ProtocolLive // OpenAI Live entry identity
	// Responses Compact entry identity.
	ProtocolResponsesCompact = protocol.ProtocolResponsesCompact
	// Alpha Search entry identity.
	ProtocolAlphaSearch = protocol.ProtocolAlphaSearch
	ProtocolWebSearch   = protocol.ProtocolWebSearch // Grok web search entry
	ProtocolXSearch     = protocol.ProtocolXSearch   // Grok X search entry
	// dedicated Qoder upstream identity.
	ProtocolQoderChat = protocol.ProtocolQoderChat
	// dedicated Gemini batch identity.
	ProtocolGeminiBatch = protocol.ProtocolGeminiBatch
	// dedicated Vertex batch identity.
	ProtocolVertexBatch = protocol.ProtocolVertexBatch
)

// protocolCatalog retains the ordered protocol descriptors used by group
// validation, native option ordering, and administrative capability queries.
var protocolCatalog = buildProtocolCatalog()

// Protocol describes one stable protocol identifier and its client adapter
// range.
// @project-doc docs/interfaces/protocol_capabilities.md#protocol_catalog
type Protocol struct {
	ID   ProtocolID `json:"id"`   // stable persisted protocol identifier
	Name string     `json:"name"` // administrative protocol display name
	// UpstreamOnly excludes dedicated execution protocols from
	// canonicalGroupClientProtocols. Provider-native option selection can still
	// include them for batch jobs or platform-specific upstream exchanges.
	UpstreamOnly bool     `json:"upstream_only"`
	Platforms    []string `json:"platforms"` // platforms with corresponding adapters
}

// ProtocolID aliases the protocol package's shared persisted protocol
// identifier type.
type ProtocolID = protocol.ProtocolID

// buildProtocolCatalog returns ordered protocol descriptors with their platform
// adapter ranges. Dedicated Qoder and batch entries are marked upstream-only so
// they remain outside group client admission choices.
func buildProtocolCatalog() []Protocol {
	all := []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformQoder, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformOpenCode}
	openai := []string{PlatformOpenAI}
	grok := []string{PlatformGrok}
	both := []string{PlatformOpenAI, PlatformGrok}
	return []Protocol{
		{ID: ProtocolAnthropicMessages, Name: "Anthropic Messages", Platforms: all},
		{ID: ProtocolOpenAIResponses, Name: "OpenAI Responses", Platforms: all},
		{ID: ProtocolOpenAIChatCompletions, Name: "Chat Completions", Platforms: all},
		{ID: ProtocolGeminiGenerateContent, Name: "Gemini GenerateContent", Platforms: []string{PlatformGemini, PlatformAntigravity, PlatformOpenCode}},
		{ID: ProtocolSystemOne, Name: "SystemOne", Platforms: []string{PlatformJev, PlatformOpenCode}},
		{ID: ProtocolEmbeddings, Name: "Embeddings", Platforms: openai},
		{ID: ProtocolImagesGenerations, Name: "Images Generations", Platforms: both},
		{ID: ProtocolImagesEdits, Name: "Images Edits", Platforms: both},
		{ID: ProtocolImageBatches, Name: "Image Batches", Platforms: []string{PlatformGemini}},
		{ID: ProtocolVideosGenerations, Name: "Video Generations", Platforms: grok},
		{ID: ProtocolVideosEdits, Name: "Video Edits", Platforms: grok},
		{ID: ProtocolVideosExtensions, Name: "Video Extensions", Platforms: grok},
		{ID: ProtocolTTS, Name: "TTS", Platforms: grok},
		{ID: ProtocolSTT, Name: "STT", Platforms: grok},
		{ID: ProtocolCustomVoices, Name: "Custom Voices", Platforms: grok},
		{ID: ProtocolVoiceRealtime, Name: "Voice Realtime", Platforms: grok},
		{ID: ProtocolResponsesWebSocket, Name: "Responses WebSocket", Platforms: both},
		{ID: ProtocolLive, Name: "OpenAI Live", Platforms: openai},
		{ID: ProtocolResponsesCompact, Name: "Responses Compact", Platforms: both},
		{ID: ProtocolAlphaSearch, Name: "Alpha Search", Platforms: openai},
		{ID: ProtocolWebSearch, Name: "Web Search", Platforms: grok},
		{ID: ProtocolXSearch, Name: "X Search", Platforms: grok},
		{ID: ProtocolQoderChat, Name: "Qoder Chat", Platforms: []string{PlatformQoder}, UpstreamOnly: true},
		{ID: ProtocolGeminiBatch, Name: "Gemini Batch GenerateContent", Platforms: []string{PlatformGemini}, UpstreamOnly: true},
		{ID: ProtocolVertexBatch, Name: "Vertex Batch Prediction", Platforms: []string{PlatformGemini}, UpstreamOnly: true},
	}
}

// NativeProtocolOptions returns a new catalogue-ordered list supported by a
// platform, provider type, and OpenAI authentication mode. Unsupported
// combinations produce an allocated empty list; enabled provider selections are
// applied separately.
func NativeProtocolOptions(platform, providerType, authMode string) []ProtocolID {
	var selected []ProtocolID
	switch platform {
	case PlatformOpenCode:
		if providerType == ProviderTypeAPIKey {
			selected = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolGeminiGenerateContent, ProtocolSystemOne}
		}
	case PlatformJev:
		if providerType == ProviderTypeAPIKey {
			selected = []ProtocolID{ProtocolSystemOne}
		}
	case PlatformAnthropic:
		if slices.Contains([]string{ProviderTypeOAuth, ProviderTypeSetupToken, ProviderTypeAPIKey, ProviderTypeBedrock, ProviderTypeServiceAccount}, providerType) {
			selected = []ProtocolID{ProtocolAnthropicMessages}
		}
	case PlatformOpenAI:
		switch providerType {
		case ProviderTypeAPIKey:
			selected = []ProtocolID{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolEmbeddings, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolResponsesWebSocket, ProtocolResponsesCompact, ProtocolAlphaSearch}
		case ProviderTypeOAuth:
			selected = []ProtocolID{ProtocolOpenAIResponses, ProtocolResponsesWebSocket, ProtocolResponsesCompact}
			if authMode != "personalAccessToken" {
				selected = append(selected, ProtocolAlphaSearch)
			}
			if authMode != "personalAccessToken" && authMode != "agentIdentity" {
				selected = append(selected, ProtocolLive)
			}
		}
	case PlatformKimi:
		if providerType == ProviderTypeAPIKey {
			selected = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIChatCompletions, ProtocolOpenAIResponses}
		}
	case PlatformDeepseek:
		if providerType == ProviderTypeAPIKey {
			selected = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIChatCompletions, ProtocolOpenAIResponses}
		}
	case PlatformZhipu:
		if providerType == ProviderTypeAPIKey {
			selected = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIChatCompletions}
		}
	case PlatformGemini:
		switch providerType {
		case ProviderTypeOAuth:
			selected = []ProtocolID{ProtocolGeminiGenerateContent}
		case ProviderTypeAPIKey:
			selected = []ProtocolID{ProtocolGeminiGenerateContent, ProtocolGeminiBatch}
		case ProviderTypeServiceAccount:
			selected = []ProtocolID{ProtocolGeminiGenerateContent, ProtocolVertexBatch}
		}
	case PlatformAntigravity:
		if providerType == ProviderTypeOAuth {
			selected = []ProtocolID{ProtocolGeminiGenerateContent}
		}
	case PlatformQoder:
		if providerType == ProviderTypeCosy {
			selected = []ProtocolID{ProtocolQoderChat}
		}
	case PlatformGrok:
		if providerType == ProviderTypeAPIKey || providerType == ProviderTypeOAuth {
			selected = []ProtocolID{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolVideosGenerations, ProtocolVideosEdits, ProtocolVideosExtensions, ProtocolTTS, ProtocolSTT, ProtocolCustomVoices, ProtocolVoiceRealtime}
		}
	}
	out := []ProtocolID{}
	for _, protocol := range protocolCatalog {
		if slices.Contains(selected, protocol.ID) {
			out = append(out, protocol.ID)
		}
	}
	return out
}

// ProtocolFallbackTargets returns implemented single-step target choices for a
// platform and client source. It excludes the source itself and returns an
// allocated empty list when the platform has no corresponding client adapter.
func ProtocolFallbackTargets(platform string, source ProtocolID) []ProtocolID {
	if !platformSupportsClientProtocol(platform, source) {
		return []ProtocolID{}
	}
	var targets []ProtocolID
	switch source {
	case ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions:
		switch platform {
		case PlatformAnthropic:
			targets = []ProtocolID{ProtocolAnthropicMessages}
		case PlatformOpenAI, PlatformGrok:
			targets = []ProtocolID{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}
		case PlatformOpenCode:
			targets = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolGeminiGenerateContent}
		case PlatformKimi:
			targets = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}
		case PlatformDeepseek:
			targets = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}
		case PlatformZhipu:
			targets = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIChatCompletions}
		case PlatformGemini, PlatformAntigravity:
			targets = []ProtocolID{ProtocolGeminiGenerateContent}
		case PlatformQoder:
			targets = []ProtocolID{ProtocolQoderChat}
		}
	case ProtocolImagesGenerations, ProtocolImagesEdits:
		if platform == PlatformOpenAI {
			targets = []ProtocolID{ProtocolOpenAIResponses}
		}
	case ProtocolResponsesWebSocket, ProtocolAlphaSearch, ProtocolWebSearch, ProtocolXSearch:
		targets = []ProtocolID{ProtocolOpenAIResponses}
	case ProtocolResponsesCompact:
		if platform == PlatformGrok {
			targets = []ProtocolID{ProtocolOpenAIResponses}
		}
	}
	out := []ProtocolID{}
	for _, target := range targets {
		if target != source {
			out = append(out, target)
		}
	}
	return out
}

// SupportsProtocolConversion reports whether the supplied platform,
// authentication, source, and target have an implemented conversion. OAuth
// images and PAT Alpha Search have dedicated authentication checks; other
// targets require native-option support.
func SupportsProtocolConversion(platform, providerType, authMode string, source, target ProtocolID) bool {
	if !slices.Contains(ProtocolFallbackTargets(platform, source), target) {
		return false
	}
	// OAuth image requests execute through Responses; API-key image selections use
	// their native image endpoints, so this conversion is reserved for OAuth.
	if source == ProtocolImagesGenerations || source == ProtocolImagesEdits {
		return platform == PlatformOpenAI && providerType == ProviderTypeOAuth
	}
	// PAT lacks the native Alpha Search option and uses its hosted-search
	// Responses path. Other authentication modes do not acquire that conversion
	// here.
	if source == ProtocolAlphaSearch {
		return platform == PlatformOpenAI && providerType == ProviderTypeOAuth && authMode == "personalAccessToken"
	}
	return slices.Contains(NativeProtocolOptions(platform, providerType, authMode), target)
}

// ProtocolCatalog copies the descriptor slice and every Platforms slice for
// caller mutation. Strings and identifiers retain their values, and changes to
// the returned platform lists leave the retained catalogue unchanged.
func ProtocolCatalog() []Protocol {
	out := slices.Clone(protocolCatalog)
	for i := range out {
		out[i].Platforms = slices.Clone(out[i].Platforms)
	}
	return out
}

// platformSupportsClientProtocol reports whether a catalogue entry lists the
// supplied platform as an adapter. UpstreamOnly is not considered here; callers
// select the appropriate client source before checking.
func platformSupportsClientProtocol(platform string, source ProtocolID) bool {
	for _, entry := range protocolCatalog {
		if entry.ID == source {
			return slices.Contains(entry.Platforms, platform)
		}
	}
	return false
}

// AutomaticProtocolFallbackTargets returns unique conversion targets in a
// deterministic traversal derived from ProviderPlatforms with OpenAI ordered
// first. First appearance determines target order, giving automatic mixed-group
// resolution a stable sequence across repeated catalogue queries.
func AutomaticProtocolFallbackTargets(source ProtocolID) []ProtocolID {
	platforms := make([]string, 0, len(ProviderPlatforms()))
	platforms = append(platforms, PlatformOpenAI)
	for _, platform := range ProviderPlatforms() {
		if platform != PlatformOpenAI {
			platforms = append(platforms, platform)
		}
	}
	targets := []ProtocolID{}
	for _, platform := range platforms {
		for _, target := range ProtocolFallbackTargets(platform, source) {
			if !slices.Contains(targets, target) {
				targets = append(targets, target)
			}
		}
	}
	return targets
}
