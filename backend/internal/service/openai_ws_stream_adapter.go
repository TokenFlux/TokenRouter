package service

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// wsStreamAdapter 投影单次帧解析和账号健康规则，不拥有读取循环、重试或完成时序。
type wsStreamAdapter struct {
	*wsPassthroughAdapter
	observer    *upstreamResponseModelObserver
	state       *gatewayws.IngressState
	groupID     int64
	writeClient func([]byte) error
}

func (p *wsStreamAdapter) BeginObservation() {
	p.observer = upstreamResponseModelObserverFromContext(p.request)
	if p.observer == nil {
		p.observer = beginUpstreamResponseModelObservation(p.request)
	}
}
func (p *wsStreamAdapter) ObserveModel(body []byte, event string) {
	p.observer.ObserveOpenAI(body, event)
}
func (p *wsStreamAdapter) ResponseTier() string { return p.observer.ServiceTier() }
func (p *wsStreamAdapter) ResolvedTier(body []byte) *string {
	return resolvedOpenAIUpstreamServiceTierFromObserver(p.observer, extractOpenAIServiceTierFromBody(body))
}
func (p *wsStreamAdapter) Reasoning(body []byte, mapped, original string) *string {
	return ApplyThinkingEnabledFallback(extractOpenAIReasoningEffortFromBody(body, mapped, original), body, mapped)
}
func (p *wsStreamAdapter) ImageCounter() gatewayws.ImageCounter { return newOpenAIImageOutputCounter() }
func (p *wsStreamAdapter) ReplayCollector() gatewayws.ReplayCollector {
	return &openAIWSToolCallReplayCollector{}
}
func (p *wsStreamAdapter) Streaming(body []byte) bool {
	return openAIWSPayloadBoolFromRaw(body, "stream", true)
}
func (p *wsStreamAdapter) StoreDisabled(body []byte) bool {
	return p.service.isOpenAIWSStoreDisabledInRequestRaw(body, p.account)
}
func (p *wsStreamAdapter) ClassifyPrevious(id string) string {
	return ClassifyOpenAIPreviousResponseIDKind(id)
}
func (p *wsStreamAdapter) HasToolOutput(body []byte) bool {
	return openAIWSRawPayloadHasToolCallOutput(body)
}
func (p *wsStreamAdapter) MappedModel(model string) string {
	return normalizeOpenAIModelForUpstream(p.account, resolveAccountMappedModelForForward(p.account, model))
}
func (p *wsStreamAdapter) Envelope(body []byte) (string, string, bool) {
	event, id, _ := parseOpenAIWSEventEnvelope(body)
	return event, id, false
}
func (p *wsStreamAdapter) ShouldParseUsage(event string) bool {
	return openAIWSEventShouldParseUsage(event)
}
func (p *wsStreamAdapter) ParseUsage(body []byte, usage *wire.ForwardUsage) {
	parseOpenAIWSResponseUsageFromCompletedEvent(body, usage)
}
func (p *wsStreamAdapter) MarkCyber(body []byte, usage *wire.ForwardUsage) {
	markOpenAICyberPolicyEvent(p.request, body, http.StatusOK, usage)
}
func (p *wsStreamAdapter) SchedulingModel(model string) string {
	return canonicalOpenAIAccountSchedulingModel(p.account, model)
}
func (p *wsStreamAdapter) ErrorDecision(ctx context.Context, model string, headers map[string][]string, body []byte) gatewayws.ErrorPolicy {
	decision := p.service.handleOpenAIWSErrorEventTransientFailure(ctx, p.account, model, headers, body)
	status := openAIWSErrorPolicyStatus(body)
	return gatewayws.ErrorPolicy{Generic: decision.ShouldReturnGenericError(), Failover: decision.ShouldFailoverWithDefaults(p.account, status, status == http.StatusTooManyRequests, p.service.shouldFailoverOpenAIWSError(p.account, status, body)), RetrySame: decision.RetryableOnSameAccount(p.account, status)}
}
func (p *wsStreamAdapter) TerminalDecision(ctx context.Context, model string, headers map[string][]string, body []byte) gatewayws.TerminalPolicy {
	policy := p.service.handleOpenAIWSTerminalTransientFailure(ctx, p.account, model, headers, body)
	d := policy.Decision
	return gatewayws.TerminalPolicy{TerminalEvent: policy.TerminalEvent, StatusCode: policy.StatusCode, Decision: gatewayws.ErrorPolicy{Generic: d.ShouldReturnGenericError(), Failover: d.ShouldFailoverWithDefaults(p.account, policy.StatusCode, false, p.service.shouldFailoverOpenAIWSError(p.account, policy.StatusCode, body)), RetrySame: d.RetryableOnSameAccount(p.account, policy.StatusCode)}}
}
func (p *wsStreamAdapter) ErrorFields(body []byte) (string, string, string) {
	return parseOpenAIWSErrorEventFields(body)
}
func (p *wsStreamAdapter) ErrorStatus(body []byte) int { return openAIWSErrorPolicyStatus(body) }
func (p *wsStreamAdapter) ClassifyError(code, kind, message string) (string, bool) {
	return classifyOpenAIWSErrorEventFromRaw(code, kind, message)
}
func (p *wsStreamAdapter) EncryptedDigests(body []byte) []string {
	return collectOpenAIEncryptedContentDigestsRaw(body)
}
func (p *wsStreamAdapter) MarkEncrypted(digests []string) {
	p.service.markOpenAIWSInvalidEncryptedContentLineage(p.groupID, p.state.SessionHash, digests)
}
func (p *wsStreamAdapter) SummarizeError(code, kind, message string) (string, string, string) {
	return summarizeOpenAIWSErrorEventFieldsFromRaw(code, kind, message)
}
func (p *wsStreamAdapter) GenericError(status int) error {
	return openAIWSGenericPolicyCloseError(status)
}
func (p *wsStreamAdapter) RawFailure(status int, headers map[string][]string, body []byte, retry bool) error {
	return &UpstreamFailoverError{StatusCode: status, ResponseHeaders: cloneHeader(headers), ResponseBody: append([]byte(nil), body...), RetryableOnSameAccount: retry}
}
func (p *wsStreamAdapter) Failure(status int, headers map[string][]string, body []byte, message string, retry bool) error {
	return newOpenAIUpstreamFailoverError(status, headers, body, message, retry)
}
func (p *wsStreamAdapter) IsToken(event string) bool  { return isOpenAIWSTokenEvent(event) }
func (p *wsStreamAdapter) Message(body []byte) string { return extractOpenAISSEErrorMessage(body) }
func (p *wsStreamAdapter) GenericEvent() []byte {
	return buildOpenAIWSHTTPBridgeErrorEvent(http.StatusInternalServerError, "Upstream gateway error")
}
func (p *wsStreamAdapter) MayContainTools(event string) bool {
	return openAIWSEventMayContainToolCalls(event)
}
func (p *wsStreamAdapter) LikelyTools(body []byte) bool {
	return openAIWSMessageLikelyContainsToolCalls(body)
}
func (p *wsStreamAdapter) CorrectTools(body []byte) ([]byte, bool) {
	return p.service.toolCorrector.CorrectToolCallsInSSEBytes(body)
}
func (p *wsStreamAdapter) CapacityShed(body []byte) ([]byte, bool) {
	return sanitizeOpenAICapacityShedErrorCodeForClient(body)
}
func (p *wsStreamAdapter) WriteClient(body []byte) error { return p.writeClient(body) }
func (p *wsStreamAdapter) IsDisconnect(err error) bool   { return isOpenAIWSClientDisconnectError(err) }
func (p *wsStreamAdapter) SummarizeClose(err error) (string, string) {
	return summarizeOpenAIWSReadCloseError(err)
}
func (p *wsStreamAdapter) NormalizeLog(value string) string { return normalizeOpenAIWSLogValue(value) }
func (p *wsStreamAdapter) Log(message string)               { logOpenAIWSModeInfo("%s", message) }
func (p *wsStreamAdapter) Debug(message string)             { logOpenAIWSModeDebug("%s", message) }

func (l *wsIngressLease) WriteRequest(ctx context.Context, body []byte, timeout time.Duration) error {
	return l.lease.WriteJSONWithContextTimeout(ctx, json.RawMessage(body), timeout)
}
func (l *wsIngressLease) ReadEvent(ctx context.Context, timeout time.Duration) ([]byte, error) {
	return l.lease.ReadMessageWithContextTimeout(ctx, timeout)
}
func (l *wsIngressLease) Headers() map[string][]string { return l.lease.HandshakeHeaders() }
