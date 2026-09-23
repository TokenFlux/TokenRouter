// 旧透传装配只投影原账号能力和 HTTP 技术参数，不持有恢复循环。
package service

import (
	"context"
	"errors"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	provider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	tierpolicy "github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"go.uber.org/zap"
)

type openAIPassthroughExecutionAdapter struct {
	*openAIMessagesExecutionAdapter
}

func (p *openAIPassthroughExecutionAdapter) Profile() forward.MessagesProfile {
	return forward.MessagesProfile{Profile: openAIForwardProfile(p.account), ID: p.account.Record.ID, Shadow: p.account.View().IsShadow()}
}
func (p *openAIPassthroughExecutionAdapter) CompactPath() bool {
	return gatewayhttp.IsOpenAIResponsesCompactPath(p.c)
}
func (p *openAIPassthroughExecutionAdapter) CompactModel(model string) string {
	return p.s.resolveOpenAICompactFallbackModel(p.account, model)
}
func (p *openAIPassthroughExecutionAdapter) InstructionsRejection(model string, body []byte) string {
	return openai.DetectOpenAIPassthroughInstructionsRejectReason(model, body)
}
func (p *openAIPassthroughExecutionAdapter) PolicyDenied() {
	gatewayhttp.MarkOpsClientBusinessLimited(p.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
}
func (p *openAIPassthroughExecutionAdapter) LogInstructionsRejected(ctx context.Context, model, reason string, body []byte) {
	logOpenAIPassthroughInstructionsRejected(ctx, p.c, p.account, model, reason, body)
}
func (p *openAIPassthroughExecutionAdapter) Reject(status int, kind, message, param string) {
	gatewayhttp.WriteOpenAIForwardRejection(p.c, status, kind, message, param)
}
func (p *openAIPassthroughExecutionAdapter) CodexModel(model string) bool {
	return openai.IsOpenAICodexModel(model)
}
func (p *openAIPassthroughExecutionAdapter) OAuthBody(body []byte, compact bool) ([]byte, bool, error) {
	return openai.NormalizeOpenAIPassthroughOAuthBody(body, compact)
}
func (p *openAIPassthroughExecutionAdapter) AccountIdentityRaw(body []byte) ([]byte, bool, error) {
	return openai.ApplyCodexAccountIdentityClientMetadataRaw(body, accountprovider.CodexIdentityNamespace(gatewayhttp.CodexIdentityRecord(p.c, p.account.View())), gatewayhttp.APIKeyIDFromContext(p.c))
}
func (p *openAIPassthroughExecutionAdapter) StageFingerprint(ids *openai.FingerprintIDs) {
	gatewayhttp.StageCodexFingerprintIDs(p.c, ids)
}
func (p *openAIPassthroughExecutionAdapter) Fingerprint() *openai.FingerprintIDs {
	var headers http.Header
	if p.c != nil && p.c.Request != nil {
		headers = p.c.Request.Header
	}
	return accountprovider.CodexFingerprintIDsFromRequest(p.account.View(), headers)
}
func (p *openAIPassthroughExecutionAdapter) FingerprintBody(body []byte, ids *openai.FingerprintIDs) ([]byte, bool, error) {
	return openai.ApplyCodexFingerprintClientMetadataRaw(body, ids)
}
func (p *openAIPassthroughExecutionAdapter) HasContext() bool {
	return p.c != nil
}
func (p *openAIPassthroughExecutionAdapter) LiteHeader() bool {
	return provider.ImageIntent().IsOpenAIResponsesLiteHeader(p.c.GetHeader(media.ResponsesLiteHeader))
}
func (p *openAIPassthroughExecutionAdapter) LitePayloadFlag(body []byte) bool {
	return provider.ImageIntent().IsOpenAIResponsesLiteWebSocketPayload(body)
}
func (p *openAIPassthroughExecutionAdapter) CompatibilityBody(body []byte, lite bool) ([]byte, bool, error) {
	return provider.NormalizeOpenAIResponsesWebSocketCompatibilityBody(body, provider.ExecutionProtocolRecord(p.account), lite)
}
func (p *openAIPassthroughExecutionAdapter) ReservedToolNames(body []byte) ([]byte, map[string]string, bool, error) {
	return openai.AliasOpenAIOAuthReservedToolNamesBody(body)
}
func (p *openAIPassthroughExecutionAdapter) MergeToolNames(value map[string]string) {
	mergeCodexToolNameReverse(p.c, value)
}
func (p *openAIPassthroughExecutionAdapter) NeedsClientTools(body []byte) bool {
	return needsOpenAIResponsesClientToolAdaptation(body)
}
func (p *openAIPassthroughExecutionAdapter) AdaptClientTools(body []byte) ([]byte, error) {
	updated, mapping, err := adaptOpenAIResponsesClientTools(body)
	if err == nil {
		setOpenAIResponsesClientToolMapping(p.c, mapping)
	}
	return updated, err
}
func (p *openAIPassthroughExecutionAdapter) NormalizeLite(body []byte) ([]byte, bool, error) {
	return provider.NormalizeResponsesLiteForAccount(p.account.View(), body)
}
func (p *openAIPassthroughExecutionAdapter) ApplyFastPass(ctx context.Context, model string, body []byte) ([]byte, error) {
	updated, err := tierpolicy.ApplyBody(body, p.s.fastModeInput(ctx, p.account, model))
	var blocked *tierpolicy.BlockedError
	if errors.As(err, &blocked) {
		gatewayhttp.WriteFastPolicyBlockedResponse(p.c, blocked)
	}
	return updated, err
}
func (p *openAIPassthroughExecutionAdapter) ImageIntent(model string, canonical []byte, policy string, body []byte, invalidated bool) bool {
	return resolveOpenAIPassthroughImageIntent(p.c, model, canonical, policy, body, invalidated, provider.ImageIntent().IsImageGenerationIntent)
}
func (p *openAIPassthroughExecutionAdapter) ExplicitImageIntent(model string, body []byte) bool {
	return provider.ImageIntent().IsExplicitImageGenerationIntent(media.OpenAIResponsesEndpoint, model, body)
}
func (p *openAIPassthroughExecutionAdapter) ImageAllowed() bool {
	return GroupAllowsResponsesImages(apiKeyGroup(getAPIKeyFromContext(p.c)))
}
func (p *openAIPassthroughExecutionAdapter) FeatureDenied() {
	gatewayhttp.MarkOpsClientBusinessLimited(p.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalFeatureGate)
}
func (p *openAIPassthroughExecutionAdapter) ImagePermissionMessage() string {
	return media.ImageGenerationPermissionMessage
}
func (p *openAIPassthroughExecutionAdapter) ImageBilling(body []byte, model string) (forward.ImageBilling, error) {
	v, err := provider.ImageIntent().ResolveOpenAIResponsesImageBillingConfigDetailedFromBody(body, model)
	return forward.ImageBilling{Model: v.Model, SizeTier: v.SizeTier, InputSize: v.InputSize}, err
}
func (p *openAIPassthroughExecutionAdapter) UpstreamError(status int, message string) {
	gatewayhttp.SetOpsUpstreamError(p.c, status, message, "")
}
func (p *openAIPassthroughExecutionAdapter) Log(format string, args ...any) {
	logging.LegacyPrintf("service.openai_gateway", format, args...)
}
func (p *openAIPassthroughExecutionAdapter) WarnTimeoutHeaders(ctx context.Context) {
	if p.c == nil || p.c.Request == nil {
		return
	}
	if h := collectOpenAIPassthroughTimeoutHeaders(p.c.Request.Header); len(h) > 0 {
		log := logging.FromContext(ctx).With(zap.String("component", "service.openai_gateway"), zap.Int64("account_id", p.account.Record.ID), zap.Strings("timeout_headers", h))
		if p.s.isOpenAIPassthroughTimeoutHeadersAllowed() {
			log.Warn("OpenAI passthrough 透传请求包含超时相关请求头，且当前配置为放行，可能导致上游提前断流")
		} else {
			log.Warn("OpenAI passthrough 检测到超时相关请求头，将按配置过滤以降低断流风险")
		}
	}
}
func (p *openAIPassthroughExecutionAdapter) AccessToken(ctx context.Context) (string, error) {
	token, _, err := p.s.executionCredentials.Resolve(ctx, provider.ExecutionRecord(p.account))
	return token, err
}
func (p *openAIPassthroughExecutionAdapter) PrepareTransportPass() {
	p.proxyURL = ""
	if p.account.Record.ProxyID != nil && p.account.Record.Proxy != nil {
		p.proxyURL = p.account.Record.Proxy.URL()
	}
}
func (p *openAIPassthroughExecutionAdapter) MarkPassthrough() {
	if p.c != nil {
		p.c.Set("openai_passthrough", true)
	}
}
func (p *openAIPassthroughExecutionAdapter) RetryState(body []byte) *openai.ResponsesRejectedFieldRetryState {
	return openAIResponsesRejectedFieldRetryStateForRequest(p.c, body)
}
func (p *openAIPassthroughExecutionAdapter) UpstreamModelObserved(model string) {
	gatewayhttp.SetOpsUpstreamModel(p.c, model)
}
func (p *openAIPassthroughExecutionAdapter) BuildPass(ctx context.Context, body []byte, token string) (*http.Request, error) {
	return p.s.buildUpstreamRequestOpenAIPassthrough(ctx, p.c, p.account, body, token, p.tls...)
}
func (p *openAIPassthroughExecutionAdapter) SendPass(r *http.Request) (*http.Response, error) {
	return p.s.httpUpstream.DoWithTLS(r, p.proxyURL, p.account.Record.ID, p.account.Record.Concurrency, p.s.resolveOpenAITLSProfile(p.account, p.tls...))
}
func (p *openAIPassthroughExecutionAdapter) Latency(d time.Duration) {
	gatewayhttp.SetOpsLatencyMs(p.c, gatewayhttp.OpsUpstreamLatencyMsKey, d.Milliseconds())
}
func (p *openAIPassthroughExecutionAdapter) TransportErrorPass(ctx context.Context, err error) error {
	return p.s.handleOpenAIUpstreamTransportError(ctx, p.c, p.account, err, true)
}
func (p *openAIPassthroughExecutionAdapter) CompactRetry(model string, body []byte, status int, message string, payload []byte, tried bool) ([]byte, string, bool) {
	return p.s.prepareOpenAICompactFallbackRetry(p.c, p.account, model, body, status, message, payload, tried)
}
func (p *openAIPassthroughExecutionAdapter) CompactObserved(r *http.Response, body []byte, message string) {
	p.s.appendOpenAICompactFallbackRetryOps(p.c, p.account, r, body, message, true)
}
func (p *openAIPassthroughExecutionAdapter) ErrorCode(body []byte) string {
	return upstream.ExtractErrorCode(body)
}
func (p *openAIPassthroughExecutionAdapter) ShouldFailover(status int, body []byte) bool {
	return shouldFailoverOpenAIPassthroughResponse(p.account, status, body)
}
func (p *openAIPassthroughExecutionAdapter) FailoverError(ctx context.Context, r *http.Response, body, payload []byte) error {
	return p.s.handleFailoverErrorResponsePassthrough(ctx, r, p.c, p.account, body, payload)
}
func (p *openAIPassthroughExecutionAdapter) ErrorResponsePass(ctx context.Context, r *http.Response, body, payload []byte) error {
	return p.s.handleErrorResponsePassthrough(ctx, r, p.c, p.account, body, payload)
}
func (p *openAIPassthroughExecutionAdapter) WrapResponseBody(r *http.Response) {
	if mapping, ok := openAIResponsesClientToolMapping(p.c); ok && isEventStreamResponse(r.Header) {
		limit := defaultMaxLineSize
		if p.s.cfg != nil && p.s.cfg.Gateway.MaxLineSize > 0 {
			limit = p.s.cfg.Gateway.MaxLineSize
		}
		r.Body = upstream.NewResponsesClientToolStreamBody(r.Body, mapping, limit)
	}
}
func (p *openAIPassthroughExecutionAdapter) ObserveProvenance(h http.Header) {
	if extractOpenAICodexTurnState(h) != "" {
		p.s.noteOpenAICodexTurnStateProvenance(p.c, p.account)
	}
}
func (p *openAIPassthroughExecutionAdapter) ResponseOptions(ctx context.Context) openai.PassthroughOptions {
	return p.s.nativePassthroughOptions(ctx, p.c, p.account)
}
func (p *openAIPassthroughExecutionAdapter) CompactFromSignal(model string, body []byte, err error, tried bool, r *http.Response) ([]byte, string, bool) {
	return p.s.applyOpenAIPassthroughCompactFallbackFromSignal(p.c, p.account, model, body, err, tried, r)
}
func (p *openAIPassthroughExecutionAdapter) CompactSignal(err error) (forward.CompactFailure, bool) {
	v, ok := asOpenAICompactFallbackSignal(err)
	if !ok {
		return forward.CompactFailure{}, false
	}
	return forward.CompactFailure{Message: v.message, Payload: v.payload}, true
}
func (p *openAIPassthroughExecutionAdapter) CompactErrorResponse(r *http.Response, v forward.CompactFailure) (*http.Response, []byte) {
	return openAICompactFallbackErrorResponse(r, &openAICompactFallbackSignal{message: v.Message, payload: v.Payload})
}
func (p *openAIPassthroughExecutionAdapter) BindOwner(ctx context.Context, id string) {
	p.s.bindHTTPResponseAccount(ctx, p.c, p.account, id)
}
func (p *openAIPassthroughExecutionAdapter) ObservedServiceTier() string {
	return gatewayhttp.ObservedUpstreamResponseServiceTier(p.c)
}
