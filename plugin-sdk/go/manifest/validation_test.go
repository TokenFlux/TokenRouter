package manifest

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateRejectsEveryManifestRule mutates one valid declaration per case.
// The reported JSON path must identify the violated rule, while unrelated
// valid contributions remain accepted by the same shared validator.
func TestValidateRejectsEveryManifestRule(t *testing.T) {
	zero, large, negative := 0, 10000000, -1
	tests := []struct {
		name   string          // scenario display name
		path   string          // expected invalid field path
		mutate func(*Manifest) // independent declaration mutation
	}{
		{"schema", "schemaVersion", func(m *Manifest) { m.SchemaVersion = 2 }},
		{"identifier", "id", func(m *Manifest) { m.ID = "Bad.id" }},
		{"version", "version", func(m *Manifest) { m.Version = "1.2" }},
		{"minimum", "minHostVersion", func(m *Manifest) { m.MinHostVersion = "v0.5.0" }},
		{"maximum", "maxHostVersion", func(m *Manifest) { m.MaxHostVersion = "0.5.0" }},
		{"api", "apiVersion", func(m *Manifest) { m.APIVersion = 0 }},
		{"english", "name.en", func(m *Manifest) { delete(m.Name, "en") }},
		{"language", "name.bad_tag", func(m *Manifest) { m.Name["bad_tag"] = "Bad" }},
		{"name-length", "name.en", func(m *Manifest) { m.Name["en"] = strings.Repeat("字", 41) }},
		{"description-length", "description.en", func(m *Manifest) { m.Description["en"] = strings.Repeat("x", 301) }},
		{"icon", "icon", func(m *Manifest) { m.Icon = "../icon.svg" }},
		{"icon-type", "icon", func(m *Manifest) { m.Icon = "icon.html" }},
		{"permission", "permissions", func(m *Manifest) { m.Permissions = []string{"wallet.superuser"} }},
		{"permission-duplicate", "permissions", func(m *Manifest) { m.Permissions = []string{"user.read", "user.read"} }},
		{"network-count", "network", func(m *Manifest) { m.Network = make([]string, 21) }},
		{"network-host", "network", func(m *Manifest) { m.Network = []string{"https://example.com"} }},
		{"memory", "resources.memoryMiB", func(m *Manifest) { m.Resources.MemoryMiB = &zero }},
		{"contribution", "$", func(m *Manifest) { m.Backend = nil; m.Frontend = nil }},
		{"executable", "backend.executable", func(m *Manifest) { m.Backend.Executable = "/bin/plugin" }},
		{"method", "backend.routes[0].method", func(m *Manifest) { m.Backend.Routes[0].Method = "HEAD" }},
		{"route-path", "backend.routes[0].path", func(m *Manifest) { m.Backend.Routes[0].Path = "/x/*/y" }},
		{"route-length", "backend.routes[0].path", func(m *Manifest) { m.Backend.Routes[0].Path = "/" + strings.Repeat("x", 128) }},
		{"route-auth", "backend.routes[0].auth", func(m *Manifest) { m.Backend.Routes[0].Auth = "owner" }},
		{"route-timeout", "backend.routes[0].timeoutSeconds", func(m *Manifest) { m.Backend.Routes[0].TimeoutSeconds = &zero }},
		{"route-body", "backend.routes[0].maxBodyBytes", func(m *Manifest) { m.Backend.Routes[0].MaxBodyBytes = &large }},
		{"route-negative", "backend.routes[0].maxBodyBytes", func(m *Manifest) { m.Backend.Routes[0].MaxBodyBytes = &negative }},
		{"task-name", "backend.tasks[0].name", func(m *Manifest) { m.Backend.Tasks[0].Name = "Bad" }},
		{"task-duplicate", "backend.tasks[1].name", func(m *Manifest) { m.Backend.Tasks = append(m.Backend.Tasks, m.Backend.Tasks[0]) }},
		{"task-schedule", "backend.tasks[0].schedule", func(m *Manifest) { m.Backend.Tasks[0].Schedule = "@hourly" }},
		{"task-timeout", "backend.tasks[0].timeoutSeconds", func(m *Manifest) { m.Backend.Tasks[0].TimeoutSeconds = &large }},
		{"entry", "frontend.entry", func(m *Manifest) { m.Frontend.Entry = "index.js" }},
		{"style", "frontend.styles[0]", func(m *Manifest) { m.Frontend.Styles[0] = "frontend/../index.css" }},
		{"page-id", "frontend.pages[0].id", func(m *Manifest) { m.Frontend.Pages[0].ID = "Bad" }},
		{"page-id-duplicate", "frontend.pages[1].id", func(m *Manifest) { m.Frontend.Pages = append(m.Frontend.Pages, m.Frontend.Pages[0]) }},
		{"page-path", "frontend.pages[0].path", func(m *Manifest) { m.Frontend.Pages[0].Path = "/home" }},
		{"page-path-duplicate", "frontend.pages[1].path", func(m *Manifest) {
			p := m.Frontend.Pages[0]
			p.ID = "other"
			m.Frontend.Pages = append(m.Frontend.Pages, p)
		}},
		{"page-audience", "frontend.pages[0].audience", func(m *Manifest) { m.Frontend.Pages[0].Audience = "public" }},
		{"page-title", "frontend.pages[0].title.en", func(m *Manifest) { m.Frontend.Pages[0].Title["en"] = strings.Repeat("x", 41) }},
		{"menu-page", "frontend.menus[0].page", func(m *Manifest) { m.Frontend.Menus[0].Page = "missing" }},
		{"menu-icon", "frontend.menus[0].icon", func(m *Manifest) { m.Frontend.Menus[0].Icon = "icon.png" }},
		{"menu-label", "frontend.menus[0].label.en", func(m *Manifest) { m.Frontend.Menus[0].Label["en"] = strings.Repeat("x", 25) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest(t)
			tt.mutate(m)
			err := m.Validate()
			var validation *ValidationError
			if !errors.As(err, &validation) || !strings.Contains(err.Error(), tt.path) {
				t.Fatalf("want %s validation error, got %v", tt.path, err)
			}
		})
	}
}

// TestValidVersionRequiresCompleteSemVer checks release identities with numeric
// prerelease components and build metadata. Shorthand versions and numeric
// leading zeros must never compare as accepted release identities.
func TestValidVersionRequiresCompleteSemVer(t *testing.T) {
	for _, value := range []string{"1.0.0", "0.5.0-rc.1", "2.3.4+build.05"} {
		if !ValidVersion(value) {
			t.Errorf("rejected %s", value)
		}
	}
	for _, value := range []string{"1", "1.2", "v1.2.3", "01.2.3", "1.2.3-01", "1.2.3-", "1.2.3+"} {
		if ValidVersion(value) {
			t.Errorf("accepted %s", value)
		}
	}
}

// TestParseScheduleRestrictsDescriptors checks intervals and five-field cron
// expressions. Host timezone ownership and the minimum interval remain enforced
// even when the cron library accepts broader descriptor syntax.
func TestParseScheduleRestrictsDescriptors(t *testing.T) {
	for _, value := range []string{"@every 1m", "@every 2h", "0 4 * * *"} {
		if _, err := ParseSchedule(value); err != nil {
			t.Errorf("%s: %v", value, err)
		}
	}
	for _, value := range []string{"@daily", "@every 59s", "@every -1m", "TZ=UTC 0 4 * * *", "* * * * * *", "80 * * * *"} {
		if _, err := ParseSchedule(value); err == nil {
			t.Errorf("accepted %s", value)
		}
	}
}
