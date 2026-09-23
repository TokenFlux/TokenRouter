//go:build unit

// 仅保留白盒契约需要的旧名称；生产消费者已使用唯一平台实现。
package service

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

func (s *GatewayService) computeFinalAnthropicBeta(
	tokenType string,
	mimicClaudeCode bool,
	modelID string,
	clientHeaders http.Header,
	body []byte,
	effectiveDropSet map[string]struct{},
) (string, bool) {
	return claude.ComputeFinalAnthropicBeta(tokenType, mimicClaudeCode, modelID, clientHeaders, body, effectiveDropSet, s.cfg != nil && s.cfg.Gateway.InjectBetaForAPIKey)
}
func (s *GatewayService) computeFinalCountTokensAnthropicBeta(
	tokenType string,
	mimicClaudeCode bool,
	modelID string,
	clientHeaders http.Header,
	body []byte,
	effectiveDropSet map[string]struct{},
) (string, bool) {
	return claude.ComputeFinalCountTokensAnthropicBeta(tokenType, mimicClaudeCode, modelID, clientHeaders, body, effectiveDropSet, s.cfg != nil && s.cfg.Gateway.InjectBetaForAPIKey)
}
func sanitizeStreamError(err error) string { return upstream.SanitizeStreamError(err) }
