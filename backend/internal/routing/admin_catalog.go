package routing

import (
	"sort"
)

// admin_catalog.go builds administrative model lists from configured provider
// candidates and display metadata.
// Administrative model lists start from IDs supplied by a provider's configured
// candidate callback. The catalog sorts that slice, filters candidates, and
// deduplicates accepted IDs before metadata lookup, so display records reflect
// provider choices even when the metadata catalog contains unrelated models.

// Administrative catalog variants select the metadata shape consumed by
// provider-test and model-picker HTTP responses.
const (
	CatalogClaude      AdminCatalogKind = "claude"      // Claude model response
	CatalogOpenAI      AdminCatalogKind = "openai"      // OpenAI model response
	CatalogGemini      AdminCatalogKind = "gemini"      // Gemini model response
	CatalogAntigravity AdminCatalogKind = "antigravity" // Antigravity model response
	CatalogQoder       AdminCatalogKind = "qoder"       // Qoder model response
	CatalogGrok        AdminCatalogKind = "grok"        // Grok model response
)

// AdminCatalogKind identifies the response shape for an administrative model
// catalog.
type AdminCatalogKind string

// AdminCatalogModel contains display metadata serialized by platform-specific
// administrative model endpoints.
type AdminCatalogModel struct {
	// ID is the configured model key; Object is the response object label; Type is
	// the response record kind; OwnedBy is the owner name; DisplayName is the
	// model label; CreatedAt is the textual creation timestamp.
	ID, Object, Type, OwnedBy, DisplayName, CreatedAt string
	Created                                           int64 // epoch creation timestamp
}

// AdminCatalogInput selects the catalog shape and optional candidate
// eligibility filter.
type AdminCatalogInput struct {
	// Platform and Site carry the provider identity supplied by callers. Available
	// uses Platform to choose the response shape, while Site remains unused by
	// that selection.
	Platform, Site string
	// OAuth and Passthrough retain caller authentication and transport flags
	// alongside Platform for metadata lookup callers.
	OAuth, Passthrough bool
	// Accept filters configured candidates before metadata lookup. A nil callback
	// accepts every distinct configured ID that reaches the catalog loop.
	Accept func(string) bool // optional model eligibility check
}

// AdminCatalogResult pairs the selected catalog shape with accepted model
// metadata.
type AdminCatalogResult struct {
	Kind   AdminCatalogKind    // selected response shape
	Models []AdminCatalogModel // accepted model metadata
}

// AdminCatalogOptions supplies optional metadata lookup for accepted configured
// model IDs.
type AdminCatalogOptions struct {
	// Lookup replaces the complete default metadata record for each accepted ID.
	// Available returns that record as supplied, including its ID and zero fields.
	// A nil callback uses platform-shaped defaults and the configured display name.
	Lookup func(AdminCatalogKind, string) AdminCatalogModel // optional display metadata lookup
}

// AdminCatalog filters configured model IDs and assembles their administrative
// metadata.
type AdminCatalog struct {
	options AdminCatalogOptions // retained metadata lookup options
}

// NewAdminCatalog retains the optional metadata lookup for administrative
// catalog queries.
func NewAdminCatalog(options AdminCatalogOptions) *AdminCatalog { return &AdminCatalog{options} }

// Available returns accepted records in sorted configured-ID order using the
// selected platform's response shape. The required configured callback supplies
// a slice that is sorted in place; an optional Accept callback filters IDs
// before Lookup supplies their complete metadata records, and unknown platforms
// use the Claude shape.
func (s *AdminCatalog) Available(input AdminCatalogInput, configured func() []string) (AdminCatalogResult, error) {
	kind := CatalogClaude
	switch input.Platform {
	case "openai":
		kind = CatalogOpenAI
	case "gemini":
		kind = CatalogGemini
	case "antigravity":
		kind = CatalogAntigravity
	case "qoder":
		kind = CatalogQoder
	case "grok":
		kind = CatalogGrok
	}
	requested := configured()
	sort.Strings(requested)
	models := make([]AdminCatalogModel, 0, len(requested))
	seen := make(map[string]bool)
	// Accepted IDs enter seen after filtering, so repeated rejected entries can
	// reach Accept again while each accepted ID receives one metadata lookup.
	for _, id := range requested {
		if seen[id] || (input.Accept != nil && !input.Accept(id)) {
			continue
		}
		seen[id] = true
		m := AdminCatalogModel{ID: id, Type: "model", DisplayName: id}
		if kind == CatalogOpenAI || kind == CatalogGrok {
			m.Object = "model"
		}
		if kind == CatalogGrok {
			m.Type = ""
			m.OwnedBy = "xai"
		}
		if s.options.Lookup != nil {
			m = s.options.Lookup(kind, id)
		}
		models = append(models, m)
	}
	return AdminCatalogResult{Kind: kind, Models: models}, nil
}
