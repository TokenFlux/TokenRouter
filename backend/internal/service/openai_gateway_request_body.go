package service

import (
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	s09wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

func (s *OpenAIGatewayService) validateUpstreamBaseURL(raw string) (string, error) {
	normalized, err := s.validateOutboundURL(raw)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return normalized, nil
}

// validateOutboundURL 按安全配置校验网关主动连接的 URL。
func (s *OpenAIGatewayService) validateOutboundURL(raw string) (string, error) {
	if s == nil || s.cfg == nil {
		return egress.ValidateURLFormat(raw, false)
	}
	if !s.cfg.Security.URLAllowlist.Enabled {
		return egress.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
	}
	return egress.ValidateHTTPSURL(raw, egress.ValidationOptions{
		AllowedHosts:     s.cfg.Security.URLAllowlist.UpstreamHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
}

func (s *OpenAIGatewayService) replaceModelInResponseBody(body []byte, fromModel, toModel string) []byte {
	return s09wire.ReplaceModelInResponseBody(body, fromModel, toModel)
}

func appendOpenAIResponsesRequestPathSuffix(base, suffix string) string {
	return openai.AppendResponsesPathSuffix(base, suffix)
}
