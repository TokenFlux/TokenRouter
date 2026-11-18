package modelcatalog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// catalog_v2.go projects models.dev v2 provider offerings into the directory's
// identity, capability and price indexes. It preserves price presence because
// a zero and an omitted rate have different effects on settlement and on the
// supplement layers.
//
// Version two publishes capabilities under models and protocols, prices under
// billing units, and provider identities independently of model authorship.
// The projection reads only supported rules and reconstructs author metadata
// from authoritative author offerings. Its compatibility aliases reference
// the same v2 quotes and preserve established qualified lookups after provider
// consolidation. Remote and offline candidates use the same validation path.

// ServicePrice describes a service-tier quote or a dated token price change.
type ServicePrice struct {
	Tier       string   `json:"tier"`                  // normalized service tier
	After      string   `json:"after,omitempty"`       // first applicable local date
	Before     string   `json:"before,omitempty"`      // first excluded local date
	Zone       string   `json:"zone,omitempty"`        // timezone for rule dates
	Factor     *float64 `json:"factor,omitempty"`      // factor for all token buckets
	TextFactor *float64 `json:"text_factor,omitempty"` // text token price factor
	Cost       *Cost    `json:"-"`                     // explicit overriding token rates
	Factors    *Cost    `json:"-"`                     // per-bucket token factors
}

// catalogProviderV2 contains the model offerings served under one provider ID.
type catalogProviderV2 struct {
	ID     string                    `json:"id"`     // provider map key
	Models map[string]catalogModelV2 `json:"models"` // exact model identifiers
}

// catalogModelV2 decodes the v2 fields used to build a directory entry.
type catalogModelV2 struct {
	ID           string                 `json:"id"`           // provider model ID
	Canonical    string                 `json:"canonical_id"` // author and model ID
	Name         string                 `json:"name"`         // catalog display name
	Type         string                 `json:"type"`         // operation category
	API          json.RawMessage        `json:"api"`          // ordered protocol object
	Capabilities *catalogCapabilitiesV2 `json:"capabilities"` // general model controls
	Cost         catalogCostV2          `json:"cost"`         // modality rates
	Limit        struct {
		Context int `json:"context"` // total context tokens
		Input   int `json:"input"`   // input token cap
		Output  int `json:"output"`  // output token cap
	} `json:"limit"` // context and output limits
	Modalities struct {
		Input  *[]string `json:"input"`  // input MIME types
		Output *[]string `json:"output"` // output MIME types
	} `json:"modalities"` // modality declarations
	Experimental struct {
		Modes map[string]catalogModeV2 `json:"modes"` // model service modes
	} `json:"experimental"` // optional service metadata
}

// catalogCapabilitiesV2 retains declared controls while permitting future fields.
type catalogCapabilitiesV2 struct {
	Temperature      *bool `json:"temperature"`       // temperature control support
	StructuredOutput *bool `json:"structured_output"` // schema output support
	Tools            struct {
		Supported *bool `json:"supported"` // model tool support
	} `json:"tools"` // tool declarations
	Reasoning struct {
		Supported *bool     `json:"supported"` // reasoning capability
		Toggle    bool      `json:"toggle"`    // explicit on/off support
		Effort    *[]string `json:"effort"`    // selectable effort values
		Budget    *struct {
			Min *int `json:"min"` // minimum reasoning tokens
			Max *int `json:"max"` // maximum reasoning tokens
		} `json:"budget"` // budget control limits
	} `json:"reasoning"` // configurable reasoning controls
}

// catalogAPIV2 carries targeting metadata used by legacy service aliases.
type catalogAPIV2 struct {
	SDK struct {
		AISDK string `json:"aisdk"` // AI SDK adapter ID
	} `json:"sdk"` // per-protocol SDK declarations
	Compatibility struct {
		ReasoningField string `json:"reasoning_field"` // output reasoning key
	} `json:"compatibility"` // protocol compatibility declarations
}

// catalogCostV2 contains supported rate families and service-tier multipliers.
type catalogCostV2 struct {
	Video       catalogModalityV2     `json:"video"`       // video output unit prices
	Text        catalogModalityV2     `json:"text"`        // text token rates
	Image       catalogModalityV2     `json:"image"`       // image token and unit prices
	Multipliers []catalogMultiplierV2 `json:"multipliers"` // conditional pricing factors
}

// catalogModalityV2 contains input and output rates for one modality.
type catalogModalityV2 struct {
	Input  catalogRateV2 `json:"input"`  // input pricing buckets
	Output catalogRateV2 `json:"output"` // output pricing buckets
}

// catalogRateV2 retains the source unit and explicitly supplied bucket prices.
type catalogRateV2 struct {
	catalogPriceV2                     // base price buckets
	Unit           string              `json:"unit"`      // non-token price unit
	Overrides      []catalogOverrideV2 `json:"overrides"` // conditional replacement rates
}

// catalogPriceV2 carries optional amounts so explicit zero prices survive decoding.
type catalogPriceV2 struct {
	Price *float64 `json:"price"` // base amount
	Cache struct {
		Read  *float64        `json:"read"`  // cache hit amount
		Write json.RawMessage `json:"write"` // scalar or duration prices
	} `json:"cache"` // input cache pricing buckets
}

// catalogOverrideV2 carries a replacement amount and its applicability.
type catalogOverrideV2 struct {
	catalogPriceV2                            // replacement amount buckets
	When           map[string]json.RawMessage `json:"when"` // conditions grouped by input
}

// catalogMultiplierV2 retains raw conditions so unsupported rules can be skipped.
type catalogMultiplierV2 struct {
	When   map[string]json.RawMessage `json:"when"`   // conditions grouped by input
	Factor json.RawMessage            `json:"factor"` // scalar or modality factors
}

// catalogModeV2 carries service-tier identity and any explicit mode prices.
type catalogModeV2 struct {
	Tier string         `json:"service_tier"` // provider tier identifier
	Cost *catalogCostV2 `json:"cost"`         // direct price replacement
	Body struct {
		ServiceTier string `json:"service_tier"` // tier sent in requests
	} `json:"body"` // request settings for this mode
}

// NormalizeRemoteURL upgrades known official v1 URLs while retaining explicit
// model type filters and custom mirror addresses. An empty address still
// selects local catalog loading.
func NormalizeRemoteURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host != "models.dev" || u.User != nil {
		return raw
	}
	switch u.Path {
	case "/api.json", "/catalog.json":
		u.Path = "/experimental/v2.0/api.json"
		return u.String()
	default:
		return raw
	}
}

// ParseV2 validates a provider-keyed v2 snapshot and builds its lookup indexes.
// A failed candidate supplies no usable catalog. Model or provider identity
// conflicts and invalid prices reject the candidate, while new fields outside
// the supported feature set can coexist with the projection.
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_catalog_metadata_lookup
func ParseV2(body []byte) (*Catalog, error) {
	var providers map[string]catalogProviderV2
	if err := json.Unmarshal(body, &providers); err != nil {
		return nil, err
	}
	source := sourceCatalog{PreferZaiGlobal: true, Providers: map[string]sourceProvider{}, Models: map[string]sourceModel{}}
	for service, provider := range providers {
		if provider.ID != service || provider.Models == nil || strings.TrimSpace(service) == "" {
			return nil, fmt.Errorf("invalid models.dev v2 provider %q", service)
		}
		models := make(map[string]sourceModel, len(provider.Models))
		source.Providers[service] = sourceProvider{Models: models}
		for id, raw := range provider.Models {
			if raw.ID != id || raw.Capabilities == nil || raw.Type == "" || strings.TrimSpace(id) == "" {
				return nil, fmt.Errorf("invalid models.dev v2 model %s/%s", service, id)
			}
			model, err := projectV2Model(raw, service)
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", service, id, err)
			}
			models[id] = model

			// Canonical metadata needs an author offering to avoid borrowing a
			// reseller's protocol overrides for a different endpoint with the same model.
			entry := classifyModelOrigin(Entry{Model: id, Canonical: model.Canonical, Provider: service}, nil)
			if entry.FirstParty && (entry.originalEndpoint || model.Canonical == "") {
				canonical := model.Canonical
				if canonical == "" && entry.originalEndpoint {
					canonical = service + "/" + id
				}
				if canonical != "" && (canonical == service+"/"+id || id == strings.TrimPrefix(canonical, service+"/")) {
					attributes := model
					attributes.Cost.Cost = Cost{}
					attributes.Cost.Tiers = nil
					attributes.ServicePrices = nil
					attributes.VideoPrices, attributes.ImagePrices = nil, nil
					source.Models[canonical] = attributes
				}
			}

			// Vertex v2 groups partner API offerings under its main provider.
			// Preserve the old lookup for records explicitly served by that SDK.
			if service == "google-vertex" && isVertexAnthropic(raw.API) {
				alias := source.Providers["google-vertex-anthropic"]
				if alias.Models == nil {
					alias.Models = map[string]sourceModel{}
				}
				alias.Models[id] = model
				source.Providers["google-vertex-anthropic"] = alias
			}
		}
	}
	if azure, ok := source.Providers["azure"]; ok {
		source.Providers["azure-cognitive-services"] = azure
	}
	return buildCatalog(body, source)
}

// projectV2Model prepares a normalized record and validates its supported prices.
// The projection preserves explicit capability presence and normalizes price
// conditions before reader indexes or supplemental prices can share the record.
func projectV2Model(raw catalogModelV2, service string) (sourceModel, error) {
	operation, err := json.Marshal(raw.Type)
	if err != nil {
		return sourceModel{}, err
	}
	capabilities := raw.Capabilities
	protocol, interleaved := projectV2API(raw.API)
	model := sourceModel{
		Protocol:         protocol,
		Operation:        operation,
		Name:             raw.Name,
		Canonical:        raw.Canonical,
		Reasoning:        capabilities.Reasoning.Supported,
		ToolCall:         capabilities.Tools.Supported,
		StructuredOutput: capabilities.StructuredOutput,
		Temperature:      capabilities.Temperature,
		Limit:            raw.Limit,
		Interleaved:      interleaved,
	}
	model.Modalities.Input = projectV2Modalities(raw.Modalities.Input)
	model.Modalities.Output = projectV2Modalities(raw.Modalities.Output)
	if model.Modalities.Input != nil {
		model.Attachment = new(slices.Contains(*model.Modalities.Input, "image") || slices.Contains(*model.Modalities.Input, "pdf") || slices.Contains(*model.Modalities.Input, "audio") || slices.Contains(*model.Modalities.Input, "video"))
	}
	reasoning := capabilities.Reasoning
	options := make([]ReasoningOption, 0, 3)
	if reasoning.Toggle {
		options = append(options, ReasoningOption{Type: "toggle"})
	}
	if reasoning.Effort != nil {
		option := ReasoningOption{Type: "effort"}
		for _, effort := range *reasoning.Effort {
			if effort == "default" {
				option.Values = append(option.Values, nil)
			} else {
				option.Values = append(option.Values, new(effort))
			}
		}
		if len(option.Values) > 0 {
			options = append(options, option)
		}
	}
	if reasoning.Budget != nil {
		options = append(options, ReasoningOption{Type: "budget_tokens", Min: reasoning.Budget.Min, Max: reasoning.Budget.Max})
	}
	if len(options) > 0 || reasoning.Effort != nil || reasoning.Supported != nil && *reasoning.Supported {
		model.ReasoningOptions = &options
	}
	if model.Canonical != "" {
		if author, id, ok := strings.Cut(model.Canonical, "/"); !ok || author == "" || id == "" {
			return sourceModel{}, fmt.Errorf("invalid canonical model ID %q", model.Canonical)
		}
	}
	model.Cost.Cost, err = projectV2Cost(raw.Cost)
	if err != nil {
		return sourceModel{}, err
	}
	// OpenAI documents GPT Image 1 mini text as input only. Correct the v2
	// modality while it lacks a text output rate so the complete image quote
	// reaches native Images billing without opening text output on this model.
	if service == "openai" && raw.ID == "gpt-image-1-mini" && raw.Type == "image" &&
		model.Cost.Output == nil && model.Cost.ImageOutput != nil {
		model.Modalities.Output = new([]string{"image"})
	}
	model.Cost.Tiers, err = projectV2Context(raw.Cost)
	if err != nil {
		return sourceModel{}, err
	}
	model.VideoPrices, err = projectV2MediaPrices(raw.Cost.Video.Output, "second", "480p")
	if err != nil {
		return sourceModel{}, err
	}
	model.ImagePrices, err = projectV2MediaPrices(raw.Cost.Image.Output, "image", "")
	if err != nil {
		return sourceModel{}, err
	}
	model.ServicePrices, err = projectV2ServicePrices(raw, service)
	return model, err
}

// projectV2API selects the first routable protocol in the author's declared
// order. Optional output reasoning hints can come from that same protocol.
// Protocol keys without a gateway adapter do not create a route suggestion.
func projectV2API(raw json.RawMessage) (string, *Interleaved) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if first, err := decoder.Token(); err != nil || first != json.Delim('{') {
		return "", nil
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "", nil
		}
		var api catalogAPIV2
		if err = decoder.Decode(&api); err != nil {
			return "", nil
		}
		var protocol string
		switch key {
		case "responses":
			protocol = "openai_responses"
		case "chat-completions":
			protocol = "openai_chat_completions"
		case "messages":
			protocol = "anthropic_messages"
		case "generate-content":
			protocol = "gemini_generate_content"
		}
		if protocol != "" {
			field := api.Compatibility.ReasoningField
			if field == "reasoning_content" || field == "reasoning_details" {
				return protocol, &Interleaved{Field: field}
			}
			return protocol, nil
		}
	}
	return "", nil
}

// isVertexAnthropic identifies a declared Vertex Anthropic Messages offering.
func isVertexAnthropic(raw json.RawMessage) bool {
	var apis map[string]catalogAPIV2
	return json.Unmarshal(raw, &apis) == nil && apis["messages"].SDK.AISDK == "@ai-sdk/google-vertex/anthropic"
}

// projectV2Modalities reduces MIME declarations to gateway modality labels.
// It preserves the presence of an empty list and collapses synonymous file
// formats without exposing a capability the model did not declare.
func projectV2Modalities(raw *[]string) *[]string {
	if raw == nil {
		return nil
	}
	out := make([]string, 0, len(*raw))
	for _, value := range *raw {
		kind, _, _ := strings.Cut(value, "/")
		switch value {
		case "application/pdf":
			kind = "pdf"
		case "decision", "embedding", "reranking":
			kind = "text"
		}
		if slices.Contains([]string{"text", "image", "audio", "video", "pdf"}, kind) && !slices.Contains(out, kind) {
			out = append(out, kind)
		}
	}
	return &out
}

// projectV2Cost reads prices whose units already have billing consumers.
// Images billed per object stay independent of token rates, which lets a model
// expose an image quote without pretending it has a text output unit.
func projectV2Cost(raw catalogCostV2) (Cost, error) {
	var cost Cost
	if raw.Text.Input.Unit == "" {
		cost.Input = raw.Text.Input.Price
		if err := applyV2Cache(&cost, raw.Text.Input.catalogPriceV2); err != nil {
			return Cost{}, err
		}
	}
	if raw.Text.Output.Unit == "" {
		cost.Output = raw.Text.Output.Price
	}
	if raw.Image.Input.Unit == "" {
		cost.ImageInput = raw.Image.Input.Price
		cost.ImageRead = raw.Image.Input.Cache.Read
	}
	switch raw.Image.Output.Unit {
	case "":
		cost.ImageOutput = raw.Image.Output.Price
	case "image":
		cost.PerImage = raw.Image.Output.Price
	}
	return cost, validateCost(cost)
}

// applyV2Cache projects scalar and duration prices into supported billing buckets.
// A declared short-duration amount supplies the aggregate fallback when the
// upstream response reports no duration detail. A new explicit write object
// replaces the preceding write rates as a unit.
func applyV2Cache(cost *Cost, price catalogPriceV2) error {
	if price.Cache.Read != nil {
		cost.CacheRead = price.Cache.Read
	}
	raw := price.Cache.Write
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	cost.CacheWrite = nil
	cost.CacheWrite1h = nil
	cost.CacheWrite30m = nil
	cost.CacheWrite12h = nil
	if raw[0] != '{' {
		return json.Unmarshal(raw, &cost.CacheWrite)
	}
	var writes map[string]*float64
	if err := json.Unmarshal(raw, &writes); err != nil {
		return err
	}
	cost.CacheWrite = writes["5m"]
	cost.CacheWrite1h = writes["1h"]
	cost.CacheWrite30m = writes["30m"]
	cost.CacheWrite12h = writes["12h"]
	if cost.CacheWrite == nil {
		cost.CacheWrite = cost.CacheWrite30m
	}
	return nil
}

// projectV2MediaPrices projects per-unit output rates with exact independent
// resolution conditions. The base video rate covers the gateway's default
// resolution. An image without a size table continues to use its flat rate.
// Compound conditions need more request evidence than the size lookup carries.
func projectV2MediaPrices(rate catalogRateV2, unit, defaultResolution string) (map[string]float64, error) {
	if rate.Unit != unit {
		return nil, nil
	}
	var prices map[string]float64
	if defaultResolution != "" && rate.Price != nil {
		prices = map[string]float64{defaultResolution: *rate.Price}
	}
	for _, override := range rate.Overrides {
		var resolution struct {
			Equals string `json:"equals"` // exact advertised output resolution
		}
		if len(override.When) != 1 || json.Unmarshal(override.When["resolution"], &resolution) != nil || override.Price == nil {
			continue
		}
		label := strings.ToLower(resolution.Equals)
		if unit == "image" {
			if !slices.Contains([]string{"1k", "2k", "4k"}, label) {
				continue
			}
			label = strings.ToUpper(label)
		}
		if label == "" {
			continue
		}
		if prices == nil {
			prices = map[string]float64{}
		}
		prices[label] = *override.Price
	}
	for _, amount := range prices {
		if err := validateCost(Cost{PerImage: &amount}); err != nil {
			return nil, err
		}
	}
	return prices, nil
}

// projectV2Context merges per-bucket context overrides into absolute ranges.
// The gateway's range uses an excluded lower bound, so an inclusive threshold
// becomes one less before sorting. Conditions on other dimensions are left
// for their own pricing policy and do not change unconditional context bands.
func projectV2Context(raw catalogCostV2) ([]CostTier, error) {
	tiers := map[int]Cost{}
	for _, rates := range []struct {
		input bool
		rate  catalogRateV2
	}{{true, raw.Text.Input}, {false, raw.Text.Output}} {
		if rates.rate.Unit != "" {
			continue
		}
		for _, override := range rates.rate.Overrides {
			var condition map[string]json.RawMessage
			if len(override.When) != 1 || json.Unmarshal(override.When["context"], &condition) != nil || len(condition) != 1 {
				continue
			}
			var bound int
			operator := "gt"
			if condition["gte"] != nil {
				operator = "gte"
			}
			threshold := condition[operator]
			if threshold == nil {
				continue
			}
			if err := json.Unmarshal(threshold, &bound); err != nil || bound < 0 || operator == "gte" && bound == 0 {
				return nil, fmt.Errorf("invalid context threshold %s", threshold)
			}
			if operator == "gte" {
				// Convert inclusive bounds to the internal excluded lower bound.
				bound--
			}
			cost := tiers[bound]
			if rates.input {
				cost.Input = override.Price
				if err := applyV2Cache(&cost, override.catalogPriceV2); err != nil {
					return nil, err
				}
			} else {
				cost.Output = override.Price
			}
			tiers[bound] = cost
		}
	}
	out := make([]CostTier, 0, len(tiers))
	for bound, cost := range tiers {
		tier := CostTier{Cost: cost, Exclusive: true}
		tier.Tier.Type = "context"
		tier.Tier.Size = bound
		out = append(out, tier)
	}
	return out, nil
}

// projectV2ServicePrices selects service and date rules supported by billing.
// Missing region or arbitrary request attributes cannot establish a matching
// rule. Explicit mode prices form independent overrides before multiplier
// evaluation, and date-only comparisons are retained in their source timezone.
func projectV2ServicePrices(raw catalogModelV2, service string) ([]ServicePrice, error) {
	var rows []ServicePrice
	for _, name := range []string{"fast", "priority", "flex", "ultrafast"} {
		mode := raw.Experimental.Modes[name]
		if mode.Cost == nil {
			continue
		}
		tier := mode.Tier
		if tier == "" {
			tier = mode.Body.ServiceTier
		}
		if tier == "" {
			tier = name
		}
		cost, err := projectV2Cost(*mode.Cost)
		if err != nil {
			return nil, err
		}
		if normalized := normalizeV2Tier(tier); normalized != "" {
			rows = append(rows, ServicePrice{Tier: normalized, Cost: &cost})
		}
	}
	for _, multiplier := range raw.Cost.Multipliers {
		row, supported := projectV2PriceCondition(multiplier.When, service)
		if !supported {
			continue
		}
		if err := readV2Factors(multiplier.Factor, &row); err != nil {
			return nil, err
		}
		if row.Factor != nil || row.TextFactor != nil || row.Factors != nil {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// projectV2PriceCondition compiles price rules needing only service tier,
// date, or both. Date-only rows have an empty tier and apply independently
// after the tier is selected. Other conditions require request evidence the
// directory pricing path does not carry, so those rows remain inapplicable.
func projectV2PriceCondition(when map[string]json.RawMessage, service string) (ServicePrice, bool) {
	var row ServicePrice
	tierCondition, dateCondition := when["service_tier"], when["date"]
	supported := 0
	if tierCondition != nil {
		var equality struct {
			Equals string `json:"equals"` // required service tier
		}
		if err := json.Unmarshal(tierCondition, &equality); err != nil {
			return ServicePrice{}, false
		}
		row.Tier = normalizeV2Tier(equality.Equals)
		if row.Tier == "" {
			return ServicePrice{}, false
		}
		supported++
	}
	if dateCondition != nil {
		var date map[string]string
		if err := json.Unmarshal(dateCondition, &date); err != nil || len(date) != 1 {
			return ServicePrice{}, false
		}
		row.Before = date["lt"]
		row.After = date["gte"]
		if row.After == "" && row.Before == "" {
			return ServicePrice{}, false
		}
		row.Zone = "UTC"
		if service == "openai" {
			row.Zone = "America/Los_Angeles"
		}
		supported++
	}
	return row, supported > 0 && len(when) == supported
}

// readV2Factors decodes the supported token prices a multiplier adjusts.
// A plain number affects all token buckets. An object can limit a factor to
// text, image, or named individual rates. Omitted buckets in that object
// inherit their existing price, and non-token units keep their own rules.
func readV2Factors(raw json.RawMessage, row *ServicePrice) error {
	if len(raw) == 0 {
		return nil
	}
	var factors Cost
	if raw[0] != '{' {
		if err := json.Unmarshal(raw, &row.Factor); err != nil {
			return err
		}
		return validateCost(Cost{Input: row.Factor})
	}
	var modalities map[string]json.RawMessage
	if err := json.Unmarshal(raw, &modalities); err != nil {
		return err
	}
	if text := modalities["text"]; len(text) > 0 {
		if text[0] != '{' {
			if err := json.Unmarshal(text, &row.TextFactor); err != nil {
				return err
			}
		} else {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(text, &fields); err != nil {
				return err
			}
			input, err := readV2RateFactor(fields["input"])
			if err != nil {
				return err
			}
			output, err := readV2RateFactor(fields["output"])
			if err != nil {
				return err
			}
			factors.Input, factors.Output = input.Price, output.Price
			if err := applyV2Cache(&factors, input); err != nil {
				return err
			}
			// A scalar input factor includes its cached input buckets. An object
			// changes only the input and cache prices it names in the directory.
			if value := fields["input"]; len(value) > 0 && value[0] != '{' {
				factors.CacheRead, factors.CacheWrite = input.Price, input.Price
			}
		}
	}
	if image := modalities["image"]; len(image) > 0 {
		if image[0] == '{' {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(image, &fields); err != nil {
				return err
			}
			input, err := readV2RateFactor(fields["input"])
			if err != nil {
				return err
			}
			output, err := readV2RateFactor(fields["output"])
			if err != nil {
				return err
			}
			factors.ImageInput, factors.ImageRead, factors.ImageOutput = input.Price, input.Cache.Read, output.Price
			if value := fields["input"]; len(value) > 0 && value[0] != '{' {
				factors.ImageRead = input.Price
			}
		} else {
			if err := json.Unmarshal(image, &factors.ImageInput); err != nil {
				return err
			}
			factors.ImageOutput, factors.ImageRead = factors.ImageInput, factors.ImageInput
		}
	}
	if err := validateCost(Cost{Input: row.Factor, Output: row.TextFactor}); err != nil {
		return err
	}
	if err := validateCost(factors); err != nil {
		return err
	}
	if factors != (Cost{}) {
		row.Factors = &factors
	}
	return nil
}

// readV2RateFactor normalizes one scalar or bucket-specific pricing factor.
func readV2RateFactor(raw json.RawMessage) (catalogPriceV2, error) {
	var factor catalogPriceV2
	if len(raw) == 0 {
		return factor, nil
	}
	if raw[0] != '{' {
		err := json.Unmarshal(raw, &factor.Price)
		return factor, err
	}
	err := json.Unmarshal(raw, &factor)
	return factor, err
}

// normalizeV2Tier maps the source's supported tier names to billing labels.
func normalizeV2Tier(tier string) string {
	switch tier {
	case "fast", "priority":
		return "priority"
	case "flex", "ultrafast":
		return tier
	default:
		return ""
	}
}
