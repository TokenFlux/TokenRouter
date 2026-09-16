// 旧审核入口仅转接唯一 moderation 实现，S15/S16 清理。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/moderation"
)

func ExtractContentModerationText(protocol string, body []byte) string {
	return native.ExtractContentModerationText(protocol, body)
}
func ExtractContentModerationPromptExcerpt(protocol string, body []byte) string {
	return native.ExtractContentModerationPromptExcerpt(protocol, body)
}
func ExtractContentModerationPromptExcerptFromInput(input ContentModerationInput) string {
	return native.ExtractContentModerationPromptExcerptFromInput(input)
}
func ExtractContentModerationInput(protocol string, body []byte) ContentModerationInput {
	return native.ExtractContentModerationInput(protocol, body)
}
