package service

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"

	grokforward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/grokforward"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	bridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"

	uuid "github.com/google/uuid"
)

// grokForwardAdapter 只持有本次受控凭据与旧能力引用，不保存新的会话/健康状态。
type grokForwardAdapter struct {
	s               *OpenAIGatewayService
	c               *gin.Context
	account         *gatewayprovider.ExecutionAccount
	token, proxyURL string
}

func (a *grokForwardAdapter) options() grokforward.Options {
	maxLine := defaultMaxLineSize
	if a.s.cfg != nil && a.s.cfg.Gateway.MaxLineSize > 0 {
		maxLine = a.s.cfg.Gateway.MaxLineSize
	}
	return grokforward.Options{Codec: (grok.BodyCodec{
		NewID: uuid.NewString}), MaxLineSize: maxLine, Enter: a.s.nativeAttemptActivity}
}
func (a *grokForwardAdapter) input(body []byte, model string, stream bool, start time.Time) grokforward.Input {
	return grokforward.Input{
		AccountID:     a.account.Record.ID,
		AccountName:   a.account.Record.Name,
		AccountType:   a.account.Record.Type,
		Platform:      a.account.Record.Platform,
		OAuth:         a.account.View().IsGrokOAuth(),
		HTTPPresent:   a.c != nil,
		Compact:       gatewayhttp.IsOpenAIResponsesCompactPath(a.c),
		Body:          body,
		OriginalModel: model,
		Stream:        stream,
		StartedAt:     start,
	}
}
func (a *grokForwardAdapter) BillingModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.account).ForwardModel(model, "")
}
func (a *grokForwardAdapter) UpstreamModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.account).NormalizeOpenAI(model)
}
func (a *grokForwardAdapter) ImageModel(model string) bool {
	return media.IsGrokImageGenerationModel(model)
}
func (a *grokForwardAdapter) InvalidRequest(message, param string) {
	gatewayhttp.WriteGrokForwardInvalidRequest(a.c, message, param)
}
func (a *grokForwardAdapter) SetError(status int, message, detail string) {
	gatewayhttp.SetOpsUpstreamError(a.c, status, message, detail)
}
func (a *grokForwardAdapter) ClientTools(mapping bridge.ResponsesClientToolMapping) {
	setGrokResponsesClientToolMapping(a.c, mapping)
}
func (a *grokForwardAdapter) CacheIdentity(body []byte, model string) string {
	return resolveGrokCacheIdentity(a.c, body, "", model)
}
func (a *grokForwardAdapter) FreeCacheRoute(body, intent []byte, identity string) ([]byte, error) {
	return applyGrokFreeRequestToolCacheRoute(a.c, body, intent, a.account, identity)
}
func (a *grokForwardAdapter) Credential(ctx context.Context) error {
	token, _, err := a.s.requestCredentials.Resolve(ctx, gatewayhttp.RequestCredentialBudget(a.c), gatewayhttp.CredentialObserver{Context: a.c}, a.account)
	a.token = token
	return err
}
func (a *grokForwardAdapter) Detach(ctx context.Context) (context.Context, func()) {
	return detachUpstreamContext(ctx)
}
func (a *grokForwardAdapter) ResolveProxy() {
	if a.account.Record.ProxyID != nil && a.account.Record.Proxy != nil {
		a.proxyURL = a.account.Record.Proxy.URL()
	}
}
func (a *grokForwardAdapter) Build(ctx context.Context, body []byte, identity string, settings bool) (*http.Request, error) {
	if settings {
		return buildGrokResponsesRequest(ctx, a.c, a.account, body, a.token, identity, a.s.cfg, a.s.settingService)
	}
	return buildGrokResponsesRequest(ctx, a.c, a.account, body, a.token, identity, a.s.cfg)
}
func (a *grokForwardAdapter) Do(req *http.Request) (*http.Response, error) {
	return a.s.httpUpstream.Do(req, a.proxyURL, a.account.Record.ID, a.account.Record.Concurrency)
}
func (a *grokForwardAdapter) ReadError(resp *http.Response) []byte {
	return a.s.readUpstreamErrorBody(resp)
}
func (a *grokForwardAdapter) Latency(value int64) {
	gatewayhttp.SetOpsLatencyMs(a.c, gatewayhttp.OpsUpstreamLatencyMsKey, value)
}
func (a *grokForwardAdapter) TransportError(ctx context.Context, err error) error {
	return a.s.handleOpenAIUpstreamTransportError(ctx, a.c, a.account, err, false)
}
func (a *grokForwardAdapter) ReplayNotice(identity bool) {
	slog.Info("grok_replay_decode_retry", "account_id", a.account.Record.ID, "cache_identity_present", identity)
}
func (a *grokForwardAdapter) ErrorMessage(body []byte) string {
	return logredact.SanitizeUpstreamQueries(upstream.ExtractErrorMessage(body))
}
func (a *grokForwardAdapter) Health(ctx context.Context, status int, headers http.Header, body []byte, model string, teamContext bool) grokforward.Decision {
	if teamContext {
		ctx = withGrokTeamRateLimitModel(ctx, model)
	}
	d := a.s.applyGrokAccountUpstreamError(ctx, a.account, status, headers, body, model)
	return grokforward.Decision{
		Generic:          d.ShouldReturnGenericError(),
		Failover:         d.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(a.account), status, a.s.shouldFailoverGrokUpstreamError(status, body)),
		RetrySameAccount: d.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(a.account), status),
	}
}
func (a *grokForwardAdapter) Observe(n grokforward.Notice) {
	gatewayhttp.AppendOpsUpstreamError(a.c, ops.OpsUpstreamErrorEvent{
		Platform:           n.Platform,
		AccountID:          n.AccountID,
		AccountName:        n.AccountName,
		UpstreamStatusCode: n.UpstreamStatusCode,
		UpstreamRequestID:  n.UpstreamRequestID,
		Kind:               n.Kind,
		Message:            n.Message,
	})
}
func (a *grokForwardAdapter) HandleError(ctx context.Context, resp *http.Response, body []byte, model string) (*grokforward.Result, error) {
	v, err := a.s.handleErrorResponse(ctx, resp, a.c, a.account, body, model)
	return nativeGrokForwardResult(v), err
}
func (a *grokForwardAdapter) ShouldMarkTeam(status int, body []byte) bool {
	return grok.ShouldMarkGrokTeamModelRateLimit(status, body)
}
func (a *grokForwardAdapter) MarkTeam(model string) {
	markGrokTeamModelRateLimit(a.account, model, accountcore.ResolveGrokTeamRateLimitUntil(time.Now().Add(grokTeamRateLimitDefaultTTL), time.Now()))
}
func (a *grokForwardAdapter) RetryMetadata(status int, body []byte) grokforward.Retry {
	retry, delay, deadline, max := grokSameAccountRetryMetadata(a.account, status, body)
	return grokforward.Retry{Retryable: retry, Delay: delay, Deadline: deadline, Max: max}
}
func (a *grokForwardAdapter) Failure(f grokforward.Failure) error {
	return &forwardcore.UpstreamFailoverError{
		StatusCode:               f.StatusCode,
		ResponseBody:             f.ResponseBody,
		ResponseHeaders:          f.ResponseHeaders,
		RetryableOnSameAccount:   f.RetryableOnSameAccount,
		RequestScopedTransient:   f.RequestScopedTransient,
		SameAccountRetryDelay:    f.SameAccountRetryDelay,
		SameAccountRetryDeadline: f.SameAccountRetryDeadline,
		SameAccountRetryMax:      f.SameAccountRetryMax,
	}
}
func (a *grokForwardAdapter) ObserveSuccess(ctx context.Context, headers http.Header, status int, model string) {
	a.s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, model), a.account, headers, status)
}
func (a *grokForwardAdapter) ReadStream(ctx context.Context, resp *http.Response, start time.Time, original, mapped string) (upstream.ResponsesObservation, error) {
	v, err := a.s.readStreamingResponseObservation(ctx, resp, a.c, a.account, start, original, mapped, "")
	if v == nil {
		return upstream.ResponsesObservation{}, err
	}
	return upstream.ResponsesObservation{
		Usage:               v.usage,
		HasUsage:            v.hasUsage,
		Served:              v.served,
		HTTPCommitted:       v.httpCommitted,
		RetryCommitted:      v.retryCommitted,
		ClientDisconnected:  v.clientDisconnected,
		FirstSemanticOutput: v.firstSemanticOutput,
		FirstTokenMs:        v.firstTokenMs,
		ResponseID:          v.responseID,
		SearchCount:         v.searchCount,
		ImageCount:          v.imageCount,
		ImageOutputSizes:    v.imageOutputSizes,
	}, err
}
func (a *grokForwardAdapter) ReadNonStream(ctx context.Context, resp *http.Response, original, mapped string) (upstream.ResponsesObservation, error) {
	v, err := a.s.handleNonStreamingResponse(ctx, resp, a.c, a.account, original, mapped)
	if err != nil {
		return upstream.ResponsesObservation{}, err
	}
	return upstream.ResponsesObservation{
		Usage:            v.usage,
		HasUsage:         v.usage != nil,
		Served:           v.served,
		HTTPCommitted:    a.c.Writer.Written(),
		RetryCommitted:   gatewayhttp.IsResponseCommitted(a.c),
		ResponseID:       v.responseID,
		SearchCount:      v.searchCount,
		ImageCount:       v.imageCount,
		ImageOutputSizes: v.imageOutputSizes,
	}, nil
}
func (a *grokForwardAdapter) Sink() upstream.OutputSink {
	if a.c == nil {
		return nil
	}
	return gatewayhttp.ResponseSink{Writer: a.c.Writer}
}
func (a *grokForwardAdapter) Effort(body []byte, model string) *string {
	return requeststate.ExtractOpenAIReasoningEffortFromBody(body, model)
}
func (a *grokForwardAdapter) ReadBody(resp *http.Response) ([]byte, error) {
	return gatewayhttp.ReadUpstreamResponseBody(resp.Body, resolveUpstreamResponseReadLimit(a.s.cfg), a.c, nil)
}
func (a *grokForwardAdapter) HasTokens(usage *protocolopenai.ForwardUsage) bool {
	return protocolopenai.OpenAIUsageHasTokens(usage)
}
