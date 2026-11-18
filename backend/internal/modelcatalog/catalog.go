package modelcatalog

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"sort"
	"strings"
)

// catalog.go parses model catalogs and resolves qualified, public, and
// ambiguous model identities.
// Catalog providers can offer the same bare model with different prices and
// capabilities. Parsing retains author records and registered runtime services
// before constructing lookup keys. Qualified keys retain each accepted
// provider's complete entry. Bare keys prefer ordinary author APIs before plan
// offerings and remain ambiguous when equally preferred records compete.
// Qualified service and public author identities are protected from aliases
// supplied by other hosts. Public metadata remains queryable without a price.
//
// An ambiguous bare name can still acquire public attributes when every
// competing entry names the same canonical model. That metadata path leaves
// the pricing ambiguity intact. Parsing validates entries before returning a
// versioned catalog; callers apply supplements before publication and keep
// published catalog maps and shared price values read-only for existing views.

// RemoteURL identifies the complete models.dev catalog endpoint used when
// configuring the application's remote catalog loader.
const RemoteURL = "https://models.dev/experimental/v2.0/api.json"

// offlineData and modelSupplements provide the catalog and official supplement
// inputs shipped with each binary.
var (
	// offlineData contains a validated models.dev snapshot for offline startup.
	//
	//go:embed catalog.json.gz
	offlineData []byte

	// modelSupplements contains the official supplements shipped with the binary.
	//
	//go:embed model_supplements.json
	modelSupplements string
)

// Cost preserves missing prices and the source unit of USD per million tokens.
type Cost struct {
	// Input, Output, CacheRead, and CacheWrite retain nil for missing prices and
	// pointers to zero for declared free usage. Conversion to per-token billing
	// prices belongs to the pricing consumer.
	Input         *float64 `json:"input"`       // input USD per million tokens
	Output        *float64 `json:"output"`      // output USD per million tokens
	CacheRead     *float64 `json:"cache_read"`  // cache read USD per million tokens
	CacheWrite    *float64 `json:"cache_write"` // aggregate write USD per million tokens
	CacheWrite1h  *float64 `json:"-"`           // USD per million hourly writes
	CacheWrite30m *float64 `json:"-"`           // USD per million thirty-minute writes
	CacheWrite12h *float64 `json:"-"`           // USD per million twelve-hour writes
	ImageInput    *float64 `json:"-"`           // USD per million image input tokens
	ImageRead     *float64 `json:"-"`           // USD per million cached image tokens
	ImageOutput   *float64 `json:"-"`           // USD per million image output tokens
	PerImage      *float64 `json:"-"`           // USD per image
}

// CostTier carries an explicit context threshold; consumers must not infer it
// from compatibility field names.
type CostTier struct {
	Exclusive bool // normalized exclusive lower threshold
	Cost           // context tier token prices
	Tier      struct {
		Type string `json:"type"` // context threshold tier
		Size int    `json:"size"` // tokens
	} `json:"tier"` // context price activation threshold
}

// Entry contains attributes and prices. Attributes remain available when
// prices are missing.
type Entry struct {
	Model            string            `json:"model"`                        // complete catalog model identity
	Provider         string            `json:"provider"`                     // catalog service provider identity
	Canonical        string            `json:"canonical_model_id,omitempty"` // declared public model identity
	Source           string            `json:"source"`                       // model entry data source
	Attributes       Attributes        `json:"attributes"`                   // model display and control values
	AttributeSources map[string]string `json:"attribute_sources,omitempty"`  // supplemental attribute source labels
	// Cost, Tiers, and Fast can share pointers and slices with a published
	// catalog entry. Lookup and Rows copy attribute data; consumers that change
	// prices also copy these price values, as Index.Search does.
	Cost          Cost               `json:"-"` // base catalog token prices
	Tiers         []CostTier         `json:"-"` // ordered context price tiers
	Fast          *Cost              `json:"-"` // optional fast mode prices
	ServicePrices []ServicePrice     `json:"-"` // supported directory service-tier rates
	VideoPrices   map[string]float64 `json:"-"` // USD per output second by resolution
	ImagePrices   map[string]float64 `json:"-"` // USD per image by size
	// FirstParty records known endpoint authorship, subject to an explicit
	// Canonical identity. Bare-name selection uses it to prefer an author's record.
	FirstParty       bool `json:"-"`
	originalEndpoint bool // original author endpoint match
	// subscriptionEndpoint identifies a plan-specific offering. Bare-name
	// selection prefers ordinary author APIs while qualified queries keep this record.
	subscriptionEndpoint bool
}

// Offering records a service model's declared SDK, operation and public identity.
type Offering struct {
	SDK              string // declared service SDK
	Protocol         string // declared gateway protocol
	Operation        string // model operation type
	CanonicalModelID string // public attribute identity
}

// Catalog holds a parsed catalog version. Apply supplements before publishing
// it to readers.
type Catalog struct {
	Version string // published content version fingerprint
	// Offerings preserves service-specific SDK and author declarations before price
	// filtering. A provider can interpret its wire protocol without changing catalog prices.
	Offerings map[string]map[string]Offering
	// Entries, Providers, Ambiguous, attributeFallbacks, and publicEntries are
	// prepared before publication. Existing views retain these maps, so callers
	// finish supplement mutations before sharing a catalog with readers.
	Entries map[string]Entry // normalized queryable model identities
	// Providers includes filtered services so their qualified names remain
	// isolated during lookup. Displayed provider choices come from retained rows.
	Providers map[string]bool // known catalog provider qualifiers
	Ambiguous map[string]bool // ambiguous bare price identities
	// attributeFallbacks holds public attributes when all competing provider
	// records agree on one canonical identity that has public metadata available.
	attributeFallbacks map[string]Attributes
	publicEntries      map[string]Entry // public author model metadata
}

// sourceCatalog groups normalized provider offerings and public author metadata.
type sourceCatalog struct {
	PreferZaiGlobal bool                      `json:"-"`         // global Z.AI wins unqualified lookups
	Providers       map[string]sourceProvider `json:"providers"` // service model collections
	Models          map[string]sourceModel    `json:"models"`    // public author records
}

// sourceProvider groups normalized model records under one service identity.
type sourceProvider struct {
	NPM    json.RawMessage        `json:"npm"`    // default service SDK
	Models map[string]sourceModel `json:"models"` // models keyed by exact ID
}

// sourceModel decodes the attributes and source-unit prices of one catalog record.
type sourceModel struct {
	VideoPrices   map[string]float64 `json:"-"`                  // USD per output second by resolution
	ImagePrices   map[string]float64 `json:"-"`                  // USD per output image by size
	Protocol      string             `json:"-"`                  // resolved primary upstream protocol
	ServicePrices []ServicePrice     `json:"-"`                  // normalized service-tier rates
	Operation     json.RawMessage    `json:"type"`               // optional operation metadata
	Provider      json.RawMessage    `json:"provider"`           // optional model-specific SDK declaration
	Name          string             `json:"name"`               // catalog model display name
	Canonical     string             `json:"canonical_model_id"` // declared public author identity
	// Reasoning, ReasoningOptions, Interleaved, ToolCall, StructuredOutput,
	// Temperature, and Attachment retain supplied false values and empty choices.
	// Nil means the catalog has no declaration for that capability.
	Reasoning        *bool              `json:"reasoning"`         // model reasoning capability flag
	ReasoningOptions *[]ReasoningOption `json:"reasoning_options"` // configurable reasoning control declarations
	Interleaved      *Interleaved       `json:"interleaved"`       // reasoning output format declaration
	ToolCall         *bool              `json:"tool_call"`         // tool call capability flag
	StructuredOutput *bool              `json:"structured_output"` // structured output capability flag
	Temperature      *bool              `json:"temperature"`       // temperature parameter capability flag
	Attachment       *bool              `json:"attachment"`        // attachment input capability flag
	// Limit contains source token counts. entry converts zero limits to unknown
	// attribute values and rejects negative limits before validating the record.
	Limit struct {
		Context int `json:"context"` // tokens
		Input   int `json:"input"`   // tokens
		Output  int `json:"output"`  // tokens
	} `json:"limit"`
	// Modalities.Input and Modalities.Output retain pointer presence for both
	// declarations.
	// Missing lists become unknown attributes, while explicit empty lists remain
	// restrictions that later supplements cannot populate.
	Modalities struct {
		Input  *[]string `json:"input"`  // declared input modality names
		Output *[]string `json:"output"` // declared output modality names
	} `json:"modalities"`
	Cost struct {
		Cost             // catalog base token prices
		Tiers []CostTier `json:"tiers"` // explicit context price tiers
		// Over200K supplies a compatibility tier at 200,000 tokens when Tiers
		// is empty. Explicit tiers take precedence over this historical field.
		Over200K *Cost `json:"context_over_200k"`
	} `json:"cost"` // source catalog pricing object
	// Experimental carries optional mode pricing as raw JSON. entry reads Fast
	// prices from object values and leaves other representations unconsumed.
	Experimental json.RawMessage `json:"experimental"`
}

// Publication retains a complete catalog for read-only lookups across a request.
// Replacing the loader's current catalog leaves this publication intact.
type Publication struct{ catalog *Catalog }

// offering reads optional protocol metadata without invalidating otherwise usable
// model prices or attributes. An omitted model SDK inherits the service SDK;
// an unreadable model override supplies no protocol evidence. Explicit author
// references take precedence over a unique same-name public model.
func (m sourceModel) offering(serviceSDK, publicIdentity string) Offering {
	sdk := serviceSDK
	if len(m.Provider) > 0 && string(m.Provider) != "null" {
		var override struct {
			NPM json.RawMessage `json:"npm"`
		}
		if err := json.Unmarshal(m.Provider, &override); err != nil {
			sdk = ""
		} else if len(override.NPM) > 0 {
			sdk = optionalCatalogString(override.NPM)
			if string(override.NPM) == `""` || string(override.NPM) == "null" {
				sdk = serviceSDK
			}
		}
	}
	if m.Canonical != "" {
		publicIdentity = m.Canonical
	}
	return Offering{SDK: sdk, Protocol: m.Protocol, Operation: optionalCatalogString(m.Operation), CanonicalModelID: publicIdentity}
}

// optionalCatalogString reads catalog extension metadata when its value is text.
// Other shapes carry no known operation or SDK and leave prices unaffected.
func optionalCatalogString(raw json.RawMessage) string {
	if len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	var text string
	_ = json.Unmarshal(raw, &text)
	return text
}

// Supplements returns a fresh byte slice containing official supplement JSON.
// Catalog loaders validate this body and merge configured custom supplements
// before publishing the resulting attribute and pricing version.
func Supplements() []byte {
	return []byte(modelSupplements)
}

// Offline decompresses the bundled models.dev catalog into fresh bytes.
// Decompression and read failures are returned to the caller, which parses the
// body and applies pricing and attribute supplements before publication.
func Offline() ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(offlineData))
	if err != nil {
		return nil, err
	}
	// Close the decompressor after ReadAll finishes; its lifetime belongs to this
	// load. ReadAll's result determines whether the returned catalog body is
	// usable.
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

// Parse validates catalog records and constructs qualified entries, preferred
// bare identities, and public metadata lookups for authors and registered
// runtime services. Other provider offerings are excluded from attributes and
// prices, while their provider names remain recognized lookup qualifiers. The
// provider collection is required, and malformed public canonical IDs,
// attributes, prices, or context tiers reject the candidate catalog.
//
// Bare-name selection prefers ordinary first-party APIs over subscription
// offerings, then the author's own endpoint within that class. Qualified
// identities accept aliases from their own service; public-only canonical IDs
// can also use a first-party offering linked to that exact model. Multiple
// remaining candidates produce ambiguity instead of selecting by iteration order.
// Public metadata can resolve attributes for that bare name when all provider
// records agree on one canonical identity. Callers apply supplements to the
// returned mutable catalog before sharing its maps through views or indexes.
func Parse(body []byte) (*Catalog, error) {
	var source sourceCatalog
	if err := json.Unmarshal(body, &source); err != nil {
		return nil, err
	}
	if source.Providers == nil {
		return ParseV2(body)
	}
	return buildCatalog(body, source)
}

// buildCatalog validates a common catalog projection and prepares its identity
// indexes before publication. The raw bytes identify the originating snapshot,
// while the normalized records supply provider-scoped capabilities and prices.
func buildCatalog(body []byte, source sourceCatalog) (*Catalog, error) {
	if len(source.Providers) == 0 {
		return nil, fmt.Errorf("expected a models.dev catalog with provider entries")
	}
	hash := sha256.Sum256(body)
	catalog := &Catalog{Offerings: make(map[string]map[string]Offering, len(source.Providers)), Version: hex.EncodeToString(hash[:]), Entries: map[string]Entry{}, Providers: map[string]bool{}, Ambiguous: map[string]bool{}}
	bare := map[string][]Entry{}
	publicIdentities := make(map[string]bool, len(source.Models))
	publicNames := make(map[string]string, len(source.Models))
	for id := range source.Models {
		lab, model, ok := strings.Cut(id, "/")
		if !ok || lab == "" || model == "" {
			return nil, fmt.Errorf("invalid canonical model ID: %s", id)
		}
		publicIdentities[normalize(id)] = true
		// Only a unique exact author model name can fill an omitted service
		// reference. Duplicate names across authors leave that identity unresolved.
		if _, seen := publicNames[model]; seen {
			publicNames[model] = ""
		} else {
			publicNames[model] = id
		}
	}
	for provider, models := range source.Providers {
		catalog.Providers[provider] = true
		sdk := optionalCatalogString(models.NPM)
		offers := make(map[string]Offering, len(models.Models))
		catalog.Offerings[provider] = offers
		for id, model := range models.Models {
			offers[id] = model.offering(sdk, publicNames[id])
			entry, err := model.entry(id, provider)
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", provider, id, err)
			}
			entry = classifyModelOrigin(entry, source.Models)
			// v2 lists both regional APIs. Keep existing unqualified Z.AI pricing on
			// the global API and retain the mainland quote under its service identity.
			if source.PreferZaiGlobal && entry.FirstParty && (provider == "zai" || provider == "zhipuai") {
				entry.originalEndpoint = provider == "zai"
			}
			if !entry.FirstParty && !retainedServiceProvider(provider) {
				continue
			}
			catalog.Entries[normalize(provider+"/"+id)] = entry
			bare[normalize(id)] = append(bare[normalize(id)], entry)
		}
	}
	for id, candidates := range bare {
		if _, exact := catalog.Entries[id]; exact {
			continue
		}
		// Qualified service names remain owned by that service. Public author
		// identities can additionally use their own linked first-party offering
		// when the author has no serving-provider qualifier in this catalog.
		prefix, _, qualified := strings.Cut(id, "/")
		if qualified && (catalog.Providers[prefix] || publicIdentities[id]) {
			scoped := make([]Entry, 0, len(candidates))
			for _, entry := range candidates {
				sameProvider := normalize(entry.Provider) == prefix
				samePublicModel := !catalog.Providers[prefix] && entry.FirstParty && normalize(entry.Canonical) == id
				if sameProvider || samePublicModel {
					scoped = append(scoped, entry)
				}
			}
			candidates = scoped
			if len(candidates) == 0 {
				continue
			}
		}
		var preferred []Entry
		var ordinary []Entry
		for _, entry := range candidates {
			if entry.FirstParty {
				preferred = append(preferred, entry)
				if !entry.subscriptionEndpoint {
					ordinary = append(ordinary, entry)
				}
			}
		}
		// Plan prices describe subscription access and can be zero even when
		// the author's ordinary API bills tokens. Prefer the ordinary source
		// class independently of the quoted amounts, including genuine free APIs.
		if len(ordinary) > 0 {
			preferred = ordinary
		}
		var original []Entry
		for _, entry := range preferred {
			if entry.originalEndpoint {
				original = append(original, entry)
			}
		}
		// Prefer original author endpoints within the first party candidates for
		// this identity. The remaining candidate count still decides whether
		// bare lookup is ambiguous.
		if len(original) > 0 {
			preferred = original
		}
		if len(preferred) == 1 {
			catalog.Entries[id] = preferred[0]
		} else if len(preferred) == 0 && len(candidates) == 1 {
			catalog.Entries[id] = candidates[0]
		} else {
			catalog.Ambiguous[id] = true
		}
	}
	publicAttributes := make(map[string]Attributes, len(source.Models))
	catalog.publicEntries = make(map[string]Entry, len(source.Models))
	// Publish public metadata even when provider entries have no unique bare
	// price. Attribute lookup can then describe a model with its author's
	// declaration.
	for id, model := range source.Models {
		entry, err := model.entry(id, strings.SplitN(id, "/", 2)[0])
		if err != nil {
			return nil, err
		}
		publicAttributes[normalize(id)] = entry.Attributes
		catalog.publicEntries[normalize(id)] = entry
		if _, exists := catalog.Entries[normalize(id)]; !exists {
			catalog.Entries[normalize(id)] = entry
		}
		bareID := strings.SplitN(id, "/", 2)[1]
		if _, exists := catalog.Entries[normalize(bareID)]; !exists && len(bare[normalize(bareID)]) == 0 {
			catalog.Entries[normalize(bareID)] = entry
		}
	}
	catalog.attributeFallbacks = make(map[string]Attributes)
	// Ambiguous service prices remain qualified, but unanimous canonical authorship
	// permits public attribute lookup for the bare identity when that metadata
	// exists.
	for id, candidates := range bare {
		if !catalog.Ambiguous[id] || strings.Contains(id, "/") {
			continue
		}
		canonical := normalize(candidates[0].Canonical)
		if canonical == "" {
			continue
		}
		consistent := true
		for _, candidate := range candidates[1:] {
			if normalize(candidate.Canonical) != canonical {
				consistent = false
				break
			}
		}
		if attributes, exists := publicAttributes[canonical]; consistent && exists {
			catalog.attributeFallbacks[id] = attributes
		}
	}
	if len(catalog.Entries) == 0 {
		return nil, fmt.Errorf("empty model catalog")
	}
	return catalog, nil
}

// retainedServiceProvider reports whether runtime routes need this service's
// complete model offerings. These records participate in default directory
// queries and exact route lookups; their independently determined FirstParty
// value controls author preference when resolving bare names.
func retainedServiceProvider(provider string) bool {
	switch provider {
	case "amazon-bedrock", "google-vertex", "google-vertex-anthropic", "ollama-cloud",
		"kimi-code-plan-cn", "kimi-code-plan-global", "zhipuai-coding-plan", "zai-coding-plan":
		return true
	default:
		return false
	}
}

// FirstPartyAliases returns sorted normalized lookup keys for the same first
// party provider and complete model identity. Nil catalogs, unknown models,
// and relay records return the supplied name as a single candidate.
func (c *Catalog) FirstPartyAliases(model string) []string {
	if c == nil {
		return []string{model}
	}
	entry, found := c.Entries[normalize(model)]
	if !found || !entry.FirstParty {
		return []string{model}
	}
	var aliases []string
	for key, candidate := range c.Entries {
		if candidate.FirstParty && candidate.Provider == entry.Provider && candidate.Model == entry.Model {
			aliases = append(aliases, key)
		}
	}
	sort.Strings(aliases)
	return aliases
}

// entry validates one source model and retains its complete identity and
// source-unit price values. Zero limits become unknown attributes, while
// negative limits and invalid capabilities or prices reject the record.
//
// Explicit context tiers take precedence over the historical context_over_200k
// field, which supplies a 200,000-token tier when explicit tiers are absent.
// Tiers are sorted and validated for positive, distinct thresholds so pricing
// consumers receive ordered values. Optional Fast prices are decoded from an
// experimental object and remain absent when that mode is unspecified.
func (m sourceModel) entry(id, provider string) (Entry, error) {
	if m.Limit.Context < 0 || m.Limit.Input < 0 || m.Limit.Output < 0 {
		return Entry{}, fmt.Errorf("negative model limit")
	}
	// Catalog zero lengths represent unknown limits. Convert them to nil before
	// administrator-style validation, which requires every populated token limit
	// to be positive.
	positive := func(value int) *int {
		if value <= 0 {
			return nil
		}
		return &value
	}
	attrs := Attributes{
		Context:          positive(m.Limit.Context),
		InputLimit:       positive(m.Limit.Input),
		OutputLimit:      positive(m.Limit.Output),
		InputModalities:  m.Modalities.Input,
		OutputModalities: m.Modalities.Output,
		Reasoning:        m.Reasoning,
		ReasoningOptions: m.ReasoningOptions,
		Interleaved:      m.Interleaved,
		ToolCall:         m.ToolCall,
		StructuredOutput: m.StructuredOutput,
		Temperature:      m.Temperature,
		Attachment:       m.Attachment,
	}
	if m.Name != "" {
		attrs.DisplayName = &m.Name
	}
	if err := attrs.Validate(); err != nil {
		return Entry{}, err
	}
	e := Entry{Model: id, Provider: provider, Canonical: m.Canonical, Source: "models.dev", Attributes: attrs, Cost: m.Cost.Cost, Tiers: m.Cost.Tiers, ServicePrices: m.ServicePrices, VideoPrices: m.VideoPrices, ImagePrices: m.ImagePrices}
	// The compatibility field names a fixed context threshold. Materialize that
	// threshold for tier consumers when the catalog has supplied no explicit
	// tier list.
	if len(e.Tiers) == 0 && m.Cost.Over200K != nil {
		tier := CostTier{Cost: *m.Cost.Over200K}
		tier.Tier.Type = "context"
		tier.Tier.Size = 200000
		e.Tiers = []CostTier{tier}
	}
	var experimental struct {
		Modes map[string]struct {
			Cost *Cost `json:"cost"` // optional mode token prices
		} `json:"modes"` // experimental named pricing modes
	}
	if len(m.Experimental) > 0 && m.Experimental[0] == '{' {
		if err := json.Unmarshal(m.Experimental, &experimental); err != nil {
			return Entry{}, err
		}
		e.Fast = experimental.Modes["fast"].Cost
	}
	if err := validateCost(e.Cost); err != nil {
		return Entry{}, err
	}
	if e.Fast != nil {
		if err := validateCost(*e.Fast); err != nil {
			return Entry{}, err
		}
	}
	sort.Slice(e.Tiers, func(i, j int) bool { return e.Tiers[i].Tier.Size < e.Tiers[j].Tier.Size })
	for i, tier := range e.Tiers {
		if tier.Tier.Type != "context" || (tier.Tier.Size < 0 || tier.Tier.Size == 0 && !tier.Exclusive) || i > 0 && tier.Tier.Size == e.Tiers[i-1].Tier.Size {
			return Entry{}, fmt.Errorf("invalid context tier")
		}
		if err := validateCost(tier.Cost); err != nil {
			return Entry{}, err
		}
	}
	return e, nil
}

// validateCost rejects negative and nonfinite catalog token price values.
// Nil prices remain unknown and zero prices remain declared free usage, so
// attribute-only entries and free models are accepted.
func validateCost(cost Cost) error {
	for _, value := range []*float64{cost.Input, cost.Output, cost.CacheRead, cost.CacheWrite, cost.CacheWrite1h, cost.CacheWrite30m, cost.CacheWrite12h, cost.ImageInput, cost.ImageRead, cost.ImageOutput, cost.PerImage} {
		if value != nil && (*value < 0 || math.IsInf(*value, 0) || math.IsNaN(*value)) {
			return fmt.Errorf("invalid model price")
		}
	}
	return nil
}

// normalize trims surrounding whitespace and folds case in catalog identity keys.
func normalize(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

// Lookup searches candidates in order and copies the first entry's attributes
// and attribute source labels. Price pointers and tier slices still belong to
// the catalog; callers copy them before mutation.
func (c *Catalog) Lookup(candidates []string) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	for _, candidate := range candidates {
		if entry, ok := c.Entries[normalize(candidate)]; ok {
			entry.Attributes = entry.Attributes.Clone()
			entry.AttributeSources = maps.Clone(entry.AttributeSources)
			return entry, true
		}
	}
	return Entry{}, false
}

// LookupAttributes returns copied attributes from the first resolved candidate,
// then considers public metadata for an unresolved bare identity. Recognized
// provider qualifiers constrain candidate preparation, and an exact entry
// remains authoritative even when some attributes are missing. Nil catalogs
// and missing metadata return false with unknown attributes.
//
// The bare-name path uses the public attributes selected by Parse when competing
// providers agree on one canonical model. Names containing a slash skip this
// path, keeping provider-specific omissions attached to that source. Attribute
// fallback leaves the catalog's pricing ambiguity unchanged, so displaying
// capabilities does not select a relay quote.
func (c *Catalog) LookupAttributes(model string, candidates []string) (Attributes, bool) {
	if entry, found := c.Lookup(c.IdentityCandidates(model, candidates)); found {
		return entry.Attributes, true
	}
	if c == nil || strings.Contains(normalize(model), "/") {
		return Attributes{}, false
	}
	// No exact candidate resolved this bare ID. Read attributeFallbacks,
	// prepared from unanimous canonical identities, while leaving the price
	// entry collection unchanged.
	attributes, found := c.attributeFallbacks[normalize(model)]
	if !found {
		return Attributes{}, false
	}
	return attributes.Clone(), true
}

// Rows returns entries sorted by their normalized lookup keys, including
// qualified names and bare aliases. Attributes and source labels are copied;
// price pointers and tier slices remain shared with the catalog.
func (c *Catalog) Rows() []Entry {
	rows := []Entry{}
	if c == nil {
		return rows
	}
	for key, value := range c.Entries {
		value.Model = key
		value.Attributes = value.Attributes.Clone()
		value.AttributeSources = maps.Clone(value.AttributeSources)
		rows = append(rows, value)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Model < rows[j].Model })
	return rows
}

// IdentityCandidates prepares lookup names while retaining recognized
// provider qualification. A qualified model accepts candidates from the same
// provider and prefixes bare candidates; candidates naming another provider
// are discarded, keeping metadata and price lookup within the requested source.
//
// The normalized input remains the first candidate. For a recognized azure/m
// identity, a bare m candidate becomes azure/m, while relay/m is excluded.
// Unknown qualifiers leave the supplied extra candidates available to Lookup,
// which normalizes them during its search.
func (c *Catalog) IdentityCandidates(model string, candidates []string) []string {
	result := []string{normalize(model)}
	prefix, _, qualified := strings.Cut(normalize(model), "/")
	if c != nil && qualified && c.Providers[prefix] {
		for _, candidate := range candidates {
			candidate = normalize(candidate)
			if strings.HasPrefix(candidate, prefix+"/") {
				result = append(result, candidate)
			} else if !strings.Contains(candidate, "/") {
				result = append(result, prefix+"/"+candidate)
			}
		}
		return result
	}
	return append(result, candidates...)
}

// RequiresExact reports whether ambiguity or a recognized provider qualifier
// requires exact pricing identity. Nil catalogs return false because they
// contain neither provider recognition nor recorded bare-name ambiguities.
func (c *Catalog) RequiresExact(model string) bool {
	if c == nil {
		return false
	}
	prefix, _, qualified := strings.Cut(normalize(model), "/")
	return c.Ambiguous[normalize(model)] || qualified && c.Providers[prefix]
}

// Publication returns a read-only handle to the catalog supplied by the loader.
func (c *Catalog) Publication() Publication { return Publication{catalog: c} }

// Version identifies the pinned publication, or returns empty before loading.
func (v Publication) Version() string {
	if v.catalog == nil {
		return ""
	}
	return v.catalog.Version
}

// Offering retrieves one exact service declaration without exposing catalog maps.
func (v Publication) Offering(service, model string) Offering {
	if v.catalog == nil {
		return Offering{}
	}
	return v.catalog.Offerings[service][model]
}

// ServiceOfferings returns a caller-owned copy of a service's model declarations.
// An unloaded publication yields nil. A loaded catalog with no such service
// yields an empty table so callers can distinguish it from unavailable data.
func (v Publication) ServiceOfferings(service string) map[string]Offering {
	if v.catalog == nil {
		return nil
	}
	result := make(map[string]Offering, len(v.catalog.Offerings[service]))
	maps.Copy(result, v.catalog.Offerings[service])
	return result
}

// LookupRoute retrieves attributes for a service, an optional public author and
// a normalized model identity. A provider-qualified name stays scoped to that
// provider. Missing service entries can use public metadata for the named author.
// Returned entries borrow the catalog publication and must be cloned before editing.
func (v Publication) LookupRoute(service, author, model string) Entry {
	c := v.catalog
	if c == nil || model == "" {
		return Entry{}
	}
	model = normalize(model)
	if prefix, _, qualified := strings.Cut(model, "/"); qualified && c.Providers[prefix] {
		if entry, found := c.Lookup([]string{model}); found && entry.Provider == prefix {
			return entry
		}
		return c.publicEntries[model]
	}
	if service == "" {
		if entry, found := c.Lookup([]string{model}); found {
			return entry
		}
		attrs, _ := c.LookupAttributes(model, nil)
		return Entry{Attributes: attrs}
	}
	if !c.Providers[service] {
		if entry, exists := c.publicEntries[service+"/"+model]; exists {
			return entry
		}
	}
	if entry, exists := c.Lookup([]string{service + "/" + model}); exists && entry.Provider == service {
		return entry
	}
	// This service has no exact record for the model. Read public author
	// metadata so limits from another provider's service cannot become this
	// route's capabilities.
	for _, identity := range []string{model, author + "/" + model} {
		if entry, found := c.publicEntries[identity]; found {
			return entry
		}
	}
	if entry, found := c.Lookup([]string{model}); found && entry.Provider == service {
		return entry
	}
	return Entry{}
}
