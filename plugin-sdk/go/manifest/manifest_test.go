package manifest

import (
	"strings"
	"testing"
)

// TestParseRejectsUnknownFieldsAndTrailingJSON protects the publication boundary
// against misspelled declarations. It also rejects concatenated documents that
// could be interpreted differently by downstream validators.
func TestParseRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	for _, suffix := range []string{`,"unknown":true}`, `} {}`, `,"backend":{"executable":"bin/plugin","unknown":true}}`} {
		data := `{"schemaVersion":1,"id":"test.plugin","version":"1.0.0","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Test"},"description":{"en":"Test plugin"},"frontend":{"entry":"frontend/index.js","pages":[]}` + suffix
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatalf("accepted malformed manifest %s", data)
		}
	}
}

// TestLocalizeUsesExactBaseAndEnglish checks language selection without mutating
// the source translations. Unsupported regional tags must retain a readable
// English fallback when no base translation exists.
func TestLocalizeUsesExactBaseAndEnglish(t *testing.T) {
	text := LocalizedText{"en": "English", "zh": "中文", "zh-TW": "繁體"}
	for tag, want := range map[string]string{"zh-TW": "繁體", "ZH-tw": "繁體", "zh-Hans": "中文", "fr": "English", "": "English"} {
		if got := text.Localize(tag); got != want {
			t.Errorf("%s: got %q, want %q", tag, got, want)
		}
	}
}

// TestRouteMatchesPreservesPathBoundary reproduces adjacent public and private
// route names. Prefix matching must require the declared slash boundary and
// retain method checks even when a path matches.
func TestRouteMatchesPreservesPathBoundary(t *testing.T) {
	route := Route{Method: "GET", Path: "/admin/*", Auth: "admin"}
	if !route.Matches("GET", "/admin/users") || route.Matches("GET", "/administrator") || route.Matches("POST", "/admin/users") || route.Matches("GET", "/admin") {
		t.Fatal("route boundary or method check failed")
	}
	if !(Route{Method: "*", Path: "/status"}).Matches("DELETE", "/status") {
		t.Fatal("wildcard method did not match exact path")
	}
}

// TestDeclarationDefaultsDistinguishExplicitZero checks omission independently
// from numeric zero. An administrator can forbid all request bodies without
// having that choice replaced by the default body allowance.
func TestDeclarationDefaultsDistinguishExplicitZero(t *testing.T) {
	zero := 0
	if (Resources{}).MemoryLimit() != 256 || (Route{}).Timeout() != 15 || (Route{}).BodyLimit() != 1<<20 || (Task{}).Timeout() != 300 || (Menu{}).SortOrder() != 100 {
		t.Fatal("unexpected declaration defaults")
	}
	if (Route{MaxBodyBytes: &zero}).BodyLimit() != 0 || (Menu{Order: &zero}).SortOrder() != 0 {
		t.Fatal("explicit zero was replaced")
	}
	m := validManifest(t)
	m.Backend.Routes[0].TimeoutSeconds = &zero
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "timeoutSeconds") {
		t.Fatalf("zero deadline accepted: %v", err)
	}
}
