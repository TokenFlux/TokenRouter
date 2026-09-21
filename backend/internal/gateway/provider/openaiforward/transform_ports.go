// TransformPorts 注入动态策略与账号侧身份投影；参数中的 map 仅承载 wire JSON。
package openaiforward

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type TransformPorts interface {
	Reject(Rejection)
	Log(string, ...any)
	Decode(body []byte) (map[string]any, error)
	GroupImagePolicy(inherited string) string
	ImageAllowed() bool
	LiteHeader() bool
	BridgeEnabled(ctx context.Context) bool
	ImageIntentHint(model string, body []byte) bool
	Models(model string, compact bool) (string, string)
	CompactModel(model string) string
	ImagePermissionMessage() string
	IsImageGenerationIntent(endpoint, model string, body []byte) bool
	IsExplicitImageGenerationIntent(endpoint, model string, body []byte) bool
	IsImageGenerationIntentMap(endpoint, model string, body map[string]any) bool
	IsExplicitImageGenerationIntentMap(endpoint, model string, body map[string]any) bool
	IsOpenAIImageGenerationModel(model string) bool
	IsCodexSparkModel(model string) bool
	OpenAIRequestBodyImageGenerationToolNeedsNormalization(body []byte) bool
	OpenAIRequestBodyHasImageGenerationDeclaration(body []byte) bool
	EnsureOpenAIResponsesImageGenerationTool(body map[string]any) bool
	EnsureOpenAIResponsesImageGenerationToolChoiceAuto(body map[string]any) bool
	NormalizeOpenAIResponsesImageOnlyModel(body map[string]any) bool
	ValidateOpenAIResponsesImageModel(body map[string]any, model string) error
	ValidateCodexSparkInput(body map[string]any, model string) error
	ApplyCodexImageGenerationBridgeInstructions(body map[string]any) bool
	CodexTransform(body map[string]any, options openai.CodexOAuthTransformOptions) openai.CodexTransformResult
	EnsureCodexOAuthInstructionsField(body map[string]any)
	ToolNameReverse(mapping map[string]string)
	ClientMetadata(body map[string]any) bool
	AccountIdentity(body map[string]any) bool
	ClearFingerprint()
	Fingerprint(ctx context.Context, body map[string]any) (*openai.FingerprintIDs, bool, error)
	FastDecision(ctx context.Context, model, tier string, hasTier bool) FastDecision
	FastBlocked(err error)
	SanitizeOpenAIResponsesOrphanToolOutputs(body map[string]any, input []any, hasPrevious bool) bool
	FirstNonEmptyString(values ...any) string
	OpenAIResponsesInputMayNeedTruncation(body []byte) bool
	TruncateOpenAIResponsesInputText(body map[string]any) bool
	Marshal(body map[string]any) ([]byte, error)
	NormalizeTrigger(body []byte) ([]byte, bool, error)
}
