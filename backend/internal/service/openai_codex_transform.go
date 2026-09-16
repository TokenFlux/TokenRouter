// 旧 Codex 转换入口仅委托唯一原生 codec，账号与平台选择保留在入站投影。
package service

import (
	"strings"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func normalizeOpenAIModelForUpstream(account *Account, model string) string {
	if account == nil {
		return strings.TrimSpace(model)
	}
	if account.IsGrok() {
		return xai.NormalizeModelID(model)
	}
	if account.UsesOpenAICodexProtocol() {
		return normalizeCodexModel(model)
	}
	return strings.TrimSpace(model)
}

var codexModelMap = native.CodexModelMap

type codexTransformResult = native.CodexTransformResult
type codexOAuthTransformOptions = native.CodexOAuthTransformOptions

const codexCallIDMaxLength = native.CodexCallIDMaxLength
const codexCallIDPrefix = native.CodexCallIDPrefix

func normalizeCodexCallID(id string) string {
	return native.NormalizeCodexCallID(id)
}

const codexImageGenerationBridgeMarker = native.CodexImageGenerationBridgeMarker

const codexSparkImageUnsupportedMarker = native.CodexSparkImageUnsupportedMarker

var openAIChatGPTInternalUnsupportedFields = native.OpenAIChatGPTInternalUnsupportedFields

func applyCodexOAuthTransform(reqBody map[string]any, isCodexCLI bool, isCompact bool) codexTransformResult {
	return applyCodexOAuthTransformWithOptions(reqBody, codexOAuthTransformOptions{IsCodexCLI: isCodexCLI, IsCompact: isCompact})
}
func applyCodexOAuthTransformWithOptions(reqBody map[string]any, opts codexOAuthTransformOptions) codexTransformResult {
	opts.ModelRules = codexModelRules()
	opts.IsMessagesBridge = isOpenAICompatMessagesBridgeRequestBody
	return native.ApplyCodexOAuthTransformWithOptions(reqBody, opts)
}

func normalizeCodexModel(model string) string {
	return native.NormalizeCodexModel(model, codexModelRules())
}

func isCodexSparkModel(model string) bool {
	return native.IsCodexSparkModel(model, codexModelRules())
}
func hasOpenAIImageGenerationTool(reqBody map[string]any) bool {
	return native.HasOpenAIImageGenerationTool(reqBody)
}
func hasCodexImageGenerationFunctionTool(reqBody map[string]any) bool {
	return native.HasCodexImageGenerationFunctionTool(reqBody)
}

func stripOpenAIImageGenerationTools(reqBody map[string]any) bool {
	return native.StripOpenAIImageGenerationTools(reqBody)
}

func stripOpenAIImageGenerationToolsFromRawPayload(payload []byte) ([]byte, bool, error) {
	return native.StripOpenAIImageGenerationToolsFromRawPayload(payload, openAIRequestBodyHasImageGenerationDeclaration(payload))
}
func stripCodexSparkImageGenerationTools(reqBody map[string]any) bool {
	return native.StripCodexSparkImageGenerationTools(reqBody)
}

func validateCodexSparkInput(reqBody map[string]any, model string) error {
	return native.ValidateCodexSparkInput(reqBody, model, isCodexSparkModel(model))
}
func normalizeOpenAIResponsesImageGenerationTools(reqBody map[string]any) bool {
	return native.NormalizeOpenAIResponsesImageGenerationTools(reqBody)
}
func normalizeOpenAIResponseFormatSchemas(reqBody map[string]any) bool {
	return native.NormalizeOpenAIResponseFormatSchemas(reqBody)
}

func ensureOpenAIResponsesImageGenerationTool(reqBody map[string]any) bool {
	return native.EnsureOpenAIResponsesImageGenerationTool(reqBody, isCodexSparkModel(firstNonEmptyString(reqBody["model"])))
}
func ensureOpenAIResponsesImageGenerationToolChoiceAuto(reqBody map[string]any) bool {
	return native.EnsureOpenAIResponsesImageGenerationToolChoiceAuto(reqBody, isCodexSparkModel(firstNonEmptyString(reqBody["model"])))
}
func applyCodexImageGenerationBridgeInstructions(reqBody map[string]any) bool {
	return native.ApplyCodexImageGenerationBridgeInstructions(reqBody, isCodexSparkModel(firstNonEmptyString(reqBody["model"])))
}

func validateOpenAIResponsesImageModel(reqBody map[string]any, model string) error {
	return native.ValidateOpenAIResponsesImageModel(reqBody, model, isOpenAIImageGenerationModel(strings.TrimSpace(model)))
}
func normalizeOpenAIResponsesImageOnlyModel(reqBody map[string]any) bool {
	return native.NormalizeOpenAIResponsesImageOnlyModel(reqBody, isOpenAIImageGenerationModel(firstNonEmptyString(reqBody["model"])))
}
func SupportsVerbosity(model string) bool {
	return native.SupportsVerbosity(model)
}
func getNormalizedCodexModel(modelID string) string {
	return native.GetNormalizedCodexModel(modelID)
}

func extractSystemMessagesFromInput(reqBody map[string]any, omitPromoted bool) bool {
	return native.ExtractSystemMessagesFromInput(reqBody, omitPromoted)
}

func extractPromptLikeInstructionsFromInput(reqBody map[string]any) string {
	return native.ExtractPromptLikeInstructionsFromInput(reqBody)
}
func defaultCodexSynthInstructions(model string) string {
	return native.DefaultCodexSynthInstructions(model)
}
func ensureCodexReasoningInclude(reqBody map[string]any) bool {
	return native.EnsureCodexReasoningInclude(reqBody)
}
func applyCodexClientMetadata(reqBody map[string]any, account *Account) bool {
	if account == nil {
		return false
	}
	return native.ApplyCodexClientMetadata(reqBody, account.GetOpenAIDeviceID())
}

func isInstructionsEmpty(reqBody map[string]any) bool {
	return native.IsInstructionsEmpty(reqBody)
}

func filterCodexInput(input []any, preserveReferences bool) []any {
	return native.FilterCodexInput(input, preserveReferences)
}

func isCodexToolCallItemType(typ string) bool {
	return native.IsCodexToolCallItemType(typ)
}

func codexInputItemRequiresName(typ string) bool {
	return native.CodexInputItemRequiresName(typ)
}

func codexModelRules() native.CodexModelRules {
	return native.CodexModelRules{
		ImageOnly:      isOpenAIImageGenerationModel,
		LastSegment:    lastOpenAIModelSegment,
		CanonicalAlias: canonicalizeOpenAIModelAliasSpelling,
		KnownModel:     normalizeKnownOpenAICodexModel,
		SupportsEffort: openAIModelSupportsReasoningEffort,
	}
}
