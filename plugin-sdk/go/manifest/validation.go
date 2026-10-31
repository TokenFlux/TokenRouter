package manifest

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"
	"golang.org/x/mod/semver"
	"golang.org/x/text/language"
)

// validation.go enforces release declarations before any code is installed.
// The same checks run in the SDK, catalog builder, and host so malformed
// contributions cannot acquire different meanings at different distribution
// stages. File contents and archive limits remain the package validator's job.

var (
	// Declaration patterns keep identities, paths, and semantic versions consistent across host operating systems and publishing tools.
	idPattern        = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}\.[a-z][a-z0-9-]{1,47}$`)
	namePattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	pagePathPattern  = regexp.MustCompile(`^[a-z0-9-]+(/[a-z0-9-]+)*$`)
	filePathPattern  = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	routePathPattern = regexp.MustCompile(`^/[a-zA-Z0-9._~/-]*$`)
	versionPattern   = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	hostLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
)

// Permissions returns a fresh list of capabilities understood by this protocol.
// Callers may edit the returned slice without changing validation. Host releases
// can advertise a smaller implemented subset during compatibility checks.
func Permissions() []string {
	return []string{
		"user.read", "user.email", "user.lookup", "user.identities",
		"wallet.read", "wallet.credit", "wallet.debit", "redeem.issue",
		"subscription.read", "subscription.grant", "usage.self", "usage.read",
		"payment.read", "catalog.read", "notify.email", "captcha.verify",
	}
}

// Validate reports every independently invalid declaration using JSON paths.
// It neither reads package files nor mutates the manifest; defaults are resolved
// by accessors and configuration normalization after the declaration is accepted.
func (m *Manifest) Validate() error {
	e := &ValidationError{}
	if m == nil {
		e.add("$", "manifest is required")
		return e
	}
	if m.SchemaVersion != 1 {
		e.add("schemaVersion", "must equal 1")
	}
	if !ValidID(m.ID) {
		e.add("id", "must be a publisher-qualified lowercase identifier")
	}
	for _, item := range []struct{ key, value string }{{"version", m.Version}, {"minHostVersion", m.MinHostVersion}} {
		if !ValidVersion(item.value) {
			e.add(item.key, "must be an unprefixed SemVer 2.0 version")
		}
	}
	if m.MaxHostVersion != "" {
		if !ValidVersion(m.MaxHostVersion) || (ValidVersion(m.MinHostVersion) && semver.Compare("v"+m.MaxHostVersion, "v"+m.MinHostVersion) <= 0) {
			e.add("maxHostVersion", "must be a SemVer greater than minHostVersion")
		}
	}
	if m.APIVersion <= 0 {
		e.add("apiVersion", "must be positive")
	}
	validateText(e, "name", m.Name, 40)
	validateText(e, "description", m.Description, 300)
	if m.Icon != "" && (!ValidPath(m.Icon) || !slices.Contains([]string{".svg", ".png"}, path.Ext(m.Icon))) {
		e.add("icon", "must be a package-relative SVG or PNG path")
	}
	seen := make(map[string]bool)
	for i, permission := range m.Permissions {
		if !slices.Contains(Permissions(), permission) || seen[permission] {
			e.add(fmt.Sprintf("permissions[%d]", i), "must be a known, unique permission")
		}
		seen[permission] = true
	}
	if len(m.Network) > 20 {
		e.add("network", "must contain at most 20 hostnames")
	}
	for i, hostname := range m.Network {
		if !validHostname(hostname) {
			e.add(fmt.Sprintf("network[%d]", i), "must be a hostname without a scheme, port, or path")
		}
	}
	if m.Resources.MemoryLimit() < 32 || m.Resources.MemoryLimit() > 2048 {
		e.add("resources.memoryMiB", "must be between 32 and 2048")
	}
	if m.Backend == nil && m.Frontend == nil {
		e.add("$", "at least one of backend and frontend is required")
	}
	if m.Backend != nil {
		m.Backend.validate(e)
	}
	if m.Frontend != nil {
		m.Frontend.validate(e)
	}
	m.Config.validate(e)
	if len(e.Issues) != 0 {
		return e
	}
	return nil
}

// ValidID reports whether an identifier satisfies the publisher and name limits.
func ValidID(id string) bool {
	return len(id) <= 80 && idPattern.MatchString(id)
}

// ValidVersion accepts complete SemVer versions without a leading v prefix.
func ValidVersion(version string) bool {
	return versionPattern.MatchString(version) && semver.IsValid("v"+version)
}

// ValidPath accepts portable package-relative names without traversal or aliases.
// Empty components, dot components, and backslashes are rejected so one archive
// entry cannot address another entry through a platform-specific spelling.
func ValidPath(value string) bool {
	if !filePathPattern.MatchString(value) || strings.HasPrefix(value, "/") {
		return false
	}
	for part := range strings.SplitSeq(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// validHostname checks DNS label spelling and the complete hostname length.
func validHostname(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for label := range strings.SplitSeq(value, ".") {
		if !hostLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

// validateText appends language-tag, required-English, and Unicode-length violations.
// It checks every translation independently. Case aliases are rejected to keep
// runtime lookup deterministic when maps are decoded in different orders.
func validateText(e *ValidationError, field string, text LocalizedText, limit int) {
	if strings.TrimSpace(text["en"]) == "" {
		e.add(field+".en", "English text is required")
	}
	seen := make(map[string]bool)
	for tag, value := range text {
		_, err := language.Parse(tag)
		if err != nil || strings.Contains(tag, "_") || seen[strings.ToLower(tag)] {
			e.add(field+"."+tag, "must use a unique BCP 47 language tag")
		}
		seen[strings.ToLower(tag)] = true
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
			e.add(field+"."+tag, fmt.Sprintf("must contain at most %d Unicode characters", limit))
		}
	}
}

// validate checks executable, HTTP declarations, and scheduled job constraints.
// Errors accumulate in the caller's collection. Route ordering is preserved
// because the host applies the first matching authorization rule.
func (b Backend) validate(e *ValidationError) {
	if !ValidPath(b.Executable) {
		e.add("backend.executable", "must be a package-relative path")
	}
	for i, route := range b.Routes {
		prefix := fmt.Sprintf("backend.routes[%d]", i)
		if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE", "*"}, route.Method) {
			e.add(prefix+".method", "must be GET, POST, PUT, PATCH, DELETE, or *")
		}
		p := strings.TrimSuffix(route.Path, "/*")
		if route.Path == "/*" {
			p = "/"
		}
		if !routePathPattern.MatchString(p) || len(route.Path) > 128 {
			e.add(prefix+".path", "must be an absolute route or a /* prefix of at most 128 characters")
		}
		if !slices.Contains([]string{"public", "user", "admin"}, route.Auth) {
			e.add(prefix+".auth", "must be public, user, or admin")
		}
		if route.Timeout() < 1 || route.Timeout() > 60 {
			e.add(prefix+".timeoutSeconds", "must be between 1 and 60")
		}
		if route.BodyLimit() < 0 || route.BodyLimit() > 8<<20 {
			e.add(prefix+".maxBodyBytes", "must be between 0 and 8388608")
		}
	}
	seen := make(map[string]bool)
	for i, task := range b.Tasks {
		prefix := fmt.Sprintf("backend.tasks[%d]", i)
		if !namePattern.MatchString(task.Name) || seen[task.Name] {
			e.add(prefix+".name", "must be a unique lowercase task identifier")
		}
		seen[task.Name] = true
		if _, err := ParseSchedule(task.Schedule); err != nil {
			e.add(prefix+".schedule", err.Error())
		}
		if task.Timeout() < 1 || task.Timeout() > 3600 {
			e.add(prefix+".timeoutSeconds", "must be between 1 and 3600")
		}
	}
}

// ParseSchedule accepts five cron fields or an @every interval of at least one
// minute. Named cron shortcuts and embedded timezone overrides are rejected;
// the host supplies its timezone when calculating the next occurrence.
func ParseSchedule(value string) (cron.Schedule, error) {
	if duration, ok := strings.CutPrefix(value, "@every "); ok {
		d, err := time.ParseDuration(duration)
		if err != nil || d < time.Minute {
			return nil, fmt.Errorf("@every duration must be at least 1m")
		}
		return cron.Every(d), nil
	}
	if len(strings.Fields(value)) != 5 || strings.Contains(value, "=") {
		return nil, fmt.Errorf("schedule must contain five cron fields or @every duration")
	}
	return cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(value)
}

// validate checks assets, audience-scoped paths, and menu page references.
// Page identifiers are global within a plugin, while identical paths may be
// declared once for each audience without colliding in host routes.
func (f Frontend) validate(e *ValidationError) {
	for i, asset := range append([]string{f.Entry}, f.Styles...) {
		field := "frontend.entry"
		if i > 0 {
			field = fmt.Sprintf("frontend.styles[%d]", i-1)
		}
		if !ValidPath(asset) || !strings.HasPrefix(asset, "frontend/") {
			e.add(field, "must be a package-relative path below frontend/")
		}
	}
	ids := make(map[string]bool)
	paths := make(map[string]bool)
	for i, page := range f.Pages {
		prefix := fmt.Sprintf("frontend.pages[%d]", i)
		if !namePattern.MatchString(page.ID) || ids[page.ID] {
			e.add(prefix+".id", "must be a unique lowercase page identifier")
		}
		ids[page.ID] = true
		key := page.Audience + ":" + page.Path
		if (page.Path != "" && !pagePathPattern.MatchString(page.Path)) || paths[key] {
			e.add(prefix+".path", "must be a valid relative route unique within its audience")
		}
		paths[key] = true
		if page.Audience != "user" && page.Audience != "admin" {
			e.add(prefix+".audience", "must be user or admin")
		}
		validateText(e, prefix+".title", page.Title, 40)
	}
	for i, menu := range f.Menus {
		prefix := fmt.Sprintf("frontend.menus[%d]", i)
		if !ids[menu.Page] {
			e.add(prefix+".page", "must reference a declared page")
		}
		validateText(e, prefix+".label", menu.Label, 24)
		if menu.Icon != "" && (!ValidPath(menu.Icon) || path.Ext(menu.Icon) != ".svg") {
			e.add(prefix+".icon", "must be a package-relative SVG path")
		}
	}
}
