package modelcatalog

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
)

// attributes.go merges model attribute sources and computes capabilities
// shared by requestable routes.
// Model attributes distinguish an unknown capability from an explicit false or
// empty value. Supplements fill missing fields, while administrator patches
// replace populated fields and return copies for request and display consumers.
// An empty modality list therefore remains a meaningful restriction after a
// merge and blocks values from a lower priority source.
//
// A displayed model can have several requestable routes with different limits
// and conditions. Common combines their restrictions, retains the smallest
// known limits, and disables a conditional capability when the routes have no
// compatible request state. Its difference flag records any disagreement with
// the first route, even when the displayed common values remain unchanged.

// Attributes describes model properties and capabilities with optional overrides.
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_attributes
type Attributes struct {
	// DisplayNameLocalization, DisplayName, Context, InputLimit, OutputLimit,
	// InputModalities, OutputModalities, Reasoning, ToolCall, StructuredOutput,
	// Temperature, Attachment, TopP, ReasoningOptions, Interleaved, Conditions,
	// and Media distinguish missing information from supplied values. Merge
	// inherits nil fields and preserves explicit false values and empty lists.
	// Validate requires supplied Context, InputLimit, and OutputLimit to be
	// positive token counts.
	DisplayNameLocalization *locale.Update[string] `json:"display_name_localization,omitempty"` // localized display name edits
	DisplayName             *string                `json:"display_name,omitempty"`              // default model display name
	Context                 *int                   `json:"context,omitempty"`                   // tokens
	InputLimit              *int                   `json:"input_limit,omitempty"`               // tokens
	OutputLimit             *int                   `json:"output_limit,omitempty"`              // tokens
	InputModalities         *[]string              `json:"input_modalities,omitempty"`          // accepted input modality names
	OutputModalities        *[]string              `json:"output_modalities,omitempty"`         // produced output modality names
	Reasoning               *bool                  `json:"reasoning,omitempty"`                 // model reasoning capability flag
	ToolCall                *bool                  `json:"tool_call,omitempty"`                 // tool call capability flag
	StructuredOutput        *bool                  `json:"structured_output,omitempty"`         // structured output capability flag
	Temperature             *bool                  `json:"temperature,omitempty"`               // temperature parameter capability flag
	Attachment              *bool                  `json:"attachment,omitempty"`                // attachment input capability flag
	TopP                    *bool                  `json:"top_p,omitempty"`                     // top_p parameter capability flag
	ReasoningOptions        *[]ReasoningOption     `json:"reasoning_options,omitempty"`         // configurable reasoning control choices
	Interleaved             *Interleaved           `json:"interleaved,omitempty"`               // interleaved reasoning representation choice
	Conditions              *ParameterConditions   `json:"conditions,omitempty"`                // request state capability restrictions
	Media                   *MediaCapabilities     `json:"media,omitempty"`                     // media adapter parameter capabilities
}

// Presentation contains model metadata for display; it does not authorize gateway requests.
type Presentation struct {
	// Variants preserves distinct route attributes for marketplace display. Each
	// value is a detached attribute combination, containing no provider route identity.
	Variants               []Attributes       `json:"variants,omitempty"`
	SearchTerms            []string           `json:"search_terms,omitempty"`            // alternate model search terms
	LocalizationResolution *locale.Resolution `json:"localization_resolution,omitempty"` // selected display name language
	Attributes                                // effective model display attributes
	RouteDifferences       bool               `json:"route_differences,omitempty"` // differing requestable route attributes
}

// Merge applies populated patch fields and returns attributes whose mutable
// values are copied from both inputs. Nil patch fields inherit their base
// values, while explicit false values and empty lists replace them.
//
// Media has its own field merge so a patch can replace image sizes while
// retaining an existing reference limit. The final clone separates pointers,
// slices, condition maps, and localization edits from the inputs before a
// request or display consumer receives the result.
func Merge(base, patch Attributes) Attributes {
	if patch.DisplayNameLocalization != nil {
		base.DisplayNameLocalization = patch.DisplayNameLocalization
	}
	if patch.DisplayName != nil {
		base.DisplayName = patch.DisplayName
	}
	if patch.Context != nil {
		base.Context = patch.Context
	}
	if patch.InputLimit != nil {
		base.InputLimit = patch.InputLimit
	}
	if patch.OutputLimit != nil {
		base.OutputLimit = patch.OutputLimit
	}
	if patch.InputModalities != nil {
		base.InputModalities = patch.InputModalities
	}
	if patch.OutputModalities != nil {
		base.OutputModalities = patch.OutputModalities
	}
	if patch.Reasoning != nil {
		base.Reasoning = patch.Reasoning
	}
	if patch.ToolCall != nil {
		base.ToolCall = patch.ToolCall
	}
	if patch.StructuredOutput != nil {
		base.StructuredOutput = patch.StructuredOutput
	}
	if patch.Temperature != nil {
		base.Temperature = patch.Temperature
	}
	if patch.Attachment != nil {
		base.Attachment = patch.Attachment
	}
	if patch.TopP != nil {
		base.TopP = patch.TopP
	}
	if patch.ReasoningOptions != nil {
		base.ReasoningOptions = patch.ReasoningOptions
	}
	if patch.Interleaved != nil {
		base.Interleaved = patch.Interleaved
	}
	if patch.Conditions != nil {
		base.Conditions = patch.Conditions
	}
	base.Media = mergeMedia(base.Media, patch.Media)
	return base.Clone()
}

// ApplyAttributeSupplement fills missing metadata from a validated supplement
// and records local_supplement as each newly populated field's source. Catalog
// false values and empty lists remain authoritative because their fields are
// supplied through non-nil attribute pointers.
//
// The supplement's provider must match an existing entry when specified.
// Ambiguous bare names are skipped, while a matching public entry can still
// receive missing metadata. A previously unknown, unambiguous model gains an
// attribute entry so it can be displayed even when no price is supplied.
func (c *Catalog) ApplyAttributeSupplement(model string, body json.RawMessage) error {
	var supplement struct {
		Provider   string      `json:"provider"`   // optional owning catalog provider
		Attributes *Attributes `json:"attributes"` // supplemental model capability values
	}
	if err := json.Unmarshal(body, &supplement); err != nil {
		return fmt.Errorf("invalid model supplement %s: %w", model, err)
	}
	if supplement.Attributes == nil {
		return nil
	}
	if err := supplement.Attributes.Validate(); err != nil {
		return fmt.Errorf("invalid model attributes %s: %w", model, err)
	}
	key := normalize(model)
	if public, exists := c.publicEntries[key]; exists && (supplement.Provider == "" || supplement.Provider == public.Provider) {
		public = fillAttributeSupplement(public, *supplement.Attributes)
		c.publicEntries[key] = public
	}
	entry, exists := c.Entries[key]
	// Provider conflicts and ambiguous bare names have no unique metadata owner.
	// Keep their existing attributes so a supplement cannot attach another service's capabilities.
	if c.Ambiguous[key] || (exists && supplement.Provider != "" && entry.Provider != supplement.Provider) {
		return nil
	}
	if !exists {
		entry = Entry{Model: model, Provider: supplement.Provider, Source: "local_supplement"}
	}
	entry = fillAttributeSupplement(entry, *supplement.Attributes)
	c.Entries[key] = entry
	// A verified supplement can restore a public author identity missing from
	// v2 provider offerings. Restrict it to an explicit author-qualified key so
	// a relay's qualified price and capability entry cannot become public metadata.
	lab, _, qualified := strings.Cut(key, "/")
	if qualified && supplement.Provider == lab && catalogProviderOrigin(lab).lab == lab {
		if _, published := c.publicEntries[key]; !published {
			if c.publicEntries == nil {
				c.publicEntries = map[string]Entry{}
			}
			c.publicEntries[key] = Entry{
				Model: key, Provider: lab, Canonical: key,
				Source: "local_supplement", Attributes: entry.Attributes, AttributeSources: entry.AttributeSources,
			}
		}
	}
	return nil
}

// fillAttributeSupplement fills absent catalog attributes from a local
// supplement and returns independently mutable attributes and source labels.
// A non-nil top-level pointer counts as supplied even when its slice or map
// value is nil, so that field keeps its existing source label.
//
// Source labels are cloned before missing catalog fields receive the
// local_supplement label. Merge receives the supplement as its base and the
// catalog attributes as its patch, giving populated catalog fields precedence
// while cloning mutable values from both inputs. Media child fields follow
// Merge's separate per-field rules; their top-level source label still follows
// the presence of the catalog Media pointer.
func fillAttributeSupplement(entry Entry, attributes Attributes) Entry {
	known := map[string]struct{}{}
	for _, field := range entry.Attributes.FieldNames() {
		known[field] = struct{}{}
	}
	entry.AttributeSources = maps.Clone(entry.AttributeSources)
	if entry.AttributeSources == nil {
		entry.AttributeSources = map[string]string{}
	}
	for _, field := range attributes.FieldNames() {
		if _, found := known[field]; !found {
			entry.AttributeSources[field] = "local_supplement"
		}
	}
	entry.Attributes = Merge(attributes, entry.Attributes)
	return entry
}

// Validate rejects invalid administrator capabilities, nonpositive token
// limits, and duplicate or unsupported modalities. Nil fields and empty
// modality lists are valid inputs that retain their inheritance and clearing
// meanings during Merge.
func (a Attributes) Validate() error {
	if err := validateCapabilities(a); err != nil {
		return err
	}
	for key, value := range map[string]*int{"context": a.Context, "input_limit": a.InputLimit, "output_limit": a.OutputLimit} {
		if value != nil && *value <= 0 {
			return fmt.Errorf("%s must be positive", key)
		}
	}
	for _, values := range []*[]string{a.InputModalities, a.OutputModalities} {
		if values == nil {
			continue
		}
		seen := map[string]bool{}
		for _, value := range *values {
			if !slices.Contains([]string{"text", "image", "audio", "video", "pdf"}, value) || seen[value] {
				return fmt.Errorf("invalid or duplicate modality: %s", value)
			}
			seen[value] = true
		}
	}
	return nil
}

// Common combines requestable route capabilities and reports whether any
// route differs from the first. Empty input returns unknown attributes and a
// false difference flag; the result's mutable values are copied.
//
// Known limits contribute their minimum even when another route's limit is
// unknown. Capabilities use false when any route rejects them, and otherwise
// remain unknown when a route has no information. Contradictory conditions
// disable their parameter, while modality and reasoning choices retain their
// intersection for display and configuration export.
//
// A display name survives when every route supplies the same name, and
// interleaving survives when its representations are equal. Localization edits
// retain the first route's value; callers resolve the display language separately.
func Common(values []Attributes) (Attributes, bool) {
	if len(values) == 0 {
		return Attributes{}, false
	}
	result := values[0]
	first := values[0]
	different := false
	for _, value := range values[1:] {
		different = different || !reflect.DeepEqual(value, first)
		result.Context = MinimumLimit(result.Context, value.Context)
		result.InputLimit = MinimumLimit(result.InputLimit, value.InputLimit)
		result.OutputLimit = MinimumLimit(result.OutputLimit, value.OutputLimit)
		result.Reasoning = commonBool(result.Reasoning, value.Reasoning)
		result.ToolCall = commonBool(result.ToolCall, value.ToolCall)
		result.StructuredOutput = commonBool(result.StructuredOutput, value.StructuredOutput)
		result.Temperature = commonBool(result.Temperature, value.Temperature)
		result.Attachment = commonBool(result.Attachment, value.Attachment)
		result.TopP = commonBool(result.TopP, value.TopP)
		result.ReasoningOptions = commonReasoningOptions(result.ReasoningOptions, value.ReasoningOptions)
		var unavailable []string
		result.Conditions, unavailable = commonConditions(result.Conditions, value.Conditions)
		// Contradictory request conditions leave no state supported by both routes.
		// Disable those parameters so exports cannot promise an unusable shared capability.
		for _, field := range unavailable {
			switch field {
			case "temperature":
				result.Temperature = valuePointer(false)
			case "top_p":
				result.TopP = valuePointer(false)
			case "tool_call":
				result.ToolCall = valuePointer(false)
			}
		}
		result.Media = commonMedia(result.Media, value.Media)
		if !reflect.DeepEqual(result.Interleaved, value.Interleaved) {
			result.Interleaved = nil
		}
		result.InputModalities = IntersectStrings(result.InputModalities, value.InputModalities)
		result.OutputModalities = IntersectStrings(result.OutputModalities, value.OutputModalities)
		if result.DisplayName == nil || value.DisplayName == nil || *result.DisplayName != *value.DisplayName {
			result.DisplayName = nil
		}
	}
	return result.Clone(), different
}

// MinimumLimit selects the smaller known limit, borrowing the selected input
// pointer. Nil means unknown; callers that expose mutable results copy the
// pointer before returning it to consumers.
func MinimumLimit(a, b *int) *int {
	if a == nil {
		return b
	}
	if b != nil && *b < *a {
		return b
	}
	return a
}

// commonBool combines support declarations, with false taking precedence over
// unknown. A supported result borrows a's pointer, so consumers that mutate
// aggregate attributes use Clone before exposing them.
func commonBool(a, b *bool) *bool {
	if a != nil && !*a || b != nil && !*b {
		value := false
		return &value
	}
	if a == nil || b == nil {
		return nil
	}
	return a
}

// IntersectStrings returns shared choices in the left input's order using a new
// slice. An unknown input returns nil; known inputs with no shared choice
// produce an explicitly empty list.
func IntersectStrings(a, b *[]string) *[]string {
	if a == nil || b == nil {
		return nil
	}
	result := []string{}
	for _, value := range *a {
		if slices.Contains(*b, value) {
			result = append(result, value)
		}
	}
	return &result
}
