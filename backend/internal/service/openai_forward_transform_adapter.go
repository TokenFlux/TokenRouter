// 请求转换适配仅提供值投影、原生 codec 和账号能力调用，不持有第二份转换状态。
package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	provider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	tierpolicy "github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type openAIForwardTransformAdapter struct{ openAIForwardPreludeAdapter }

func (p openAIForwardTransformAdapter) Decode(body []byte) (map[string]any, error) {
	return requeststate.DecodeOpenAIRequestBody(body)
}
func (p openAIForwardTransformAdapter) GroupImagePolicy(inherited string) string {
	key := getAPIKeyFromContext(p.c)
	return groupResponsesExplicitToolPolicy(responsesPolicyGroup(p.ctx, apiKeyGroup(key)), inherited)
}
func (p openAIForwardTransformAdapter) ImageAllowed() bool {
	key := getAPIKeyFromContext(p.c)
	if key == nil {
		return routing.GroupAllowsResponsesImages(nil)
	}
	return routing.GroupAllowsResponsesImages(key.Group)
}
func (p openAIForwardTransformAdapter) LiteHeader() bool {
	return p.openAIForwardPreludeAdapter.LiteHeader()
}
func (p openAIForwardTransformAdapter) BridgeEnabled(ctx context.Context) bool {
	return p.s.isCodexImageGenerationBridgeEnabled(ctx, p.account, getAPIKeyFromContext(p.c))
}
func (p openAIForwardTransformAdapter) ImageIntentHint(model string, body []byte) bool {
	return gatewayhttp.ResolveOpenAIImageIntentHint(p.c, model, body, provider.ImageIntent().IsImageGenerationIntent)
}
func (p openAIForwardTransformAdapter) Models(model string, compact bool) (string, string) {
	return provider.ExecutionModelPolicy(p.account).ForwardMappedModels(model, compact)
}
func (p openAIForwardTransformAdapter) CompactModel(model string) string {
	return p.s.resolveOpenAICompactFallbackModel(p.account, model)
}
func (p openAIForwardTransformAdapter) ImagePermissionMessage() string {
	return media.ImageGenerationPermissionMessage
}
func (p openAIForwardTransformAdapter) IsImageGenerationIntent(endpoint, model string, body []byte) bool {
	return provider.ImageIntent().IsImageGenerationIntent(endpoint, model, body)
}
func (p openAIForwardTransformAdapter) IsExplicitImageGenerationIntent(endpoint, model string, body []byte) bool {
	return provider.ImageIntent().IsExplicitImageGenerationIntent(endpoint, model, body)
}
func (p openAIForwardTransformAdapter) IsImageGenerationIntentMap(endpoint, model string, body map[string]any) bool {
	return provider.ImageIntent().IsImageGenerationIntentMap(endpoint, model, body)
}
func (p openAIForwardTransformAdapter) IsExplicitImageGenerationIntentMap(endpoint, model string, body map[string]any) bool {
	return provider.ImageIntent().IsExplicitImageGenerationIntentMap(endpoint, model, body)
}
func (p openAIForwardTransformAdapter) IsOpenAIImageGenerationModel(model string) bool {
	return media.IsImageGenerationModel(model)
}
func (p openAIForwardTransformAdapter) IsCodexSparkModel(model string) bool {
	return isCodexSparkModel(model)
}
func (p openAIForwardTransformAdapter) OpenAIRequestBodyImageGenerationToolNeedsNormalization(body []byte) bool {
	return provider.ImageIntent().OpenAIRequestBodyImageGenerationToolNeedsNormalization(body)
}
func (p openAIForwardTransformAdapter) OpenAIRequestBodyHasImageGenerationDeclaration(body []byte) bool {
	return provider.ImageIntent().OpenAIRequestBodyHasImageGenerationDeclaration(body)
}
func (p openAIForwardTransformAdapter) EnsureOpenAIResponsesImageGenerationTool(body map[string]any) bool {
	return ensureOpenAIResponsesImageGenerationTool(body)
}
func (p openAIForwardTransformAdapter) EnsureOpenAIResponsesImageGenerationToolChoiceAuto(body map[string]any) bool {
	return ensureOpenAIResponsesImageGenerationToolChoiceAuto(body)
}
func (p openAIForwardTransformAdapter) NormalizeOpenAIResponsesImageOnlyModel(body map[string]any) bool {
	return normalizeOpenAIResponsesImageOnlyModel(body)
}
func (p openAIForwardTransformAdapter) ValidateOpenAIResponsesImageModel(body map[string]any, model string) error {
	return validateOpenAIResponsesImageModel(body, model)
}
func (p openAIForwardTransformAdapter) ValidateCodexSparkInput(body map[string]any, model string) error {
	return validateCodexSparkInput(body, model)
}
func (p openAIForwardTransformAdapter) ApplyCodexImageGenerationBridgeInstructions(body map[string]any) bool {
	return applyCodexImageGenerationBridgeInstructions(body)
}
func (p openAIForwardTransformAdapter) CodexTransform(body map[string]any, options openai.CodexOAuthTransformOptions) openai.CodexTransformResult {
	return applyCodexOAuthTransformWithOptions(body, options)
}
func (p openAIForwardTransformAdapter) EnsureCodexOAuthInstructionsField(body map[string]any) {
	ensureCodexOAuthInstructionsField(body)
}
func (p openAIForwardTransformAdapter) ToolNameReverse(mapping map[string]string) {
	setCodexToolNameReverse(p.c, mapping)
}
func (p openAIForwardTransformAdapter) ClientMetadata(body map[string]any) bool {
	return applyCodexClientMetadata(body, p.account)
}
func (p openAIForwardTransformAdapter) AccountIdentity(body map[string]any) bool {
	return openai.ApplyCodexAccountIdentityClientMetadataMap(body, accountprovider.CodexIdentityNamespace(gatewayhttp.CodexIdentityRecord(p.c, p.account.View())), gatewayhttp.APIKeyIDFromContext(p.c))
}
func (p openAIForwardTransformAdapter) ClearFingerprint() {
	gatewayhttp.StageCodexFingerprintIDs(p.c, nil)
}
func (p openAIForwardTransformAdapter) Fingerprint(ctx context.Context, body map[string]any) (*openai.FingerprintIDs, bool, error) {
	account, err := provider.CredentialAccount(ctx, p.s.accountRepo, p.account)
	if err != nil {
		return nil, false, fmt.Errorf("resolve Codex fingerprint account: %w", err)
	}
	changed := applyCodexClientMetadata(body, account)
	var headers http.Header
	if p.c != nil && p.c.Request != nil {
		headers = p.c.Request.Header
	}
	ids := accountprovider.CodexFingerprintIDsFromRequest(account.View(), headers)
	if openai.ApplyCodexFingerprintClientMetadata(body, ids) {
		changed = true
	}
	return ids, changed, nil
}
func (p openAIForwardTransformAdapter) FastDecision(ctx context.Context, model, tier string, hasTier bool) forward.FastDecision {
	decision := tierpolicy.Resolve(p.s.fastModeInput(ctx, p.account, model), tier, hasTier)
	value := forward.FastDecision{DeleteField: decision.DeleteField, Tier: decision.Tier}
	if decision.Blocked != nil {
		value.Blocked = decision.Blocked
	}
	return value
}
func (p openAIForwardTransformAdapter) FastBlocked(err error) {
	var blocked *tierpolicy.BlockedError
	if errors.As(err, &blocked) {
		gatewayhttp.WriteFastPolicyBlockedResponse(p.c, blocked)
	}
}
func (p openAIForwardTransformAdapter) SanitizeOpenAIResponsesOrphanToolOutputs(body map[string]any, input []any, hasPrevious bool) bool {
	return provider.SanitizeOpenAIResponsesOrphanToolOutputs(body, input, hasPrevious)
}
func (p openAIForwardTransformAdapter) FirstNonEmptyString(values ...any) string {
	return openai.FirstNonEmptyString(values...)
}

func (p openAIForwardTransformAdapter) Marshal(body map[string]any) ([]byte, error) {
	return wirejson.Marshal(body)
}
func (p openAIForwardTransformAdapter) NormalizeTrigger(body []byte) ([]byte, bool, error) {
	return protocolopenai.NormalizeCompactionTriggerInputOrder(body)
}
