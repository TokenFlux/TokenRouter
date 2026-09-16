// 图片响应只合并原先使用的非零用量字段，不统一其它协议的合并规则。
package openai

func MergeImageResponseUsage(dst *ForwardUsage, body []byte) {
	if dst == nil {
		return
	}
	if parsed, ok := ExtractOpenAIUsageFromJSONBytes(body); ok {
		if parsed.InputTokens > 0 {
			dst.InputTokens = parsed.InputTokens
		}
		if parsed.OutputTokens > 0 {
			dst.OutputTokens = parsed.OutputTokens
		}
		if parsed.CacheReadInputTokens > 0 {
			dst.CacheReadInputTokens = parsed.CacheReadInputTokens
		}
		if parsed.ImageInputTokens > 0 {
			dst.ImageInputTokens = parsed.ImageInputTokens
		}
		if parsed.ImageOutputTokens > 0 {
			dst.ImageOutputTokens = parsed.ImageOutputTokens
		}
	}
}
