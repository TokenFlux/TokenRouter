// Chat 旧装配复用同一账号/输出依赖，差异策略通过专属投影提供。
package httpapi

import (
	"context"
	"errors"

	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	tierpolicy "github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"

	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	"net/http"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type openAIChatExecutionAdapter struct {
	*openAIMessagesExecutionAdapter
}

func (p *openAIChatExecutionAdapter) PrepareChat(ctx context.Context) (openaiexecution.ChatProfile, error) {
	account, err := gatewayprovider.AccountForProtocolAttempt(ctx, p.account)
	if err != nil {
		return openaiexecution.ChatProfile{}, err
	}
	p.account = account
	BeginUpstreamResponseModelObservation(p.c)
	ClearActualOpenAIUpstreamEndpoint(p.c)
	if gatewayprovider.ExecutionModelPolicy(account).RawChat() {
		SetActualOpenAIUpstreamEndpoint(p.c, "/v1/chat/completions")
	}
	SetCodexToolNameReverse(p.c, nil)
	if _, err := PrepareCodexIdentity(ctx, p.c, p.s.Requests.Accounts, account); err != nil {
		return openaiexecution.ChatProfile{}, err
	}
	return openaiexecution.ChatProfile{MessagesProfile: openaiexecution.MessagesProfile{Profile: openAIForwardProfile(account), ID: account.Record.ID, GrokOAuth: account.View().IsGrokOAuth(), Shadow: account.View().IsShadow()}, Protocol: account.Route.Protocol(), Adaptive: gatewayprovider.ExecutionProtocolTarget(account).IsAdaptiveAPIProtocol(), SupportsNativeCN: account.View().SupportsNativeCNResponses(), CNProvider: account.View().IsCNProvider(), OpenAIAPIKey: account.View().IsOpenAIApiKey()}, nil
}
func (p *openAIChatExecutionAdapter) DispatchChat(ctx context.Context, route openaiexecution.Dispatch, body []byte, key, model string) (*openaiexecution.Result, error) {
	var r *protocolforward.OpenAIResult
	var err error
	switch route {
	case openaiexecution.DispatchAnthropic:
		r, err = p.s.NativeChat(ctx, p.c, p.account, body, model)
	case openaiexecution.DispatchRawChat:
		r, err = p.s.RawChat(ctx, p.c, p.account, body, model, p.tls...)
	case openaiexecution.DispatchGrok:
		var handled bool
		r, handled, err = p.s.Grok.ChatResponses(ctx, p.c, p.account, body, key, model, p.tls...)
		if !handled && err == nil {
			r, err = p.s.RawChat(ctx, p.c, p.account, body, model, p.tls...)
		}
	}
	return openaiexecution.FromForwardResult(r), err
}
func (p *openAIChatExecutionAdapter) ClientAllowed(ctx context.Context, body []byte) bool {
	var match egress.TLSFingerprintRouterMatchResult
	if len(p.tls) > 0 {
		match = p.tls[0]
	} else {
		match = p.s.Requests.MatchTLS(p.c, p.account)
	}
	restriction := p.s.Requests.DetectClient(p.c, p.account, match)
	LogCodexCLIOnlyDetection(ctx, p.c, p.account, APIKeyIDFromContext(p.c), restriction, body)
	return !restriction.Enabled || restriction.Matched
}
func (p *openAIChatExecutionAdapter) PolicyDenied() {
	MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
}
func (p *openAIChatExecutionAdapter) Reject(status int, kind, message string) {
	WriteOpenAIForwardRejection(p.c, status, kind, message, "")
}
func (p *openAIChatExecutionAdapter) ResponsesToChat(r *protocolopenai.ResponsesRequest) (*protocolopenai.ChatCompletionsRequest, error) {
	return protocolbridge.ResponsesToChatCompletionsRequestWithOptions(r, &protocolbridge.ResponsesToChatOptions{ReasoningContentByID: p.s.Output.Reasoning.Lookup})
}
func (p *openAIChatExecutionAdapter) GrokBridgeEligible(body []byte) (bool, string) {
	return gatewayprovider.GrokBodyCodec().GrokChatResponsesBridgeEligibility(body)
}
func (p *openAIChatExecutionAdapter) ChatError(status int, kind, message string) {
	MarkResponseCommitted(p.c)
	WriteOpenAIForwardRejection(p.c, status, kind, message, "")
}
func (p *openAIChatExecutionAdapter) DefaultChat() bool {
	return resolveOpenAITextProtocolForAttempt(p.c, p.account, accountcore.TextProtocolChatCompletions) == accountcore.TextProtocolChatCompletions
}
func (p *openAIChatExecutionAdapter) DeriveCacheKey(r *protocolopenai.ChatCompletionsRequest, model string) string {
	return gatewayprovider.DeriveCompatPromptCacheKey(r, model)
}
func (p *openAIChatExecutionAdapter) IsolateCacheKey(key string) string {
	return upstream.IsolateSessionID(APIKeyIDFromContext(p.c), key)
}
func (p *openAIChatExecutionAdapter) NormalizeBodyTier(body []byte) ([]byte, string, error) {
	return normalizeResponsesBodyServiceTier(body)
}
func (p *openAIChatExecutionAdapter) ChatToResponses(r *protocolopenai.ChatCompletionsRequest) (*protocolopenai.ResponsesRequest, error) {
	return protocolbridge.ChatCompletionsToResponses(r, protocolforward.ConversionOptionsForModel(r.Model))
}
func (p *openAIChatExecutionAdapter) NormalizeRequestTier(r *protocolopenai.ResponsesRequest) {
	normalizeResponsesRequestServiceTier(r)
}
func (p *openAIChatExecutionAdapter) ApplyChatFast(ctx context.Context, model string, body []byte) ([]byte, error) {
	updated, err := tierpolicy.ApplyBody(body, p.s.FastPolicy.Input(ctx, p.account, model))
	var blocked *tierpolicy.BlockedError
	if errors.As(err, &blocked) {
		p.PolicyDenied()
		p.ChatError(403, "permission_error", blocked.Message)
	}
	return updated, err
}
func (p *openAIChatExecutionAdapter) EffectiveEffort(body, original []byte, models ...string) *string {
	return requeststate.ExtractEffectiveOpenAIReasoningEffortFromBody(body, original, models...)
}
func (p *openAIChatExecutionAdapter) ThinkingFallback(effort *string, body []byte, model string) *string {
	return gatewayprovider.ApplyThinkingEnabledFallback(effort, body, model)
}
func (p *openAIChatExecutionAdapter) AccessToken(ctx context.Context) (string, error) {
	token, _, err := p.s.Requests.Credentials.Resolve(ctx, gatewayprovider.ExecutionRecord(p.account))
	return token, err
}
func (p *openAIChatExecutionAdapter) BuildChat(ctx context.Context, body []byte, token, key string) (*http.Request, error) {
	return p.s.Requests.Build(ctx, p.c, p.account, body, token, true, key, false, p.tls...)
}
func (p *openAIChatExecutionAdapter) UpstreamSessionKey(id int64, key string) string {
	return openai.IsolateOpenAIUpstreamSessionID(id, accountprovider.CodexIdentityNamespace(CodexIdentityRecord(p.c, p.account.View())), key)
}
func (p *openAIChatExecutionAdapter) SessionUUID(key string) string {
	return upstream.GenerateSessionUUID(key)
}
func (p *openAIChatExecutionAdapter) ChatErrorResponse(r *http.Response, model string) (*openaiexecution.Result, error) {
	_, err := p.s.chatError(r, p.c, p.account, model)
	return nil, err
}
func (p *openAIChatExecutionAdapter) ChatResponseOptions(r *http.Response, original, billing, model string) openai.ChatResponseOptions {
	return p.s.Output.ChatOptions(p.c, p.account, r, original, billing, model)
}
