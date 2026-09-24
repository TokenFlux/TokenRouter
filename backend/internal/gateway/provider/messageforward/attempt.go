package messageforward

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"

	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// attempt 保存本次请求的准备状态、凭据和响应句柄，执行顺序由 gateway/forward 决定。
type attempt struct {
	s                          *Runtime
	c                          HTTPBoundary
	account                    *gatewayprovider.ExecutionAccount
	token, tokenType, proxyURL string
	profile                    *tlsfingerprint.Profile
	toolRewrite                *claude.ToolNameRewrite
	state                      *AttemptState
	response                   *http.Response
}

func newAttempt(s *Runtime, c HTTPBoundary, account *gatewayprovider.ExecutionAccount) *attempt {
	return &attempt{s: s, c: c, account: account, state: &AttemptState{}}
}
func (a *attempt) input() forwardcore.MessageInput {
	in := forwardcore.MessageInput{AccountPresent: a.account != nil, HTTPPresent: a.c.Present()}
	if v := a.account; v != nil {
		in.AccountID = v.Record.ID
		in.AccountName = v.Record.Name
		in.AccountType = v.Record.Type
		in.Platform = v.Record.Platform
		in.OAuth = v.View().IsOAuth()
		in.Passthrough = v.View().IsAnthropicAPIKeyPassthroughEnabled()
		in.Bedrock = v.View().IsBedrock()
	}
	if a.s.options.Configured {
		in.LogErrorBody = a.s.options.LogErrorBody
		in.LogErrorBodyMaxBytes = a.s.options.LogErrorBodyMaxBytes
		in.FailoverOn400 = a.s.options.FailoverOn400
	}
	return in
}
func (a *attempt) ShouldEmulate(ctx context.Context, group *int64, body []byte) bool {
	return a.s.shouldEmulate(ctx, a.account, group, body)
}
func (a *attempt) Emulate(ctx context.Context, p *requeststate.ParsedRequest) (*forwardcore.Result, error) {
	return a.s.emulate(ctx, a.c, a.account, p)
}
func (a *attempt) MappedModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.account).Mapped(model)
}
func (a *attempt) ReplaceModel(body []byte, model string) []byte {
	return protocolopenai.ReplaceModelInBody(body, model)
}
func (a *attempt) Passthrough(ctx context.Context, in forwardcore.PassthroughInput) (*forwardcore.Result, error) {
	return a.s.passthrough(ctx, a.c, a.account, forwardcore.APIKeyInput{Body: in.Body, Parsed: in.Parsed, RequestModel: in.RequestModel, OriginalModel: in.OriginalModel, RequestStream: in.Stream, StartTime: in.StartedAt})
}
func (a *attempt) Bedrock(ctx context.Context, p *requeststate.ParsedRequest, start time.Time) (*forwardcore.Result, error) {
	return a.s.bedrock(ctx, a.c, a.state, a.account, p, start)
}
func (a *attempt) Begin() (func(), error) {
	if a.s.dependencies.Enter != nil {
		return a.s.dependencies.Enter()
	}
	return func() {}, nil
}
func (a *attempt) Beta(ctx context.Context, model string) error {
	return a.s.checkBeta(ctx, a.state, a.account, a.c.RequestHeaders().Get("anthropic-beta"), model)
}
func (a *attempt) DebugOriginal(body []byte, model string, stream bool) {
	if a.s.dependencies.Debug != nil {
		a.s.dependencies.Debug.Snapshot("CLIENT_ORIGINAL", a.c.RequestHeaders(), body, map[string]string{"account": fmt.Sprintf("%d(%s)", a.account.Record.ID, a.account.Record.Name), "account_type": a.account.Record.Type, "model": model, "stream": strconv.FormatBool(stream)})
	}
}
func (a *attempt) AccountMappedModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.account).Mapped(model)
}
func (a *attempt) IsClaudeCode(ctx context.Context, body []byte, metadata string) bool {
	ua := ""
	if a.c.Present() {
		ua = a.c.RequestHeaders().Get("User-Agent")
	}
	return requeststate.IsClaudeCodeClient(ctx) || clientmeta.IsClaudeCodeClient(ua, metadata) || claude.IsProxiedClaudeCodeRequest(body, metadata)
}
func (a *attempt) SystemSettings(ctx context.Context) (bool, string, string) {
	if a.s.dependencies.Settings == nil {
		return true, "", ""
	}
	return a.s.dependencies.Settings.GetClaudeOAuthSystemPromptInjectionSettings(ctx)
}
func (a *attempt) RewriteSystem(body []byte, p *requeststate.ParsedRequest, prompt, blocks string) []byte {
	system, _ := p.SystemValue()
	return claude.RewriteSystemForNonClaudeCodeWithPromptBlocks(body, system, prompt, blocks)
}
func (a *attempt) Metadata(ctx context.Context, p *requeststate.ParsedRequest) string {
	if a.s.dependencies.Fingerprint == nil || !a.c.Present() {
		return ""
	}
	fp, err := a.s.dependencies.Fingerprint.GetOrCreateFingerprint(ctx, a.account.Record.ID, a.c.RequestHeaders())
	if err != nil || fp == nil {
		return ""
	}
	_, mimic, _ := a.s.dependencies.Settings.GetGatewayForwardingSettings(ctx)
	if mimic {
		return ""
	}
	return metadataUserID(p, a.account, fp)
}
func (a *attempt) NormalizeOAuth(body []byte, model string, o forwardcore.NormalizeOptions) ([]byte, string) {
	return claude.NormalizeClaudeOAuthRequestBody(body, model, claude.ClaudeOAuthNormalizeOptions{StripSystemCacheControl: o.StripSystemCacheControl, InjectMetadata: o.InjectMetadata, MetadataUserID: o.MetadataUserID})
}
func (a *attempt) RewriteCache(ctx context.Context, body []byte) []byte {
	return a.s.rewriteCache(ctx, body)
}
func (a *attempt) RewriteTools(body []byte) ([]byte, bool) {
	a.toolRewrite = claude.BuildToolNameRewriteFromBody(body)
	if a.toolRewrite == nil {
		return body, false
	}
	return claude.ApplyToolNameRewriteToBody(body, a.toolRewrite), true
}
func (a *attempt) BindTools() {
	a.state.ToolNames = a.toolRewrite
}
func (a *attempt) ToolsLast(body []byte) []byte {
	return claude.ApplyToolsLastCacheBreakpoint(body)
}
func (a *attempt) NormalizeDateline(ctx context.Context, body []byte) ([]byte, bool) {
	return a.s.normalizeDateline(ctx, a.account, body)
}
func (a *attempt) CacheLimit(body []byte) []byte {
	return claude.EnforceCacheControlLimit(body)
}
func (a *attempt) PlatformModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.account).AnthropicUpstream(model)
}
func (a *attempt) InjectTTL(ctx context.Context) bool {
	return a.s.injectTTL(ctx, a.account)
}
func (a *attempt) CacheTTL(body []byte) []byte {
	return claude.InjectAnthropicCacheControlTTL1h(body)
}
func (a *attempt) Credential(ctx context.Context) error {
	token, kind, e := a.s.dependencies.Credentials.Resolve(ctx, gatewayprovider.ExecutionRecord(a.account))
	a.token, a.tokenType = token, kind
	return e
}
func (a *attempt) Transport() {
	if a.account.Record.ProxyID != nil && a.account.Record.Proxy != nil && (!a.account.View().IsCustomBaseURLEnabled() || a.account.View().GetCustomBaseURL() == "") {
		a.proxyURL = a.account.Record.Proxy.URL()
	}
	a.profile = a.s.requestTLS(a.account)
	logging.LegacyPrintf("service.gateway", "[Forward] Using account: ID=%d Name=%s Platform=%s Type=%s TLSFingerprint=%v Proxy=%s", a.account.Record.ID, a.account.Record.Name, a.account.Record.Platform, a.account.Record.Type, a.profile, a.proxyURL)
}
func (a *attempt) FilterSearchHistory(body []byte, model string) []byte {
	return searchtools.FilterWebSearchHistoryBlocks(body, modelidentity.ResolveThinkingProtocol(model) == modelidentity.ThinkingProtocolPassbackRequired)
}
func (a *attempt) FilterThinking(body []byte, model string) []byte {
	return gatewayprovider.FilterThinkingBlocks(body, model)
}
func (a *attempt) PassbackThinking(model string) bool {
	return modelidentity.ResolveThinkingProtocol(model) == modelidentity.ThinkingProtocolPassbackRequired
}
func (a *attempt) NormalizeThinking(body []byte, model string) ([]byte, bool) {
	return gatewayprovider.NormalizeChineseLLMThinking(body, model)
}
func (a *attempt) ReadErrorBody() ([]byte, error) {
	return a.s.readErrorBody(a.response)
}
func (a *attempt) ResetErrorBody(body []byte) {
	_ = a.response.Body.Close()
	a.response.Body = io.NopCloser(bytes.NewReader(body))
}
func (a *attempt) Health(ctx context.Context, mode string, status int, headers map[string][]string, body []byte, model string) forwardcore.ErrorDecision {
	var d accountcore.UpstreamErrorDecision
	switch mode {
	case "retry":
		d = a.s.retryHealth(ctx, a.response, a.account, model)
	case "failover":
		d = a.s.failoverHealth(ctx, a.response, a.account, model)
	case "failover_synthetic":
		resp := &http.Response{StatusCode: status, Header: http.Header(headers).Clone(), Body: io.NopCloser(bytes.NewReader(body))}
		d = a.s.failoverHealth(ctx, resp, a.account, model)
		_ = resp.Body.Close()
	default:
		d = accountcore.ErrorDecisionWithoutPersistence(gatewayprovider.ExecutionErrorPolicy(a.account), status)
		if mode == "persist" && a.s.dependencies.Health != nil {
			d = gatewayprovider.ApplyExecutionHealth(ctx, a.s.dependencies.Health, a.account, gatewayprovider.HealthObservationFromContext(ctx, status, headers, body, []string{model}))
		}
	}
	return forwardcore.ErrorDecision{
		Generic:          d.ShouldReturnGenericError(),
		Failover:         d.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(a.account), status, true),
		RetrySameAccount: d.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(a.account), status),
	}
}
func (a *attempt) HandleError(ctx context.Context, model string, retry bool) (*forwardcore.Result, error) {
	return a.s.handleError(ctx, a.c, a.state, a.account, a.response, retry, model)
}
func (a *attempt) Observe(n forwardcore.Notice) {
	a.c.Observe(n)
}
func (a *attempt) Failover400(body []byte) bool { return messageFailover400(body) }
func (a *attempt) FailoverError(status int, body []byte, retry bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: status, ResponseBody: body, RetryableOnSameAccount: retry}
}
func (a *attempt) IsFailover(err error) bool {
	var v *forwardcore.UpstreamFailoverError
	return errors.As(err, &v)
}
func (a *attempt) Truncate(value string, n int) string {
	return logredact.TruncateUTF8(value, n)
}
func (a *attempt) TruncateBytes(body []byte, n int) string {
	return logredact.TruncateLine(body, n)
}
func (a *attempt) Sanitize(value string) string {
	return logredact.SanitizeUpstreamQueries(value)
}
func (a *attempt) Log(value string) {
	logging.LegacyPrintf("service.gateway", "%s", value)
}
func (a *attempt) Execute(ctx context.Context, in forwardcore.MessageExecution) (upstream.AttemptResult, error) {
	exchange := a.s.exchangeOptions(a.c, a.state, a.account, a.token, a.tokenType, in.Model, in.Stream, in.Mimic, a.proxyURL, a.profile, in.ReplaceBody)
	target := &claude.Target{
		AccountID: a.account.Record.ID, Model: in.Model, Exchange: exchange, Response: a.s.responseOptions(ctx, a.c, a.state, a.account, in.Model, false), StartedAt: in.StartedAt, MimicClaudeCode: in.Mimic,
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
	return (claude.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocolcore.ProtocolAnthropicMessages, Body: in.Body, ResponseModel: in.OriginalModel, Stream: in.Stream, Target: target}, a.c.Sink())
}
func (a *attempt) StreamError(err error) (string, bool) {
	var e *claude.StreamErrorEventError
	if errors.As(err, &e) {
		return e.RawData, true
	}
	return "", false
}
func (a *attempt) Size() int     { return a.c.Size() }
func (a *attempt) Written() bool { return a.c.Written() }
func (a *attempt) GenericError() {
	a.c.GenericError()
}
func (a *attempt) ServiceTier() string {
	return a.c.ServiceTier()
}
