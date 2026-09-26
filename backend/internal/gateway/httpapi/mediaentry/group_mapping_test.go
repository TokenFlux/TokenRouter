package mediaentry

import (
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

func applyGrokMediaGroupMapping(body []byte, contentType string, mapping routing.GroupMappingResult) ([]byte, string, error) {
	return gatewaymedia.RewriteMappedMediaBody(body, contentType, mapping.Mapped, mapping.MappedModel, gatewaycapture.GrokMediaCodec().RewriteGrokMediaRequestModel)
}
