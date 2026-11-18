package forward

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// response_observer.go collects upstream model declarations and reconciles
// service-tier observations for each attempt or turn. Upstream model and
// service-tier declarations answer different completion questions. Model
// evidence is retained before protocol conversion changes the client's visible
// metadata, while tier evidence is reconciled later with the tier sent
// upstream. Typed nonterminal Responses events can echo a requested tier, so
// OpenAI tier observation accepts terminal or untyped payloads that also
// contain a usable model declaration.
//
// Terminal tier declarations take precedence over earlier nonterminal values.
// Conflicting nonterminal tiers leave no selected tier until terminal evidence
// arrives; Anthropic speed observations use that nonterminal rule even when a
// later usage event has no model. One observer belongs to an attempt or turn,
// and callers serialize access to its mutable state. ResetModel resets model
// evidence while retaining all tier evidence in that same instance.

// ResponseObserver records model and tier evidence for one forwarding attempt or turn.
type ResponseObserver struct {
	model protocol.ResponseModelObserver // protocol model declaration observer

	// firstTier and firstTierConflict retain nonterminal tier agreement, while
	// terminalTier supplies the latest preferred declaration. Conflict remains
	// recorded even if later nonterminal events repeat the first value.
	firstTier         string // first nonterminal tier declaration
	firstTierConflict bool   // conflicting nonterminal tier evidence
	terminalTier      string // latest terminal tier declaration
}

// Observe delegates model evidence to the protocol observer using the caller's
// terminal classification. A nil receiver is ignored; callers serialize updates
// and use a fresh observer for each unrelated attempt or turn.
func (o *ResponseObserver) Observe(model string, terminal bool) {
	if o != nil {
		o.model.Observe(model, terminal)
	}
}

// ObserveOpenAI records a usable OpenAI model and eligible service-tier evidence
// from payload. Supply a nonnil observer when the payload can declare a model,
// and pass the event classification selected by the output adapter.
//
// Model evidence follows the protocol observer's Responses-versus-Chat rules.
// Tier evidence needs a model declaration and is ignored for typed nonterminal
// events, which can echo the request's desired tier. Terminal events and untyped
// Chat or unary payloads can provide normalized tier evidence, allowing billing
// reconciliation to use a declared execution tier rather than a progress echo.
func (o *ResponseObserver) ObserveOpenAI(payload []byte, eventType string) {
	model := firstValidTrimmedGJSONModel(payload, "response.model", "model")
	terminal := isUpstreamResponseModelTerminalEvent(eventType)
	// Model-free frames cannot establish the executed tier for this observer's
	// billing evidence. Require a usable model in the same payload before retaining
	// either OpenAI observation here.
	if model == "" {
		return
	}
	o.model.ObserveOpenAI(payload, eventType)
	// Typed nonterminal Responses events can echo the requested tier. Accept
	// tier evidence from terminal events and untyped Chat or unary payloads so
	// progress echoes cannot decide the later billing reconciliation.
	if !terminal && strings.TrimSpace(eventType) != "" {
		return
	}
	tier := NormalizeObservedOpenAIServiceTier(firstValidTrimmedGJSONModel(payload, "response.service_tier", "service_tier"))
	o.ObserveServiceTier(tier, terminal)
}

// ObserveAnthropic records the first usable Messages model and normalized fast
// or standard speed evidence. Speed can arrive in later usage events without
// a model; both values are observed as nonterminal, and a nil receiver is ignored.
func (o *ResponseObserver) ObserveAnthropic(payload []byte) {
	model := firstValidTrimmedGJSONModel(payload, "message.model", "model")
	if model != "" {
		o.Observe(model, false)
	}
	tier := normalizeObservedAnthropicSpeed(firstValidTrimmedGJSONModel(payload, "message.usage.speed", "usage.speed"))
	o.ObserveServiceTier(tier, false)
}

// ObserveServiceTier retains the supplied tier using the caller's terminal
// classification. Callers provide an already interpreted nonempty tier and
// serialize updates; a nil observer or empty tier makes no state change.
//
// A terminal declaration replaces earlier terminal evidence and wins over
// nonterminal disagreement. Otherwise the first tier is retained, and any
// different later nonterminal tier records a conflict that remains until
// terminal evidence supplies a preferred value. ServiceTier returns empty for
// that unresolved conflict so completion can apply its own missing-evidence rule.
func (o *ResponseObserver) ObserveServiceTier(tier string, terminal bool) {
	if o == nil || tier == "" {
		return
	}
	if terminal {
		// Terminal evidence describes the completed execution and supersedes
		// nonterminal echoes or disagreement. Retain the latest such declaration
		// for the completion reader's preferred tier.
		o.terminalTier = tier
		return
	}
	if o.firstTier == "" {
		o.firstTier = tier
		return
	}
	if o.firstTier != tier {
		o.firstTierConflict = true
	}
}

// ServiceTier returns the latest terminal tier when one is recorded, otherwise
// the first nonterminal tier when its observations agree. A nil observer or an
// unresolved nonterminal conflict returns an empty string.
//
// Terminal precedence applies even when conflicting earlier values were
// observed. Without terminal evidence, choosing either side of a disagreement
// would invent a selected tier, so the empty result leaves completion's
// credential-specific reconciliation responsible for absent evidence. The
// returned value reflects the state at this read; callers serialize reads with updates.
func (o *ResponseObserver) ServiceTier() string {
	if o == nil {
		return ""
	}
	// A terminal declaration supplies preferred execution evidence. Without it,
	// retain a nonterminal tier only when observations agree, leaving unresolved
	// disagreement to completion's missing-evidence handling.
	if o.terminalTier != "" {
		return o.terminalTier
	}
	if o.firstTierConflict {
		return ""
	}
	return o.firstTier
}

// NormalizeObservedOpenAIServiceTier trims and case-folds observed tier text,
// accepting the reported Standard, Flex, Fast, and Ultrafast tiers. The fast
// alias maps to priority, and unknown values return no usable tier.
func NormalizeObservedOpenAIServiceTier(raw string) string {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "priority", "fast":
		// Completion uses priority as its internal Fast billing name. Normalize
		// the accepted fast spelling here so observed aliases share that tier representation.
		return tierpolicy.OpenAIFastTierPriority
	case "default", "flex", "scale", tierpolicy.OpenAIFastTierUltrafast:
		return value
	default:
		return ""
	}
}

// normalizeObservedAnthropicSpeed returns fast or standard after trimming and
// case folding. Empty and unknown values supply no tier evidence; the observer
// retains only these recognized Anthropic processing modes.
func normalizeObservedAnthropicSpeed(raw string) string {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "fast", "standard":
		return value
	default:
		return ""
	}
}

// Model returns the preferred captured model from the protocol observer.
// A nil receiver or absent usable declaration returns empty, letting request
// audit and completion distinguish missing evidence from the sent request model.
func (o *ResponseObserver) Model() string {
	if o == nil {
		return ""
	}
	return o.model.Model()
}

// firstValidTrimmedGJSONModel returns the first usable string from the supplied
// paths through protocol.ResponseModelString. Invalid JSON, missing fields, and
// nonstring or blank values return empty; model and tier readers share this check.
func firstValidTrimmedGJSONModel(payload []byte, paths ...string) string {
	return protocol.ResponseModelString(payload, paths...)
}

// isUpstreamResponseModelTerminalEvent delegates Responses terminal-name recognition
// to the protocol helper. Trimmed completion, failure, incomplete, and both
// cancellation spellings qualify; other event names remain nonterminal for observation.
func isUpstreamResponseModelTerminalEvent(event string) bool {
	return protocol.IsResponseModelTerminalEvent(event)
}

// ResetModel clears this observer's model evidence and leaves its tier history
// intact. A nil receiver is ignored; callers needing independent tier evidence
// for a different attempt or turn create a fresh observer.
func (o *ResponseObserver) ResetModel() {
	if o != nil {
		o.model = protocol.ResponseModelObserver{}
	}
}

// ObserveOpenAIModel records model declarations for paths that do not collect service tiers.
// It uses the protocol observer's event rules and ignores a nil receiver.
// Existing service-tier observations stay available because this operation updates
// model evidence alone.
func (o *ResponseObserver) ObserveOpenAIModel(payload []byte, event string) {
	if o != nil {
		o.model.ObserveOpenAI(payload, event)
	}
}
