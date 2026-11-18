package tierpolicy

import (
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// decision.go resolves client, group, system, and API Key service-tier choices
// for request execution. A system action on the initial tier ends the decision
// before key policy or pricing eligibility is read, and a group force_off also
// stops a later key override.
//
// When the system passes the initial tier, an eligible key may inject priority
// or ultrafast, or remove an accelerated request. A newly injected tier is
// checked against system policy again so the same block or filter rules apply
// to tiers introduced by either key override. The result keeps rejection,
// field removal, and tier replacement distinct for HTTP and WebSocket callers.

// DecisionInput supplies platform and group policy along with lazy rule and
// pricing callbacks for the shared HTTP and WebSocket decision.
type DecisionInput struct {
	Model       string // model used for policy diagnostics
	GroupPolicy string // selected group Fast policy
	OpenAI      bool   // provider is an OpenAI target
	// Evaluate, KeyPolicy, and CanForceTier are invoked lazily in policy order.
	// Callers supply each callback that the selected branch can reach. Early system
	// and group decisions can avoid both the key read and its eligibility check.
	Evaluate     func(string) (string, string) // system action and message reader
	KeyPolicy    func() string                 // authenticated key policy reader
	CanForceTier func(string) bool             // forced tier eligibility check
}

// Decision describes the service tier to write, remove, or reject.
type Decision struct {
	// Tier is a nonempty replacement when selected. An empty tier with neither
	// DeleteField nor Blocked leaves the caller's existing JSON field unchanged.
	Tier        string        // selected normalized tier value
	DeleteField bool          // remove supplied service_tier field
	Blocked     *BlockedError // client-facing policy rejection error
}

// IsAcceleratedTier reports whether the tier is Fast or Ultra Fast.
func IsAcceleratedTier(tier string) bool {
	return tier == OpenAIFastTierPriority || tier == OpenAIFastTierUltrafast
}

// Resolve selects a tier replacement, field removal, or policy rejection from
// the client, group, system, and key inputs. Callers provide callbacks for
// reachable policy reads; hasField records whether removal has an existing
// field to target.
//
// Group forcing precedes the first system check. A handled system action ends
// resolution, while a passing action permits group force_off or key policy to
// run. Key force_on retains a group-forced ultrafast tier. Key force_ultrafast
// can replace a passing tier when the target supports it. Any new tier from
// a key override receives a second system check before being returned.
func Resolve(input DecisionInput, rawTier string, hasField bool) Decision {
	normTier := openai.ServiceTierValue(rawTier)
	groupPolicy := input.GroupPolicy
	switch groupPolicy {
	case routing.GroupOpenAIFastPolicyForcePriority:
		normTier, hasField = OpenAIFastTierPriority, true
	case routing.GroupOpenAIFastPolicyForceUltrafast:
		normTier, hasField = OpenAIFastTierUltrafast, true
	}
	applySystemAction := func(tier string) (Decision, bool) {
		action, errMsg := input.Evaluate(tier)
		switch action {
		case "block":
			if errMsg == "" {
				errMsg = fmt.Sprintf("openai service_tier=%s is not allowed for model %s", tier, input.Model)
			}
			return Decision{Blocked: &BlockedError{Message: errMsg}}, true
		case "filter":
			return Decision{DeleteField: true}, true
		case OpenAIFastPolicyActionForcePriority:
			return Decision{Tier: OpenAIFastTierPriority}, true
		case OpenAIFastPolicyActionForceUltrafast:
			return Decision{Tier: OpenAIFastTierUltrafast}, true
		default:
			return Decision{}, false
		}
	}

	// Return a handled system action before reading key settings, so its block or
	// filter determines this request even when the key would enable acceleration.
	if normTier != "" {
		if decision, handled := applySystemAction(normTier); handled {
			return decision
		}
	}

	// Stop at the group prohibition after system evaluation; key policy and its
	// pricing eligibility read cannot reopen acceleration for this decision.
	if groupPolicy == routing.GroupOpenAIFastPolicyForceOff {
		if IsAcceleratedTier(normTier) {
			return Decision{DeleteField: hasField}
		}
		return Decision{Tier: normTier}
	}
	candidateTier := normTier
	switch input.KeyPolicy() {
	case apikey.APIKeyFastModePolicyForceOn:
		// Keep the group's higher tier when the key asks for priority, even
		// when this target would otherwise qualify for the key override.
		if input.CanForceTier(OpenAIFastTierPriority) && groupPolicy != routing.GroupOpenAIFastPolicyForceUltrafast {
			candidateTier = OpenAIFastTierPriority
		}
	case apikey.APIKeyFastModePolicyForceUltrafast:
		if input.CanForceTier(OpenAIFastTierUltrafast) {
			candidateTier = OpenAIFastTierUltrafast
		}
	case apikey.APIKeyFastModePolicyForceOff:
		// Remove OpenAI accelerated tiers without disturbing other recognized
		// tiers such as flex and scale or another provider's tier handling.
		if input.OpenAI && IsAcceleratedTier(normTier) {
			return Decision{DeleteField: hasField}
		}
	}

	// Evaluate a newly introduced tier against the same system rules, so
	// forcing it through a key applies any tier-specific block or filter.
	if candidateTier != normTier {
		if decision, handled := applySystemAction(candidateTier); handled {
			return decision
		}
	}
	return Decision{Tier: candidateTier}
}

// ApplyBody applies the resolved Fast policy to the service_tier JSON field
// and writes canonical labels such as priority for fast. Empty bodies bypass
// all callbacks; blocking or JSON mutation errors return the supplied body
// with an error.
func ApplyBody(body []byte, input DecisionInput) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	tierResult := gjson.GetBytes(body, "service_tier")
	decision := Resolve(input, tierResult.String(), tierResult.Exists())
	if decision.Blocked != nil {
		return body, decision.Blocked
	}
	if decision.DeleteField {
		trimmed, err := sjson.DeleteBytes(body, "service_tier")
		if err != nil {
			return body, fmt.Errorf("strip service_tier from body: %w", err)
		}
		return trimmed, nil
	}
	if decision.Tier != "" && (!tierResult.Exists() || decision.Tier != tierResult.String()) {
		updated, err := sjson.SetBytes(body, "service_tier", decision.Tier)
		if err != nil {
			return body, fmt.Errorf("apply service_tier to body: %w", err)
		}
		return updated, nil
	}
	return body, nil
}
