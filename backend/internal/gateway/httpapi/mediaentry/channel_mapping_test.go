package mediaentry

import (
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

func applyGrokMediaChannelMapping(body []byte, contentType string, mapping routing.ChannelMappingResult) ([]byte, string, error) {
	return gatewaymedia.RewriteMappedMediaBody(body, contentType, mapping.Mapped, mapping.MappedModel, gatewaycapture.GrokMediaCodec().RewriteGrokMediaRequestModel)
}
