package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

// config_test.go verifies manifest field constraints, localized explanations, and
// administrator value normalization. Descriptions remain declaration metadata,
// while submitted values retain explicit zero and false settings and cannot
// acquire undeclared keys or silently bypass their configured type constraints.

// TestConfigDescriptionsRoundTrip preserves localized help through manifest parsing.
// The explanation must remain metadata rather than become a submitted setting,
// and descriptions must obey the same English fallback and Unicode bounds as labels.
func TestConfigDescriptionsRoundTrip(t *testing.T) {
	m := validManifest(t)
	m.Config.Fields[0].Description = LocalizedText{
		"en": "Balance granted by each daily check-in.",
		"zh": "每次签到发放的余额。",
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Config.Fields[0].Description.Localize("zh-Hans") != "每次签到发放的余额。" {
		t.Fatal("localized description was lost")
	}
	values, err := parsed.Config.Normalize(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := values["description"]; found {
		t.Fatal("description became a configuration value")
	}
	for _, description := range []LocalizedText{
		{"zh": "缺少英文回退"},
		{"en": strings.Repeat("x", 301)},
	} {
		m.Config.Fields[0].Description = description
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), ".description") {
			t.Fatalf("invalid description accepted: %v", err)
		}
	}
}

// TestConfigNormalizePreservesExplicitValues exercises all seven field types.
// Defaults fill only missing inputs, secret strings survive for host encryption,
// and returned scalar values do not share mutable administrator input.
func TestConfigNormalizePreservesExplicitValues(t *testing.T) {
	c := Config{Fields: []Field{
		{Key: "string", Type: "string", Label: LocalizedText{"en": "String"}, Default: json.RawMessage(`"default"`)},
		{Key: "textarea", Type: "textarea", Label: LocalizedText{"en": "Text"}},
		{Key: "number", Type: "number", Label: LocalizedText{"en": "Number"}, Default: json.RawMessage(`0.1`)},
		{Key: "integer", Type: "integer", Label: LocalizedText{"en": "Integer"}},
		{Key: "boolean", Type: "boolean", Label: LocalizedText{"en": "Boolean"}, Default: json.RawMessage(`true`)},
		{Key: "select", Type: "select", Label: LocalizedText{"en": "Select"}, Options: []Option{{Value: "fixed", Label: LocalizedText{"en": "Fixed"}}}},
		{Key: "secret", Type: "secret", Label: LocalizedText{"en": "Secret"}},
	}}
	values := map[string]any{"string": "", "textarea": "line\nline", "integer": 3, "boolean": false, "select": "fixed", "secret": "token"}
	got, err := c.Normalize(values)
	if err != nil {
		t.Fatal(err)
	}
	if got["string"] != "" || got["number"] != json.Number("0.1") || got["integer"] != json.Number("3") || got["boolean"] != false || got["secret"] != "token" {
		t.Fatalf("unexpected normalized values: %#v", got)
	}
	got["secret"] = "changed"
	if values["secret"] != "token" {
		t.Fatal("normalization reused input map")
	}
	values["extra"] = true
	if _, err := c.Normalize(values); err == nil {
		t.Fatal("accepted undeclared field")
	}
}

// TestConfigValidationRejectsInvalidSchemas checks field identity, type-specific
// constraints, and defaults before administrator input is evaluated. Every
// invalid schema must fail even when no values were supplied.
func TestConfigValidationRejectsInvalidSchemas(t *testing.T) {
	zero, huge := 0, 10001
	minimum, maximum := 10.0, 1.0
	tests := []struct {
		name   string        // schema mutation scenario
		mutate func(*Config) // invalid schema transformation
	}{
		{"key", func(c *Config) { c.Fields[0].Key = "_invalid" }},
		{"duplicate", func(c *Config) { c.Fields = append(c.Fields, c.Fields[0]) }},
		{"count", func(c *Config) {
			for len(c.Fields) < 51 {
				c.Fields = append(c.Fields, c.Fields[0])
			}
		}},
		{"type", func(c *Config) { c.Fields[0].Type = "object" }},
		{"label", func(c *Config) { c.Fields[0].Label = LocalizedText{"zh": "名字"} }},
		{"secret-default", func(c *Config) { c.Fields[0].Type = "secret" }},
		{"wrong-default", func(c *Config) { c.Fields[0].Default = json.RawMessage(`"0.1"`) }},
		{"bounds-order", func(c *Config) { c.Fields[0].Min = &minimum; c.Fields[0].Max = &maximum }},
		{"text-bounds", func(c *Config) { c.Fields[0].Type = "string"; c.Fields[0].Default = nil }},
		{"length-type", func(c *Config) { c.Fields[0].MaxLength = &zero }},
		{"length-max", func(c *Config) {
			c.Fields[0] = Field{Key: "text", Type: "string", Label: LocalizedText{"en": "Text"}, MaxLength: &huge}
		}},
		{"pattern", func(c *Config) {
			c.Fields[0] = Field{Key: "text", Type: "string", Label: LocalizedText{"en": "Text"}, Pattern: "["}
		}},
		{"pattern-type", func(c *Config) { c.Fields[0].Pattern = "x" }},
		{"select-options", func(c *Config) {
			c.Fields[0] = Field{Key: "choice", Type: "select", Label: LocalizedText{"en": "Choice"}}
		}},
		{"options-type", func(c *Config) { c.Fields[0].Options = []Option{{Value: "x", Label: LocalizedText{"en": "X"}}} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest(t)
			tt.mutate(&m.Config)
			if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "config.fields") {
				t.Fatalf("invalid schema accepted: %v", err)
			}
		})
	}
}

// TestConfigNormalizeRejectsInvalidValues protects required and typed settings.
// False and zero remain legitimate explicit values, while strings that resemble
// numbers do not bypass numeric constraints or select membership checks.
func TestConfigNormalizeRejectsInvalidValues(t *testing.T) {
	maxLength, min, max := 3, 0.0, 2.0
	tests := []struct {
		field Field // validated field schema
		value any   // invalid administrator input
	}{
		{Field{Type: "string", Required: true}, "  "},
		{Field{Type: "string", MaxLength: &maxLength}, "四个字符"},
		{Field{Type: "string", Pattern: "^[a-z]+$"}, "123"},
		{Field{Type: "number", Min: &min, Max: &max}, 3.0},
		{Field{Type: "number"}, "1.0"},
		{Field{Type: "integer"}, 1.1},
		{Field{Type: "boolean"}, "false"},
		{Field{Type: "secret"}, nil},
		{Field{Type: "select", Options: []Option{{Value: "a", Label: LocalizedText{"en": "A"}}}}, "b"},
	}
	for _, tt := range tests {
		tt.field.Key = "value"
		tt.field.Label = LocalizedText{"en": "Value"}
		c := Config{Fields: []Field{tt.field}}
		if _, err := c.Normalize(map[string]any{"value": tt.value}); err == nil {
			t.Errorf("accepted %s input %#v", tt.field.Type, tt.value)
		}
	}
	c := Config{Fields: []Field{{Key: "required", Type: "boolean", Required: true, Label: LocalizedText{"en": "Required"}}}}
	if _, err := c.Normalize(nil); err == nil {
		t.Fatal("accepted missing required boolean")
	}
	if _, err := c.Normalize(map[string]any{"required": false}); err != nil {
		t.Fatal(err)
	}
}
