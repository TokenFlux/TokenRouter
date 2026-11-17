package modelcatalog

import (
	"maps"
	"slices"
)

// attributes_clone.go copies nested model attributes and applies populated
// media patch fields.
// Request attributes contain nested slices and pointers that consumers may
// change after resolving a route. Copying each populated value keeps those
// changes separate from the catalog and from other requests, including empty
// lists that record an administrator's cleared capability.
//
// Media patches distinguish leaving a field unset from replacing it with an
// empty list. Their intermediate result can borrow values from either input,
// and Merge detaches those values before exposing its result. Source tracking
// uses populated field names so an explicit clear receives the patch's source
// label just as a populated list does.

// Clone returns independently mutable attributes, including nested conditions,
// media options, reasoning choices, and string translations. Nil values and
// populated empty slices retain their distinct meanings for inheritance and
// explicit administrator overrides.
func (a Attributes) Clone() Attributes {
	a.DisplayName = clonePointer(a.DisplayName)
	a.Context = clonePointer(a.Context)
	a.InputLimit = clonePointer(a.InputLimit)
	a.OutputLimit = clonePointer(a.OutputLimit)
	a.Reasoning = clonePointer(a.Reasoning)
	a.ToolCall = clonePointer(a.ToolCall)
	a.StructuredOutput = clonePointer(a.StructuredOutput)
	a.Temperature = clonePointer(a.Temperature)
	a.Attachment = clonePointer(a.Attachment)
	a.TopP = clonePointer(a.TopP)
	a.Interleaved = clonePointer(a.Interleaved)
	if a.InputModalities != nil {
		a.InputModalities = valuePointer(slices.Clone(*a.InputModalities))
	}
	if a.OutputModalities != nil {
		a.OutputModalities = valuePointer(slices.Clone(*a.OutputModalities))
	}
	if a.DisplayNameLocalization != nil {
		copy := *a.DisplayNameLocalization
		copy.SourceLocale = clonePointer(copy.SourceLocale)
		copy.Translations = maps.Clone(copy.Translations)
		copy.ReviewedLocales = slices.Clone(copy.ReviewedLocales)
		copy.DeletedLocales = slices.Clone(copy.DeletedLocales)
		a.DisplayNameLocalization = &copy
	}
	if a.ReasoningOptions != nil {
		options := slices.Clone(*a.ReasoningOptions)
		for i := range options {
			options[i].Values = slices.Clone(options[i].Values)
			for j, value := range options[i].Values {
				options[i].Values[j] = clonePointer(value)
			}
			options[i].Min = clonePointer(options[i].Min)
			options[i].Max = clonePointer(options[i].Max)
		}
		a.ReasoningOptions = &options
	}
	if a.Conditions != nil {
		conditions := maps.Clone(*a.Conditions)
		for key, condition := range conditions {
			condition.Efforts = slices.Clone(condition.Efforts)
			condition.Thinking = clonePointer(condition.Thinking)
			conditions[key] = condition
		}
		a.Conditions = &conditions
	}
	if a.Media != nil {
		media := *a.Media
		media.VideoOperations = slices.Clone(media.VideoOperations)
		media.VideoParameters = slices.Clone(media.VideoParameters)
		media.ImageSizes = slices.Clone(media.ImageSizes)
		media.AspectRatios = slices.Clone(media.AspectRatios)
		media.Qualities = slices.Clone(media.Qualities)
		media.BackgroundOptions = slices.Clone(media.BackgroundOptions)
		media.OutputFormats = slices.Clone(media.OutputFormats)
		media.MaxReferenceImages = clonePointer(media.MaxReferenceImages)
		media.MaxOutputCount = clonePointer(media.MaxOutputCount)
		media.InputFidelity = clonePointer(media.InputFidelity)
		a.Media = &media
	}
	return a
}

// clonePointer copies a populated pointed value and preserves nil inputs.
// The copy is shallow for T values containing references; callers copy their
// nested collections separately when independent mutation is needed.
func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	return valuePointer(*value)
}

// mergeMedia applies populated media fields, keeping inherited fields from
// base when the patch omits them. A supplied empty slice replaces the prior
// choices, so clearing supported sizes remains distinct from leaving sizes
// unchanged.
//
// A nil input can return the other input directly, and populated inputs
// contribute their slice and pointer values to a shallow result. Merge calls
// Clone afterward to detach those references before returning attributes to
// consumers that modify their request or display data.
func mergeMedia(base, patch *MediaCapabilities) *MediaCapabilities {
	if patch == nil {
		return base
	}
	if base == nil {
		return patch
	}
	result := *base
	if patch.VideoOperations != nil {
		result.VideoOperations = patch.VideoOperations
	}
	if patch.VideoParameters != nil {
		result.VideoParameters = patch.VideoParameters
	}
	if patch.ImageSizes != nil {
		result.ImageSizes = patch.ImageSizes
	}
	if patch.AspectRatios != nil {
		result.AspectRatios = patch.AspectRatios
	}
	if patch.Qualities != nil {
		result.Qualities = patch.Qualities
	}
	if patch.BackgroundOptions != nil {
		result.BackgroundOptions = patch.BackgroundOptions
	}
	if patch.OutputFormats != nil {
		result.OutputFormats = patch.OutputFormats
	}
	if patch.MaxReferenceImages != nil {
		result.MaxReferenceImages = patch.MaxReferenceImages
	}
	if patch.MaxOutputCount != nil {
		result.MaxOutputCount = patch.MaxOutputCount
	}
	if patch.InputFidelity != nil {
		result.InputFidelity = patch.InputFidelity
	}
	return &result
}

// FieldNames returns populated attribute JSON names for supplement precedence
// and resolution source tracking. Every non-nil field pointer contributes a
// name, including pointers to nil collections, while nil fields inherit their
// existing values and sources.
func (a Attributes) FieldNames() []string {
	var names []string
	if a.DisplayNameLocalization != nil {
		names = append(names, "display_name_localization")
	}
	if a.DisplayName != nil {
		names = append(names, "display_name")
	}
	if a.Context != nil {
		names = append(names, "context")
	}
	if a.InputLimit != nil {
		names = append(names, "input_limit")
	}
	if a.OutputLimit != nil {
		names = append(names, "output_limit")
	}
	if a.InputModalities != nil {
		names = append(names, "input_modalities")
	}
	if a.OutputModalities != nil {
		names = append(names, "output_modalities")
	}
	if a.Reasoning != nil {
		names = append(names, "reasoning")
	}
	if a.ToolCall != nil {
		names = append(names, "tool_call")
	}
	if a.StructuredOutput != nil {
		names = append(names, "structured_output")
	}
	if a.Temperature != nil {
		names = append(names, "temperature")
	}
	if a.Attachment != nil {
		names = append(names, "attachment")
	}
	if a.TopP != nil {
		names = append(names, "top_p")
	}
	if a.ReasoningOptions != nil {
		names = append(names, "reasoning_options")
	}
	if a.Interleaved != nil {
		names = append(names, "interleaved")
	}
	if a.Conditions != nil {
		names = append(names, "conditions")
	}
	if a.Media != nil {
		names = append(names, "media")
	}
	return names
}

// FieldNames returns populated media JSON names for per-field source tracking.
// Empty slices contribute their names because they clear capabilities, while
// nil slices and pointers leave the inherited media fields unchanged.
func (m MediaCapabilities) FieldNames() []string {
	var names []string
	if m.VideoOperations != nil {
		names = append(names, "video_operations")
	}
	if m.VideoParameters != nil {
		names = append(names, "video_parameters")
	}
	if m.ImageSizes != nil {
		names = append(names, "image_sizes")
	}
	if m.AspectRatios != nil {
		names = append(names, "aspect_ratios")
	}
	if m.Qualities != nil {
		names = append(names, "qualities")
	}
	if m.BackgroundOptions != nil {
		names = append(names, "background_options")
	}
	if m.OutputFormats != nil {
		names = append(names, "output_formats")
	}
	if m.MaxReferenceImages != nil {
		names = append(names, "max_reference_images")
	}
	if m.MaxOutputCount != nil {
		names = append(names, "max_output_count")
	}
	if m.InputFidelity != nil {
		names = append(names, "input_fidelity")
	}
	return names
}
