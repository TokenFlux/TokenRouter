package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/gin-gonic/gin"
)

// messageExecutionAdapter 仅投影旧装配的单步能力；准备和结果决定在 gateway/forward。
type messageExecutionAdapter struct {
	s                          *GatewayService
	c                          *gin.Context
	account                    *Account
	token, tokenType, proxyURL string
	profile                    *tlsfingerprint.Profile
	toolRewrite                *claude.ToolNameRewrite
	response                   *http.Response
}

func newMessageExecutionAdapter(s *GatewayService, c *gin.Context, account *Account) *messageExecutionAdapter {
	return &messageExecutionAdapter{s: s, c: c, account: account}
}
func (a *messageExecutionAdapter) input() forwardcore.MessageInput {
	in := forwardcore.MessageInput{AccountPresent: a.account != nil, HTTPPresent: a.c != nil}
	if v := a.account; v != nil {
		in.AccountID = v.ID
		in.AccountName = v.Name
		in.AccountType = v.Type
		in.Platform = v.Platform
		in.OAuth = v.IsOAuth()
		in.Passthrough = v.IsAnthropicAPIKeyPassthroughEnabled()
		in.Bedrock = v.IsBedrock()
	}
	if a.s.cfg != nil {
		in.LogErrorBody = a.s.cfg.Gateway.LogUpstreamErrorBody
		in.LogErrorBodyMaxBytes = a.s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		in.FailoverOn400 = a.s.cfg.Gateway.FailoverOn400
	}
	return in
}
func (a *messageExecutionAdapter) ShouldEmulate(ctx context.Context, group *int64, body []byte) bool {
	return a.s.shouldEmulateWebSearch(ctx, a.account, group, body)
}
func (a *messageExecutionAdapter) Emulate(ctx context.Context, p *requeststate.ParsedRequest) (*forwardcore.Result, error) {
	v, e := a.s.handleWebSearchEmulation(ctx, a.c, a.account, p)
	return nativeForwardExecutionResult(v), e
}
func (a *messageExecutionAdapter) MappedModel(model string) string {
	return a.account.GetMappedModel(model)
}
func (a *messageExecutionAdapter) ReplaceModel(body []byte, model string) []byte {
	return a.s.replaceModelInBody(body, model)
}
func (a *messageExecutionAdapter) Passthrough(ctx context.Context, in forwardcore.PassthroughInput) (*forwardcore.Result, error) {
	v, e := a.s.forwardAnthropicAPIKeyPassthroughWithInput(ctx, a.c, a.account, forwardcore.APIKeyInput{Body: in.Body, Parsed: in.Parsed, RequestModel: in.RequestModel, OriginalModel: in.OriginalModel, RequestStream: in.Stream, StartTime: in.StartedAt})
	return nativeForwardExecutionResult(v), e
}
func (a *messageExecutionAdapter) Bedrock(ctx context.Context, p *requeststate.ParsedRequest, start time.Time) (*forwardcore.Result, error) {
	v, e := a.s.forwardBedrock(ctx, a.c, a.account, p, start)
	return nativeForwardExecutionResult(v), e
}
func (a *messageExecutionAdapter) Begin() (func(), error) {
	if a.s.nativeAttemptActivity != nil {
		return a.s.nativeAttemptActivity()
	}
	return func() {}, nil
}
func (a *messageExecutionAdapter) Beta(ctx context.Context, model string) error {
	policy := a.s.evaluateBetaPolicy(ctx, a.c.GetHeader("anthropic-beta"), a.account, model)
	if policy.blockErr != nil {
		return policy.blockErr
	}
	set := policy.filterSet
	if set == nil {
		set = map[string]struct{}{}
	}
	a.c.Set(betaPolicyFilterSetKey, set)
	return nil
}
func (a *messageExecutionAdapter) DebugOriginal(body []byte, model string, stream bool) {
	a.s.debugLogGatewaySnapshot("CLIENT_ORIGINAL", a.c.Request.Header, body, map[string]string{"account": fmt.Sprintf("%d(%s)", a.account.ID, a.account.Name), "account_type": a.account.Type, "model": model, "stream": strconv.FormatBool(stream)})
}
func (a *messageExecutionAdapter) AccountMappedModel(model string) string {
	return resolveAccountMappedModelForForward(a.account, model)
}
func (a *messageExecutionAdapter) IsClaudeCode(ctx context.Context, body []byte, metadata string) bool {
	ua := ""
	if a.c != nil {
		ua = a.c.GetHeader("User-Agent")
	}
	return requeststate.IsClaudeCodeClient(ctx) || isClaudeCodeClient(ua, metadata) || claude.IsProxiedClaudeCodeRequest(body, metadata)
}
func (a *messageExecutionAdapter) SystemSettings(ctx context.Context) (bool, string, string) {
	return a.s.claudeOAuthSystemPromptInjectionSettings(ctx)
}
func (a *messageExecutionAdapter) RewriteSystem(body []byte, p *requeststate.ParsedRequest, prompt, blocks string) []byte {
	system, _ := p.SystemValue()
	return claude.RewriteSystemForNonClaudeCodeWithPromptBlocks(body, system, prompt, blocks)
}
func (a *messageExecutionAdapter) Metadata(ctx context.Context, p *requeststate.ParsedRequest) string {
	if a.s.identityService == nil || a.c == nil {
		return ""
	}
	fp, err := a.s.identityService.GetOrCreateFingerprint(ctx, a.account.ID, a.c.Request.Header)
	if err != nil || fp == nil {
		return ""
	}
	_, mimic, _ := a.s.settingService.Gateway.GetGatewayForwardingSettings(ctx)
	if mimic {
		return ""
	}
	return a.s.buildOAuthMetadataUserID(p, a.account, fp)
}
func (a *messageExecutionAdapter) NormalizeOAuth(body []byte, model string, o forwardcore.NormalizeOptions) ([]byte, string) {
	return claude.NormalizeClaudeOAuthRequestBody(body, model, claude.ClaudeOAuthNormalizeOptions{StripSystemCacheControl: o.StripSystemCacheControl, InjectMetadata: o.InjectMetadata, MetadataUserID: o.MetadataUserID})
}
func (a *messageExecutionAdapter) RewriteCache(ctx context.Context, body []byte) []byte {
	return a.s.rewriteMessageCacheControlIfEnabled(ctx, body)
}
func (a *messageExecutionAdapter) RewriteTools(body []byte) ([]byte, bool) {
	a.toolRewrite = claude.BuildToolNameRewriteFromBody(body)
	if a.toolRewrite == nil {
		return body, false
	}
	return claude.ApplyToolNameRewriteToBody(body, a.toolRewrite), true
}
func (a *messageExecutionAdapter) BindTools() {
	if a.c != nil {
		a.c.Set(toolNameRewriteKey, a.toolRewrite)
	}
}
func (a *messageExecutionAdapter) ToolsLast(body []byte) []byte {
	return claude.ApplyToolsLastCacheBreakpoint(body)
}
func (a *messageExecutionAdapter) NormalizeDateline(ctx context.Context, body []byte) ([]byte, bool) {
	return a.s.normalizeClientDatelineIfEnabled(ctx, a.account, body)
}
func (a *messageExecutionAdapter) CacheLimit(body []byte) []byte {
	return claude.EnforceCacheControlLimit(body)
}
func (a *messageExecutionAdapter) PlatformModel(model string) string {
	return resolveAnthropicAccountUpstreamModel(a.account, model)
}
func (a *messageExecutionAdapter) InjectTTL(ctx context.Context) bool {
	return a.s.shouldInjectAnthropicCacheTTL1h(ctx, a.account)
}
func (a *messageExecutionAdapter) CacheTTL(body []byte) []byte {
	return claude.InjectAnthropicCacheControlTTL1h(body)
}
func (a *messageExecutionAdapter) Credential(ctx context.Context) error {
	token, kind, e := a.s.GetAccessToken(ctx, a.account)
	a.token, a.tokenType = token, kind
	return e
}
func (a *messageExecutionAdapter) Transport() {
	if a.account.ProxyID != nil && a.account.Proxy != nil && (!a.account.IsCustomBaseURLEnabled() || a.account.GetCustomBaseURL() == "") {
		a.proxyURL = a.account.Proxy.URL()
	}
	a.profile = a.s.tlsFPProfileService.ResolveRequestTLS(accountTLSSelection(a.account, nil))
	logging.LegacyPrintf("service.gateway", "[Forward] Using account: ID=%d Name=%s Platform=%s Type=%s TLSFingerprint=%v Proxy=%s", a.account.ID, a.account.Name, a.account.Platform, a.account.Type, a.profile, a.proxyURL)
}
func (a *messageExecutionAdapter) FilterSearchHistory(body []byte, model string) []byte {
	return searchtools.FilterWebSearchHistoryBlocks(body, modelidentity.ResolveThinkingProtocol(model) == modelidentity.ThinkingProtocolPassbackRequired)
}
func (a *messageExecutionAdapter) FilterThinking(body []byte, model string) []byte {
	return FilterThinkingBlocks(body, model)
}
func (a *messageExecutionAdapter) PassbackThinking(model string) bool {
	return modelidentity.ResolveThinkingProtocol(model) == modelidentity.ThinkingProtocolPassbackRequired
}
func (a *messageExecutionAdapter) NormalizeThinking(body []byte, model string) ([]byte, bool) {
	return NormalizeChineseLLMThinking(body, model)
}
func (a *messageExecutionAdapter) ReadErrorBody() ([]byte, error) {
	return a.s.readUpstreamErrorBody(a.response)
}
func (a *messageExecutionAdapter) ResetErrorBody(body []byte) {
	_ = a.response.Body.Close()
	a.response.Body = io.NopCloser(bytes.NewReader(body))
}
func (a *messageExecutionAdapter) Health(ctx context.Context, mode string, status int, headers map[string][]string, body []byte, model string) forwardcore.ErrorDecision {
	var d UpstreamErrorDecision
	switch mode {
	case "retry":
		d = a.s.handleRetryExhaustedSideEffects(ctx, a.response, a.account, model)
	case "failover":
		d = a.s.handleFailoverSideEffects(ctx, a.response, a.account, model)
	case "failover_synthetic":
		resp := &http.Response{StatusCode: status, Header: http.Header(headers).Clone(), Body: io.NopCloser(bytes.NewReader(body))}
		d = a.s.handleFailoverSideEffects(ctx, resp, a.account, model)
		_ = resp.Body.Close()
	default:
		d = upstreamErrorDecisionWithoutPersistence(a.account, status)
		if mode == "persist" && a.s.rateLimitService != nil {
			d = a.s.rateLimitService.ApplyUpstreamError(ctx, a.account, status, headers, body, model)
		}
	}
	return forwardcore.ErrorDecision{
		Generic:          d.ShouldReturnGenericError(),
		Failover:         d.ShouldFailover(a.account, status, true),
		RetrySameAccount: d.RetryableOnSameAccount(a.account, status),
	}
}
func (a *messageExecutionAdapter) HandleError(ctx context.Context, model string, retry bool) (*forwardcore.Result, error) {
	var v *forwardcore.MessagesResult
	var e error
	if retry {
		v, e = a.s.handleRetryExhaustedError(ctx, a.response, a.c, a.account, model)
	} else {
		v, e = a.s.handleErrorResponse(ctx, a.response, a.c, a.account, model)
	}
	return nativeForwardExecutionResult(v), e
}
func (a *messageExecutionAdapter) Observe(n forwardcore.Notice) {
	gatewayhttp.AppendOpsUpstreamError(a.c, ops.OpsUpstreamErrorEvent{UpstreamURL: n.UpstreamURL, Passthrough: n.Passthrough, Platform: n.Platform, AccountID: n.AccountID, AccountName: n.AccountName, UpstreamStatusCode: n.UpstreamStatusCode, UpstreamRequestID: n.UpstreamRequestID, Kind: n.Kind, Message: n.Message, Detail: n.Detail})
}
func (a *messageExecutionAdapter) Failover400(body []byte) bool { return a.s.shouldFailoverOn400(body) }
func (a *messageExecutionAdapter) FailoverError(status int, body []byte, retry bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: status, ResponseBody: body, RetryableOnSameAccount: retry}
}
func (a *messageExecutionAdapter) IsFailover(err error) bool {
	var v *forwardcore.UpstreamFailoverError
	return errors.As(err, &v)
}
func (a *messageExecutionAdapter) Truncate(value string, n int) string {
	return logredact.TruncateUTF8(value, n)
}
func (a *messageExecutionAdapter) TruncateBytes(body []byte, n int) string {
	return truncateForLog(body, n)
}
func (a *messageExecutionAdapter) Sanitize(value string) string {
	return logredact.SanitizeUpstreamQueries(value)
}
func (a *messageExecutionAdapter) Log(value string) {
	logging.LegacyPrintf("service.gateway", "%s", value)
}
func (a *messageExecutionAdapter) Execute(ctx context.Context, in forwardcore.MessageExecution) (upstream.AttemptResult, error) {
	exchange := a.s.anthropicExchangeOptions(ctx, a.c, a.account, a.token, a.tokenType, in.Model, in.Stream, in.Mimic, a.proxyURL, a.profile, in.ReplaceBody)
	target := &claude.Target{
		AccountID: a.account.ID, Model: in.Model, Exchange: exchange, Response: a.s.anthropicResponseOptions(ctx, a.c, a.account, in.Model, false), StartedAt: in.StartedAt, MimicClaudeCode: in.Mimic,
		BeforeResponse: func(ctx context.Context, resp *http.Response, wire []byte) (bool, error) {
			a.response = resp
			return in.Hooks.Before(ctx, &forwardcore.ExchangeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, RequestID: resp.Header.Get("x-request-id")}, wire)
		},
		OnWireBody: in.Hooks.Wire, Accepted: in.Hooks.Accepted, BeforeStream: in.Hooks.BeforeStream,
		OnStream: func(r *claude.StreamResult, _ error) {
			if r != nil {
				in.Hooks.Stream(&forwardcore.StreamOutcome{Usage: r.Usage, FirstTokenMs: r.FirstTokenMs, ClientDisconnect: r.ClientDisconnect})
			}
		},
	}
	return (claude.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocolcore.ProtocolAnthropicMessages, Body: in.Body, ResponseModel: in.OriginalModel, Stream: in.Stream, Target: target}, gatewayhttp.ResponseSink{Writer: a.c.Writer})
}
func (a *messageExecutionAdapter) StreamError(err error) (string, bool) {
	var e *claude.StreamErrorEventError
	if errors.As(err, &e) {
		return e.RawData, true
	}
	return "", false
}
func (a *messageExecutionAdapter) Size() int     { return a.c.Writer.Size() }
func (a *messageExecutionAdapter) Written() bool { return a.c.Writer.Written() }
func (a *messageExecutionAdapter) GenericError() {
	gatewayhttp.WriteForwardMessageGenericError(a.c, func() { gatewayhttp.MarkResponseCommitted(a.c) })
}
func (a *messageExecutionAdapter) ServiceTier() string {
	return gatewayhttp.ObservedUpstreamResponseServiceTier(a.c)
}
