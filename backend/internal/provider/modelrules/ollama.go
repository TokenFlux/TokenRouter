package modelrules

import "encoding/json"

// ollama.go reads provider-supplied Ollama output caps from supported numeric
// representations.
// Ollama output limits arrive through provider Extra as several numeric Go
// representations. Reading a missing or unsupported representation selects the
// execution default, while an explicitly nonpositive value disables clamping
// when model attributes are resolved.

// OllamaOutputCapExtraKey identifies the provider Extra value consumed when
// resolving Ollama output parameter limits.
const OllamaOutputCapExtraKey = "ollama_max_tokens_cap"

// OllamaOutputCap reads a provider's output token cap from supported numeric
// representations. Missing values, unsupported types, and json.Number values
// that cannot parse as integers use the default; nonpositive caps disable
// output parameter clamping.
func OllamaOutputCap(value any, present bool) int64 {
	if !present {
		return OllamaDefaultOutputLimit
	}

	switch number := value.(type) {
	case float64:
		// Provider Extra decoded by JSON uses floating point numbers.
		// Conversion truncates fractions toward zero when the value fits int64;
		// out-of-range values receive Go's implementation-dependent conversion
		// result before resolution interprets the cap.
		return int64(number)
	case int64:
		return number
	case int:
		return int64(number)
	case json.Number:
		parsed, err := number.Int64()
		if err != nil {
			// An unreadable number cannot supply a provider token limit.
			// Use the execution default so resolution follows the missing
			// setting behavior.
			return OllamaDefaultOutputLimit
		}
		return parsed
	default:
		// Extra can contain arbitrary administrator JSON values for this key.
		// Unsupported representations use the execution default rather than
		// supplying an unusable limit.
		return OllamaDefaultOutputLimit
	}
}
