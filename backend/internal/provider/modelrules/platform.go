package modelrules

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// platform.go combines service policies with model encoding rules and protocol
// adapter restrictions for resolved routes.
// Model metadata and request encoding rules have different sources. Catalog
// attributes describe the complete model. Model encoding rules select sampling
// conditions and payload restrictions, while service policies supply authentication
// restrictions, converted thinking limits, and registered media parameters.
// These patches run before administrator overrides so configured values can
// replace attribute fields.
//
// Adapter restrictions apply after those overrides because the chosen
// converter still determines which modalities and media parameters it can
// represent. Antigravity converted requests reserve output room for thinking,
// and Ollama cap settings select an output clamping rule. Execution rules
// remain separate from the attribute patches, allowing request encoders to
// interpret aliases, dropped fields, and signatures alongside the resolved
// capability values.

// Platform token defaults supply converted Antigravity thinking/output limits
// and the initial Ollama output parameter cap.
const (
	AntigravityAdaptiveBudget     = 24576 // tokens
	AntigravityDefaultOutputLimit = 64000 // tokens
	OllamaDefaultOutputLimit      = 65535 // tokens
)

// applyPlatform patches attributes and encoding rules for the detected
// service and route. It normalizes metadata lookup spellings locally, while
// retaining the target's outbound model and resolved protocol identifiers.
//
// Model encoding rules run before service authentication and endpoint variants
// select their own sampling, thinking, signature, and media behavior. Platform
// attribute sources are recorded when the patch is merged; later administrator
// overrides can replace them before adapter restrictions run. Nonpositive
// Ollama caps leave the output clamping rule disabled.
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_attributes
func (r *Resolution) applyPlatform() {
	t := r.Target
	_, model := t.modelEncodingIdentity()
	patch := modelcatalog.Attributes{}
	conditions := modelcatalog.ParameterConditions{}
	r.applyModelEncoding(&patch, conditions)
	switch t.servicePlatform() {
	case "openai":
		r.applyOpenAIAccess(model, &patch)
	case "anthropic":
		r.applyAnthropicAccess(model, conditions)
	case "antigravity":
		r.antigravityPatch(&patch)
	case "kimi":
		r.Rules.SignatureMode = "passback"
	case "zhipu":
		r.Rules.SignatureMode = "passback"
		if model == "glm-5.3" {
			r.Rules.ThinkingMode = "glm53"
		}
	case "deepseek":
		r.Rules.SignatureMode = "passback"
	}
	r.applyMessagesEncoding(conditions)
	if len(conditions) > 0 {
		patch.Conditions = &conditions
	}
	if t.Ollama {
		cap := OllamaDefaultOutputLimit
		if t.OllamaOutputCap != nil {
			cap = *t.OllamaOutputCap
		}
		if cap > 0 {
			// A positive provider cap narrows the catalog output limit used by
			// ApplyModelAttributeParameters for clamping. Administrator
			// attribute overrides can replace that limit afterward.
			r.Rules.ClampOutput = true
			patch.OutputLimit = modelcatalog.MinimumLimit(r.Attributes.OutputLimit, &cap)
		}
	}
	patch.Media = MediaForModel(t.servicePlatform(), model)
	r.patch(patch, "platform")
}

// applyAdapter restricts resolved attributes to the selected converter's
// representations after administrator overrides have been applied. Cross-text
// conversion intersects known modalities with text and image.
//
// Image and video entries impose their output modalities separately from
// ordinary text conversion. Batch images retain registered 1K sizes, and
// OpenAI OAuth image routes disable input fidelity even when configured true.
// Provider-specific restrictions run after these shared protocol constraints.
// Each constrained attribute receives the adapter source label.
func (r *Resolution) applyAdapter() {
	t := r.Target
	patch := modelcatalog.Attributes{}
	if t.ClientProtocol != "" && t.UpstreamProtocol != "" && t.ClientProtocol != t.UpstreamProtocol && !strings.Contains(t.ClientProtocol, "images_") && !strings.Contains(t.ClientProtocol, "videos_") && t.ClientProtocol != "image_batches" {
		// Text protocol conversion has a narrower representation than a catalog
		// modality declaration. Restrict known lists after configuration so
		// unsupported audio, video, and PDF choices disappear.
		allowed := []string{"text", "image"}
		if r.Attributes.InputModalities != nil {
			patch.InputModalities = modelcatalog.IntersectStrings(r.Attributes.InputModalities, &allowed)
		}
		if r.Attributes.OutputModalities != nil {
			patch.OutputModalities = modelcatalog.IntersectStrings(r.Attributes.OutputModalities, &allowed)
		}
	}
	if t.ClientProtocol == "image_batches" && r.Attributes.Media != nil && r.Attributes.Media.ImageSizes != nil {
		// Batch image conversion registers the 1K size. Intersect the resolved
		// sizes so catalog or administrator choices cannot advertise another
		// batch size through this adapter.
		patch.Media = &modelcatalog.MediaCapabilities{ImageSizes: *modelcatalog.IntersectStrings(&r.Attributes.Media.ImageSizes, new([]string{"1K"}))}
	}
	if strings.Contains(t.ClientProtocol, "images_") && t.Platform == "openai" && (t.ProviderType == "oauth" || t.ProviderType == "setup-token") {
		// The OAuth image path applies its own tool payload. Override
		// configured input fidelity support so PrepareModelAttributeParameters
		// removes the unsupported parameter for this route.
		patch.Media = &modelcatalog.MediaCapabilities{InputFidelity: new(false)}
	}
	// Media client entries need their own output representation after ordinary
	// text conversion handling. Restrict known image outputs to image and
	// known video outputs to video.
	if strings.Contains(t.ClientProtocol, "images_") || t.ClientProtocol == "image_batches" {
		if r.Attributes.InputModalities != nil {
			patch.InputModalities = modelcatalog.IntersectStrings(r.Attributes.InputModalities, new([]string{"text", "image"}))
		}
		if r.Attributes.OutputModalities != nil {
			patch.OutputModalities = modelcatalog.IntersectStrings(r.Attributes.OutputModalities, new([]string{"image"}))
		}
	} else if strings.Contains(t.ClientProtocol, "videos_") {
		if r.Attributes.OutputModalities != nil {
			patch.OutputModalities = modelcatalog.IntersectStrings(r.Attributes.OutputModalities, new([]string{"video"}))
		}
	}
	r.patch(patch, "adapter")
}
