package openai

import protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

// correctToolCallsInResponseBody 修正响应体中的工具调用
func (c *CodexToolCorrector) CorrectResponseBody(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	updated := body
	if c != nil {
		if corrected, changed := c.CorrectToolCallsInSSEBytes(updated); changed {
			updated = corrected
		}
	}
	if normalized, changed := protocolopenai.NormalizeOpenAIResponsesFunctionCallArguments(updated); changed {
		updated = normalized
	}
	return updated
}
