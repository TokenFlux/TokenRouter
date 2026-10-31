package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// manifest.go defines the shared plugin declaration consumed by publishers,
// index builders, and hosts. Parsing rejects unknown fields before semantic
// validation so spelling mistakes cannot silently disable a permission,
// configuration constraint, or contribution declared by a plugin author.

// Manifest describes one immutable release and its host-facing contributions.
type Manifest struct {
	SchemaVersion  int           `json:"schemaVersion"`            // manifest schema major
	ID             string        `json:"id"`                       // publisher-qualified plugin identity
	Version        string        `json:"version"`                  // unprefixed release SemVer
	APIVersion     int           `json:"apiVersion"`               // host protocol major
	MinHostVersion string        `json:"minHostVersion"`           // inclusive host version
	MaxHostVersion string        `json:"maxHostVersion,omitempty"` // exclusive host version
	Name           LocalizedText `json:"name"`                     // localized display name
	Description    LocalizedText `json:"description"`              // localized release description
	Publisher      Publisher     `json:"publisher"`                // publisher attribution metadata
	Homepage       string        `json:"homepage,omitempty"`       // project homepage URL
	License        string        `json:"license"`                  // declared license identifier
	Icon           string        `json:"icon,omitempty"`           // package-relative display icon
	Permissions    []string      `json:"permissions,omitempty"`    // requested host capabilities
	Network        []string      `json:"network,omitempty"`        // declared external hostnames
	Resources      Resources     `json:"resources,omitempty"`      // process resource hints
	Backend        *Backend      `json:"backend,omitempty"`        // optional process contribution
	Frontend       *Frontend     `json:"frontend,omitempty"`       // optional browser contribution
	Config         Config        `json:"config,omitempty"`         // administrator configuration schema
}

// Publisher identifies the release author displayed during installation review.
type Publisher struct {
	Name string `json:"name"`          // publisher display name
	URL  string `json:"url,omitempty"` // publisher profile URL
}

// Resources supplies advisory process limits rather than operating-system isolation.
type Resources struct {
	// MemoryMiB defaults to 256 when omitted; hosts use it for GOMEMLIMIT and observations.
	MemoryMiB *int `json:"memoryMiB,omitempty"`
}

// Backend declares the executable and the services it implements.
type Backend struct {
	Executable string  `json:"executable"`       // package-relative executable path
	Routes     []Route `json:"routes,omitempty"` // ordered HTTP route declarations
	Tasks      []Task  `json:"tasks,omitempty"`  // scheduled task declarations
}

// Route describes one ordered HTTP authorization and forwarding rule.
type Route struct {
	Method string `json:"method"` // HTTP method or wildcard
	Path   string `json:"path"`   // exact path or prefix
	Auth   string `json:"auth"`   // public, user, or admin
	// TimeoutSeconds defaults to 15 when omitted; explicit values must be between 1 and 60.
	TimeoutSeconds *int `json:"timeoutSeconds,omitempty"`
	// MaxBodyBytes defaults to 1 MiB when omitted; zero explicitly forbids request bodies.
	MaxBodyBytes *int `json:"maxBodyBytes,omitempty"`
}

// Task describes a named job invoked by the host scheduler.
type Task struct {
	Name     string `json:"name"`     // plugin-local task identity
	Schedule string `json:"schedule"` // cron or every expression
	// TimeoutSeconds defaults to 300 when omitted; explicit values range from 1 to 3600.
	TimeoutSeconds *int `json:"timeoutSeconds,omitempty"`
}

// Frontend declares browser assets and authenticated page and menu contributions.
type Frontend struct {
	Entry  string   `json:"entry"`            // package-relative IIFE script
	Styles []string `json:"styles,omitempty"` // package-relative CSS stylesheets
	Pages  []Page   `json:"pages"`            // authenticated page declarations
	Menus  []Menu   `json:"menus,omitempty"`  // sidebar menu declarations
}

// Page maps a component registration to an audience-scoped relative route.
type Page struct {
	ID       string        `json:"id"`       // component registration identifier
	Path     string        `json:"path"`     // plugin-relative route path
	Audience string        `json:"audience"` // user or admin
	Title    LocalizedText `json:"title"`    // localized document title
}

// Menu links a sidebar entry to an already declared page.
type Menu struct {
	Page  string        `json:"page"`           // referenced page identifier
	Label LocalizedText `json:"label"`          // localized sidebar label
	Icon  string        `json:"icon,omitempty"` // package-relative SVG icon
	// Order defaults to 100 when omitted; equal orders retain manifest menu ordering.
	Order *int `json:"order,omitempty"`
}

// LocalizedText maps language tags to text with a required English fallback.
type LocalizedText map[string]string

// ValidationError collects independent declaration violations using JSON field paths.
type ValidationError struct {
	Issues []Issue `json:"issues"` // ordered field validation errors
}

// Issue identifies one invalid field and a publisher-readable explanation.
type Issue struct {
	Path    string `json:"path"`    // JSON field path
	Message string `json:"message"` // validation failure explanation
}

// Parse decodes a single manifest object and rejects unknown fields. It applies
// semantic validation without rewriting the original bytes, which publishers
// and hosts must hash separately for package integrity checks.
// @project-doc docs/interfaces/plugin_contracts.md#manifest
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("manifest must contain one JSON object")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Error formats all collected paths and messages into one diagnostic string.
func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		parts[i] = issue.Path + ": " + issue.Message
	}
	return strings.Join(parts, "; ")
}

// add appends an independent rule violation without discarding earlier diagnostics.
func (e *ValidationError) add(path, message string) {
	e.Issues = append(e.Issues, Issue{Path: path, Message: message})
}

// Localize selects the exact tag, its base language, or English. Comparisons
// ignore tag casing; callers may pass an unsupported or empty tag to obtain
// the required English fallback of a validated declaration.
func (t LocalizedText) Localize(tag string) string {
	for _, candidate := range []string{tag, strings.SplitN(tag, "-", 2)[0], "en"} {
		for key, value := range t {
			if strings.EqualFold(key, candidate) {
				return value
			}
		}
	}
	return ""
}

// MemoryLimit returns the declared process memory hint in mebibytes.
// Omitted values select 256 MiB. Validate checks explicit bounds before hosts
// use this value for runtime settings or resource observations.
func (r Resources) MemoryLimit() int {
	if r.MemoryMiB == nil {
		return 256
	}
	return *r.MemoryMiB
}

// Timeout returns the route deadline in seconds, defaulting to fifteen.
// Explicit zero remains zero so Validate can reject it. Callers use this
// accessor only after accepting the enclosing manifest.
func (r Route) Timeout() int {
	if r.TimeoutSeconds == nil {
		return 15
	}
	return *r.TimeoutSeconds
}

// BodyLimit returns the request limit in bytes, defaulting to one mebibyte.
// Explicit zero forbids request bodies. Callers validate the declaration first
// so negative or excessive configured limits never reach request readers.
func (r Route) BodyLimit() int {
	if r.MaxBodyBytes == nil {
		return 1 << 20
	}
	return *r.MaxBodyBytes
}

// Matches reports whether the method and path satisfy this declaration. Prefix
// routes include their trailing slash, so /admin/* matches /admin/users but
// cannot accidentally authorize /administrator or the separate /admin path.
func (r Route) Matches(method, path string) bool {
	if r.Method != "*" && r.Method != method {
		return false
	}
	if strings.HasSuffix(r.Path, "/*") {
		return strings.HasPrefix(path, strings.TrimSuffix(r.Path, "*"))
	}
	return r.Path == path
}

// Timeout returns the task deadline in seconds, defaulting to five minutes.
// Explicit values remain unchanged for validation. Hosts apply this limit to
// each scheduled occurrence rather than to the scheduler's own lifetime.
func (t Task) Timeout() int {
	if t.TimeoutSeconds == nil {
		return 300
	}
	return *t.TimeoutSeconds
}

// SortOrder returns the sidebar order, defaulting to one hundred.
// Explicit zero and negative orders retain their sorting meaning. Hosts resolve
// equal values by plugin identity and original menu declaration order.
func (m Menu) SortOrder() int {
	if m.Order == nil {
		return 100
	}
	return *m.Order
}
