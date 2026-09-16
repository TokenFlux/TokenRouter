// Chat 旧装配复用同一账号/输出依赖，差异策略通过专属投影提供。
package service

import (
	"context"
	"errors"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"net/http"
)

type openAIChatExecutionAdapter struct {
	*openAIMessagesExecutionAdapter
}

func (p *openAIChatExecutionAdapter) PrepareChat(ctx context.Context) (forward.ChatProfile, error) {
	account, err := accountForProtocolAttempt(ctx, p.account)
	if err != nil {
		return forward.ChatProfile{}, err
	}
	p.account = account
	beginUpstreamResponseModelObservation(p.c)
	ClearActualOpenAIUpstreamEndpoint(p.c)
	if shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
		SetActualOpenAIUpstreamEndpoint(p.c, "/v1/chat/completions")
	}
	setCodexToolNameReverse(p.c, nil)
	if _, err := p.s.prepareCodexAccountIdentitySource(ctx, p.c, account); err != nil {
		return forward.ChatProfile{}, err
	}
	return forward.ChatProfile{MessagesProfile: forward.MessagesProfile{Profile: openAIForwardProfile(account), ID: account.ID, GrokOAuth: account.IsGrokOAuth(), Shadow: account.IsShadow()}, Protocol: account.resolvedProtocol, Adaptive: account.IsAdaptiveAPIProtocol(), SupportsNativeCN: account.SupportsNativeCNResponses(), CNProvider: account.IsCNProvider(), OpenAIAPIKey: account.IsOpenAIApiKey()}, nil
}
func (p *openAIChatExecutionAdapter) DispatchChat(ctx context.Context, route forward.Dispatch, body []byte, key, model string) (*forward.Result, error) {
	var r *OpenAIForwardResult
	var err error
	switch route {
	case forward.DispatchAnthropic:
		r, err = p.s.forwardChatCompletionsViaNativeAnthropic(ctx, p.c, p.account, body, model)
	case forward.DispatchRawChat:
		r, err = p.s.forwardAsRawChatCompletions(ctx, p.c, p.account, body, model, p.tls...)
	case forward.DispatchGrok:
		r, err = p.s.forwardGrokChatCompletionsViaResponses(ctx, p.c, p.account, body, key, model, p.tls...)
	}
	return openAIHTTPResultFromForward(r), err
}
func (p *openAIChatExecutionAdapter) ClientAllowed(ctx context.Context, body []byte) bool {
	var match TLSFingerprintRouterMatchResult
	if len(p.tls) > 0 {
		match = p.tls[0]
	} else {
		match = p.s.matchTLSFingerprintRouter(p.c, p.account)
	}
	restriction := p.s.detectCodexClientRestriction(p.c, p.account, match)
	logCodexCLIOnlyDetection(ctx, p.c, p.account, getAPIKeyIDFromContext(p.c), restriction, body)
	return !restriction.Enabled || restriction.Matched
}
func (p *openAIChatExecutionAdapter) PolicyDenied() {
	MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
}
func (p *openAIChatExecutionAdapter) Reject(status int, kind, message string) {
	gatewayhttp.WriteOpenAIForwardRejection(p.c, status, kind, message, "")
}
func (p *openAIChatExecutionAdapter) ResponsesToChat(r *protocolopenai.ResponsesRequest) (*protocolopenai.ChatCompletionsRequest, error) {
	return apicompat.ResponsesToChatCompletionsRequestWithOptions(r, &apicompat.ResponsesToChatOptions{ReasoningContentByID: p.s.reasoningContentByID})
}
func (p *openAIChatExecutionAdapter) GrokBridgeEligible(body []byte) (bool, string) {
	return grokChatResponsesBridgeEligibility(body)
}
func (p *openAIChatExecutionAdapter) ChatError(status int, kind, message string) {
	MarkResponseCommitted(p.c)
	gatewayhttp.WriteOpenAIForwardRejection(p.c, status, kind, message, "")
}
func (p *openAIChatExecutionAdapter) DefaultChat() bool {
	return resolveOpenAITextProtocolForAttempt(p.c, p.account, openai_compat.TextProtocolChatCompletions) == openai_compat.TextProtocolChatCompletions
}
func (p *openAIChatExecutionAdapter) DeriveCacheKey(r *protocolopenai.ChatCompletionsRequest, model string) string {
	return deriveCompatPromptCacheKey(r, model)
}
func (p *openAIChatExecutionAdapter) IsolateCacheKey(key string) string {
	return isolateOpenAISessionID(getAPIKeyIDFromContext(p.c), key)
}
func (p *openAIChatExecutionAdapter) NormalizeBodyTier(body []byte) ([]byte, string, error) {
	return normalizeResponsesBodyServiceTier(body)
}
func (p *openAIChatExecutionAdapter) ChatToResponses(r *protocolopenai.ChatCompletionsRequest) (*protocolopenai.ResponsesRequest, error) {
	return apicompat.ChatCompletionsToResponses(r)
}
func (p *openAIChatExecutionAdapter) NormalizeRequestTier(r *protocolopenai.ResponsesRequest) {
	normalizeResponsesRequestServiceTier(r)
}
func (p *openAIChatExecutionAdapter) ApplyChatFast(ctx context.Context, model string, body []byte) ([]byte, error) {
	updated, err := p.s.applyOpenAIFastPolicyToBody(ctx, p.account, model, body)
	var blocked *OpenAIFastBlockedError
	if errors.As(err, &blocked) {
		p.PolicyDenied()
		p.ChatError(403, "permission_error", blocked.Message)
	}
	return updated, err
}
func (p *openAIChatExecutionAdapter) EffectiveEffort(body, original []byte, models ...string) *string {
	return extractEffectiveOpenAIReasoningEffortFromBody(body, original, models...)
}
func (p *openAIChatExecutionAdapter) ThinkingFallback(effort *string, body []byte, model string) *string {
	return ApplyThinkingEnabledFallback(effort, body, model)
}
func (p *openAIChatExecutionAdapter) AccessToken(ctx context.Context) (string, error) {
	token, _, err := p.s.GetAccessToken(ctx, p.account)
	return token, err
}
func (p *openAIChatExecutionAdapter) BuildChat(ctx context.Context, body []byte, token, key string) (*http.Request, error) {
	return p.s.buildUpstreamRequest(ctx, p.c, p.account, body, token, true, key, false, p.tls...)
}
func (p *openAIChatExecutionAdapter) UpstreamSessionKey(id int64, key string) string {
	return isolateOpenAIUpstreamSessionID(id, codexAccountIdentitySource(p.c, p.account), key)
}
func (p *openAIChatExecutionAdapter) SessionUUID(key string) string {
	return generateSessionUUID(key)
}
func (p *openAIChatExecutionAdapter) ChatErrorResponse(r *http.Response, model string) (*forward.Result, error) {
	_, err := p.s.handleChatCompletionsErrorResponse(r, p.c, p.account, model)
	return nil, err
}
func (p *openAIChatExecutionAdapter) ChatResponseOptions(r *http.Response, original, billing, model string) native.ChatResponseOptions {
	return p.s.nativeChatResponseOptions(p.c, p.account, r, original, billing, model)
}
