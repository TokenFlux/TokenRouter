package modelrules

import (
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// resolution.go resolves route capabilities and execution rules from catalog,
// platform, and administrator data.
// A route's metadata depends on the actual service, authentication, protocols,
// and complete outbound model identity. Recognized Google resource paths are
// reduced for catalog lookup while the Target retains its outbound spelling,
// and recognized service qualification keeps relay attributes separate from
// the author's metadata.
//
// Resolution reads one retained catalog publication, then applies platform
// patches, administrator overrides, and adapter restrictions in that order.
// Request conditions are evaluated later against reasoning state, leaving the
// display attributes reusable. Cache keys capture output caps by value and
// retain service detection results; attribute caches can omit the raw address,
// while caches returning the complete route keep it.

// RequestState distinguishes unknown display state from reasoning options
// supplied by a request.
type RequestState struct {
	// Effort and Thinking hold request choices rather than display capabilities.
	// Nil choices are unknown until ForRequest applies its model and platform
	// defaults before testing the corresponding parameter conditions.
	Effort   *string // selected request effort value
	Thinking *bool   // selected request thinking state
}

// ExecutionRules contains message-encoding choices interpreted by platform adapters.
type ExecutionRules struct {
	ThinkingMode  string `json:"thinking_mode,omitempty"`  // adapter thinking encoding choice
	SignatureMode string `json:"signature_mode,omitempty"` // adapter reasoning signature choice
	// EffortAliases maps request effort strings to their upstream replacements.
	// ApplyModelAttributeParameters rewrites recognized effort fields using
	// these mappings.
	EffortAliases          map[string]string `json:"effort_aliases,omitempty"`
	DropFields             []string          `json:"drop_fields,omitempty"`              // request fields removed before forwarding
	OmitEmptyToolConfig    bool              `json:"omit_empty_tool_config,omitempty"`   // omit empty Gemini tool configuration
	OmitStopSequences      bool              `json:"omit_stop_sequences,omitempty"`      // omit transformed Gemini stop sequences
	AdaptiveThinkingBudget int               `json:"adaptive_thinking_budget,omitempty"` // tokens
	// BudgetPadding reserves output tokens above the thinking budget.
	// GeminiThinking subtracts it from the resolved output limit when bounding
	// a positive budget.
	BudgetPadding int `json:"budget_padding,omitempty"`
	// ImageGenerationTool, OutputParameter, ToolSearch, and Verbosity retain nil
	// when no control rule is declared. Populated booleans distinguish declared
	// support from a declared restriction in the serialized execution metadata.
	ImageGenerationTool *bool    `json:"image_generation_tool,omitempty"` // image generation tool support
	ClampOutput         bool     `json:"clamp_output,omitempty"`          // clamp supplied output token parameters
	OutputParameter     *bool    `json:"output_parameter,omitempty"`      // output token parameter support
	CacheTTL            []string `json:"cache_ttl,omitempty"`             // registered cache duration labels
	ToolSearch          *bool    `json:"tool_search,omitempty"`           // tool search control support
	Verbosity           *bool    `json:"verbosity,omitempty"`             // output verbosity parameter support
}

// Resolution contains a route's model attributes, field sources, and
// message-encoding rules.
type Resolution struct {
	// ReferenceModel identifies the profile target supplying effective attributes.
	// Target continues to describe the actual outbound model and execution route.
	ReferenceModel string `json:"reference_model,omitempty"`
	// ConfiguredAttributes preserves the effective profile inputs when the wire
	// adapter constrains them. Preview exposes both values to explain a conflict.
	ConfiguredAttributes *modelcatalog.Attributes `json:"configured_attributes,omitempty"`

	Target     Target                  `json:"target"`     // route inputs with detected service
	Attributes modelcatalog.Attributes `json:"attributes"` // resolved model capability values
	Sources    map[string]string       `json:"sources"`    // effective attribute source labels
	Rules      ExecutionRules          `json:"rules"`      // adapter message encoding decisions
	Version    string                  `json:"version"`    // retained catalog publication version
}

// View retains one catalog publication for consistent route metadata resolution.
type View struct {
	// catalog is retained by reference. Loaders publish a different Catalog for
	// updates and keep this publication's maps read-only for existing views.
	catalog modelcatalog.Publication
	// lookupModel supplies base attributes for a single-model reader, before the
	// same route, provider and profile rules used by a full catalog publication.
	lookupModel func(string) modelcatalog.Attributes
}

// NewView returns a reader retaining this catalog's current maps and values.
// Callers complete catalog mutations before sharing the view and keep the
// publication read-only while any view uses it.
func NewView(c modelcatalog.Publication) View { return View{catalog: c} }

// ViewFromLookup adapts a single-model attribute reader to route resolution.
// The caller keeps the reader stable for the lifetime of the view. Nil
// supplies unknown base attributes, while platform and profile rules still run.
func ViewFromLookup(lookup func(string) modelcatalog.Attributes) View {
	return View{lookupModel: lookup}
}

// Version returns the content version of the catalog retained by this view.
// A zero view returns an empty string; publishing another catalog leaves
// this view's version attached to its retained metadata.
func (v View) Version() string {
	return v.catalog.Version()
}

// Resolve returns model attributes and encoding rules for one target using
// the retained catalog version. Service recognition fixes lookup identity;
// OpenCode canonical references, provider-supplied identities, and the registered
// Antigravity thinking alias affect metadata lookup while Target.Model retains
// its outbound identity.
//
// Catalog attributes are copied, platform patches precede administrator
// overrides, and adapter restrictions apply last. Explicitly disabled reasoning
// clears configurable reasoning choices and records the disabling field's
// source. modelcatalog.Attributes and source labels are independently mutable; the Target's
// optional output-cap pointer can still belong to the input until Clone is used.
func (v View) Resolve(target Target, override modelcatalog.Attributes) Resolution {
	return v.resolve(target.WithServiceIdentity(), override, "")
}

// ResolveReference borrows attributes for a profile reference while retaining
// the actual route for encoding restrictions. The referenced model can belong
// to this account, the public catalog, or neither. Conditions use the reference
// identity and native encoders continue to receive the original route.
func (v View) ResolveReference(route Target, reference string, override modelcatalog.Attributes) Resolution {
	return v.resolve(route.WithServiceIdentity(), override, reference)
}

// resolve reads catalog metadata and merges populated provider and profile fields.
// A reference changes the metadata lookup target, while the actual route
// selects the native encoder constraints. An explicit configuration stays available to
// preview when its merged values exceed the adapter's representation.
func (v View) resolve(route Target, override modelcatalog.Attributes, reference string) Resolution {
	target := route
	if reference != "" {
		target = route.ForReference(reference)
	}
	result := Resolution{Target: target, Sources: map[string]string{}, Version: v.Version()}
	model := target.Model
	if target.usesCanonicalIdentity() {
		// The offering's outbound name can differ from its author's model. Read
		// the canonical reference from this publication, so a refreshed catalog
		// supplies the alias's attributes and encoding rules.
		if target.AttributeModel == "" {
			mode := target.ProviderMode
			if mode == "" {
				mode = "payg"
			}
			target.AttributeModel = v.catalog.Offering(openCodeService(mode), model).CanonicalModelID
		}
		if target.AttributeModel != "" {
			model = target.AttributeModel
		}
		result.Target = target
	}
	var providerAttrs modelcatalog.Attributes
	if target.ProviderModels != nil {
		identity, attrs, found := target.ProviderModels.LookupAttributes(target.Model)
		model, providerAttrs = identity, attrs
		result.Target.AttributeModel = identity
		if !found {
			// A profile reference may explicitly select catalog data outside the
			// account. Ordinary unknown routes have no inferred public identity.
			model = target.AttributeModel
		}
	}
	if target.Platform == "antigravity" && model == "claude-sonnet-4-5-thinking" {
		// This registered thinking alias shares Sonnet 4.5 metadata while the
		// target keeps the alias required for outbound Antigravity model routing.
		model = "claude-sonnet-4-5"
	}
	entry := v.lookup(target, model)
	result.Attributes = entry.Attributes.Clone()
	for _, field := range result.Attributes.FieldNames() {
		result.Sources[field] = "models.dev"
	}
	maps.Copy(result.Sources, entry.AttributeSources)
	result.applyPlatform()
	if target.ProviderModels != nil {
		result.patch(providerAttrs, "provider")
	}
	result.patch(override, "config")
	if result.Attributes.Reasoning != nil && !*result.Attributes.Reasoning {
		// Disabled reasoning takes precedence over inherited adjustable
		// controls. Record the same source on the empty choices so request
		// consumers see their origin.
		result.Attributes.ReasoningOptions = new([]modelcatalog.ReasoningOption{})
		result.Sources["reasoning_options"] = result.Sources["reasoning"]
	}
	configured := result.Attributes
	result.applyAdapter()
	if target.ProviderModels != nil {
		result.patch(target.ProviderModels.AdapterRestrictions(route.Model, result.Attributes), "adapter")
	}
	// A reference explicitly borrows all its model fields. For direct overrides,
	// a restriction on an unrelated inherited field should not produce a warning.
	if (reference != "" || override != (modelcatalog.Attributes{})) && !reflect.DeepEqual(configured, result.Attributes) &&
		(reference != "" || !reflect.DeepEqual(modelcatalog.Merge(result.Attributes, override), result.Attributes)) {
		result.ConfiguredAttributes = &configured
	}
	if reference != "" {
		route.AttributeModel = result.Target.AttributeModel
		result.Target, result.ReferenceModel = route, reference
	}
	return result
}

// lookup supplies base catalog data for a provider's resolved model identity.
// Resource-path normalization and author aliases precede catalog lookup. A
// callback-backed view retains the original spelling expected by its caller.
func (v View) lookup(target Target, model string) modelcatalog.Entry {
	if model != "" && v.lookupModel != nil {
		return modelcatalog.Entry{Attributes: v.lookupModel(model)}
	}
	if target.Platform == "gemini" || target.Platform == "antigravity" {
		model = googleResourceModel(normalize(model))
	}
	service := target.ServiceID()
	return v.catalog.LookupRoute(service, publicLab(service), model)
}

// patch merges supplied attributes and assigns their populated fields to
// source. Media receives per-field source labels, including explicit empty
// choices; callers initialize Sources before invoking this mutation.
func (r *Resolution) patch(attrs modelcatalog.Attributes, source string) {
	for _, field := range attrs.FieldNames() {
		if field != "media" {
			r.Sources[field] = source
		}
	}
	if attrs.Media != nil {
		for _, field := range attrs.Media.FieldNames() {
			r.Sources["media."+field] = source
		}
	}
	r.Attributes = modelcatalog.Merge(r.Attributes, attrs)
}

// ForRequest evaluates attribute conditions against the request's reasoning
// state and returns a copy of the attributes. Source labels, rules, and target
// references remain shared with the receiver and are treated as read-only.
//
// Missing thinking state uses adaptive-mode defaults for Claude Messages and enabled
// thinking for DeepSeek. The retained canonical identity supplies these defaults
// for OpenCode aliases. Profile references use their target model for these
// conditions while retaining the outbound Target. Missing effort defaults to none for the registered
// GPT-5.2 and GPT-5.4 identities. Every supplied condition must match its
// request state; a mismatch disables that parameter without changing the
// display attributes retained by the receiver.
func (r Resolution) ForRequest(state RequestState) Resolution {
	target := r.Target
	if r.ReferenceModel != "" {
		target.Model = r.ReferenceModel
	}
	author, model := target.modelEncodingIdentity()
	r.Attributes = r.Attributes.Clone()
	if state.Thinking == nil {
		// Conditional parameters need the adapter's default thinking state when
		// the client omits a choice, so their eligibility agrees with that
		// request's encoding.
		if r.Target.UpstreamProtocol == "anthropic_messages" && r.Rules.SignatureMode == "anthropic" {
			state.Thinking = new(r.Rules.ThinkingMode == "adaptive" || r.Rules.ThinkingMode == "adaptive_between_tools")
		}
		switch author {
		case "anthropic":
			state.Thinking = new(r.Rules.ThinkingMode == "adaptive" || r.Rules.ThinkingMode == "adaptive_between_tools")
		case "deepseek":
			state.Thinking = new(true)
		}
	}
	if r.Attributes.Conditions == nil {
		return r
	}
	for parameter, condition := range *r.Attributes.Conditions {
		allowed := true
		if condition.Thinking != nil {
			allowed = state.Thinking != nil && *state.Thinking == *condition.Thinking
		}
		if len(condition.Efforts) > 0 {
			effort := ""
			if state.Effort != nil {
				effort = *state.Effort
			} else if slices.Contains([]string{"gpt-5.2", "gpt-5.4"}, strings.TrimPrefix(model, "openai/")) {
				// These registered models use none when effort is omitted.
				// Evaluate their sampling conditions using that default,
				// allowing the same fields as an explicit none request.
				effort = "none"
			}
			allowed = allowed && slices.Contains(condition.Efforts, effort)
		}
		if !allowed {
			switch parameter {
			case "temperature":
				r.Attributes.Temperature = new(false)
			case "top_p":
				r.Attributes.TopP = new(false)
			case "tool_call":
				r.Attributes.ToolCall = new(false)
			}
		}
	}
	return r
}

// Clone returns an independently mutable resolution by copying the target's
// optional cap, nested attributes, source map, and mutable rule values.
// Nil and empty values retain their meanings for later request adaptation.
func (r Resolution) Clone() Resolution {
	if r.ConfiguredAttributes != nil {
		configured := r.ConfiguredAttributes.Clone()
		r.ConfiguredAttributes = &configured
	}
	r.Target.OllamaOutputCap = clonePointer(r.Target.OllamaOutputCap)
	r.Attributes = r.Attributes.Clone()
	r.Sources = maps.Clone(r.Sources)
	r.Rules.EffortAliases = maps.Clone(r.Rules.EffortAliases)
	r.Rules.DropFields = slices.Clone(r.Rules.DropFields)
	r.Rules.CacheTTL = slices.Clone(r.Rules.CacheTTL)
	r.Rules.ImageGenerationTool = clonePointer(r.Rules.ImageGenerationTool)
	r.Rules.OutputParameter = clonePointer(r.Rules.OutputParameter)
	r.Rules.ToolSearch = clonePointer(r.Rules.ToolSearch)
	r.Rules.Verbosity = clonePointer(r.Rules.Verbosity)
	return r
}

// clonePointer detaches optional scalar rule values before returning cached resolutions.
func clonePointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	return new(*p)
}
