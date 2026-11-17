package qodermodel

import (
	"regexp"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// catalog.go describes model capabilities observed for one Qoder account.
// Service keys can change their public model identity without changing their
// spelling, so each observation carries its own name, identity, and execution
// controls. Consumers must use the observation attached to the selected
// provider rather than infer a model from a historical service-key table.
//
// These values contain no credentials or upstream URLs. Provider snapshots own
// their freshness and publication, while this package translates a selected
// value into the existing attribute and encoding contracts. Clone separates
// mutable option slices before a request or administrator override changes
// them. Unknown catalog identities remain empty and cannot inherit a different
// model's price through a reused route key.

// displayIDSyntax restricts generated request aliases to ordinary portable model ID characters.
var displayIDSyntax = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Model records one account's service route and observed model controls.
type Model struct {
	Key      string `json:"key"`                // upstream service route
	Name     string `json:"name"`               // upstream display name
	Alias    string `json:"alias,omitempty"`    // current public request alias
	Identity string `json:"identity,omitempty"` // explicit public model identity
	Enabled  bool   `json:"enabled"`            // account route eligibility
	Default  bool   `json:"default"`            // upstream default route
	// Reasoning and CanDisable preserve omitted observations independently. A missing
	// capability inherits catalog data, while explicit false remains a provider value.
	Reasoning       *bool  `json:"reasoning,omitempty"`      // observed reasoning support
	CanDisable      *bool  `json:"can_disable,omitempty"`    // explicit disable support
	DefaultThinking bool   `json:"default_thinking"`         // upstream default switch
	DefaultEffort   string `json:"default_effort,omitempty"` // upstream default effort
	// Efforts is nil for unknown controls. A nonnil empty slice explicitly
	// clears inherited levels, including when the route accepts only a switch.
	Efforts        []string `json:"efforts"`                   // supported enabled efforts
	InputLimit     int      `json:"input_limit"`               // tokens
	OutputLimit    int      `json:"output_limit,omitempty"`    // tokens
	ContextWindows []int    `json:"context_windows"`           // selectable input token windows
	DefaultContext int      `json:"default_context,omitempty"` // tokens
	Vision         *bool    `json:"vision,omitempty"`          // upstream visual input support
}

// Catalog indexes one immutable account observation by service key and public alias.
// Its version includes the owning provider and observation identity. Consumers
// may share this index between preview, admission and execution after construction.
type Catalog struct {
	version string           // provider-scoped observation version
	models  map[string]Model // normalized keys and aliases
}

// DisplayAlias converts a usable upstream label into its public request spelling.
// Unknown model families can have a readable alias without receiving a public
// author identity or catalog prices. Empty or unsupported labels yield no alias.
func DisplayAlias(name string) string {
	id := strings.ToLower(strings.Join(strings.Fields(name), "-"))
	if !displayIDSyntax.MatchString(id) {
		return ""
	}
	return id
}

// NormalizeModels applies native defaults and upgrades aliases in stored observations.
// Raw route keys remain reserved. Two names that generate the same public ID
// keep their original routes so reordering an upstream list cannot redirect a
// request. Run this on exclusively owned models before publishing the index.
func NormalizeModels(models []Model) {
	keys := make(map[string]bool, len(models))
	counts := make(map[string]int, len(models))
	for i := range models {
		m := &models[i]
		// Older observations can mark reasoning mandatory while omitting its default
		// switch. Resolve the contradiction identically on sync and on database reload.
		if m.RequiresThinking() {
			m.DefaultThinking = true
		}
		keys[strings.ToLower(m.Key)] = true
		if m.Alias == "" {
			m.Alias = DisplayAlias(m.Name)
		}
		counts[strings.ToLower(m.Alias)]++
	}
	for i := range models {
		m := &models[i]
		id := strings.ToLower(m.Alias)
		if counts[id] > 1 || keys[id] && !strings.EqualFold(id, m.Key) {
			m.Alias = ""
		}
	}
}

// RequestID chooses the readable account alias when the snapshot supplies one.
// Routes without a unique display alias remain addressable through their key.
func (m Model) RequestID() string {
	if m.Alias != "" {
		return m.Alias
	}
	return m.Key
}

// Clone returns a model observation with independently owned capability slices.
// Callers may edit returned flags, effort options and context windows without
// changing the immutable source used by another request or preview.
func (m Model) Clone() Model {
	if m.Reasoning != nil {
		m.Reasoning = new(*m.Reasoning)
	}
	if m.Vision != nil {
		m.Vision = new(*m.Vision)
	}
	if m.CanDisable != nil {
		m.CanDisable = new(*m.CanDisable)
	}
	m.Efforts = slices.Clone(m.Efforts)
	m.ContextWindows = slices.Clone(m.ContextWindows)
	return m
}

// Attributes returns observed Qoder metadata before administrator overrides.
// Nonpositive limits are omitted; adapter restrictions subsequently
// narrow advertised modalities to those the gateway can actually encode.
func (m Model) Attributes() modelcatalog.Attributes {
	options := []modelcatalog.ReasoningOption{}
	if m.CanDisable != nil && *m.CanDisable {
		options = append(options, modelcatalog.ReasoningOption{Type: "toggle"})
	}
	if len(m.Efforts) > 0 {
		option := modelcatalog.ReasoningOption{Type: "effort"}
		for _, effort := range m.Efforts {
			option.Values = append(option.Values, new(effort))
		}
		options = append(options, option)
	}
	result := modelcatalog.Attributes{}
	if m.Reasoning != nil {
		result.Reasoning = new(*m.Reasoning)
	}
	if m.Efforts != nil || m.CanDisable != nil {
		result.ReasoningOptions = &options
	}
	if m.Name != "" {
		result.DisplayName = new(m.Name)
	}
	if m.InputLimit > 0 {
		result.InputLimit = new(m.InputLimit)
	}
	if m.OutputLimit > 0 {
		result.OutputLimit = new(m.OutputLimit)
	}
	return result
}

// NewCatalog captures detached models in a read-only lookup table. Version must
// change when either the account or its model data changes, since downstream
// request caches reuse resolutions for identical versions and route names.
func NewCatalog(version string, models []Model) *Catalog {
	c := &Catalog{version: version, models: make(map[string]Model, len(models))}
	for _, model := range models {
		copy := model.Clone()
		c.models[strings.ToLower(model.Key)] = copy
		if model.Alias != "" {
			c.models[strings.ToLower(model.Alias)] = copy
		}
	}
	return c
}

// Version identifies this immutable observation for route attribute cache keys.
func (c *Catalog) Version() string { return c.version }

// Lookup returns a detached observation for the supplied key or alias.
// Missing models have no account controls. Callers can edit the slices and
// optional reasoning flag without changing this catalog or another lookup.
func (c *Catalog) Lookup(name string) (Model, bool) {
	model, found := c.models[strings.ToLower(strings.TrimSpace(name))]
	return model.Clone(), found
}

// LookupAttributes supplies the confirmed catalog identity and populated account
// fields for one model. Opaque service tiers have no public identity. An
// absent model can still acquire attributes from an explicit profile reference.
func (c *Catalog) LookupAttributes(name string) (string, modelcatalog.Attributes, bool) {
	model, found := c.models[strings.ToLower(strings.TrimSpace(name))]
	return model.Identity, model.Attributes(), found
}

// AdapterRestrictions returns the patch imposed by Qoder's text converter.
// Explicitly cleared modality sets remain empty, while an unspecified modality
// set takes the converter's text representation. Unsupported sampling and
// attachment parameters remain unavailable when a profile declares them.
func (c *Catalog) AdapterRestrictions(name string, attrs modelcatalog.Attributes) modelcatalog.Attributes {
	patch := modelcatalog.Attributes{
		InputModalities: textModalities(attrs.InputModalities), OutputModalities: textModalities(attrs.OutputModalities),
		Attachment: new(false), Temperature: new(false), TopP: new(false), StructuredOutput: new(false),
	}
	model, found := c.models[strings.ToLower(name)]
	if !found {
		return patch
	}
	if model.RequiresThinking() && attrs.Reasoning != nil && !*attrs.Reasoning {
		patch.Reasoning = new(true)
	}
	if attrs.ReasoningOptions != nil && (model.CanDisable != nil || model.Efforts != nil) {
		// A profile may narrow the available settings. Hardware constraints apply to
		// the actual wire route even when its displayed attributes borrow another model.
		options := []modelcatalog.ReasoningOption{}
		changed := false
		for _, opt := range *attrs.ReasoningOptions {
			switch {
			case opt.Type == "toggle" && model.CanDisable != nil && !*model.CanDisable:
				changed = true
				continue
			case opt.Type == "effort" && model.Efforts != nil:
				values := make([]*string, 0, len(opt.Values))
				for _, value := range opt.Values {
					if value != nil && slices.Contains(model.Efforts, *value) {
						values = append(values, value)
					}
				}
				changed = changed || len(values) != len(opt.Values)
				if len(values) == 0 {
					changed = true
					continue
				}
				opt.Values = values
			}
			options = append(options, opt)
		}
		if changed {
			patch.ReasoningOptions = &options
		}
	}
	return patch
}

// textModalities intersects a configured modality set with the text wire format.
func textModalities(values *[]string) *[]string {
	if values == nil || slices.Contains(*values, "text") {
		return new([]string{"text"})
	}
	return new([]string{})
}

// RouteKey resolves a public alias before profile matching and outbound dispatch.
// Explicit custom routes retain their trimmed spelling, and a missing publication
// cannot assign a historical alias to a new account.
func (c *Catalog) RouteKey(name string) string {
	name = strings.TrimSpace(name)
	if c != nil {
		if model, ok := c.models[strings.ToLower(name)]; ok {
			return model.Key
		}
	}
	return name
}

// Enabled distinguishes an absent route from an observed disabled route.
func (c *Catalog) Enabled(name string) (bool, bool) {
	model, found := c.models[strings.ToLower(strings.TrimSpace(name))]
	return model.Enabled, found
}

// RequiresThinking reports mandatory reasoning only when both observations are explicit.
// Missing switch metadata leaves the decision to inherited controls and request
// validation. A profile cannot turn a known mandatory switch off on the wire.
func (m Model) RequiresThinking() bool {
	return m.Reasoning != nil && *m.Reasoning && m.CanDisable != nil && !*m.CanDisable
}
