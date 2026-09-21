package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	coderws "github.com/coder/websocket"
	"github.com/tidwall/sjson"
)

// wsRequestAdapter 只投影账号/分组资格并调用唯一平台 codec，不拥有逐轮处理顺序。
type wsRequestAdapter struct {
	*wsPassthroughAdapter
	client  *coderws.Conn
	isCodex bool
}

func (p *wsRequestAdapter) Mutate(current []byte, path, value string) ([]byte, error) {
	next, err := sjson.SetBytes(current, path, value)
	if err == nil {
		return next, nil
	}

	// 仅在确实需要修改 payload 且 sjson 失败时，退回 map 路径确保兼容性。
	payload := make(map[string]any)
	if unmarshalErr := json.Unmarshal(current, &payload); unmarshalErr != nil {
		return nil, err
	}
	switch path {
	case "type", "model":
		payload[path] = value
	case "client_metadata." + openai.WSTurnMetadataHeader:
		openai.SetOpenAIWSTurnMetadata(payload, fmt.Sprintf("%v", value))
	default:
		return nil, err
	}
	rebuilt, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return nil, marshalErr
	}
	return rebuilt, nil
}
func (p *wsRequestAdapter) RequestedEffort(body []byte, model string) *string {
	return CanonicalRequestedReasoningEffort(body, model)
}
func (p *wsRequestAdapter) ClassifyPrevious(id string) string {
	return ClassifyOpenAIPreviousResponseIDKind(id)
}
func (p *wsRequestAdapter) TurnMetadata() string {
	return strings.TrimSpace(p.request.GetHeader(openai.WSTurnMetadataHeader))
}
func (p *wsRequestAdapter) ImagePolicy(ctx context.Context, body []byte) gatewayws.ImagePolicy {
	apiKey := getAPIKeyFromContext(p.request)
	allowed := GroupAllowsResponsesImages(apiKeyGroup(apiKey))
	explicit := codexImageGenerationExplicitToolPolicyAllow
	if p.isCodex {
		explicit = p.account.CodexImageGenerationExplicitToolPolicy()
	}
	explicit = groupResponsesExplicitToolPolicy(responsesPolicyGroup(ctx, apiKeyGroup(apiKey)), explicit)
	bridge := p.isCodex && !isOpenAIResponsesLiteWebSocketPayload(body) && allowed && explicit != codexImageGenerationExplicitToolPolicyStrip && p.service.isCodexImageGenerationBridgeEnabled(ctx, p.account, apiKey)
	return gatewayws.ImagePolicy{Allowed: allowed, Explicit: explicit, Bridge: bridge}
}
func (p *wsRequestAdapter) BridgeImages(normalized []byte) ([]byte, error) {
	payloadMap := make(map[string]any)
	if err := wirejson.DecodeUseNumber(normalized, &payloadMap); err != nil {
		return nil, err
	}
	bridgeModified := false
	if ensureOpenAIResponsesImageGenerationTool(payloadMap) {
		bridgeModified = true
		gatewayprovider.LogOpenAIWSModeInfo("ingress_ws_codex_image_tool_injected account_id=%d", p.account.ID)
	}
	if ensureOpenAIResponsesImageGenerationToolChoiceAuto(payloadMap) {
		bridgeModified = true
		gatewayprovider.LogOpenAIWSModeInfo("ingress_ws_codex_image_tool_choice_auto account_id=%d", p.account.ID)
	}
	if openai.NormalizeOpenAIResponsesImageGenerationTools(payloadMap) {
		bridgeModified = true
	}
	if applyCodexImageGenerationBridgeInstructions(payloadMap) {
		bridgeModified = true
		gatewayprovider.LogOpenAIWSModeInfo("ingress_ws_codex_image_bridge_instructions_added account_id=%d", p.account.ID)
	}
	if bridgeModified {
		rebuilt, marshalErr := json.Marshal(payloadMap)
		if marshalErr != nil {
			return nil, marshalErr
		}
		normalized = rebuilt
	}

	return normalized, nil

}
func (p *wsRequestAdapter) StripImages(body []byte) ([]byte, bool, error) {
	return stripOpenAIImageGenerationToolsFromRawPayload(body)
}
func (p *wsRequestAdapter) StripSparkImages(body []byte, model string) ([]byte, bool, error) {
	return stripCodexSparkImageGenerationToolFromRawPayload(body, model)
}
func (p *wsRequestAdapter) ImageIntent(routing, upstream string, body []byte) ([]byte, bool, bool) {
	return openAIWSImageIntentForRoutingModel(routing, upstream, body, p.account.Platform)
}
func (p *wsRequestAdapter) FeatureDenied() {
	gatewayhttp.MarkOpsClientBusinessLimited(p.request, gatewayhttp.OpsClientBusinessLimitedReasonLocalFeatureGate)
}
func (p *wsRequestAdapter) ImageDeniedMessage() string { return ImageGenerationPermissionMessage() }
func (p *wsRequestAdapter) ImageBilling(body []byte, model string) (gatewayws.ImageBilling, error) {
	result, err := resolveOpenAIResponsesImageBillingConfigDetailedFromBody(body, model)
	if err != nil {
		return gatewayws.ImageBilling{}, err
	}
	return gatewayws.ImageBilling{Model: result.Model, SizeTier: result.SizeTier, InputSize: result.InputSize}, nil
}
func (p *wsRequestAdapter) WriteBlocked(ctx context.Context, body []byte) {
	writeCtx, cancel := newOpenAIWSDownstreamWriteContext(ctx, p.hooks, p.service.openAIWSWriteTimeout())
	defer cancel()
	_ = p.client.Write(writeCtx, coderws.MessageText, body)
}
func (p *wsRequestAdapter) Log(message string) { gatewayprovider.LogOpenAIWSModeInfo("%s", message) }
