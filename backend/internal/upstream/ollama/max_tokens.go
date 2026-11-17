package ollama

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// max_tokens.go resolves Ollama output caps and clamps supplied numeric token
// limits before forwarding.
// Ollama output caps are resolved from provider configuration using the model
// catalog's shared numeric rules. Request clamping changes supplied numeric
// output-limit fields when their integer interpretation exceeds a positive cap,
// leaving missing fields available for the upstream's own default.

// The cap aliases connect provider Extra lookup and request clamping to the
// model catalog's shared output-limit configuration and execution default.
const (
	// MaxTokensCapExtraKey: provider cap setting key.
	MaxTokensCapExtraKey = modelrules.OllamaOutputCapExtraKey
	// DefaultMaxTokensCap: default output token count.
	DefaultMaxTokensCap = modelrules.OllamaDefaultOutputLimit
)

// MaxTokensCap resolves provider output-cap configuration through modelcatalog.
// Missing values, unsupported types, and unreadable integer json.Number values
// select DefaultMaxTokensCap. Accepted nonpositive values disable clamping;
// float64 values use Go's int64 conversion, truncating fractions toward zero
// when representable and performing no range validation here.
func MaxTokensCap(value any, present bool) int64 {
	return modelrules.OllamaOutputCap(value, present)
}

// ClampMaxTokens lowers numeric max_tokens and max_completion_tokens fields
// whose gjson integer interpretation exceeds cap, and reports whether either
// field changed. Nonpositive caps, invalid JSON, and update errors return the
// supplied body with false; missing, nonnumeric, and integer-within-cap values
// remain as supplied. For example, max_tokens=10.9 remains unchanged at cap=10
// because its integer interpretation is ten.
func ClampMaxTokens(body []byte, cap int64) ([]byte, bool) {
	if cap <= 0 || !gjson.ValidBytes(body) {
		return body, false
	}
	clamped := false
	out := body
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		result := gjson.GetBytes(out, key)
		if !result.Exists() || result.Type != gjson.Number || result.Int() <= cap {
			continue
		}
		// Apply the cap to either supported output-limit spelling after
		// the integer comparison, so both request representations share
		// the same configured threshold.
		updated, err := sjson.SetBytes(out, key, cap)
		if err != nil {
			// An update failure cannot supply a completed clamp
			// result. Return the provided body and clear the
			// changed flag so callers retain one consistent
			// request.
			return body, false
		}
		out = updated
		clamped = true
	}
	return out, clamped
}
