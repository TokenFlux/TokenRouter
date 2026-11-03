package manifest

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// config.go validates administrator configuration against a plugin's declared
// fields. Schema validation checks defaults before installations can accept
// them, while normalization creates an independent value map and rejects
// unknown inputs rather than forwarding undeclared settings to a process.
// Secret values remain ordinary strings here; encryption belongs to the host.
// Missing values differ from explicitly supplied empty values, so defaults
// never overwrite an administrator's explicit choice.
//
// Numbers are decoded with json.Number before conversion to avoid accepting
// strings as numeric settings. Constraints apply equally to defaults and
// submitted values, and required checks run after default selection. Hosts
// must merge omitted stored secrets before normalization, then separate and
// encrypt those fields before persistence or administrative responses.

// Configuration keys are portable across JSON, frontend forms, and plugin language bindings without reserved punctuation or whitespace.
var fieldKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

// Config defines the administrator-editable fields accepted by one plugin release.
type Config struct {
	Fields []Field `json:"fields,omitempty"` // ordered configuration field declarations
}

// Field declares a typed setting, its presentation, and optional validation constraints.
type Field struct {
	Key         string        `json:"key"`                   // unique configuration field key
	Type        string        `json:"type"`                  // configuration field type
	Label       LocalizedText `json:"label"`                 // localized form field label
	Description LocalizedText `json:"description,omitempty"` // optional localized field explanation
	Required    bool          `json:"required,omitempty"`    // nonempty value requirement
	// Default retains its JSON type; secret fields cannot declare any default, including null.
	Default json.RawMessage `json:"default,omitempty"`
	Min     *float64        `json:"min,omitempty"` // inclusive numeric lower bound
	Max     *float64        `json:"max,omitempty"` // inclusive numeric upper bound
	// MaxLength defaults to 1000 characters for text fields; an explicit zero permits only empty text.
	MaxLength *int     `json:"maxLength,omitempty"`
	Pattern   string   `json:"pattern,omitempty"` // RE2 string validation expression
	Options   []Option `json:"options,omitempty"` // declared select field choices
}

// Option pairs a select value with its localized display label.
type Option struct {
	Value string        `json:"value"` // stored selection value
	Label LocalizedText `json:"label"` // localized selection display label
}

// validate appends schema and default-value violations to the shared collector.
// Defaults use the same constraint checker as submitted values, ensuring an
// omitted optional value cannot introduce an otherwise prohibited setting.
func (c Config) validate(e *ValidationError) {
	if len(c.Fields) > 50 {
		e.add("config.fields", "must contain at most 50 fields")
	}
	seen := make(map[string]bool)
	for i, f := range c.Fields {
		prefix := fmt.Sprintf("config.fields[%d]", i)
		if !fieldKeyPattern.MatchString(f.Key) || seen[f.Key] {
			e.add(prefix+".key", "must be a unique configuration key")
		}
		seen[f.Key] = true
		validateText(e, prefix+".label", f.Label, 24)
		if len(f.Description) > 0 {
			validateText(e, prefix+".description", f.Description, 300)
		}
		if !slices.Contains([]string{"string", "textarea", "number", "integer", "boolean", "select", "secret"}, f.Type) {
			e.add(prefix+".type", "must be a supported configuration type")
		}
		numeric := f.Type == "number" || f.Type == "integer"
		textual := slices.Contains([]string{"string", "textarea", "secret"}, f.Type)
		if (f.Min != nil || f.Max != nil) && !numeric {
			e.add(prefix, "min and max apply only to numeric fields")
		}
		for _, bound := range []*float64{f.Min, f.Max} {
			if bound != nil && (math.IsNaN(*bound) || math.IsInf(*bound, 0)) {
				e.add(prefix, "numeric bounds must be finite")
			}
		}
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			e.add(prefix, "min must not exceed max")
		}
		if f.MaxLength != nil && (!textual || *f.MaxLength < 0 || *f.MaxLength > 10000) {
			e.add(prefix+".maxLength", "must be between 0 and 10000 and apply to a text field")
		}
		if f.Pattern != "" {
			_, err := regexp.Compile(f.Pattern)
			if f.Type != "string" || err != nil {
				e.add(prefix+".pattern", "must be a valid RE2 expression on a string field")
			}
		}
		if f.Type == "select" {
			if len(f.Options) < 1 || len(f.Options) > 50 {
				e.add(prefix+".options", "must contain between 1 and 50 choices")
			}
		} else if len(f.Options) != 0 {
			e.add(prefix+".options", "applies only to select fields")
		}
		choices := make(map[string]bool)
		for j, option := range f.Options {
			p := fmt.Sprintf("%s.options[%d]", prefix, j)
			if choices[option.Value] {
				e.add(p+".value", "must be unique within the field")
			}
			choices[option.Value] = true
			validateText(e, p+".label", option.Label, 24)
		}
		if len(f.Default) != 0 {
			if f.Type == "secret" {
				e.add(prefix+".default", "secret fields cannot declare defaults")
			} else if value, err := decodeValue(f.Default); err != nil {
				e.add(prefix+".default", "must contain a valid JSON value")
			} else if _, err := f.normalize(value); err != nil {
				e.add(prefix+".default", err.Error())
			}
		}
	}
}

// Normalize validates submitted values and fills declared defaults. It returns
// a new map containing only supplied or defaulted fields; missing optional
// fields remain absent. Omitted secrets must already have been merged by the
// host if their stored values should survive this update.
//
// Both the schema and each selected value are checked before any result is
// returned. Independent failures accumulate as JSON paths, so callers can
// present all invalid fields without partially applying a configuration.
func (c Config) Normalize(values map[string]any) (map[string]any, error) {
	e := &ValidationError{}
	c.validate(e)
	fields := make(map[string]Field, len(c.Fields))
	for _, f := range c.Fields {
		fields[f.Key] = f
	}
	for key := range values {
		if _, ok := fields[key]; !ok {
			e.add(key, "field is not declared")
		}
	}
	result := make(map[string]any)
	for _, f := range c.Fields {
		value, supplied := values[f.Key]
		if !supplied && len(f.Default) != 0 {
			var err error
			value, err = decodeValue(f.Default)
			if err != nil {
				continue
			}
			supplied = true
		}
		if !supplied {
			if f.Required {
				e.add(f.Key, "value is required")
			}
			continue
		}
		normalized, err := f.normalize(value)
		if err != nil {
			e.add(f.Key, err.Error())
			continue
		}
		result[f.Key] = normalized
	}
	if len(e.Issues) > 0 {
		return nil, e
	}
	return result, nil
}

// decodeValue preserves numeric JSON values for schema and administrator validation.
func decodeValue(data []byte) (any, error) {
	var value any
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.UseNumber()
	err := d.Decode(&value)
	if err == nil && !json.Valid(data) {
		err = fmt.Errorf("invalid JSON value")
	}
	return value, err
}

// normalize checks a field value and returns its scalar JSON representation.
// Numeric Go inputs pass through JSON decoding so strings cannot become numbers.
// Required text rejects whitespace-only input, but accepted text retains its spelling.
//
// Numeric settings retain their JSON number spelling after validation. Integer
// checks use exact rational parsing rather than rounded float64 observations,
// while declared numeric bounds use their schema's float64 representation.
// Selection values must match a declared choice, and text limits count Unicode
// characters so multibyte content obeys the same form constraint as ASCII text.
func (f Field) normalize(value any) (any, error) {
	switch f.Type {
	case "string", "textarea", "secret", "select":
		s, ok := value.(string)
		if !ok || !utf8.ValidString(s) {
			return nil, fmt.Errorf("must be a UTF-8 string")
		}
		if f.Required && strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("value is required")
		}
		if f.Type == "select" {
			for _, option := range f.Options {
				if option.Value == s {
					return s, nil
				}
			}
			return nil, fmt.Errorf("must match a declared option")
		}
		limit := 1000
		if f.MaxLength != nil {
			limit = *f.MaxLength
		}
		if utf8.RuneCountInString(s) > limit {
			return nil, fmt.Errorf("must contain at most %d characters", limit)
		}
		if f.Pattern != "" {
			pattern, err := regexp.Compile(f.Pattern)
			if err != nil || !pattern.MatchString(s) {
				return nil, fmt.Errorf("must match the declared pattern")
			}
		}
		return s, nil
	case "number", "integer":
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("must be a finite number")
		}
		decoded, err := decodeValue(data)
		if err != nil {
			return nil, err
		}
		number, ok := decoded.(json.Number)
		if !ok {
			return nil, fmt.Errorf("must be a number")
		}
		n, err := number.Float64()
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("must be a finite number")
		}
		if f.Type == "integer" {
			integer, ok := new(big.Rat).SetString(string(number))
			if !ok || !integer.IsInt() {
				return nil, fmt.Errorf("must be an integer")
			}
		}
		if (f.Min != nil && n < *f.Min) || (f.Max != nil && n > *f.Max) {
			return nil, fmt.Errorf("must be within the declared numeric bounds")
		}
		return number, nil
	case "boolean":
		if b, ok := value.(bool); ok {
			return b, nil
		}
		return nil, fmt.Errorf("must be a boolean")
	default:
		return nil, fmt.Errorf("unsupported field type")
	}
}
