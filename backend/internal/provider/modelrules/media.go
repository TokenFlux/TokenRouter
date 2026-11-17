package modelrules

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
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
func MediaForModel(platform, model string) *modelcatalog.MediaCapabilities {
	model = strings.TrimPrefix(normalize(model), "models/")
	switch platform {
	case "openai":
		return openaiMedia(model)
	case "grok":
		return grokMedia(model)
	case "gemini", "antigravity":
		return geminiMedia(model)
	default:
		return nil
	}
}
