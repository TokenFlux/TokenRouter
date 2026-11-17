package modelrules

import (
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// gemini.go defines Gemini image model limits shared by direct and Antigravity
// routes. Each registered model provides supported image sizes and a maximum
// reference count to both creative requests and attribute previews.

// geminiMedia returns supported media parameters for the exact provider model.
func geminiMedia(model string) *modelcatalog.MediaCapabilities {
	result := &modelcatalog.MediaCapabilities{}
	if !slices.Contains([]string{"gemini-2.5-flash-image", "gemini-3.1-flash-lite-image", "gemini-3.1-flash-image", "gemini-3.1-flash-image-preview", "gemini-3-pro-image", "gemini-3-pro-image-preview", "gemini-2.0-flash-exp-image-generation"}, model) {
		return nil
	}
	result.ImageSizes = []string{"1K", "2K", "4K"}
	result.AspectRatios = []string{"1:1", "1:4", "4:1", "1:8", "8:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"}
	// The experimental Gemini 2.0 registration uses this initial reference
	// count. Other registered variants replace it before creative validation
	// limits supplied source images.
	result.MaxReferenceImages = new(1)
	switch {
	case strings.HasPrefix(model, "gemini-3.1-flash-image"):
		result.ImageSizes = append([]string{"512"}, result.ImageSizes...)
		result.MaxReferenceImages = new(14)
	case strings.HasPrefix(model, "gemini-3.1-flash-lite-image"):
		result.ImageSizes = []string{"1K"}
		result.MaxReferenceImages = new(14)
	case strings.Contains(model, "pro-image"):
		result.MaxReferenceImages = new(14)
	case strings.Contains(model, "flash-image"):
		result.MaxReferenceImages = new(3)
	}
	if strings.HasPrefix(model, "gemini-2.5-flash-image") || model == "gemini-2.0-flash-exp-image-generation" {
		result.ImageSizes = []string{"1K"}
	}
	return result
}
