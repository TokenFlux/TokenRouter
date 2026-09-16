// 旧装配仅为请求准备提供账号资格、技术编解码和同步观测投影。
package service

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

type openAIForwardPreludeAdapter struct {
	s       *OpenAIGatewayService
	c       *gin.Context
	account *Account
	ctx     context.Context
}

func openAIForwardProfile(account *Account) forward.Profile {
	return forward.Profile{Platform: account.Platform, Name: account.Name, Type: account.Type, UsesCodex: account.UsesOpenAICodexProtocol(), OpenAI: account.IsOpenAI(), OAuth: account.IsOAuth(), OAuthLike: account.IsOpenAIOAuthLike(), APIKey: account.Type == AccountTypeAPIKey, Grok: account.Platform == PlatformGrok, DeepSeek: account.Platform == PlatformDeepseek, NativeCN: account.UsesNativeCNResponses(), Anthropic: account.IsAnthropicProtocol(), RawChat: shouldForwardOpenAIResponsesViaRawChatCompletions(account), ResolvedChat: account.resolvedProtocol == protocol.ProtocolOpenAIChatCompletions, Passthrough: account.IsOpenAIPassthroughEnabled()}
}
func (p openAIForwardPreludeAdapter) BlockGroupImages() bool {
	if source, _ := p.ctx.Value(clientProtocolContextKey{}).(protocol.ProtocolID); source == protocol.ProtocolOpenAIResponses {
		if key := getAPIKeyFromContext(p.c); key != nil && key.Group != nil {
			return key.Group.ResponsesImagePolicy == "block"
		}
	}
	return false
}
func (p openAIForwardPreludeAdapter) StripImages(body []byte) ([]byte, bool, error) {
	return stripOpenAIImageGenerationToolsFromRawPayload(body)
}
func (p openAIForwardPreludeAdapter) Begin() {
	beginUpstreamResponseModelObservation(p.c)
	ClearActualOpenAIUpstreamEndpoint(p.c)
	if shouldForwardOpenAIResponsesViaRawChatCompletions(p.account) {
		SetActualOpenAIUpstreamEndpoint(p.c, "/v1/chat/completions")
	}
}
func (p openAIForwardPreludeAdapter) FilterNoneReasoning(body []byte) ([]byte, error) {
	return filterOpenAIResponsesNoneReasoningEffortForAccount(p.account, body)
}
func (p openAIForwardPreludeAdapter) ClearMappings() {
	clearGrokResponsesClientToolMapping(p.c)
	clearOpenAIResponsesClientToolMapping(p.c)
	clearOpenAIResponsesNamespaceNames(p.c)
	setCodexToolNameReverse(p.c, nil)
}
func (p openAIForwardPreludeAdapter) PrepareIdentity(ctx context.Context) error {
	_, err := p.s.prepareCodexAccountIdentitySource(ctx, p.c, p.account)
	return err
}
func (p openAIForwardPreludeAdapter) MatchTLS() egress.TLSFingerprintRouterMatchResult {
	return p.s.matchTLSFingerprintRouter(p.c, p.account)
}
func (p openAIForwardPreludeAdapter) ClientAllowed(ctx context.Context, tls egress.TLSFingerprintRouterMatchResult, body []byte) (bool, string) {
	result := p.s.detectCodexClientRestriction(p.c, p.account, tls)
	logCodexCLIOnlyDetection(ctx, p.c, p.account, getAPIKeyIDFromContext(p.c), result, body)
	if result.Enabled && !result.Matched {
		return false, openAIClientPolicyForbiddenMessage(result)
	}
	return true, ""
}
func (p openAIForwardPreludeAdapter) Reject(v forward.Rejection) {
	if v.PolicyDenied {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	}
	if v.FeatureDenied {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalFeatureGate)
	}
	if v.ObserveUpstream {
		setOpsUpstreamError(p.c, v.Status, v.Message, "")
	}
	gatewayhttp.WriteOpenAIForwardRejection(p.c, v.Status, v.Type, v.Message, v.Param)
}
func (p openAIForwardPreludeAdapter) CompactEffort(body []byte) ([]byte, bool, error) {
	return normalizeOpenAICodexCompactReasoningEffortForAccount(p.c, p.account, body)
}
func (p openAIForwardPreludeAdapter) ToolSchemas(body []byte) ([]byte, bool, error) {
	return sanitizeOpenAIResponsesToolSchemasForPlatform(body, p.account.Platform)
}
func (p openAIForwardPreludeAdapter) LiteHeader() bool {
	return isOpenAIResponsesLiteHeader(p.c.GetHeader(responsesLiteHeader))
}
func (p openAIForwardPreludeAdapter) LitePayload(body []byte) ([]byte, bool, string, error) {
	updated, changed, err := normalizeOpenAIResponsesLitePayloadForAccount(p.account, body)
	param := "tools"
	var validation *openAIResponsesLiteValidationError
	if errors.As(err, &validation) {
		param = validation.param
	}
	return updated, changed, param, err
}
func (p openAIForwardPreludeAdapter) Transport() forward.TransportDecision {
	v := p.s.getOpenAIWSProtocolResolver().Resolve(p.account)
	v = resolveOpenAIWSDecisionByClientTransport(v, GetOpenAIClientTransport(p.c))
	return forward.TransportDecision{Transport: string(v.Transport), Reason: v.Reason}
}
func (p openAIForwardPreludeAdapter) CompactPath() bool { return isOpenAIResponsesCompactPath(p.c) }
func (p openAIForwardPreludeAdapter) CompactBody(body []byte) ([]byte, bool, error) {
	return normalizeOpenAICompactRequestBody(body)
}
func (p openAIForwardPreludeAdapter) CompactAPIKeyReplay(body []byte) ([]byte, bool, error) {
	return normalizeOpenAIAPIKeyStoreFalseReasoningReplay(body, true)
}
func (p openAIForwardPreludeAdapter) FlattenRequired(v forward.TransportDecision, passthrough, compact bool) bool {
	return shouldFlattenOpenAIResponsesNamespaces(p.account, OpenAIUpstreamTransport(v.Transport), passthrough, compact)
}
func (p openAIForwardPreludeAdapter) Flatten(body []byte) ([]byte, error) {
	return flattenOpenAIResponsesNamespaces(p.c, body)
}
func (p openAIForwardPreludeAdapter) StripNamespacesRequired(v forward.TransportDecision, passthrough bool) bool {
	return shouldStripOpenAIResponsesInputNamespaces(p.account, OpenAIUpstreamTransport(v.Transport), passthrough)
}
func (p openAIForwardPreludeAdapter) KeepNamespaces(v forward.TransportDecision, passthrough, compact bool, body []byte) bool {
	return shouldKeepOpenAIResponsesToolCallNamespaces(p.account, OpenAIUpstreamTransport(v.Transport), passthrough, compact, body)
}
func (p openAIForwardPreludeAdapter) StripNamespaces(body []byte, keep bool) ([]byte, error) {
	return stripOpenAIResponsesInputNamespaces(body, keep)
}
func (p openAIForwardPreludeAdapter) NeedsClientTools(body []byte) bool {
	return needsOpenAIResponsesClientToolAdaptation(body)
}
func (p openAIForwardPreludeAdapter) AdaptClientTools(body []byte) ([]byte, error) {
	body, mapping, err := adaptOpenAIResponsesClientTools(body)
	if err == nil {
		setOpenAIResponsesClientToolMapping(p.c, mapping)
	}
	return body, err
}
func (p openAIForwardPreludeAdapter) ValidateEffort(body []byte, model string) error {
	return validateOpenAIReasoningEffort(body, model)
}
func (p openAIForwardPreludeAdapter) ReasoningReplay(body []byte) ([]byte, bool, error) {
	return normalizeOpenAIResponsesReasoningContentReplay(body)
}
func (p openAIForwardPreludeAdapter) InputItemIDs(body []byte) ([]byte, bool, error) {
	return sanitizeOpenAIResponsesInputItemIDs(body)
}
func (p openAIForwardPreludeAdapter) MessagesBridge(body []byte) bool {
	return isOpenAICompatMessagesBridgeBody(body)
}
func (p openAIForwardPreludeAdapter) BindMessagesBridge(v bool) {
	setOpenAICompatMessagesBridgeContext(p.c, v)
}
func (p openAIForwardPreludeAdapter) CodexClient() bool {
	return native.IsCodexOfficialClientByHeaders(p.c.GetHeader("User-Agent"), p.c.GetHeader("originator")) || (p.s.cfg != nil && p.s.cfg.Gateway.ForceCodexCLI)
}
func (p openAIForwardPreludeAdapter) ImageToolPolicy() string {
	return p.account.CodexImageGenerationExplicitToolPolicy()
}
func (p openAIForwardPreludeAdapter) ObserveTransport(v forward.TransportDecision, model string, stream bool) {
	if p.c != nil {
		p.c.Set("openai_ws_transport_decision", v.Transport)
		p.c.Set("openai_ws_transport_reason", v.Reason)
	}
	if v.Transport == string(OpenAIUpstreamTransportResponsesWebsocketV2) {
		logOpenAIWSModeDebug("selected account_id=%d account_type=%s transport=%s reason=%s model=%s stream=%v", p.account.ID, p.account.Type, normalizeOpenAIWSLogValue(v.Transport), normalizeOpenAIWSLogValue(v.Reason), model, stream)
	}
}
func (p openAIForwardPreludeAdapter) MappedModel(model string) string {
	return p.account.GetMappedModel(model)
}
func (p openAIForwardPreludeAdapter) PassthroughEffort(body []byte, model string) *string {
	return ApplyThinkingEnabledFallback(extractOpenAIReasoningEffortFromBody(body, model), body, model)
}
func (p openAIForwardPreludeAdapter) Log(format string, args ...any) {
	logger.LegacyPrintf("service.openai_gateway", format, args...)
}
