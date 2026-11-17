package modelrules

import (
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// target.go identifies the final provider route for attribute resolution and
// cache lookup. The client protocol, service endpoint, authentication method
// and resolved model all contribute to a route. Canonical model references
// identify metadata without replacing the name used for dispatch.
//
// Full route keys retain the configured base address. Attribute-only keys use
// the detected service identity, allowing equivalent relay addresses to share
// model data. Optional output limits and provider publications are keyed by
// value or immutable version so separate records for the same account can
// reuse a resolution without borrowing mutable pointers.

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
	LookupAttributes(model string) (string, modelcatalog.Attributes, bool)
	// AdapterRestrictions returns the capability patch imposed by the selected wire
	// format after profile resolution. Returned fields must describe representation
	// limits of the converter, since they affect both display and execution.
	AdapterRestrictions(model string, attrs modelcatalog.Attributes) modelcatalog.Attributes
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

// normalize trims and folds a model identity for rule lookup only.
func normalize(id string) string { return strings.ToLower(strings.TrimSpace(id)) }
