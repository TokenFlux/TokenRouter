package modelcatalog

import (
	"slices"
	"strings"
)

// media.go selects registered image and video parameters for each platform and
// model.
// Media parameter support varies by platform and full model identity.
// Registered choices supply image sizes, reference limits, and video operations
// to creative requests, while input and output modalities are resolved from
// catalog attributes separately.

// MediaForModel returns registered parameters implemented by the media
// adapters for a normalized model identity. Unknown platform or model choices
// return nil, allowing callers to distinguish missing registration from empty
// capabilities.
func MediaForModel(platform, model string) *MediaCapabilities {
	model = strings.TrimPrefix(normalize(model), "models/")
	result := &MediaCapabilities{}
	switch platform {
	case "openai":
		if !slices.Contains([]string{"gpt-image-1", "gpt-image-1-mini", "gpt-image-1.5"}, model) && !strings.HasPrefix(model, "gpt-image-2") {
			return nil
		}
		result.ImageSizes = []string{"1K", "2K"}
		result.AspectRatios = []string{"1:1", "4:3", "3:4", "16:9", "9:16"}
		result.Qualities = []string{"low", "medium", "high", "auto"}
		result.BackgroundOptions = []string{"auto", "opaque"}
		result.MaxReferenceImages = valuePointer(16)
		result.InputFidelity = valuePointer(true)
		// GPT Image 2 registration changes supported sizes and background options.
		// Its false input fidelity flag makes PrepareModelAttributeParameters
		// remove that tool field.
		if strings.HasPrefix(model, "gpt-image-2") {
			result.ImageSizes = append(result.ImageSizes, "4K")
			result.BackgroundOptions = append(result.BackgroundOptions, "transparent")
			result.InputFidelity = valuePointer(false)
		}
		if model == "gpt-image-2.6-sunburst" || model == "gpt-image-2.6-flare" {
			result.Qualities = []string{"low", "medium", "high", "xhigh", "max", "auto"}
			result.OutputFormats = []string{"png", "jpeg", "webp", "gif"}
		}
	case "grok":
		if slices.Contains([]string{"grok-imagine-video", "grok-imagine-video-1.5", "grok-imagine-video-1.5-preview", "grok-imagine-video-2"}, model) {
			result.VideoOperations = []string{"generate", "edit", "extend"}
			result.VideoParameters = []string{"resolution", "duration", "aspect_ratio"}
			return result
		}
		if !slices.Contains([]string{"grok-imagine", "grok-imagine-edit", "grok-imagine-image", "grok-imagine-image-quality", "grok-imagine-image-2.0"}, model) {
			return nil
		}
		result.ImageSizes = []string{"1K", "2K"}
		result.AspectRatios = []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "2:1", "1:2", "19.5:9", "9:19.5", "20:9", "9:20", "21:9", "5:2", "auto"}
		result.MaxReferenceImages = valuePointer(3)
		if model == "grok-imagine-image-2.0" {
			result.Qualities = []string{"low", "medium"}
		}
	case "gemini", "antigravity":
		if !slices.Contains([]string{"gemini-2.5-flash-image", "gemini-3.1-flash-lite-image", "gemini-3.1-flash-image", "gemini-3.1-flash-image-preview", "gemini-3-pro-image", "gemini-3-pro-image-preview", "gemini-2.0-flash-exp-image-generation"}, model) {
			return nil
		}
		result.ImageSizes = []string{"1K", "2K", "4K"}
		result.AspectRatios = []string{"1:1", "1:4", "4:1", "1:8", "8:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"}
		// The experimental Gemini 2.0 registration uses this initial reference
		// count. Other registered variants replace it before creative validation
		// limits supplied source images.
		result.MaxReferenceImages = valuePointer(1)
		switch {
		case strings.HasPrefix(model, "gemini-3.1-flash-image"):
			result.ImageSizes = append([]string{"512"}, result.ImageSizes...)
			result.MaxReferenceImages = valuePointer(14)
		case strings.HasPrefix(model, "gemini-3.1-flash-lite-image"):
			result.ImageSizes = []string{"1K"}
			result.MaxReferenceImages = valuePointer(14)
		case strings.Contains(model, "pro-image"):
			result.MaxReferenceImages = valuePointer(14)
		case strings.Contains(model, "flash-image"):
			result.MaxReferenceImages = valuePointer(3)
		}
		if strings.HasPrefix(model, "gemini-2.5-flash-image") || model == "gemini-2.0-flash-exp-image-generation" {
			result.ImageSizes = []string{"1K"}
		}
	default:
		return nil
	}
	return result
}
