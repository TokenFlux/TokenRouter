// Messages 旧入口只绑定账号/会话及原生转换端口，不拥有请求恢复循环。
package httpapi

import (
	"context"
	"errors"

	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	"net/http"
	"strings"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type openAIMessagesExecutionAdapter struct {
	proxyURL string
	s        *OpenAITextExecutor
	c        *gin.Context
	account  *gatewayprovider.ExecutionAccount
	tls      []egress.TLSFingerprintRouterMatchResult
}

func (p *openAIMessagesExecutionAdapter) Prepare(ctx context.Context) (openaiexecution.MessagesProfile, openaiexecution.Dispatch, error) {
	account, err := gatewayprovider.AccountForProtocolAttempt(ctx, p.account)
	if err != nil {
		return openaiexecution.MessagesProfile{}, openaiexecution.DispatchResponses, err
	}
	p.account = account
	BeginUpstreamResponseModelObservation(p.c)
	ClearActualOpenAIUpstreamEndpoint(p.c)
	if gatewayprovider.ExecutionModelPolicy(account).RawChat() {
		SetActualOpenAIUpstreamEndpoint(p.c, "/v1/chat/completions")
	}
	SetCodexToolNameReverse(p.c, nil)
	if _, err := PrepareCodexIdentity(ctx, p.c, p.s.Requests.Accounts, account); err != nil {
		return openaiexecution.MessagesProfile{}, openaiexecution.DispatchResponses, err
	}
	profile := openaiexecution.MessagesProfile{Profile: openAIForwardProfile(account), ID: account.Record.ID, GrokOAuth: account.View().IsGrokOAuth(), Shadow: account.View().IsShadow(), ContinuationSupported: accountcore.ResolveResponsesContinuationSupported(account.Record.Extra)}
	route := openaiexecution.DispatchResponses
	if gatewayprovider.ExecutionProtocolTarget(account).IsAnthropicProtocol() || gatewayprovider.ExecutionProtocolTarget(account).IsAdaptiveAPIProtocol() {
		route = openaiexecution.DispatchAnthropic
	} else if gatewayprovider.ExecutionModelPolicy(account).RawChat() || (!account.View().IsCNProvider() && resolveOpenAITextProtocolForAttempt(p.c, account, accountcore.TextProtocolResponses) == accountcore.TextProtocolChatCompletions) {
		route = openaiexecution.DispatchRawChat
	}
	return profile, route, nil
}
func (p *openAIMessagesExecutionAdapter) Dispatch(ctx context.Context, route openaiexecution.Dispatch, body []byte, key, model string) (*openaiexecution.Result, error) {
	var r *protocolforward.OpenAIResult
	var err error
	if route == openaiexecution.DispatchAnthropic {
		r, err = p.s.NativeMessages(ctx, p.c, p.account, body, model)
	} else {
		r, err = p.s.MessagesViaRawChat(ctx, p.c, p.account, body, model, p.tls...)
	}
	return openaiexecution.FromForwardResult(r), err
}
func (p *openAIMessagesExecutionAdapter) ValidateEffort(body []byte, model string) error {
	return requeststate.ValidateOpenAIReasoningEffort(body, model)
}
func (p *openAIMessagesExecutionAdapter) Error(status int, kind, message string) {
	WriteAnthropicError(p.c, status, kind, "", message)
}
func (p *openAIMessagesExecutionAdapter) CloneDigest(r *protocolanthropic.AnthropicRequest) *protocolanthropic.AnthropicRequest {
	return session.CloneAnthropicDigestRequest(r)
}
func (p *openAIMessagesExecutionAdapter) NormalizeModel(r *protocolanthropic.AnthropicRequest) {
	gatewayprovider.ApplyOpenAICompatModelNormalization(r)
}
func (p *openAIMessagesExecutionAdapter) BillingModel(model, fallback string) string {
	return gatewayprovider.ExecutionModelPolicy(p.account).ForwardModel(model, fallback)
}
func (p *openAIMessagesExecutionAdapter) UpstreamModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(p.account).NormalizeOpenAI(model)
}
func (p *openAIMessagesExecutionAdapter) APIKeyID() int64 {
	return APIKeyIDFromContext(p.c)
}
func (p *openAIMessagesExecutionAdapter) ClaudeSession(body []byte) string {
	return ExtractClaudeCodeSessionID(p.c, body)
}
func (p *openAIMessagesExecutionAdapter) MetadataSession(r *protocolanthropic.AnthropicRequest) string {
	return session.AnthropicMetadataPromptCacheKey(r)
}
func (p *openAIMessagesExecutionAdapter) AutoCacheKey(model string) bool {
	return gatewayprovider.ShouldAutoInjectPromptCacheKeyForCompat(model)
}
func (p *openAIMessagesExecutionAdapter) CacheControlKey(r *protocolanthropic.AnthropicRequest) string {
	return gatewayprovider.DeriveAnthropicCacheControlPromptCacheKey(r)
}
func (p *openAIMessagesExecutionAdapter) DigestChain(r *protocolanthropic.AnthropicRequest) string {
	return session.BuildAnthropicDigestChain(r)
}
func (p *openAIMessagesExecutionAdapter) FindDigestKey(key int64, chain string) (string, string) {
	var id int64
	if p.account != nil {
		id = p.account.Record.ID
	}
	return p.s.PromptCache.Find(id, key, chain)
}
func (p *openAIMessagesExecutionAdapter) DigestKey(chain string) string {
	return session.AnthropicDigestPromptCacheKey(chain)
}
func (p *openAIMessagesExecutionAdapter) ContinuationEnabled(model string) bool {
	return gatewayprovider.CompatContinuationEnabled(p.account, model)
}
func (p *openAIMessagesExecutionAdapter) ResponseID(ctx context.Context, key string) string {
	return p.s.Continuation.Response(compatResponseKey(p.c, p.account, key))
}
func (p *openAIMessagesExecutionAdapter) ContinuationDisabled(ctx context.Context, key string) bool {
	return p.s.Continuation.Disabled(compatResponseKey(p.c, p.account, key))
}
func (p *openAIMessagesExecutionAdapter) ReplayGuard(r *protocolanthropic.AnthropicRequest) bool {
	return protocolbridge.ApplyAnthropicCompatFullReplayGuard(r)
}
func (p *openAIMessagesExecutionAdapter) Convert(r *protocolanthropic.AnthropicRequest) (*protocolopenai.ResponsesRequest, error) {
	return protocolbridge.AnthropicToResponses(r, protocolforward.ConversionOptionsForModel(r.Model))
}
func (p *openAIMessagesExecutionAdapter) BetaFast() bool {
	return claude.ContainsBetaToken(p.c.GetHeader("anthropic-beta"), claude.BetaFastMode)
}
func (p *openAIMessagesExecutionAdapter) MessagesEffort(r *protocolanthropic.AnthropicRequest, model, effort string) string {
	return gatewayprovider.OpenAICompatAnthropicReasoningEffort(r, model, effort)
}
func (p *openAIMessagesExecutionAdapter) TrimLatestTurn(r *protocolopenai.ResponsesRequest) {
	protocolbridge.TrimCompatResponsesInputToLatestTurn(r)
}
func (p *openAIMessagesExecutionAdapter) TodoGuard(r *protocolopenai.ResponsesRequest) {
	gatewayprovider.AppendOpenAICompatClaudeCodeTodoGuard(r)
}
func (p *openAIMessagesExecutionAdapter) HashForLog(s string) string {
	return upstream.HashSensitiveValueForLog(s)
}
func (p *openAIMessagesExecutionAdapter) Truncate(s string, limit int) string {
	return gatewayprovider.TruncateOpenAIWSLogValue(s, limit)
}
func (p *openAIMessagesExecutionAdapter) LogIDLimit() int {
	return gatewayprovider.OpenAIWSIDValueMaxLen
}
func (p *openAIMessagesExecutionAdapter) Debug(msg string, fields ...zap.Field) {
	logging.L().Debug(msg, fields...)
}
func (p *openAIMessagesExecutionAdapter) Info(msg string, fields ...zap.Field) {
	logging.L().Info(msg, fields...)
}
func (p *openAIMessagesExecutionAdapter) CodexTransform(body map[string]any, o openai.CodexOAuthTransformOptions) openai.CodexTransformResult {
	return gatewayprovider.ApplyCodexOAuthTransformWithOptions(body, o)
}
func (p *openAIMessagesExecutionAdapter) ToolNameReverse(value map[string]string) {
	SetCodexToolNameReverse(p.c, value)
}
func (p *openAIMessagesExecutionAdapter) ForcedTemplate() string {
	return p.s.ForcedTemplate
}
func (p *openAIMessagesExecutionAdapter) ForcedInstructions(body map[string]any, text string, data openaiexecution.TemplateData) (bool, error) {
	return applyForcedCodexInstructionsTemplate(body, text, forcedCodexInstructionsTemplateData(data))
}
func (p *openAIMessagesExecutionAdapter) EnsureInstructions(body map[string]any) {
	protocolopenai.EnsureCodexInstructionsField(body)
}
func (p *openAIMessagesExecutionAdapter) TodoGuardBody(body map[string]any) {
	gatewayprovider.AppendOpenAICompatClaudeCodeTodoGuardToRequestBody(body)
}
func (p *openAIMessagesExecutionAdapter) AccountIdentity(body map[string]any, key int64) {
	openai.ApplyCodexAccountIdentityClientMetadataMap(body, accountprovider.CodexIdentityNamespace(CodexIdentityRecord(p.c, p.account.View())), key)
}
func (p *openAIMessagesExecutionAdapter) TurnState(ctx context.Context, key string) string {
	return p.s.Continuation.TurnState(compatResponseKey(p.c, p.account, key))
}
func (p *openAIMessagesExecutionAdapter) ApplyEffort(ctx context.Context, body []byte) ([]byte, bool, error) {
	updated, changed, err := requeststate.ApplyOpenAIReasoningEffortPolicyFromContext(ctx, body)
	var limited *routing.ReasoningEffortOverLimitError
	if errors.As(err, &limited) {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		p.Error(403, "forbidden_error", limited.Error())
	}
	return updated, changed, err
}
func (p *openAIMessagesExecutionAdapter) ApplyFast(ctx context.Context, model string, body []byte) ([]byte, error) {
	updated, err := tierpolicy.ApplyBody(body, p.s.FastPolicy.Input(ctx, p.account, model))
	var blocked *tierpolicy.BlockedError
	if errors.As(err, &blocked) {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		p.Error(403, "forbidden_error", blocked.Message)
	}
	return updated, err
}
func (p *openAIMessagesExecutionAdapter) ServiceTier(body []byte) *string {
	return requeststate.ExtractOpenAIServiceTierFromBody(body)
}
func (p *openAIMessagesExecutionAdapter) ResolvedServiceTier(tier *string) *string {
	return ResolvedOpenAIUpstreamServiceTier(p.c, tier)
}
func (p *openAIMessagesExecutionAdapter) GrokCacheIdentity(body []byte, key, model string) string {
	return ResolveGrokCacheIdentity(p.c, body, key, model)
}
func (p *openAIMessagesExecutionAdapter) PatchGrokBody(body []byte, model string) ([]byte, error) {
	return gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, model)
}
func (p *openAIMessagesExecutionAdapter) ApplyGrokCache(body, intent []byte, key string, oauth bool) ([]byte, error) {
	return grok.ApplyGrokResponsesCacheIdentity(body, intent, key, oauth)
}
func (p *openAIMessagesExecutionAdapter) GrokFreeToolRoute(body, intent []byte, key string) ([]byte, error) {
	return gatewayprovider.ApplyGrokFreeMessagesFunctionToolCacheRoute(body, intent, p.account, key)
}
func (p *openAIMessagesExecutionAdapter) Credential(ctx context.Context) (string, error) {
	token, _, err := p.s.Credentials.Resolve(ctx, RequestCredentialBudget(p.c), CredentialObserver{Context: p.c}, p.account)
	return token, err
}
func (p *openAIMessagesExecutionAdapter) BindMessagesBridge(v bool) {
	SetOpenAICompatMessagesBridgeContext(p.c, v)
}
func (p *openAIMessagesExecutionAdapter) UpstreamContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return gatewayprovider.DetachUpstreamContext(ctx)
}
func (p *openAIMessagesExecutionAdapter) Build(_ context.Context, ctx context.Context, body []byte, token string, stream bool, key, grokIdentity string) (*http.Request, error) {
	if p.account.Record.Platform == capability.PlatformGrok {
		return p.s.Grok.BuildResponsesRequest(ctx, p.c, p.account, body, token, grokIdentity, true)
	}
	return p.s.Requests.Build(ctx, p.c, p.account, body, token, stream, key, false, p.tls...)
}
func (p *openAIMessagesExecutionAdapter) IsolatedSessionID(key int64, cache string) string {
	return upstream.GenerateSessionUUID(openai.IsolateOpenAIUpstreamSessionID(key, accountprovider.CodexIdentityNamespace(CodexIdentityRecord(p.c, p.account.View())), cache))
}
func (p *openAIMessagesExecutionAdapter) RestoreIdentity(h http.Header) {
	openai.EnsureCodexIdentityHeaders(h)
	openai.EnforceCodexIdentityHeaders(h)
}
func (p *openAIMessagesExecutionAdapter) Header(key string) string {
	return p.c.GetHeader(key)
}
func (p *openAIMessagesExecutionAdapter) PrepareTransport() {
	p.proxyURL = ""
	if p.account.Record.Proxy != nil {
		p.proxyURL = p.account.Record.Proxy.URL()
	}
}
func (p *openAIMessagesExecutionAdapter) Send(r *http.Request) (*http.Response, error) {
	return p.s.Requests.Transport.DoWithTLS(r, p.proxyURL, p.account.Record.ID, p.account.Record.Concurrency, p.s.Requests.TLSProfile(p.account, p.tls...))
}

func (p *openAIMessagesExecutionAdapter) TransportError(ctx context.Context, err error) error {
	return p.s.Requests.Failure.Handle(ctx, p.c, p.account, err, false)
}
func (p *openAIMessagesExecutionAdapter) ReadErrorBody(r *http.Response) []byte {
	return p.s.Output.ReadErrorBody(r)
}
func (p *openAIMessagesExecutionAdapter) GrokInvalidEncrypted(status int, body []byte) bool {
	return gatewayprovider.GrokBodyCodec().IsGrokInvalidEncryptedContentResponse(status, body)
}
func (p *openAIMessagesExecutionAdapter) GrokHasEncrypted(body []byte) bool {
	return gatewayprovider.GrokBodyCodec().RequestHasGrokEncryptedReasoning(body)
}
func (p *openAIMessagesExecutionAdapter) TrimGrokEncrypted(body []byte) ([]byte, bool, error) {
	return gatewayprovider.GrokBodyCodec().TrimGrokInvalidEncryptedContentRetryBody(body)
}
func (p *openAIMessagesExecutionAdapter) ReadUpstreamError(r *http.Response) ([]byte, string) {
	return p.s.Output.ReadReplayableError(r)
}
func (p *openAIMessagesExecutionAdapter) AgentRecoveryTried(ctx context.Context) bool {
	return requeststate.AgentTaskRecoveryTried(ctx)
}
func (p *openAIMessagesExecutionAdapter) IsAgentIdentity(ctx context.Context) bool {
	return p.s.Requests.Identity.UsesAgentIdentity(ctx, p.account)
}
func (p *openAIMessagesExecutionAdapter) InvalidAgentTask(status int, body []byte) bool {
	return openai.IsAgentTaskInvalidHTTPResponse(status, body)
}
func (p *openAIMessagesExecutionAdapter) RecoverAgentTask(ctx context.Context) error {
	return p.s.Requests.Identity.Recover(ctx, p.account, p.account.View().GetCredential("task_id"))
}
func (p *openAIMessagesExecutionAdapter) MarkAgentRecovery(ctx context.Context) context.Context {
	return requeststate.WithAgentTaskRecovery(ctx)
}
func (p *openAIMessagesExecutionAdapter) RedactErrorBody(ctx context.Context, body []byte) []byte {
	return p.s.Requests.Identity.Redact(ctx, p.account, body)
}
func (p *openAIMessagesExecutionAdapter) ErrorMessage(body []byte) string {
	return logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
}
func (p *openAIMessagesExecutionAdapter) PreviousMissing(status int, msg string, body []byte) bool {
	return gatewayprovider.CompatPreviousResponseNotFound(status, msg, body)
}
func (p *openAIMessagesExecutionAdapter) PreviousUnsupported(status int, msg string, body []byte) bool {
	return gatewayprovider.CompatPreviousResponseUnsupported(status, msg, body)
}
func (p *openAIMessagesExecutionAdapter) DisableContinuation(ctx context.Context, key string) {
	p.s.Continuation.Disable(compatResponseKey(p.c, p.account, key))
}
func (p *openAIMessagesExecutionAdapter) DeleteResponseID(ctx context.Context, key string) {
	p.s.Continuation.DeleteResponse(compatResponseKey(p.c, p.account, key))
}
func (p *openAIMessagesExecutionAdapter) GrokStripRetried(ctx context.Context) bool {
	return gatewayprovider.GrokBodyCodec().GrokEncryptedContentStripRetried(ctx)
}
func (p *openAIMessagesExecutionAdapter) StripThinkingSignatures(body []byte) ([]byte, bool) {
	return protocolanthropic.StripThinkingSignaturesJSON(body)
}
func (p *openAIMessagesExecutionAdapter) MarkGrokStrip(ctx context.Context) context.Context {
	return gatewayprovider.GrokBodyCodec().MarkGrokEncryptedContentStripRetried(ctx)
}
func (p *openAIMessagesExecutionAdapter) FailoverHTTP(ctx context.Context, r *http.Response, body []byte, msg, model string) error {
	failure := p.s.httpFailover(ctx, p.c, p.account, r, body, msg, model)
	if failure == nil {
		return nil
	}
	return failure
}
func (p *openAIMessagesExecutionAdapter) ErrorResponse(r *http.Response, model string) (*openaiexecution.Result, error) {
	_, err := p.s.messagesError(r, p.c, p.account, model)
	return nil, err
}
func (p *openAIMessagesExecutionAdapter) UpdateGrokUsage(ctx context.Context, model string, h http.Header, status int) {
	p.s.Grok.Health.ObserveResponse(ctx, p.account.View(), h, status, model)
}
func (p *openAIMessagesExecutionAdapter) BindTurnState(ctx context.Context, key, state string) {
	p.s.Continuation.BindTurnState(compatResponseKey(p.c, p.account, key), state)
}
func (p *openAIMessagesExecutionAdapter) Sink() upstream.OutputSink {
	return ResponseSink{Writer: p.c.Writer}
}
func (p *openAIMessagesExecutionAdapter) ResponseOptions(r *http.Response, original, billing, model string) openai.MessagesResponseOptions {
	return p.s.Output.MessagesOptions(p.c, p.account, r, original, billing, model)
}
func (p *openAIMessagesExecutionAdapter) CyberPolicy() bool {
	return GetOpsCyberPolicy(p.c) != nil
}
func (p *openAIMessagesExecutionAdapter) CyberError() error {
	return protocolforward.ErrCyberPolicyForwarded
}
func (p *openAIMessagesExecutionAdapter) BindResponseID(ctx context.Context, key, id string) {
	p.s.Continuation.BindResponse(compatResponseKey(p.c, p.account, key), id)
}
func (p *openAIMessagesExecutionAdapter) BindDigestKey(id int64, chain, key, matched string) {
	if p.s == nil || p.account == nil {
		return
	}
	p.s.PromptCache.Bind(p.account.Record.ID, id, chain, key, matched, p.s.ResponseTTL())
}
func (p *openAIMessagesExecutionAdapter) UpdateCodexUsage(ctx context.Context, h http.Header) {
	if snapshot := openai.ParseCodexRateLimitHeaders(h); snapshot != nil {
		p.s.CodexUsage.Observe(ctx, p.account.Record.ID, snapshot)
	}
}
