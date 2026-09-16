// 内联图片观察不估算用量，保留 camel/snake 字段与非空数据规则。
package gemini

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/tidwall/gjson"
)

// CountGeminiInlineImageOutputs 统计一段 Gemini 响应 JSON 里的内联图片 part。
// Gemini REST 回 camelCase 的 inlineData，官方 SDK 与部分中转会回 snake_case
// 的 inline_data，两种都要认。
func CountGeminiInlineImageOutputs(payload []byte) int {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return 0
	}
	count := 0
	gjson.GetBytes(payload, "candidates").ForEach(func(_, candidate gjson.Result) bool {
		candidate.Get("content.parts").ForEach(func(_, part gjson.Result) bool {
			if GeminiPartIsInlineImage(part) {
				count++
			}
			return true
		})
		return true
	})
	return count
}
func GeminiPartIsInlineImage(part gjson.Result) bool {
	inline := part.Get("inlineData")
	if !inline.Exists() {
		inline = part.Get("inline_data")
	}
	if !inline.Exists() {
		return false
	}

	mimeType := inline.Get("mimeType")
	if !mimeType.Exists() {
		mimeType = inline.Get("mime_type")
	}
	if !bridge.NativeIsGeminiInlineImageMIMEType(strings.ToLower(strings.TrimSpace(mimeType.String()))) {
		return false
	}

	// 只认真的带上了 base64 数据的 part，空壳 part 不计费。
	return strings.TrimSpace(inline.Get("data").String()) != ""
}
