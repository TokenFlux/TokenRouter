// Messages 旧入口只绑定账号/会话及原生转换端口，不拥有请求恢复循环。
package service

import (
	"context"
	"errors"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"net/http"
	"strings"
)

type openAIMessagesExecutionAdapter struct {
	proxyURL string
	s        *OpenAIGatewayService
	c        *gin.Context
	account  *Account
	tls      []TLSFingerprintRouterMatchResult
}

func (p *openAIMessagesExecutionAdapter) Prepare(ctx context.Context) (forward.MessagesProfile, forward.Dispatch, error) {
	account, err := accountForProtocolAttempt(ctx, p.account)
	if err != nil {
		return forward.MessagesProfile{}, forward.DispatchResponses, err
	}
	p.account = account
	beginUpstreamResponseModelObservation(p.c)
	ClearActualOpenAIUpstreamEndpoint(p.c)
	if shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
		SetActualOpenAIUpstreamEndpoint(p.c, "/v1/chat/completions")
	}
	setCodexToolNameReverse(p.c, nil)
	if _, err := p.s.prepareCodexAccountIdentitySource(ctx, p.c, account); err != nil {
		return forward.MessagesProfile{}, forward.DispatchResponses, err
	}
	profile := forward.MessagesProfile{Profile: openAIForwardProfile(account), ID: account.ID, GrokOAuth: account.IsGrokOAuth(), Shadow: account.IsShadow(), ContinuationSupported: openai_compat.ResolveResponsesContinuationSupported(account.Extra)}
	route := forward.DispatchResponses
	if account.IsAnthropicProtocol() || account.IsAdaptiveAPIProtocol() {
		route = forward.DispatchAnthropic
	} else if shouldForwardOpenAIResponsesViaRawChatCompletions(account) || (!account.IsCNProvider() && resolveOpenAITextProtocolForAttempt(p.c, account, openai_compat.TextProtocolResponses) == openai_compat.TextProtocolChatCompletions) {
		route = forward.DispatchRawChat
	}
	return profile, route, nil
}
func (p *openAIMessagesExecutionAdapter) Dispatch(ctx context.Context, route forward.Dispatch, body []byte, key, model string) (*forward.Result, error) {
	var r *OpenAIForwardResult
	var err error
	if route == forward.DispatchAnthropic {
		r, err = p.s.forwardAnthropicViaNativeAnthropicEndpoint(ctx, p.c, p.account, body, model)
	} else {
		r, err = p.s.forwardAnthropicViaRawChatCompletions(ctx, p.c, p.account, body, model, p.tls...)
	}
	return openAIHTTPResultFromForward(r), err
}
func (p *openAIMessagesExecutionAdapter) ValidateEffort(body []byte, model string) error {
	return validateOpenAIReasoningEffort(body, model)
}
func (p *openAIMessagesExecutionAdapter) Error(status int, kind, message string) {
	gatewayhttp.WriteAnthropicError(p.c, status, kind, "", message)
}
func (p *openAIMessagesExecutionAdapter) CloneDigest(r *protocolanthropic.AnthropicRequest) *protocolanthropic.AnthropicRequest {
	return cloneAnthropicRequestForDigest(r)
}
func (p *openAIMessagesExecutionAdapter) NormalizeModel(r *protocolanthropic.AnthropicRequest) {
	applyOpenAICompatModelNormalization(r)
}
func (p *openAIMessagesExecutionAdapter) BillingModel(model, fallback string) string {
	return resolveOpenAIForwardModel(p.account, model, fallback)
}
func (p *openAIMessagesExecutionAdapter) UpstreamModel(model string) string {
	return normalizeOpenAIModelForUpstream(p.account, model)
}
func (p *openAIMessagesExecutionAdapter) APIKeyID() int64 {
	return getAPIKeyIDFromContext(p.c)
}
func (p *openAIMessagesExecutionAdapter) ClaudeSession(body []byte) string {
	return extractClaudeCodeSessionID(p.c, body)
}
func (p *openAIMessagesExecutionAdapter) MetadataSession(r *protocolanthropic.AnthropicRequest) string {
	return promptCacheKeyFromAnthropicMetadataSession(r)
}
func (p *openAIMessagesExecutionAdapter) AutoCacheKey(model string) bool {
	return shouldAutoInjectPromptCacheKeyForCompat(model)
}
func (p *openAIMessagesExecutionAdapter) CacheControlKey(r *protocolanthropic.AnthropicRequest) string {
	return deriveAnthropicCacheControlPromptCacheKey(r)
}
func (p *openAIMessagesExecutionAdapter) DigestChain(r *protocolanthropic.AnthropicRequest) string {
	return buildOpenAICompatAnthropicDigestChain(r)
}
func (p *openAIMessagesExecutionAdapter) FindDigestKey(key int64, chain string) (string, string) {
	return p.s.findOpenAICompatAnthropicDigestPromptCacheKey(p.account, key, chain)
}
func (p *openAIMessagesExecutionAdapter) DigestKey(chain string) string {
	return promptCacheKeyFromAnthropicDigest(chain)
}
func (p *openAIMessagesExecutionAdapter) ContinuationEnabled(model string) bool {
	return openAICompatContinuationEnabled(p.account, model)
}
func (p *openAIMessagesExecutionAdapter) ResponseID(ctx context.Context, key string) string {
	return p.s.getOpenAICompatSessionResponseID(ctx, p.c, p.account, key)
}
func (p *openAIMessagesExecutionAdapter) ContinuationDisabled(ctx context.Context, key string) bool {
	return p.s.isOpenAICompatSessionContinuationDisabled(ctx, p.c, p.account, key)
}
func (p *openAIMessagesExecutionAdapter) ReplayGuard(r *protocolanthropic.AnthropicRequest) bool {
	return applyAnthropicCompatFullReplayGuard(r)
}
func (p *openAIMessagesExecutionAdapter) Convert(r *protocolanthropic.AnthropicRequest) (*protocolopenai.ResponsesRequest, error) {
	return apicompat.AnthropicToResponses(r)
}
func (p *openAIMessagesExecutionAdapter) BetaFast() bool {
	return containsBetaToken(p.c.GetHeader("anthropic-beta"), claude.BetaFastMode)
}
func (p *openAIMessagesExecutionAdapter) MessagesEffort(r *protocolanthropic.AnthropicRequest, model, effort string) string {
	return openAICompatAnthropicReasoningEffort(r, model, effort)
}
func (p *openAIMessagesExecutionAdapter) TrimLatestTurn(r *protocolopenai.ResponsesRequest) {
	trimAnthropicCompatResponsesInputToLatestTurn(r)
}
func (p *openAIMessagesExecutionAdapter) TodoGuard(r *protocolopenai.ResponsesRequest) {
	appendOpenAICompatClaudeCodeTodoGuard(r)
}
func (p *openAIMessagesExecutionAdapter) HashForLog(s string) string {
	return hashSensitiveValueForLog(s)
}
func (p *openAIMessagesExecutionAdapter) Truncate(s string, limit int) string {
	return truncateOpenAIWSLogValue(s, limit)
}
func (p *openAIMessagesExecutionAdapter) LogIDLimit() int {
	return openAIWSIDValueMaxLen
}
func (p *openAIMessagesExecutionAdapter) Debug(msg string, fields ...zap.Field) {
	logger.L().Debug(msg, fields...)
}
func (p *openAIMessagesExecutionAdapter) Info(msg string, fields ...zap.Field) {
	logger.L().Info(msg, fields...)
}
func (p *openAIMessagesExecutionAdapter) CodexTransform(body map[string]any, o native.CodexOAuthTransformOptions) native.CodexTransformResult {
	return applyCodexOAuthTransformWithOptions(body, o)
}
func (p *openAIMessagesExecutionAdapter) ToolNameReverse(value map[string]string) {
	setCodexToolNameReverse(p.c, value)
}
func (p *openAIMessagesExecutionAdapter) ForcedTemplate() string {
	if p.s.cfg == nil {
		return ""
	}
	return p.s.cfg.Gateway.ForcedCodexInstructionsTemplate
}
func (p *openAIMessagesExecutionAdapter) ForcedInstructions(body map[string]any, text string, data forward.TemplateData) (bool, error) {
	return applyForcedCodexInstructionsTemplate(body, text, forcedCodexInstructionsTemplateData(data))
}
func (p *openAIMessagesExecutionAdapter) EnsureInstructions(body map[string]any) {
	ensureCodexOAuthInstructionsField(body)
}
func (p *openAIMessagesExecutionAdapter) TodoGuardBody(body map[string]any) {
	appendOpenAICompatClaudeCodeTodoGuardToRequestBody(body)
}
func (p *openAIMessagesExecutionAdapter) AccountIdentity(body map[string]any, key int64) {
	applyCodexAccountIdentityClientMetadataMap(body, codexAccountIdentitySource(p.c, p.account), key)
}
func (p *openAIMessagesExecutionAdapter) TurnState(ctx context.Context, key string) string {
	return p.s.getOpenAICompatSessionTurnState(ctx, p.c, p.account, key)
}
func (p *openAIMessagesExecutionAdapter) ApplyEffort(ctx context.Context, body []byte) ([]byte, bool, error) {
	updated, changed, err := ApplyOpenAIReasoningEffortPolicyFromContext(ctx, body)
	var limited *ReasoningEffortOverLimitError
	if errors.As(err, &limited) {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		p.Error(403, "forbidden_error", limited.Error())
	}
	return updated, changed, err
}
func (p *openAIMessagesExecutionAdapter) ApplyFast(ctx context.Context, model string, body []byte) ([]byte, error) {
	updated, err := p.s.applyOpenAIFastPolicyToBody(ctx, p.account, model, body)
	var blocked *OpenAIFastBlockedError
	if errors.As(err, &blocked) {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		p.Error(403, "forbidden_error", blocked.Message)
	}
	return updated, err
}
func (p *openAIMessagesExecutionAdapter) ServiceTier(body []byte) *string {
	return extractOpenAIServiceTierFromBody(body)
}
func (p *openAIMessagesExecutionAdapter) ResolvedServiceTier(tier *string) *string {
	return resolvedOpenAIUpstreamServiceTier(p.c, tier)
}
func (p *openAIMessagesExecutionAdapter) GrokCacheIdentity(body []byte, key, model string) string {
	return resolveGrokCacheIdentity(p.c, body, key, model)
}
func (p *openAIMessagesExecutionAdapter) PatchGrokBody(body []byte, model string) ([]byte, error) {
	return patchGrokResponsesBody(body, model)
}
func (p *openAIMessagesExecutionAdapter) ApplyGrokCache(body, intent []byte, key string, oauth bool) ([]byte, error) {
	return applyGrokResponsesCacheIdentity(body, intent, key, oauth)
}
func (p *openAIMessagesExecutionAdapter) GrokFreeToolRoute(body, intent []byte, key string) ([]byte, error) {
	return applyGrokFreeMessagesFunctionToolCacheRoute(body, intent, p.account, key)
}
func (p *openAIMessagesExecutionAdapter) Credential(ctx context.Context) (string, error) {
	token, _, err := p.s.getRequestCredential(ctx, p.c, p.account)
	return token, err
}
func (p *openAIMessagesExecutionAdapter) BindMessagesBridge(v bool) {
	setOpenAICompatMessagesBridgeContext(p.c, v)
}
func (p *openAIMessagesExecutionAdapter) UpstreamContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return detachUpstreamContext(ctx)
}
func (p *openAIMessagesExecutionAdapter) Build(_ context.Context, ctx context.Context, body []byte, token string, stream bool, key, grokIdentity string) (*http.Request, error) {
	if p.account.Platform == PlatformGrok {
		return buildGrokResponsesRequest(ctx, p.c, p.account, body, token, grokIdentity, p.s.cfg, p.s.settingService)
	}
	return p.s.buildUpstreamRequest(ctx, p.c, p.account, body, token, stream, key, false, p.tls...)
}
func (p *openAIMessagesExecutionAdapter) IsolatedSessionID(key int64, cache string) string {
	return generateSessionUUID(isolateOpenAIUpstreamSessionID(key, codexAccountIdentitySource(p.c, p.account), cache))
}
func (p *openAIMessagesExecutionAdapter) RestoreIdentity(h http.Header) {
	ensureCodexIdentityHeaders(h)
	enforceCodexIdentityHeaders(h)
}
func (p *openAIMessagesExecutionAdapter) Header(key string) string {
	return p.c.GetHeader(key)
}
func (p *openAIMessagesExecutionAdapter) PrepareTransport() {
	p.proxyURL = ""
	if p.account.Proxy != nil {
		p.proxyURL = p.account.Proxy.URL()
	}
}
func (p *openAIMessagesExecutionAdapter) Send(r *http.Request) (*http.Response, error) {
	return p.s.httpUpstream.DoWithTLS(r, p.proxyURL, p.account.ID, p.account.Concurrency, p.s.resolveOpenAITLSProfile(p.account, p.tls...))
}

func (p *openAIMessagesExecutionAdapter) TransportError(ctx context.Context, err error) error {
	return p.s.handleOpenAIUpstreamTransportError(ctx, p.c, p.account, err, false)
}
func (p *openAIMessagesExecutionAdapter) ReadErrorBody(r *http.Response) []byte {
	return p.s.readUpstreamErrorBody(r)
}
func (p *openAIMessagesExecutionAdapter) GrokInvalidEncrypted(status int, body []byte) bool {
	return isGrokInvalidEncryptedContentResponse(status, body)
}
func (p *openAIMessagesExecutionAdapter) GrokHasEncrypted(body []byte) bool {
	return requestHasGrokEncryptedReasoning(body)
}
func (p *openAIMessagesExecutionAdapter) TrimGrokEncrypted(body []byte) ([]byte, bool, error) {
	return trimGrokInvalidEncryptedContentRetryBody(body)
}
func (p *openAIMessagesExecutionAdapter) ReadUpstreamError(r *http.Response) ([]byte, string) {
	return p.s.readOpenAIUpstreamError(r)
}
func (p *openAIMessagesExecutionAdapter) AgentRecoveryTried(ctx context.Context) bool {
	return agentIdentityTaskRecoveryWasTried(ctx)
}
func (p *openAIMessagesExecutionAdapter) IsAgentIdentity(ctx context.Context) bool {
	return p.s.isAgentIdentityAccount(ctx, p.account)
}
func (p *openAIMessagesExecutionAdapter) InvalidAgentTask(status int, body []byte) bool {
	return isAgentIdentityTaskInvalidHTTPResponse(status, body)
}
func (p *openAIMessagesExecutionAdapter) RecoverAgentTask(ctx context.Context) error {
	return p.s.recoverAgentIdentityTask(ctx, p.account, p.account.GetCredential("task_id"))
}
func (p *openAIMessagesExecutionAdapter) MarkAgentRecovery(ctx context.Context) context.Context {
	return markAgentIdentityTaskRecoveryTried(ctx)
}
func (p *openAIMessagesExecutionAdapter) RedactErrorBody(ctx context.Context, body []byte) []byte {
	return p.s.redactAgentIdentitySensitiveBody(ctx, p.account, body)
}
func (p *openAIMessagesExecutionAdapter) ErrorMessage(body []byte) string {
	return sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
}
func (p *openAIMessagesExecutionAdapter) PreviousMissing(status int, msg string, body []byte) bool {
	return isOpenAICompatPreviousResponseNotFound(status, msg, body)
}
func (p *openAIMessagesExecutionAdapter) PreviousUnsupported(status int, msg string, body []byte) bool {
	return isOpenAICompatPreviousResponseUnsupported(status, msg, body)
}
func (p *openAIMessagesExecutionAdapter) DisableContinuation(ctx context.Context, key string) {
	p.s.disableOpenAICompatSessionContinuation(ctx, p.c, p.account, key)
}
func (p *openAIMessagesExecutionAdapter) DeleteResponseID(ctx context.Context, key string) {
	p.s.deleteOpenAICompatSessionResponseID(ctx, p.c, p.account, key)
}
func (p *openAIMessagesExecutionAdapter) GrokStripRetried(ctx context.Context) bool {
	return grokEncryptedContentStripRetried(ctx)
}
func (p *openAIMessagesExecutionAdapter) StripThinkingSignatures(body []byte) ([]byte, bool) {
	return stripAnthropicThinkingSignatures(body)
}
func (p *openAIMessagesExecutionAdapter) MarkGrokStrip(ctx context.Context) context.Context {
	return markGrokEncryptedContentStripRetried(ctx)
}
func (p *openAIMessagesExecutionAdapter) FailoverHTTP(ctx context.Context, r *http.Response, body []byte, msg, model string) error {
	failure := p.s.failoverOpenAIUpstreamHTTPError(ctx, p.c, p.account, r, body, msg, model)
	if failure == nil {
		return nil
	}
	return failure
}
func (p *openAIMessagesExecutionAdapter) ErrorResponse(r *http.Response, model string) (*forward.Result, error) {
	_, err := p.s.handleAnthropicErrorResponse(r, p.c, p.account, model)
	return nil, err
}
func (p *openAIMessagesExecutionAdapter) UpdateGrokUsage(ctx context.Context, model string, h http.Header, status int) {
	p.s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, model), p.account, h, status)
}
func (p *openAIMessagesExecutionAdapter) BindTurnState(ctx context.Context, key, state string) {
	p.s.bindOpenAICompatSessionTurnState(ctx, p.c, p.account, key, state)
}
func (p *openAIMessagesExecutionAdapter) Sink() upstream.OutputSink {
	return gatewayhttp.ResponseSink{Writer: p.c.Writer}
}
func (p *openAIMessagesExecutionAdapter) ResponseOptions(r *http.Response, original, billing, model string) native.MessagesResponseOptions {
	return p.s.nativeMessagesResponseOptions(p.c, p.account, r, original, billing, model)
}
func (p *openAIMessagesExecutionAdapter) CyberPolicy() bool {
	return GetOpsCyberPolicy(p.c) != nil
}
func (p *openAIMessagesExecutionAdapter) CyberError() error {
	return errOpenAICyberPolicyForwarded
}
func (p *openAIMessagesExecutionAdapter) BindResponseID(ctx context.Context, key, id string) {
	p.s.bindOpenAICompatSessionResponseID(ctx, p.c, p.account, key, id)
}
func (p *openAIMessagesExecutionAdapter) BindDigestKey(id int64, chain, key, matched string) {
	p.s.bindOpenAICompatAnthropicDigestPromptCacheKey(p.account, id, chain, key, matched)
}
func (p *openAIMessagesExecutionAdapter) UpdateCodexUsage(ctx context.Context, h http.Header) {
	if snapshot := ParseCodexRateLimitHeaders(h); snapshot != nil {
		p.s.updateCodexUsageSnapshot(ctx, p.account.ID, snapshot)
	}
}
