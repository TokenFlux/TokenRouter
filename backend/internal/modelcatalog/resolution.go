package modelcatalog

import (
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strings"
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

// ProviderModels supplies the attribute contribution of an immutable provider snapshot.
// The provider owns its route names, catalog identities and encoding constraints.
// Resolvers borrow the snapshot throughout a request, including reference lookups.
type ProviderModels interface {
	// Version identifies the provider and the content of this immutable snapshot.
	Version() string
	// RouteKey resolves aliases to the final name used for profile rule matching.
	RouteKey(string) string
	// LookupAttributes returns a catalog identity and sparse provider attributes.
	// A known opaque route can have an empty identity. Unknown references can
	// instead use their explicit profile target for a direct catalog lookup.
	LookupAttributes(model string) (string, Attributes, bool)
	// AdapterRestrictions returns the capability patch imposed by the selected wire
	// format after profile resolution. Returned fields must describe representation
	// limits of the converter, since they affect both display and execution.
	AdapterRestrictions(model string, attrs Attributes) Attributes
}

// Target contains route inputs that determine metadata for a complete outbound
// model.
type Target struct {
	// ProviderModels retains this account's read-only attribute source. Its version
	// participates in cache identity, and different models can share the publication.
	ProviderModels ProviderModels `json:"-"`
	// AttributeModel is the canonical author identity for platforms whose
	// offerings rename another author's model. Resolve derives it from the
	// catalog publication it reads when the caller leaves it empty.
	AttributeModel string `json:"attribute_model,omitempty"`

	Model        string `json:"model"`                   // complete outbound model identity
	Platform     string `json:"platform,omitempty"`      // configured provider platform name
	ProviderType string `json:"provider_type,omitempty"` // configured provider authentication type
	ProviderMode string `json:"provider_mode,omitempty"` // payg or coding service mode
	Site         string `json:"site,omitempty"`          // regional service site identity
	// CatalogProvider selects a catalog service directly when populated.
	// ServiceID uses it before detecting service identity from the other route
	// fields.
	CatalogProvider  string `json:"catalog_provider,omitempty"`
	ClientProtocol   string `json:"client_protocol,omitempty"`   // resolved client protocol identifier
	UpstreamProtocol string `json:"upstream_protocol,omitempty"` // resolved upstream protocol identifier
	Endpoint         string `json:"endpoint,omitempty"`          // endpoint specific capability variant
	// BaseURL supplies the host and path for service recognition. Full route
	// keys retain it, while attribute keys keep its resolved service identity.
	BaseURL string `json:"-"`
	Ollama  bool   `json:"-"` // Ollama output cap policy
	// OllamaOutputCap supplies a provider token cap when present. Nil selects the
	// platform default, and a nonpositive value disables output parameter
	// clamping.
	OllamaOutputCap *int `json:"-"`
}

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
	ConfiguredAttributes *Attributes `json:"configured_attributes,omitempty"`

	Target     Target            `json:"target"`     // route inputs with detected service
	Attributes Attributes        `json:"attributes"` // resolved model capability values
	Sources    map[string]string `json:"sources"`    // effective attribute source labels
	Rules      ExecutionRules    `json:"rules"`      // adapter message encoding decisions
	Version    string            `json:"version"`    // retained catalog publication version
}

// View retains one catalog publication for consistent route metadata resolution.
type View struct {
	// catalog is retained by reference. Loaders publish a different Catalog for
	// updates and keep this publication's maps read-only for existing views.
	catalog *Catalog
	// lookupModel supplies base attributes for a single-model reader, before the
	// same route, provider and profile rules used by a full catalog publication.
	lookupModel func(string) Attributes
}

// TargetKey compares route fields that affect attributes by value.
type TargetKey struct {
	// target retains route values after service detection, with its cap pointer
	// cleared. outputCap and hasOutputCap capture the pointer's value and
	// presence so a missing cap remains distinct from zero during key comparison.
	providerVersion string // immutable provider snapshot identity
	target          Target // service-detected comparable route fields
	outputCap       int    // tokens
	hasOutputCap    bool   // explicit output cap presence
}

// View returns a reader retaining this catalog's current maps and values.
// Callers complete catalog mutations before sharing the view and keep the
// publication read-only while any view uses it.
func (c *Catalog) View() View { return View{catalog: c} }

// ViewFromLookup adapts a single-model attribute reader to route resolution.
// The caller keeps the reader stable for the lifetime of the view. Nil
// supplies unknown base attributes, while platform and profile rules still run.
func ViewFromLookup(lookup func(string) Attributes) View { return View{lookupModel: lookup} }

// Version returns the content version of the catalog retained by this view.
// A zero view returns an empty string; publishing another catalog leaves
// this view's version attached to its retained metadata.
func (v View) Version() string {
	if v.catalog == nil {
		return ""
	}
	return v.catalog.Version
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
// source. Attributes and source labels are independently mutable; the Target's
// optional output-cap pointer can still belong to the input until Clone is used.
func (v View) Resolve(target Target, override Attributes) Resolution {
	return v.resolve(target.WithServiceIdentity(), override, target.Model)
}

// ResolveReference borrows attributes for a profile reference while retaining
// the actual route for encoding restrictions. The referenced model can belong
// to this account, the public catalog, or neither. Conditions use the reference
// identity and native encoders continue to receive the original route.
func (v View) ResolveReference(route Target, reference string, override Attributes) Resolution {
	route = route.WithServiceIdentity()
	target := route.ForReference(reference)
	result := v.resolve(target, override, route.Model)
	route.AttributeModel = result.Target.AttributeModel
	result.Target, result.ReferenceModel = route, reference
	return result
}

// ForReference creates the lookup identity used by a profile reference.
// The original route stays available for outbound encoding. An account can
// normalize the reference as an alias or look up an explicit public identity.
func (t Target) ForReference(reference string) Target {
	t.Model, t.AttributeModel = reference, ""
	if t.ProviderModels != nil {
		t.Model = t.ProviderModels.RouteKey(reference)
		t.AttributeModel = reference
	}
	return t
}

// resolve reads catalog metadata and merges populated provider and profile fields.
// The lookup target can be a reference, while outboundModel selects constraints
// imposed by the actual encoder. An explicit configuration stays available to
// preview when its merged values exceed the adapter's representation.
func (v View) resolve(target Target, override Attributes, outboundModel string) Resolution {
	result := Resolution{Target: target, Sources: map[string]string{}, Version: v.Version()}
	model := target.Model
	if target.usesCanonicalIdentity() {
		// The offering's outbound name can differ from its author's model. Read
		// the canonical reference from this publication, so a refreshed catalog
		// supplies the alias's attributes and encoding rules.
		if target.AttributeModel == "" && v.catalog != nil {
			mode := target.ProviderMode
			if mode == "" {
				mode = "payg"
			}
			target.AttributeModel = v.catalog.OpenCodeOfferings[mode][model].CanonicalModelID
		}
		if target.AttributeModel != "" {
			model = target.AttributeModel
		}
		result.Target = target
	}
	var providerAttrs Attributes
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
	for _, field := range result.Attributes.fieldNames() {
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
		result.Attributes.ReasoningOptions = valuePointer([]ReasoningOption{})
		result.Sources["reasoning_options"] = result.Sources["reasoning"]
	}
	configured := result.Attributes
	result.applyAdapter()
	if target.ProviderModels != nil {
		result.patch(target.ProviderModels.AdapterRestrictions(outboundModel, result.Attributes), "adapter")
		if len(override.fieldNames()) > 0 && !reflect.DeepEqual(Merge(result.Attributes, override), result.Attributes) {
			result.ConfiguredAttributes = &configured
		}
	}
	return result
}

// lookup selects exact service metadata before considering public author
// attributes. Recognized provider-qualified identities stay within that
// provider, and missing qualified entries can use their exact public record.
//
// An identified service first uses its own qualified model entry, then public
// identities when that service lacks the model. Unknown services use the
// catalog's bare identity and unanimous canonical attribute lookup. Returned
// public entries can borrow catalog values; Resolve clones attributes before
// applying platform or administrator patches to them.
func (v View) lookup(target Target, model string) Entry {
	if model != "" && v.lookupModel != nil {
		return Entry{Attributes: v.lookupModel(model)}
	}
	if model == "" || v.catalog == nil {
		return Entry{}
	}
	model = normalize(model)
	if target.Platform == "gemini" || target.Platform == "antigravity" {
		model = googleResourceModel(model)
	}
	if prefix, _, qualified := strings.Cut(model, "/"); qualified && v.catalog.Providers[prefix] {
		if entry, found := v.catalog.Lookup([]string{model}); found && entry.Provider == prefix {
			return entry
		}
		return v.catalog.publicEntries[model]
	}
	service := target.ServiceID()
	if service == "" {
		if entry, found := v.catalog.Lookup([]string{model}); found {
			return entry
		}
		attrs, _ := v.catalog.LookupAttributes(model, nil)
		return Entry{Attributes: attrs}
	}
	if !v.catalog.Providers[service] {
		if entry, exists := v.catalog.publicEntries[service+"/"+model]; exists {
			return entry
		}
	}
	if entry, exists := v.catalog.Lookup([]string{service + "/" + model}); exists && entry.Provider == service {
		return entry
	}
	// This service has no exact record for the model. Read public author
	// metadata so limits from another provider's service cannot become this
	// route's capabilities.
	for _, identity := range []string{model, publicLab(service) + "/" + model} {
		if entry, found := v.catalog.publicEntries[identity]; found {
			return entry
		}
	}
	if entry, found := v.catalog.Lookup([]string{model}); found && entry.Provider == service {
		return entry
	}
	return Entry{}
}

// googleResourceModel returns the short identity from a recognized Google
// resource path while leaving other model strings intact. It accepts models,
// publisher models, and project/location/publisher model paths with the
// expected component counts and nonempty project and location names.
//
// Restricting the shapes keeps custom relay paths and supplier qualifiers from
// being reduced to another service's bare identity. Resolve uses this short
// value for metadata lookup and retains the outbound spelling separately in
// Target.Model for the execution adapter.
func googleResourceModel(model string) string {
	// Compare complete path shapes before discarding any component. This keeps
	// a custom relay path containing models intact even when its last segment
	// resembles a public model.
	parts := strings.Split(model, "/")
	if len(parts) == 2 && parts[0] == "models" {
		return parts[1]
	}
	if len(parts) == 4 && parts[0] == "publishers" && parts[1] == "google" && parts[2] == "models" {
		return parts[3]
	}
	if len(parts) == 8 && parts[0] == "projects" && parts[1] != "" && parts[2] == "locations" && parts[3] != "" && parts[4] == "publishers" && parts[5] == "google" && parts[6] == "models" {
		return parts[7]
	}
	return model
}

// patch merges supplied attributes and assigns their populated fields to
// source. Media receives per-field source labels, including explicit empty
// choices; callers initialize Sources before invoking this mutation.
func (r *Resolution) patch(attrs Attributes, source string) {
	for _, field := range attrs.fieldNames() {
		if field != "media" {
			r.Sources[field] = source
		}
	}
	if attrs.Media != nil {
		for _, field := range attrs.Media.fieldNames() {
			r.Sources["media."+field] = source
		}
	}
	r.Attributes = Merge(r.Attributes, attrs)
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
			state.Thinking = valuePointer(r.Rules.ThinkingMode == "adaptive" || r.Rules.ThinkingMode == "adaptive_between_tools")
		}
		switch author {
		case "anthropic":
			state.Thinking = valuePointer(r.Rules.ThinkingMode == "adaptive" || r.Rules.ThinkingMode == "adaptive_between_tools")
		case "deepseek":
			state.Thinking = valuePointer(true)
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
				r.Attributes.Temperature = valuePointer(false)
			case "top_p":
				r.Attributes.TopP = valuePointer(false)
			case "tool_call":
				r.Attributes.ToolCall = valuePointer(false)
			}
		}
	}
	return r
}

// ServiceID identifies the catalog service from an explicit CatalogProvider
// or recognized route fields. Authentication type, platform, service mode,
// site, and known endpoint hosts participate; unidentified relays return an
// empty service identity.
//
// An explicit identity takes precedence over detection. OpenAI API-compatible
// endpoints are recognized before platform defaults so official Kimi, Zhipu,
// and other compatible services receive their own catalog data. Missing or
// unparseable addresses supply an empty host, allowing the platform and
// authentication defaults to select an official service where configured.
func (t Target) ServiceID() string {
	if t.CatalogProvider != "" {
		return t.CatalogProvider
	}
	// A failed address parse supplies no host for service recognition. Continue
	// with platform, authentication, and site defaults to determine the
	// metadata lookup identity.
	u, _ := url.Parse(t.BaseURL)
	host := ""
	if u != nil {
		host = strings.ToLower(u.Hostname())
	}
	if t.Platform == "openai" && t.ProviderType != "oauth" && t.ProviderType != "setup-token" {
		// Recognized compatible endpoints select their actual service metadata
		// before OpenAI defaults, giving the subsequent platform patch the
		// corresponding service's control rules.
		path := ""
		if u != nil {
			path = u.Path
		}
		if service := compatibleService(host, path, t.ProviderMode); service != "" {
			return service
		}
	}
	switch t.Platform {
	case "openai":
		if t.ProviderType == "oauth" || t.ProviderType == "setup-token" || host == "" || host == "api.openai.com" || host == "chatgpt.com" {
			return "openai"
		}
	case "anthropic":
		switch t.ProviderType {
		case "bedrock":
			return "amazon-bedrock"
		case "service_account":
			return "google-vertex-anthropic"
		}
		if t.ProviderType == "oauth" || t.ProviderType == "setup-token" || host == "" || host == "api.anthropic.com" {
			return "anthropic"
		}
	case "gemini":
		if t.ProviderType == "service_account" {
			return "google-vertex"
		}
		if t.ProviderType == "oauth" || host == "" || host == "generativelanguage.googleapis.com" {
			return "google"
		}
	case "grok":
		if t.ProviderType == "oauth" || host == "" || host == "api.x.ai" || strings.HasSuffix(host, ".api.x.ai") {
			return "xai"
		}
	case "kimi":
		if t.ProviderMode == "coding" {
			if host == "api.kimi.com" || host == "" && t.Site != "global" {
				return "kimi-code-plan-cn"
			}
			if host == "api.kimi.ai" || host == "" && t.Site == "global" {
				return "kimi-code-plan-global"
			}
		} else if host == "" || host == "api.moonshot.cn" {
			return "moonshotai-cn"
		} else if host == "api.moonshot.ai" {
			return "moonshotai"
		}
	case "zhipu":
		service := ""
		switch host {
		case "", "open.bigmodel.cn":
			service = "zhipuai"
		case "api.z.ai":
			service = "zai"
		}
		if service != "" && t.ProviderMode == "coding" {
			service += "-coding-plan"
		}
		return service
	case "deepseek":
		if host == "" || host == "api.deepseek.com" {
			return "deepseek"
		}
	case "jev":
		if host == "" || host == "api.typesafe.ai" {
			return "typesafe"
		}
	}
	return ""
}

// WithServiceIdentity fills the detected catalog service and normalizes its
// mode and site for route rules. Compact protocol selections set Endpoint to
// compact. Providers supply their regional site before resolving attributes.
func (t Target) WithServiceIdentity() Target {
	t.CatalogProvider = t.ServiceID()
	// Compact protocol identities select the compact endpoint variant for
	// applyPlatform's effort rules while the route retains both resolved
	// protocol identifiers.
	if t.ClientProtocol == "openai_responses_compact" || t.UpstreamProtocol == "openai_responses_compact" {
		t.Endpoint = "compact"
	}
	switch t.CatalogProvider {
	case "kimi-code-plan-cn", "kimi-code-plan-global", "zhipuai-coding-plan", "zai-coding-plan":
		t.ProviderMode = "coding"
	case "moonshotai", "moonshotai-cn", "zhipuai", "zai":
		t.ProviderMode = "payg"
	}
	switch t.CatalogProvider {
	case "moonshotai-cn", "kimi-code-plan-cn", "zhipuai", "zhipuai-coding-plan":
		t.Site = "cn"
	case "moonshotai", "kimi-code-plan-global", "zai", "zai-coding-plan":
		t.Site = "global"
	}
	return t
}

// publicLab maps regional, coding, and hosted services to their public author
// identity for metadata lookup. Services without a registered relationship
// retain their supplied identity so qualified lookup remains predictable.
func publicLab(service string) string {
	switch service {
	case "moonshotai-cn", "kimi-code-plan-cn", "kimi-code-plan-global":
		return "moonshotai"
	case "zai", "zai-coding-plan", "zhipuai-coding-plan":
		return "zhipuai"
	case "google-vertex":
		return "google"
	case "google-vertex-anthropic":
		return "anthropic"
	}
	return service
}

// compatibleService identifies recognized official services behind OpenAI
// API-compatible endpoints. Coding mode or a coding path selects the matching
// Zhipu service variant; unrecognized hosts return an empty identity.
func compatibleService(host, path, mode string) string {
	switch host {
	case "api.anthropic.com":
		return "anthropic"
	case "generativelanguage.googleapis.com":
		return "google"
	case "api.x.ai":
		return "xai"
	case "api.deepseek.com":
		return "deepseek"
	case "api.moonshot.cn":
		return "moonshotai-cn"
	case "api.moonshot.ai":
		return "moonshotai"
	case "api.kimi.com":
		return "kimi-code-plan-cn"
	case "api.kimi.ai":
		return "kimi-code-plan-global"
	case "open.bigmodel.cn", "api.z.ai":
		service := "zhipuai"
		if host == "api.z.ai" {
			service = "zai"
		}
		if mode == "coding" || strings.Contains(path, "/coding/") {
			service += "-coding-plan"
		}
		return service
	case "ollama.com", "api.ollama.com":
		return "ollama-cloud"
	case "api.typesafe.ai":
		return "typesafe"
	default:
		return ""
	}
}

// usesCanonicalIdentity reports whether the target's model rules come only from
// a canonical author reference. OpenCode offerings rename models from many
// authors, so an unknown reference leaves their author-specific rules unapplied
// instead of guessing an author from the outbound name.
func (t Target) usesCanonicalIdentity() bool {
	return t.Platform == "opencode"
}

// servicePlatform selects the encoding platform for recognized compatible
// services. OpenAI OAuth and Setup Token routes retain OpenAI, while unknown
// API-compatible services retain the target's configured platform.
func (t Target) servicePlatform() string {
	if t.Platform != "openai" || t.ProviderType == "oauth" || t.ProviderType == "setup-token" {
		return t.Platform
	}
	if family := serviceRuleFamily(t.ServiceID()); family != "" {
		return family
	}
	return t.Platform
}

// serviceRuleFamily maps a catalog service or author lab to the platform name
// whose model rules apply to it. Unregistered identities return empty, so
// callers keep their own platform or leave author rules unapplied.
func serviceRuleFamily(service string) string {
	switch service {
	case "openai", "anthropic", "deepseek", "minimax", "alibaba":
		return service
	case "xai":
		return "grok"
	case "google":
		return "gemini"
	case "moonshotai", "moonshotai-cn", "kimi-code-plan-cn", "kimi-code-plan-global":
		return "kimi"
	case "zhipuai", "zhipuai-coding-plan", "zai", "zai-coding-plan":
		return "zhipu"
	case "typesafe":
		return "jev"
	default:
		return ""
	}
}

// Key returns comparable route values after resolving service identity and
// site defaults. The raw address remains in the full key so caches returning
// a complete route keep distinct caller addresses.
//
// An output cap is represented by its integer value and a presence flag,
// then its pointer is removed from the retained Target. This distinguishes
// nil from zero and prevents pointer addresses or later pointed-value changes
// from altering comparison of a previously constructed key. Attribute caches
// can derive a key that omits the resolved route's raw address.
func (t Target) Key() TargetKey {
	t = t.WithServiceIdentity()
	key := TargetKey{target: t}
	if t.ProviderModels != nil {
		// The publication identity survives independent decodes and record copies.
		// Clearing its interface keeps native maps and pointer identity out of cache keys.
		key.providerVersion = t.ProviderModels.Version()
		key.target.ProviderModels = nil
	}
	if t.OllamaOutputCap != nil {
		// Clear the pointer after capturing its contents so two allocations with
		// the same cap compare equally, while a missing cap remains
		// distinguishable from zero.
		key.outputCap, key.hasOutputCap = *t.OllamaOutputCap, true
		key.target.OllamaOutputCap = nil
	}
	return key
}

// ForAttributes omits the raw address after Key has captured service identity.
// Attribute caches can share results across equivalent service routes; caches
// returning the route itself use the full key to retain its address.
func (key TargetKey) ForAttributes() TargetKey {
	key.target.BaseURL = ""
	return key
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
