package grok

import (
	"reflect"
	"strings"
	"testing"
)

// setGrokTestOverride publishes one field and restores the previous snapshot.
// Tests using process defaults remain serial, matching startup configuration;
// nested fixtures restore snapshots in reverse order without environment mutation.
func setGrokTestOverride(t *testing.T, field, raw string) {
	t.Helper()
	previous := currentOverrides()
	next := previous
	value := reflect.ValueOf(&next).Elem().FieldByName(field)
	if value.Kind() == reflect.Bool {
		normalized := strings.ToLower(strings.TrimSpace(raw))
		value.SetBool(normalized == "1" || normalized == "true" || normalized == "yes" || normalized == "y" || normalized == "on")
	} else {
		value.SetString(raw)
	}
	ConfigureOverrides(next)
	t.Cleanup(func() { ConfigureOverrides(previous) })
}
