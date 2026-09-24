// 旧装配仅为请求准备提供账号资格、技术编解码和同步观测投影。
package service

import (
	"context"
	"errors"
	"strings"

	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/tidwall/gjson"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/protocol"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

type openAIForwardPreludeAdapter struct {
	s       *OpenAIGatewayService
	c       *gin.Context
	account *gatewayprovider.ExecutionAccount
	ctx     context.Context
}

func openAIForwardProfile(account *gatewayprovider.ExecutionAccount) forward.Profile {
	return forward.Profile{Platform: account.Record.Platform, Name: account.Record.Name, Type: account.Record.Type, UsesCodex: account.View().UsesOpenAICodexProtocol(), OpenAI: account.View().IsOpenAI(), OAuth: account.View().IsOAuth(), OAuthLike: account.View().IsOpenAIOAuthLike(), APIKey: account.Record.Type == capability.AccountTypeAPIKey, Grok: account.Record.Platform == capability.PlatformGrok, DeepSeek: account.Record.Platform == capability.PlatformDeepseek, NativeCN: gatewayprovider.ExecutionProtocolTarget(account).UsesNativeCNResponses(), Anthropic: gatewayprovider.ExecutionProtocolTarget(account).IsAnthropicProtocol(), RawChat: shouldForwardOpenAIResponsesViaRawChatCompletions(account), ResolvedChat: account.Route.Protocol() == protocol.ProtocolOpenAIChatCompletions, Passthrough: account.View().IsOpenAIPassthroughEnabled()}
}
func (p openAIForwardPreludeAdapter) BlockGroupImages() bool {
	if source, _ := requeststate.ClientProtocolFromContext(p.ctx); source == protocol.ProtocolOpenAIResponses {
		if key := getAPIKeyFromContext(p.c); key != nil && key.Group != nil {
			return key.Group.ResponsesImagePolicy == "block"
		}
	}
	return false
}
func (p openAIForwardPreludeAdapter) StripImages(body []byte) ([]byte, bool, error) {
	return gatewayprovider.StripOpenAIImageGenerationToolsFromRawPayload(body)
}
func (p openAIForwardPreludeAdapter) Begin() {
	gatewayhttp.BeginUpstreamResponseModelObservation(p.c)
	gatewayhttp.ClearActualOpenAIUpstreamEndpoint(p.c)
	if shouldForwardOpenAIResponsesViaRawChatCompletions(p.account) {
		gatewayhttp.SetActualOpenAIUpstreamEndpoint(p.c, "/v1/chat/completions")
	}
}
func (p openAIForwardPreludeAdapter) FilterNoneReasoning(body []byte) ([]byte, error) {
	return gatewayprovider.FilterOpenAIResponsesNoneReasoningEffortForAccount(gatewayprovider.ExecutionProtocolRecord(p.account), body)
}
func (p openAIForwardPreludeAdapter) ClearMappings() {
	gatewayhttp.ClearGrokResponsesClientToolMapping(p.c)
	gatewayhttp.ClearOpenAIResponsesClientToolMapping(p.c)
	gatewayhttp.ClearOpenAIResponsesNamespaceNames(p.c)
	gatewayhttp.SetCodexToolNameReverse(p.c, nil)
}
func (p openAIForwardPreludeAdapter) PrepareIdentity(ctx context.Context) error {
	_, err := gatewayhttp.PrepareCodexIdentity(ctx, p.c, p.s.accountRepo, p.account)
	return err
}
func (p openAIForwardPreludeAdapter) MatchTLS() egress.TLSFingerprintRouterMatchResult {
	return p.s.Requests.MatchTLS(p.c, p.account)
}
func (p openAIForwardPreludeAdapter) ClientAllowed(ctx context.Context, tls egress.TLSFingerprintRouterMatchResult, body []byte) (bool, string) {
	result := p.s.Requests.DetectClient(p.c, p.account, tls)
	gatewayhttp.LogCodexCLIOnlyDetection(ctx, p.c, p.account, gatewayhttp.APIKeyIDFromContext(p.c), result, body)
	if result.Enabled && !result.Matched {
		return false, openAIClientPolicyForbiddenMessage(result)
	}
	return true, ""
}
func (p openAIForwardPreludeAdapter) Reject(v forward.Rejection) {
	if v.PolicyDenied {
		gatewayhttp.MarkOpsClientBusinessLimited(p.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	}
	if v.FeatureDenied {
		gatewayhttp.MarkOpsClientBusinessLimited(p.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalFeatureGate)
	}
	if v.ObserveUpstream {
		gatewayhttp.SetOpsUpstreamError(p.c, v.Status, v.Message, "")
	}
	gatewayhttp.WriteOpenAIForwardRejection(p.c, v.Status, v.Type, v.Message, v.Param)
}
func (p openAIForwardPreludeAdapter) CompactEffort(body []byte) ([]byte, bool, error) {
	if p.account == nil || !p.account.View().IsOpenAIOAuthLike() || !gatewayhttp.IsOpenAIResponsesCompactPath(p.c) {
		return body, false, nil
	}
	requestedModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	return gatewayprovider.NormalizeOpenAICodexCompactReasoningEffort(body, gatewayprovider.ExecutionModelPolicy(p.account).Mapped(requestedModel))
}
func (p openAIForwardPreludeAdapter) ToolSchemas(body []byte) ([]byte, bool, error) {
	return gatewayprovider.SanitizeOpenAIResponsesToolSchemasForPlatform(body, p.account.Record.Platform)
}
func (p openAIForwardPreludeAdapter) LiteHeader() bool {
	return gatewayprovider.ImageIntent().IsOpenAIResponsesLiteHeader(p.c.GetHeader(media.ResponsesLiteHeader))
}
func (p openAIForwardPreludeAdapter) LitePayload(body []byte) ([]byte, bool, string, error) {
	updated, changed, err := gatewayprovider.NormalizeResponsesLiteForAccount(p.account.View(), body)
	param := "tools"
	var validation *openai.ResponsesLiteValidationError
	if errors.As(err, &validation) {
		param = validation.Parameter()
	}
	return updated, changed, param, err
}
func (p openAIForwardPreludeAdapter) Transport() forward.TransportDecision {
	v := p.s.selection.ResolveTransport(p.account)
	v = gatewayhttp.ResolveOpenAIWSDecisionByClientTransport(v, gatewayhttp.GetOpenAIClientTransport(p.c))
	return forward.TransportDecision{Transport: string(v.Transport), Reason: v.Reason}
}
func (p openAIForwardPreludeAdapter) CompactPath() bool {
	return gatewayhttp.IsOpenAIResponsesCompactPath(p.c)
}
func (p openAIForwardPreludeAdapter) CompactBody(body []byte) ([]byte, bool, error) {
	return openai.NormalizeOpenAICompactRequestBody(body)
}
func (p openAIForwardPreludeAdapter) CompactAPIKeyReplay(body []byte) ([]byte, bool, error) {
	return openai.NormalizeOpenAIAPIKeyStoreFalseReasoningReplay(body, true)
}
func (p openAIForwardPreludeAdapter) FlattenRequired(v forward.TransportDecision, passthrough, compact bool) bool {
	return gatewayprovider.ShouldFlattenOpenAIResponsesNamespaces(p.account, egress.OpenAIUpstreamTransport(v.Transport), passthrough, compact)
}
func (p openAIForwardPreludeAdapter) Flatten(body []byte) ([]byte, error) {
	return gatewayhttp.FlattenOpenAIResponsesNamespaces(p.c, body)
}
func (p openAIForwardPreludeAdapter) StripNamespacesRequired(v forward.TransportDecision, passthrough bool) bool {
	return gatewayprovider.ShouldStripOpenAIResponsesInputNamespaces(p.account, egress.OpenAIUpstreamTransport(v.Transport), passthrough)
}
func (p openAIForwardPreludeAdapter) KeepNamespaces(v forward.TransportDecision, passthrough, compact bool, body []byte) bool {
	return gatewayprovider.ShouldKeepOpenAIResponsesToolCallNamespaces(p.account, egress.OpenAIUpstreamTransport(v.Transport), passthrough, compact, body)
}
func (p openAIForwardPreludeAdapter) StripNamespaces(body []byte, keep bool) ([]byte, error) {
	return protocolbridge.StripOpenAIResponsesInputNamespaces(body, keep)
}
func (p openAIForwardPreludeAdapter) NeedsClientTools(body []byte) bool {
	return protocolbridge.NeedsOpenAIResponsesClientToolAdaptation(body)
}
func (p openAIForwardPreludeAdapter) AdaptClientTools(body []byte) ([]byte, error) {
	body, mapping, err := protocolbridge.AdaptOpenAIResponsesClientTools(body)
	if err == nil {
		gatewayhttp.SetOpenAIResponsesClientToolMapping(p.c, mapping)
	}
	return body, err
}
func (p openAIForwardPreludeAdapter) ValidateEffort(body []byte, model string) error {
	return requeststate.ValidateOpenAIReasoningEffort(body, model)
}
func (p openAIForwardPreludeAdapter) ReasoningReplay(body []byte) ([]byte, bool, error) {
	return openai.NormalizeOpenAIResponsesReasoningContentReplay(body)
}
func (p openAIForwardPreludeAdapter) InputItemIDs(body []byte) ([]byte, bool, error) {
	return gatewayprovider.SanitizeOpenAIResponsesInputItemIDs(body)
}
func (p openAIForwardPreludeAdapter) MessagesBridge(body []byte) bool {
	return gatewayprovider.IsOpenAICompatMessagesBridgeBody(body)
}
func (p openAIForwardPreludeAdapter) BindMessagesBridge(v bool) {
	gatewayhttp.SetOpenAICompatMessagesBridgeContext(p.c, v)
}
func (p openAIForwardPreludeAdapter) CodexClient() bool {
	return openai.IsCodexOfficialClientByHeaders(p.c.GetHeader("User-Agent"), p.c.GetHeader("originator")) || (p.s.cfg != nil && p.s.cfg.Gateway.ForceCodexCLI)
}
func (p openAIForwardPreludeAdapter) ImageToolPolicy() string {
	return gatewayprovider.ExecutionProtocolRecord(p.account).CodexImageGenerationExplicitToolPolicy()
}
func (p openAIForwardPreludeAdapter) ObserveTransport(v forward.TransportDecision, model string, stream bool) {
	if p.c != nil {
		p.c.Set("openai_ws_transport_decision", v.Transport)
		p.c.Set("openai_ws_transport_reason", v.Reason)
	}
	if v.Transport == string(egress.OpenAIUpstreamTransportResponsesWebsocketV2) {
		gatewayprovider.LogOpenAIWSModeDebug("selected account_id=%d account_type=%s transport=%s reason=%s model=%s stream=%v", p.account.Record.ID, p.account.Record.Type, gatewayprovider.NormalizeOpenAIWSLogValue(v.Transport), gatewayprovider.NormalizeOpenAIWSLogValue(v.Reason), model, stream)
	}
}
func (p openAIForwardPreludeAdapter) MappedModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(p.account).Mapped(model)
}
func (p openAIForwardPreludeAdapter) PassthroughEffort(body []byte, model string) *string {
	return gatewayprovider.ApplyThinkingEnabledFallback(requeststate.ExtractOpenAIReasoningEffortFromBody(body, model), body, model)
}
func (p openAIForwardPreludeAdapter) Log(format string, args ...any) {
	logging.LegacyPrintf("service.openai_gateway", format, args...)
}
