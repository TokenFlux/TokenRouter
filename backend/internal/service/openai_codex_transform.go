// 旧 Codex 转换入口仅委托唯一原生 codec，账号与平台选择保留在入站投影。
package service

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func normalizeOpenAIModelForUpstream(account *Account, model string) string {
	return accountModelPolicy(account).NormalizeOpenAI(model)
}

var openAIChatGPTInternalUnsupportedFields = openai.OpenAIChatGPTInternalUnsupportedFields

func applyCodexOAuthTransform(reqBody map[string]any, isCodexCLI bool, isCompact bool) openai.CodexTransformResult {
	return applyCodexOAuthTransformWithOptions(reqBody, openai.CodexOAuthTransformOptions{IsCodexCLI: isCodexCLI, IsCompact: isCompact})
}
func applyCodexOAuthTransformWithOptions(reqBody map[string]any, opts openai.CodexOAuthTransformOptions) openai.CodexTransformResult {
	opts.ModelRules = gatewayprovider.CodexModelRules()
	opts.IsMessagesBridge = isOpenAICompatMessagesBridgeRequestBody
	return openai.ApplyCodexOAuthTransformWithOptions(reqBody, opts)
}

func isCodexSparkModel(model string) bool {
	return openai.IsCodexSparkModel(model, gatewayprovider.CodexModelRules())
}

func stripOpenAIImageGenerationToolsFromRawPayload(payload []byte) ([]byte, bool, error) {
	return openai.StripOpenAIImageGenerationToolsFromRawPayload(payload, openAIRequestBodyHasImageGenerationDeclaration(payload))
}

func validateCodexSparkInput(reqBody map[string]any, model string) error {
	return openai.ValidateCodexSparkInput(reqBody, model, isCodexSparkModel(model))
}

func ensureOpenAIResponsesImageGenerationTool(reqBody map[string]any) bool {
	return openai.EnsureOpenAIResponsesImageGenerationTool(reqBody, isCodexSparkModel(openai.FirstNonEmptyString(reqBody["model"])))
}
func ensureOpenAIResponsesImageGenerationToolChoiceAuto(reqBody map[string]any) bool {
	return openai.EnsureOpenAIResponsesImageGenerationToolChoiceAuto(reqBody, isCodexSparkModel(openai.FirstNonEmptyString(reqBody["model"])))
}
func applyCodexImageGenerationBridgeInstructions(reqBody map[string]any) bool {
	return openai.ApplyCodexImageGenerationBridgeInstructions(reqBody, isCodexSparkModel(openai.FirstNonEmptyString(reqBody["model"])))
}

func validateOpenAIResponsesImageModel(reqBody map[string]any, model string) error {
	return openai.ValidateOpenAIResponsesImageModel(reqBody, model, media.IsImageGenerationModel(strings.TrimSpace(model)))
}
func normalizeOpenAIResponsesImageOnlyModel(reqBody map[string]any) bool {
	return openai.NormalizeOpenAIResponsesImageOnlyModel(reqBody, media.IsImageGenerationModel(openai.FirstNonEmptyString(reqBody["model"])))
}

func applyCodexClientMetadata(reqBody map[string]any, account *Account) bool {
	if account == nil {
		return false
	}
	return openai.ApplyCodexClientMetadata(reqBody, account.GetOpenAIDeviceID())
}
