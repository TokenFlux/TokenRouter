package provider

import "strings"

// record_temp_unschedulable.go defines temporary suspension rules and parses
// rule objects from provider Credentials. Health handling requires each rule to
// supply a positive HTTP status, a positive minute duration, and at least one
// nonblank keyword; incomplete JSON entries are skipped so malformed configuration
// cannot install an empty or indefinite suspension.

// TempUnschedulableRule selects an upstream failure that triggers a temporary
// scheduling pause.
type TempUnschedulableRule struct {
	// ErrorCode and Keywords select a failure together. The health matcher
	// requires the exact status and a usable keyword in the response body;
	// GetTempUnschedulableRules discards rules missing either required component.
	ErrorCode int      `json:"error_code"` // upstream HTTP status code
	Keywords  []string `json:"keywords"`   // response text match keywords
	// DurationMinutes sets the pause length. GetTempUnschedulableRules skips a
	// stored rule when this converted duration is nonpositive, before health handling.
	DurationMinutes int    `json:"duration_minutes"` // minutes
	Description     string `json:"description"`      // administrator rule display description
}

// IsTempUnschedulableEnabled reads the temporary scheduling-rule switch from
// Credentials. Missing maps, absent values, nil values, and nonboolean
// representations return false, so a string such as true does not enable these
// rules.
func (r *Record) IsTempUnschedulableEnabled() bool {
	if r.Credentials == nil {
		return false
	}
	raw, ok := r.Credentials["temp_unschedulable_enabled"]
	if !ok || raw == nil {
		return false
	}
	enabled, ok := raw.(bool)
	return ok && enabled
}

// GetTempUnschedulableRules parses the JSON-shaped temporary suspension rules
// stored in Credentials. The top-level value must be []any; missing or other
// container types return nil.
//
// Each object supplies status, keywords, duration, and description through
// their dedicated parsers. Entries with a nonpositive status or duration, or no
// usable keywords, are skipped. Valid entries retain input order for the
// health-rule matcher, while an accepted empty or wholly rejected array returns
// an empty allocated rule slice.
func (r *Record) GetTempUnschedulableRules() []TempUnschedulableRule {
	if r.Credentials == nil {
		return nil
	}
	raw, ok := r.Credentials["temp_unschedulable_rules"]
	if !ok || raw == nil {
		return nil
	}

	arr, ok := raw.([]any)
	if !ok {
		return nil
	}

	rules := make([]TempUnschedulableRule, 0, len(arr))
	for _, item := range arr {
		entry, ok := item.(map[string]any)
		if !ok || entry == nil {
			continue
		}

		rule := TempUnschedulableRule{
			ErrorCode:       ParseExtraInt(entry["error_code"]),
			Keywords:        ParseTempUnschedStrings(entry["keywords"]),
			DurationMinutes: ParseExtraInt(entry["duration_minutes"]),
			Description:     ParseTempUnschedString(entry["description"]),
		}

		// The matcher needs a status, usable keyword, and positive pause duration.
		// Skip incomplete stored objects so they cannot create an unusable
		// suspension result in later health handling.
		if rule.ErrorCode <= 0 || rule.DurationMinutes <= 0 || len(rule.Keywords) == 0 {
			continue
		}

		rules = append(rules, rule)
	}

	return rules
}

// ParseTempUnschedString returns trimmed string input for suspension-rule
// fields. Unsupported dynamic values return an empty string, allowing keyword
// and description parsing to ignore unusable JSON values without manufacturing
// text.
func ParseTempUnschedString(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// ParseTempUnschedStrings returns nonblank trimmed strings from []string or
// []any input. Mixed arrays skip nonstrings, and missing or unsupported
// containers return nil; supplied supported arrays can produce an empty result.
func ParseTempUnschedStrings(value any) []string {
	if value == nil {
		return nil
	}

	var raw []string
	switch v := value.(type) {
	case []string:
		raw = v
	case []any:
		raw = make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				raw = append(raw, s)
			}
		}
	default:
		return nil
	}

	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s := strings.TrimSpace(item)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
