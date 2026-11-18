package ws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
)

// service_tier_frame.go applies service-tier decisions to response.create
// frames and renders policy rejection events.
// WebSocket response.create carries the same top-level service_tier choices
// used by HTTP Fast policy. ApplyServiceTierFrame validates reasoning first,
// then asks tierpolicy to pass, normalize, remove, force, or block that tier;
// rejected choices can be rendered as a correlated Realtime-style error event.

// ApplyServiceTierFrame applies Fast policy to a response.create using the
// turn's resolved upstream model. Empty or invalid JSON passes through; valid
// JSON first receives reasoning validation, including for other frame types.
//
// Only response.create enters tier selection. tierpolicy.Resolve interprets the
// supplied policy inputs and tier presence, returning a block, field removal,
// or accepted tier. The returned block is separate from encoding errors so the
// caller can emit a policy event and close locally. Callers supply
// model-resolved DecisionInput callbacks: Evaluate and KeyPolicy are required
// when their Resolve paths run, and CanForceTier checks a tier forced by the key.
// The caller forwards the accepted frame or writes the selected policy event.
func ApplyServiceTierFrame(frame []byte, model string, input tierpolicy.DecisionInput) ([]byte, *tierpolicy.BlockedError, error) {
	if len(frame) == 0 {
		return frame, nil, nil
	}
	if !gjson.ValidBytes(frame) {
		return frame, nil, nil
	}
	// Valid frames share reasoning validation before event dispatch, so even a
	// non-response.create frame can fail this local check before passthrough.
	if err := requeststate.ValidateOpenAIReasoningEffort(frame, model); err != nil {
		return frame, nil, err
	}
	frameType := strings.TrimSpace(gjson.GetBytes(frame, "type").String())
	// Tier mutation applies to response.create; other valid frame types retain
	// their bytes after reasoning validation so upstream can process their
	// event-specific fields.
	if frameType != "response.create" {
		return frame, nil, nil
	}
	tierResult := gjson.GetBytes(frame, "service_tier")
	decision := tierpolicy.Resolve(input, tierResult.String(), tierResult.Exists())
	if decision.Blocked != nil {
		return frame, decision.Blocked, nil
	}
	// Filtering or force_off removes the requested tier, letting
	// upstream apply its omitted-field behavior after the policy decision succeeds.
	if decision.DeleteField {
		trimmed, err := sjson.DeleteBytes(frame, "service_tier")
		if err != nil {
			return frame, nil, fmt.Errorf("strip service_tier from ws frame: %w", err)
		}
		return trimmed, nil, nil
	}
	// Write the selected canonical or forced tier into outgoing bytes, so the
	// provider receives the accepted policy value even when the client used an alias.
	if decision.Tier != "" && (!tierResult.Exists() || decision.Tier != tierResult.String()) {
		updated, err := sjson.SetBytes(frame, "service_tier", decision.Tier)
		if err != nil {
			return frame, nil, fmt.Errorf("apply service_tier in ws frame: %w", err)
		}
		return updated, nil, nil
	}
	return frame, nil, nil
}

// newOpenAIFastPolicyWSEventID returns an evt_-prefixed correlation identifier
// for a local rejection. Random generation failure uses the fixed
// evt_openai_fast_policy label, so uniqueness is conditional on successful UUID
// creation.
func newOpenAIFastPolicyWSEventID() string {
	id, err := uuid.NewRandom()
	if err != nil {
		// The client error still needs an event_id when randomness fails; use a fixed
		// nonempty label while giving up per-event uniqueness on that path.
		return "evt_openai_fast_policy"
	}
	// Use a compact hexadecimal UUID after the event prefix.
	return "evt_" + strings.ReplaceAll(id.String(), "-", "")
}

// BuildFastPolicyBlockedEvent encodes a policy rejection as a Realtime-style
// error with event_id. Nil err returns nil; the stable policy_violation code
// and supplied message let clients classify the locally blocked request.
func BuildFastPolicyBlockedEvent(err *tierpolicy.BlockedError) []byte {
	if err == nil {
		return nil
	}
	eventID := newOpenAIFastPolicyWSEventID()
	payload, mErr := json.Marshal(map[string]any{
		"event_id": eventID,
		"type":     "error",
		"error": map[string]any{
			"type":    "invalid_request_error",
			"code":    "policy_violation",
			"message": err.Message,
		},
	})
	if mErr != nil {
		// If encoding fails, return the same stable policy code and correlation
		// identifier with a fixed safe message so rejection presentation can still
		// proceed.
		return []byte(`{"event_id":"` + eventID + `","type":"error","error":{"type":"invalid_request_error","code":"policy_violation","message":"openai fast policy blocked this request"}}`)
	}
	return payload
}
