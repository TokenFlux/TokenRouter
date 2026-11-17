package modelrules

import (
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// grok.go defines Grok model parameters across text, image and video routes. The
// selected upstream protocol determines which text controls can be sent, and
// full model names select the registered media sizes and operations.

// applyGrokEncoding selects payload controls for the final Grok author identity.
func (r *Resolution) applyGrokEncoding(model string, patch *modelcatalog.Attributes) {
	t := r.Target
	// Grok field restrictions are registered for OpenAI-shaped requests; a
	// Messages route does not establish the same payload field ownership.
	if t.UpstreamProtocol == "anthropic_messages" {
		return
	}
	// Grok model-specific removals accompany its control declarations. The
	// forwarding parameter pass consumes DropFields even when the base
	// catalog contains other capability metadata.
	if slices.Contains([]string{"grok-composer", "grok-composer-2.5-fast", "composer-2.5"}, model) {
		patch.ReasoningOptions = new([]modelcatalog.ReasoningOption{})
	}
	if strings.HasPrefix(model, "grok-4.20") {
		r.Rules.DropFields = append(r.Rules.DropFields, "logprobs", "top_logprobs")
	}
	if model == "grok-4.5" {
		r.Rules.DropFields = append(r.Rules.DropFields, "presence_penalty", "presencePenalty", "frequency_penalty", "frequencyPenalty", "stop")
	}
}

// grokMedia returns supported media parameters for the exact provider model.
func grokMedia(model string) *modelcatalog.MediaCapabilities {
	result := &modelcatalog.MediaCapabilities{}
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
	result.MaxReferenceImages = new(3)
	if model == "grok-imagine-image-2.0" {
		result.Qualities = []string{"low", "medium"}
	}
	return result
}
