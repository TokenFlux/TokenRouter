package service

import (
	"net/http"
	strings "strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func grokContentPolicyClientMessage(responseBody []byte) string {
	return grok.GrokContentPolicyClientMessage(logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(responseBody))))
}

// shouldFailoverGrokUpstreamError 在状态码之外结合响应体判断是否故障转移。
// Grok 内容拒绝必须留在当前账号并返回调用方，不能继续消耗账号池。
func (s *OpenAIGatewayService) shouldFailoverGrokUpstreamError(statusCode int, responseBody []byte) bool {
	if grok.IsGrokContentPolicyRejection(statusCode, responseBody) {
		return false
	}
	// A 422 emitted by xAI's ModelInput decoder is account/runtime compatibility,
	// not quota exhaustion. Another account may run a different upstream build,
	// so fail over without applying an account cooldown.
	if grok.IsGrokDecoderCompatibilityError(statusCode, responseBody) {
		return true
	}
	// xAI 某些兼容端点用 405 表示当前账号不支持该接口；切换账号后仍可能
	// 命中另一种能力配置，因此不能沿用 OpenAI 通用状态码集合将其留在原账号。
	if statusCode == http.StatusMethodNotAllowed {
		return true
	}
	decision := grok.ClassifyGrokUpstreamFailure(statusCode, responseBody, "")
	switch decision.Class {
	case grok.GrokFailureFreeUsage, grok.GrokFailureEmptyUpstream, grok.GrokFailureBilling, grok.GrokFailureModelCapacity, grok.GrokFailureCompatibility:
		return decision.ShouldFailover
	}
	return s.shouldFailoverUpstreamError(statusCode)
}
