// Legacy 出口只服务未迁入口，算法和状态仍在本包，S15/S16 删除。
package moderation

import "github.com/tidwall/gjson"

func LegacyContentModerationAuditInput(input ContentModerationInput, cfg *ContentModerationConfig) ContentModerationInput {
	return contentModerationAuditInput(input, cfg)
}
func LegacyContentModerationRunePrefix(text string, limit int) (string, int) {
	return contentModerationRunePrefix(text, limit)
}

type LegacyContentModerationInputBuilder = contentModerationInputBuilder

func LegacyNewContentModerationInputBuilder() *contentModerationInputBuilder {
	return newContentModerationInputBuilder()
}
func LegacyCollectChatCurrentTurn(messages gjson.Result, builder *contentModerationInputBuilder) {
	collectChatCurrentTurn(messages, builder)
}
func LegacyCollectAnthropicCurrentTurn(messages gjson.Result, builder *contentModerationInputBuilder) {
	collectAnthropicCurrentTurn(messages, builder)
}
func LegacyCollectResponsesCurrentInput(input gjson.Result, builder *contentModerationInputBuilder) {
	collectResponsesCurrentInput(input, builder)
}
func LegacyIndexAfterLastResponsesModelItem(items []gjson.Result) int {
	return indexAfterLastResponsesModelItem(items)
}
func LegacyIsResponsesModelToolCallType(typ string) bool { return isResponsesModelToolCallType(typ) }
func LegacyCollectResponsesInputItem(item gjson.Result, builder *contentModerationInputBuilder) {
	collectResponsesInputItem(item, builder)
}
func LegacyIsResponsesToolOutputType(typ string) bool { return isResponsesToolOutputType(typ) }
func LegacyCollectGeminiCurrentTurn(contents gjson.Result, builder *contentModerationInputBuilder) {
	collectGeminiCurrentTurn(contents, builder)
}
func LegacyIndexAfterLastRole(items []gjson.Result, role string) int {
	return indexAfterLastRole(items, role)
}
func LegacyCollectContentValue(value gjson.Result, source string, builder *contentModerationInputBuilder, preserveStructured bool) {
	collectContentValue(value, source, builder, preserveStructured)
}
func LegacyAddKnownModerationImages(builder *contentModerationInputBuilder, source string, value gjson.Result) {
	addKnownModerationImages(builder, source, value)
}
func LegacyCollectImagesRecursively(value gjson.Result, source string, builder *contentModerationInputBuilder) {
	collectImagesRecursively(value, source, builder)
}
func LegacyAddGeminiModerationImage(builder *contentModerationInputBuilder, source string, part gjson.Result) {
	addGeminiModerationImage(builder, source, part)
}
func LegacyAddModerationImageData(builder *contentModerationInputBuilder, source string, mimeType string, data string) {
	addModerationImageData(builder, source, mimeType, data)
}
func LegacyIsSupportedModerationImageReference(reference string) bool {
	return isSupportedModerationImageReference(reference)
}
func LegacyNormalizeModerationImages(images []string) []string {
	return normalizeModerationImages(images)
}
func LegacyNormalizeContentModerationText(text string) string {
	return normalizeContentModerationText(text)
}
