package modelcatalog

import (
	"slices"
	"strings"
)

// qoder_catalog.go describes model capabilities observed for one Qoder account.
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

// QoderFallbackInputLimit supplies the execution-only input cap for explicitly
// configured custom routes with no observed metadata. It does not advertise
// those routes in the account catalog.
const QoderFallbackInputLimit = 200000

// QoderModel records one account's service route and observed model controls.
type QoderModel struct {
	Key             string   `json:"key"`                       // upstream service route
	Name            string   `json:"name"`                      // upstream display name
	Alias           string   `json:"alias,omitempty"`           // current public request alias
	Identity        string   `json:"identity,omitempty"`        // explicit public model identity
	Enabled         bool     `json:"enabled"`                   // account route eligibility
	Default         bool     `json:"default"`                   // upstream default route
	Reasoning       bool     `json:"reasoning"`                 // observed reasoning support
	CanDisable      bool     `json:"can_disable"`               // explicit disable support
	DefaultThinking bool     `json:"default_thinking"`          // upstream default switch
	DefaultEffort   string   `json:"default_effort,omitempty"`  // upstream default effort
	Efforts         []string `json:"efforts"`                   // supported enabled efforts
	InputLimit      int      `json:"input_limit"`               // tokens
	OutputLimit     int      `json:"output_limit,omitempty"`    // tokens
	ContextWindows  []int    `json:"context_windows"`           // selectable input token windows
	DefaultContext  int      `json:"default_context,omitempty"` // tokens
	Vision          bool     `json:"vision"`                    // upstream visual input support
}

// Clone returns a model observation with independently owned capability slices.
// Scalar fields are copied by value. Callers can change returned efforts and
// context windows without modifying the original observation.
func (m QoderModel) Clone() QoderModel {
	m.Efforts = slices.Clone(m.Efforts)
	m.ContextWindows = slices.Clone(m.ContextWindows)
	return m
}

// Matches reports whether a request names this route or its current alias.
// Matching ignores surrounding whitespace and case; an empty alias never
// matches an empty request.
func (m QoderModel) Matches(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && (strings.EqualFold(m.Key, value) || m.Alias != "" && strings.EqualFold(m.Alias, value))
}

// Attributes returns observed Qoder metadata before administrator overrides.
// Nonpositive limits are omitted; adapter restrictions subsequently
// narrow advertised modalities to those the gateway can actually encode.
func (m QoderModel) Attributes() Attributes {
	options := []ReasoningOption{}
	if m.CanDisable {
		options = append(options, ReasoningOption{Type: "toggle"})
	}
	if len(m.Efforts) > 0 {
		option := ReasoningOption{Type: "effort"}
		for _, effort := range m.Efforts {
			option.Values = append(option.Values, valuePointer(effort))
		}
		options = append(options, option)
	}
	result := Attributes{Reasoning: valuePointer(m.Reasoning), ReasoningOptions: &options}
	if m.Name != "" {
		result.DisplayName = valuePointer(m.Name)
	}
	if m.InputLimit > 0 {
		result.InputLimit = valuePointer(m.InputLimit)
	}
	if m.OutputLimit > 0 {
		result.OutputLimit = valuePointer(m.OutputLimit)
	}
	return result
}
