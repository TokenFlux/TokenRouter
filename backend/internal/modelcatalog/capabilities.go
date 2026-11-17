package modelcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
)

// capabilities.go encodes optional capabilities and intersects reasoning
// controls and request conditions.
// Capability lists can be unknown or explicitly empty, and JSON must retain
// that difference when administrator rules are stored and returned. Media list
// encoding restores empty arrays omitted by ordinary omitempty serialization;
// interleaved reasoning accepts either a boolean declaration or a named field
// representation used by adapters.
//
// Shared route capabilities depend on both control choices and their request
// conditions. Effort choices are intersected, budget ranges use the largest
// minimum and smallest maximum, and incompatible ranges disappear. Conditions
// combine thinking state and effort restrictions; contradictory states mark
// the affected parameter unavailable so Common can disable its capability.

// ReasoningOption describes a configurable toggle, effort choice, or token budget.
type ReasoningOption struct {
	Type string `json:"type"` // toggle, effort, or budget_tokens
	// Values enumerates effort levels; a nil element represents the provider's
	// default choice. Validate rejects Values for toggles and token budgets.
	Values []*string `json:"values,omitempty"`
	// Min and Max bound budget_tokens options and are absent for other control
	// types. Validate accepts Min down to -1, rejects negative Max, and checks
	// the ordering when both endpoints are supplied.
	Min *int `json:"min,omitempty"` // tokens
	Max *int `json:"max,omitempty"` // tokens
}

// Interleaved represents a boolean declaration or named reasoning output field.
type Interleaved struct {
	// Field names reasoning_content or reasoning_details in object encodings.
	// An empty field marshals as true to retain the catalog's boolean representation.
	Field string `json:"field,omitempty"`
}

// ParameterCondition limits a parameter to request states satisfying all
// supplied conditions.
type ParameterCondition struct {
	// Efforts and Thinking restrict the same request state and are combined
	// conjunctively by ForRequest. An empty Efforts list leaves effort
	// unrestricted; a nil Thinking value leaves the thinking state unrestricted.
	Efforts  []string `json:"efforts,omitempty"`  // allowed request effort values
	Thinking *bool    `json:"thinking,omitempty"` // required request thinking state
}

// ParameterConditions maps configurable sampling and tool parameters to
// request restrictions.
type ParameterConditions map[string]ParameterCondition

// MediaCapabilities contains media operations and parameters implemented by
// the adapters.
type MediaCapabilities struct {
	// VideoOperations, VideoParameters, ImageSizes, AspectRatios, Qualities,
	// BackgroundOptions, and OutputFormats inherit when nil. Populated empty lists
	// clear the corresponding choices during Merge, and MarshalJSON retains those
	// clears as empty arrays for administrator rule round trips.
	VideoOperations    []string `json:"video_operations,omitempty"`     // supported video operation names
	VideoParameters    []string `json:"video_parameters,omitempty"`     // supported video parameter names
	ImageSizes         []string `json:"image_sizes,omitempty"`          // supported image size labels
	AspectRatios       []string `json:"aspect_ratios,omitempty"`        // supported image aspect ratios
	Qualities          []string `json:"qualities,omitempty"`            // supported image quality names
	BackgroundOptions  []string `json:"background_options,omitempty"`   // supported image background choices
	OutputFormats      []string `json:"output_formats,omitempty"`       // supported media output formats
	MaxReferenceImages *int     `json:"max_reference_images,omitempty"` // maximum input image count
	MaxOutputCount     *int     `json:"max_output_count,omitempty"`     // maximum output item count
	// InputFidelity declares whether the image parameter is supported. A false
	// value makes PrepareModelAttributeParameters remove input_fidelity from
	// OpenAI image tools.
	InputFidelity *bool `json:"input_fidelity,omitempty"`
}

// MarshalJSON encodes media capabilities while retaining populated empty
// lists as JSON arrays. Nil lists remain absent so persisted rules can inherit
// from their catalog or platform source on their next merge.
//
// Ordinary omitempty encoding removes both kinds of empty list. The encoder
// first produces the scalar representation, then restores arrays for each
// supplied empty list, including video controls. This lets administrator clears
// survive serialization and subsequent attribute resolution.
func (m MediaCapabilities) MarshalJSON() ([]byte, error) {
	// plain is the media representation used before restoring explicit empty lists.
	// Its distinct type prevents recursive marshaling while retaining the same
	// fields for the initial omitempty encoding.
	type plain MediaCapabilities
	body, err := json.Marshal(plain(m))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	// Restore lists deliberately cleared by the administrator after omitempty
	// encoding, so their next merge replaces inherited choices with an empty set.
	for name, values := range map[string][]string{
		"image_sizes": m.ImageSizes, "aspect_ratios": m.AspectRatios,
		"qualities": m.Qualities, "background_options": m.BackgroundOptions, "output_formats": m.OutputFormats, "video_operations": m.VideoOperations, "video_parameters": m.VideoParameters,
	} {
		if values != nil && len(values) == 0 {
			fields[name] = json.RawMessage(`[]`)
		}
	}
	return json.Marshal(fields)
}

// MarshalJSON encodes interleaving as true when no output field is named.
// A named field produces an object representation so adapters can select the
// corresponding reasoning output format after decoding.
func (v Interleaved) MarshalJSON() ([]byte, error) {
	if v.Field == "" {
		return []byte("true"), nil
	}
	return json.Marshal(struct {
		Field string `json:"field"` // named reasoning output field
	}{v.Field})
}

// UnmarshalJSON reads the catalog's boolean true or a supported reasoning
// field object. False, null, and unknown field names are rejected; the receiver
// is updated after a recognized representation is decoded.
func (v *Interleaved) UnmarshalJSON(body []byte) error {
	if bytes.Equal(bytes.TrimSpace(body), []byte("true")) {
		v.Field = ""
		return nil
	}
	var value struct {
		Field string `json:"field"` // decoded reasoning output field
	}
	if err := json.Unmarshal(body, &value); err != nil {
		return err
	}
	if value.Field != "reasoning_content" && value.Field != "reasoning_details" {
		return fmt.Errorf("invalid interleaved reasoning field")
	}
	v.Field = value.Field
	return nil
}

// OutputSupport reports declared support for one output modality. Nil means
// the catalog and overrides supplied no output declaration; an explicit empty
// list rejects every modality and remains distinct from missing information.
func (a Attributes) OutputSupport(modality string) *bool {
	if a.OutputModalities == nil {
		return nil
	}
	return valuePointer(slices.Contains(*a.OutputModalities, modality))
}

// OutputOnly reports whether the complete declared output set contains only
// the supplied modality. Unknown and empty declarations return false, keeping
// mixed text and image models eligible for ordinary text protocol handling.
func (a Attributes) OutputOnly(modality string) bool {
	return a.OutputModalities != nil && len(*a.OutputModalities) == 1 && (*a.OutputModalities)[0] == modality
}

// EffortSupport reports declared support for an effort level using the
// reasoning flag and available control options together. An explicit false
// reasoning flag rejects every effort, including choices still present in the
// option list.
//
// Nil ReasoningOptions returns unknown support, while an explicit empty list
// rejects the effort. A populated list accepts an exact nonnil effort value
// from an effort option; nil values denote provider defaults and therefore do
// not match an effort string.
func (a Attributes) EffortSupport(effort string) *bool {
	if a.Reasoning != nil && !*a.Reasoning {
		return valuePointer(false)
	}
	if a.ReasoningOptions == nil {
		return nil
	}
	for _, option := range *a.ReasoningOptions {
		if option.Type != "effort" {
			continue
		}
		for _, value := range option.Values {
			if value != nil && *value == effort {
				return valuePointer(true)
			}
		}
	}
	return valuePointer(false)
}

// Efforts returns configured nonnil effort strings in their declared order.
// Explicitly disabled reasoning returns nil, and provider default choices
// represented by nil pointers are omitted from the returned list.
func (a Attributes) Efforts() []string {
	if a.Reasoning != nil && !*a.Reasoning {
		return nil
	}
	var result []string
	if a.ReasoningOptions != nil {
		for _, option := range *a.ReasoningOptions {
			if option.Type == "effort" {
				for _, value := range option.Values {
					if value != nil {
						result = append(result, *value)
					}
				}
			}
		}
	}
	return result
}

// ReasoningBudget returns the first budget_tokens option's endpoints, or nil
// endpoints when no such option exists. The pointers belong to the attributes;
// callers treat them as read-only or clone before mutation.
func (a Attributes) ReasoningBudget() (minimum, maximum *int) {
	if a.ReasoningOptions != nil {
		for _, option := range *a.ReasoningOptions {
			if option.Type == "budget_tokens" {
				return option.Min, option.Max
			}
		}
	}
	return nil, nil
}

// HasReasoningOption reports whether the configured controls include the
// requested kind.
func (a Attributes) HasReasoningOption(kind string) bool {
	return a.ReasoningOptions != nil && slices.ContainsFunc(*a.ReasoningOptions, func(option ReasoningOption) bool { return option.Type == kind })
}

// validateCapabilities rejects duplicate controls, unsupported effort names,
// inconsistent budgets, unknown conditional parameters, and negative media
// counts. Optional fields can remain unset, while explicit empty control lists
// and zero media counts are valid.
func validateCapabilities(a Attributes) error {
	if a.ReasoningOptions != nil {
		seen := map[string]bool{}
		for _, option := range *a.ReasoningOptions {
			if seen[option.Type] {
				return fmt.Errorf("duplicate reasoning option %q", option.Type)
			}
			seen[option.Type] = true
			switch option.Type {
			case "toggle":
				if option.Values != nil || option.Min != nil || option.Max != nil {
					return fmt.Errorf("toggle cannot declare effort values or budgets")
				}
			case "effort":
				if option.Min != nil || option.Max != nil {
					return fmt.Errorf("effort cannot declare a token budget")
				}
				values := map[string]bool{}
				for _, value := range option.Values {
					key := "null"
					if value != nil {
						key = *value
					}
					if !slices.Contains([]string{"null", "default", "none", "minimal", "low", "medium", "high", "xhigh", "max"}, key) || values[key] {
						return fmt.Errorf("invalid or duplicate reasoning effort %q", key)
					}
					values[key] = true
				}
			case "budget_tokens":
				if option.Values != nil || option.Min != nil && *option.Min < -1 || option.Max != nil && *option.Max < 0 || option.Min != nil && option.Max != nil && *option.Min > *option.Max {
					return fmt.Errorf("invalid reasoning token budget")
				}
			default:
				return fmt.Errorf("unknown reasoning option %q", option.Type)
			}
		}
	}
	if a.Conditions != nil {
		for key, condition := range *a.Conditions {
			if !slices.Contains([]string{"temperature", "top_p", "tool_call"}, key) {
				return fmt.Errorf("unsupported conditional capability %q", key)
			}
			for _, effort := range condition.Efforts {
				if !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "default"}, effort) {
					return fmt.Errorf("invalid conditional reasoning effort %q", effort)
				}
			}
		}
	}
	if a.Media != nil {
		for _, limit := range []*int{a.Media.MaxReferenceImages, a.Media.MaxOutputCount} {
			if limit != nil && *limit < 0 {
				return fmt.Errorf("media limits must be nonnegative")
			}
		}
	}
	return nil
}

// commonReasoningOptions intersects controls declared by both routes and
// returns nil when either control list is unknown. An explicit empty list
// remains known and produces an empty intersection.
//
// Effort values retain the left route's order, including shared nil defaults.
// Budget endpoints are narrowed to the largest known minimum and smallest
// known maximum, and an empty budget range removes that control. Returned
// endpoint pointers and effort values can borrow their inputs; Common clones
// the final attributes before exposing mutable aggregate data.
func commonReasoningOptions(a, b *[]ReasoningOption) *[]ReasoningOption {
	if a == nil || b == nil {
		return nil
	}
	result := []ReasoningOption{}
	for _, left := range *a {
		for _, right := range *b {
			if left.Type != right.Type {
				continue
			}
			option := ReasoningOption{Type: left.Type}
			switch left.Type {
			case "effort":
				option.Values = []*string{}
				for _, value := range left.Values {
					if slices.ContainsFunc(right.Values, func(candidate *string) bool { return reflect.DeepEqual(value, candidate) }) {
						option.Values = append(option.Values, value)
					}
				}
			case "budget_tokens":
				option.Min = maximum(left.Min, right.Min)
				option.Max = MinimumLimit(left.Max, right.Max)
				if option.Min != nil && option.Max != nil && *option.Min > *option.Max {
					// Disjoint budget ranges offer no value executable on both
					// routes. Omit this control so the common attributes cannot
					// advertise an impossible budget.
					continue
				}
			}
			result = append(result, option)
		}
	}
	return &result
}

// commonConditions combines parameter restrictions and returns the names
// whose restrictions cannot coexist. With two unknown inputs it returns nil
// conditions; a restriction supplied by either known route is retained.
//
// Shared thinking requirements must agree, and populated effort requirements
// use their intersection. An empty effort requirement is unrestricted, so it
// adopts the other route's restriction. Contradictions remove the parameter's
// condition and report its name separately, allowing Common to disable that
// capability rather than interpreting a missing condition as unrestricted.
func commonConditions(a, b *ParameterConditions) (*ParameterConditions, []string) {
	result := ParameterConditions{}
	if a != nil {
		maps.Copy(result, *a)
	}
	var unavailable []string
	if b != nil {
		for key, condition := range *b {
			prior, exists := result[key]
			if !exists {
				result[key] = condition
				continue
			}
			if prior.Thinking != nil && condition.Thinking != nil && *prior.Thinking != *condition.Thinking {
				// Opposite thinking requirements cannot be satisfied by one
				// request. Report the parameter as unavailable so Common also
				// disables its capability flag.
				unavailable = append(unavailable, key)
				delete(result, key)
				continue
			}
			if prior.Thinking != nil {
				condition.Thinking = prior.Thinking
			}
			if len(prior.Efforts) > 0 {
				if len(condition.Efforts) == 0 {
					condition.Efforts = slices.Clone(prior.Efforts)
				} else {
					condition.Efforts = commonStrings(prior.Efforts, condition.Efforts)
					if len(condition.Efforts) == 0 {
						// Both routes constrain effort but have no accepted value
						// in common. Report the parameter for disabling instead of
						// leaving an unrestricted empty condition.
						unavailable = append(unavailable, key)
						delete(result, key)
						continue
					}
				}
			}
			result[key] = condition
		}
	}
	if a == nil && b == nil {
		return nil, nil
	}
	return &result, unavailable
}

// commonMedia combines known media choices and limits through the same
// helpers used by Common. Either unknown media input returns nil; the result
// can borrow count pointers until the final attribute clone.
func commonMedia(a, b *MediaCapabilities) *MediaCapabilities {
	if a == nil || b == nil {
		return nil
	}
	return &MediaCapabilities{
		VideoOperations: commonStrings(a.VideoOperations, b.VideoOperations), VideoParameters: commonStrings(a.VideoParameters, b.VideoParameters),
		ImageSizes: commonStrings(a.ImageSizes, b.ImageSizes), AspectRatios: commonStrings(a.AspectRatios, b.AspectRatios),
		Qualities: commonStrings(a.Qualities, b.Qualities), BackgroundOptions: commonStrings(a.BackgroundOptions, b.BackgroundOptions),
		OutputFormats: commonStrings(a.OutputFormats, b.OutputFormats), MaxReferenceImages: MinimumLimit(a.MaxReferenceImages, b.MaxReferenceImages),
		MaxOutputCount: MinimumLimit(a.MaxOutputCount, b.MaxOutputCount), InputFidelity: commonBool(a.InputFidelity, b.InputFidelity),
	}
}

// commonStrings intersects known lists in the left input's order using new
// slice storage. Either nil input makes the result unknown; populated lists
// with no common value retain an empty result.
func commonStrings(a, b []string) []string {
	if a == nil || b == nil {
		return nil
	}
	return *IntersectStrings(&a, &b)
}

// maximum selects the larger known endpoint, borrowing its input pointer.
// A nil endpoint is unknown, so the other endpoint wins; callers copy the
// selected pointer before exposing mutable attributes.
func maximum(a, b *int) *int {
	if a == nil || b != nil && *b > *a {
		return b
	}
	return a
}

// valuePointer returns a pointer to a copied value for optional attributes.
func valuePointer[T any](value T) *T { return &value }
