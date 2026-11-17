package batchimage

import (
	"context"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// model_attributes.go validates batch-image preferences against resolved
// attributes for the selected provider target.
// Image size, aspect ratio and reference limits may be supplied by the model
// attribute resolver for the selected upstream model. Submission checks only
// attributes that were resolved, so an absent media description leaves the
// request's earlier validation in effect.

// validateModelAttributes checks the normalized submission against media
// attributes for its final provider target. Missing media fields leave the
// corresponding request preference unrestricted here; resolver errors and
// exceeded declared limits reject submission.
func (s *Public) validateModelAttributes(ctx context.Context, session *routing.ModelAttributeSession, groupID *int64, provider *Candidate, model string, request BatchImageSubmitRequest) error {
	target := provider.modelAttributeTarget(model)
	var group int64
	if groupID != nil {
		group = *groupID
	}
	result, err := session.Resolve(ctx, group, target)
	if err != nil {
		return err
	}
	if err := routing.RequireImageOutput(result.Attributes); err != nil {
		return err
	}
	media := result.Attributes.Media
	if media == nil {
		return nil
	}
	if media.ImageSizes != nil && !slices.Contains(media.ImageSizes, request.ImageSize) {
		return ErrBatchImageInvalidItems
	}
	if request.AspectRatio != "" && media.AspectRatios != nil && !slices.Contains(media.AspectRatios, request.AspectRatio) {
		return ErrBatchImageInvalidItems
	}
	for _, item := range request.Items {
		if media.MaxReferenceImages != nil && len(item.ReferenceImages) > *media.MaxReferenceImages {
			return ErrBatchImageTooManyReferenceImages
		}
	}
	return nil
}

// modelAttributeTarget creates the catalogue target for the final upstream
// model and image_batches client protocol. A configured AttributeTarget
// replaces the default Gemini target before the client protocol is assigned.
func (c *Candidate) modelAttributeTarget(model string) modelrules.Target {
	target := modelrules.Target{Platform: "gemini", Model: model}
	if c.AttributeTarget != nil {
		target = c.AttributeTarget(model)
	}
	target.ClientProtocol = "image_batches"
	return target
}
