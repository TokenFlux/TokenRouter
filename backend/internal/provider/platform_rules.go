package provider

import (
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// platform_rules.go exposes platform-owned text, usage, health, and billing
// choices to shared provider workflows. Each platform declares its own values;
// consumers request the policy needed for their operation instead of inferring
// unrelated behavior from membership in a supplier group. Missing declarations
// leave optional policies disabled and preserve existing dedicated executors.
//
// Text mode reads and save validation have different compatibility obligations.
// Historical records can retain a readable mode that new writes must reject,
// so mode entries carry both endpoint data and an optional validation error.
// The registry stays private and immutable after initialization. Callers read
// policy values or request an operation without access to the underlying
// declarations, so shared maps and slices cannot be changed through a lookup.

// platformRuleRegistry binds platform identities to independently declared policies.
// Unlisted platforms retain their existing dedicated credential and execution paths.
var platformRuleRegistry = map[string]platformRules{
	PlatformKimi:     kimiRules(),
	PlatformZhipu:    zhipuRules(),
	PlatformDeepseek: deepseekRules(),
	PlatformOpenAI: {
		countForbidden:        true,
		repairToolSchema:      true,
		apiKeyHeaderOverrides: true,
	},
	PlatformAnthropic: {
		messagesAuthOverride:  true,
		repairToolSchema:      true,
		apiKeyHeaderOverrides: true,
	},
	PlatformGrok: {
		repairToolSchema:      true,
		apiKeyHeaderOverrides: true,
		oauthHeaderOverrides:  true,
	},
	PlatformJev:      {apiKeyHeaderOverrides: true},
	PlatformOpenCode: {apiKeyHeaderOverrides: true},
}

// platformRules holds independent choices consumed by provider workflow owners.
// @project-doc docs/interfaces/upstream_provider_matrix.md#cn_provider_protocols
type platformRules struct {
	text                     *textEndpointRules   // optional mode and endpoint rules
	usage                    usageMonitoringRules // automatic query and monitoring rules
	countForbidden           bool                 // count structured forbidden failures
	temporaryBalanceCooldown bool                 // recoverable balance failure policy
	explicitClaudePricing    bool                 // require configured Claude alias prices
	messagesAuthOverride     bool                 // permit Messages authentication override
	apiKeyTLSFingerprint     bool                 // permit API-key TLS profiles
	repairToolSchema         bool                 // repair compatible tool schemas
	apiKeyHeaderOverrides    bool                 // allow API-key Header overrides
	oauthHeaderOverrides     bool                 // allow OAuth Header overrides

	// prepareChatBody adapts the outbound Chat payload for this platform. A nil
	// function leaves the supplied body available to the sender as-is.
	prepareChatBody func([]byte) ([]byte, error)
}

// textEndpointRules describes legacy selection and endpoint defaults for text APIs.
type textEndpointRules struct {
	// defaultMode identifies the entry selected when a stored mode is absent or
	// unknown. modes retains readable historical entries, including modes whose
	// save restrictions are declared alongside their endpoint values.
	defaultMode        string                   // absent or unknown read mode
	modes              map[string]textModeRules // exact recognized access modes
	statelessResponses bool                     // discard stored Responses continuation state
	// responsesPath overrides the native operation suffix; an empty value selects
	// the versioned OpenAI path through ResponsesOperationPath before URL joining.
	responsesPath        string
	responsesClientTools bool // encode Responses client-tool envelopes
}

// textModeRules keeps one mode's endpoints, usage format, and write restrictions.
type textModeRules struct {
	chatBase       string // Chat protocol default root
	messagesBase   string // Messages protocol default root
	responsesBase  string // Responses protocol default root
	usageAdapter   string // fixed normalized usage adapter
	codingProvider string // coding quota supplier identity
	// rejectionCode and rejectionMessage reject writes after legacy protocol
	// validation. Empty values allow the mode while preserving historical read defaults.
	rejectionCode    string // persisted API error reason
	rejectionMessage string // public invalid-mode diagnostic
}

// usageMonitoringRules declares eligibility and official hosts for periodic queries.
type usageMonitoringRules struct {
	enabled       bool     // permits periodic usage monitoring
	fixedAdapter  bool     // platform selects usage format
	officialHosts []string // trusted automatic query hosts
}

// rules returns the immutable rules for this record's exact platform spelling.
// Nil records and unregistered platforms return zero rules, leaving all optional
// policies disabled for their dedicated handlers.
func (r *Record) rules() platformRules {
	if r == nil {
		return platformRules{}
	}
	return platformRuleRegistry[r.Platform]
}

// HasProtocolEndpointDefaults reports whether the platform supplies text
// endpoint slots and mode defaults. Nil and undeclared records return false.
// This is an address-interpretation capability; callers separately validate
// credential type and enabled protocols.
func (r *Record) HasProtocolEndpointDefaults() bool {
	return r.rules().text != nil
}

// PrepareChatRequestBody applies the platform's wire policy to a Chat
// Completions payload after that upstream operation has been selected. A nil
// record or an unconfigured policy returns the supplied bytes. Preparation
// errors stop request construction in the caller.
func (r *Record) PrepareChatRequestBody(body []byte) ([]byte, error) {
	prepare := r.rules().prepareChatBody
	if prepare == nil {
		return body, nil
	}
	return prepare(body)
}

// RequiresStatelessResponses reports whether the platform forbids stored Responses continuation.
// It checks platform policy independently of credential type or enabled protocols;
// operation-level routing applies those checks before execution.
func (r *Record) RequiresStatelessResponses() bool {
	rules := r.rules().text
	return rules != nil && rules.statelessResponses
}

// SupportsAPIKeyResponses reports the platform's declared API-key Responses capability.
// Stored credential type and enabled protocol selections are deliberately checked
// by route admission; this lookup describes the platform option before selection.
func (r *Record) SupportsAPIKeyResponses() bool {
	if r == nil {
		return false
	}
	return slices.Contains(capability.NativeProtocolOptions(r.Platform, ProviderTypeAPIKey, ""), capability.ProtocolOpenAIResponses)
}

// ResponsesOperationPath returns the platform's native Responses operation suffix.
// Platforms without an override use the versioned OpenAI path. Endpoint builders
// remain responsible for joining it to configured roots with deployment prefixes.
func ResponsesOperationPath(platform string) string {
	if rules := platformRuleRegistry[platform].text; rules != nil && rules.responsesPath != "" {
		return rules.responsesPath
	}
	return "/v1/responses"
}

// UsesCountedForbiddenPolicy selects shared counters for structured forbidden
// failures and their successful-request reset. Nil and undeclared records
// return false. The selected health handler still classifies the upstream
// response before updating state.
func (r *Record) UsesCountedForbiddenPolicy() bool {
	return r.rules().countForbidden
}

// UsesTemporaryBalanceCooldown selects recoverable pauses for payment-required
// or recognized balance failures. Nil and undeclared records return false.
// Response classification decides whether a particular failure is eligible for
// this policy.
func (r *Record) UsesTemporaryBalanceCooldown() bool {
	return r.rules().temporaryBalanceCooldown
}

// RequiresExplicitClaudePricing selects explicit pricing for compatible Claude
// model aliases during completion. Nil and undeclared records return false.
// Ordinary model prices continue through the normal resolver regardless of
// this flag.
func (r *Record) RequiresExplicitClaudePricing() bool {
	return r.rules().explicitClaudePricing
}

// SupportsUsageMonitoring reports whether the platform permits periodic usage observations.
// Record status, API-key type, adapter availability, and query enablement remain
// separate admission requirements checked before any background request.
func (r *Record) SupportsUsageMonitoring() bool {
	return r.rules().usage.enabled
}

// SupportsToolSchemaRepair selects shared schema cleanup for the platform's
// tool requests. Unknown platforms return false. The caller retains
// responsibility for choosing the request protocol and applying any
// platform-specific schema restrictions.
func SupportsToolSchemaRepair(platform string) bool {
	return platformRuleRegistry[platform].repairToolSchema
}

// usageMonitorPlatforms returns monitored platforms in the supported catalogue order.
// A fresh slice is returned on every call. Query mode and record-level
// eligibility are evaluated separately after providers are loaded.
func usageMonitorPlatforms() []string {
	var result []string
	for _, platform := range capability.ProviderPlatforms() {
		if platformRuleRegistry[platform].usage.enabled {
			result = append(result, platform)
		}
	}
	return result
}

// textMode returns the endpoint policy for the record's interpreted access mode.
// Missing platform declarations return zero values. GetProviderMode applies the
// platform's historical read default before this exact mode lookup.
func (r *Record) textMode() textModeRules {
	rules := r.rules().text
	if rules == nil {
		return textModeRules{}
	}
	return rules.modes[r.GetProviderMode()]
}

// DefaultTextAPIKeyProtocolBaseURL returns this mode's default root for a legacy protocol.
// Unknown protocol spellings and platforms without endpoint declarations return
// empty text, allowing the caller to retain its dedicated lookup path.
func (r *Record) DefaultTextAPIKeyProtocolBaseURL(protocol string) string {
	mode := r.textMode()
	switch protocol {
	case APIProtocolAnthropic:
		return mode.messagesBase
	case APIProtocolChatCompletions:
		return mode.chatBase
	case APIProtocolResponses:
		return mode.responsesBase
	default:
		return ""
	}
}

// IsDefaultTextAPIKeyMessagesBaseURL recognizes a declared Messages root across modes.
// Surrounding whitespace and trailing slashes are ignored. Comparison otherwise
// remains exact, including case and path, for legacy endpoint migration.
func IsDefaultTextAPIKeyMessagesBaseURL(baseURL string) bool {
	normalized := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	for _, rules := range platformRuleRegistry {
		if rules.text == nil {
			continue
		}
		for _, mode := range rules.text.modes {
			if mode.messagesBase != "" && normalized == mode.messagesBase {
				return true
			}
		}
	}
	return false
}
