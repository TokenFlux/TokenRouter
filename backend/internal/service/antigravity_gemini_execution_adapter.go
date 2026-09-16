package service

import (
	"context"
	"errors"
	"log"
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/gin-gonic/gin"
)

// geminiExecutionAdapter 持有本次受控凭据/响应，调用现有平台恢复和原生 Executor。
type geminiExecutionAdapter struct {
	s                       *AntigravityGatewayService
	c                       *gin.Context
	account                 *Account
	token, proxyURL, prefix string
	retry                   *antigravity.RetryAdapter
	params                  antigravity.RetryInput
	response                *http.Response
}

func (a *geminiExecutionAdapter) GoogleError(status int, message string) error {
	return a.s.writeGoogleError(a.c, status, message)
}
func (a *geminiExecutionAdapter) ImageInputSize(body []byte) string {
	return a.s.extractImageInputSize(body)
}
func (a *geminiExecutionAdapter) ImageTier(size string) string {
	return normalizeOpenAIImageSizeTier(size)
}
func (a *geminiExecutionAdapter) ZeroCount() { gatewayhttp.WriteForwardGeminiZeroCount(a.c) }
func (a *geminiExecutionAdapter) MappedModel(model string) string {
	return a.s.getMappedModel(a.account, model)
}
func (a *geminiExecutionAdapter) FeatureDenied() {
	MarkOpsClientBusinessLimited(a.c, OpsClientBusinessLimitedReasonLocalFeatureGate)
}
func (a *geminiExecutionAdapter) Credential(ctx context.Context) error {
	token, err := a.s.tokenProvider.GetAccessToken(ctx, a.account)
	a.token = token
	return err
}
func (a *geminiExecutionAdapter) ProjectID() (string, error) {
	return resolveAntigravityProjectID(a.account)
}
func (a *geminiExecutionAdapter) Transport() {
	if a.account.ProxyID != nil && a.account.Proxy != nil {
		a.proxyURL = a.account.Proxy.URL()
	}
}
func (a *geminiExecutionAdapter) InjectIdentity(body []byte) ([]byte, error) {
	return injectIdentityPatchToGeminiRequest(body)
}
func (a *geminiExecutionAdapter) CleanSchema(body []byte) ([]byte, error) {
	return cleanGeminiRequest(body)
}
func (a *geminiExecutionAdapter) Wrap(project, model string, body []byte) ([]byte, error) {
	return a.s.wrapV1InternalRequest(project, model, body)
}
func (a *geminiExecutionAdapter) ProjectRequired(err error) bool {
	return errors.Is(err, errAntigravityProjectIDRequired)
}
func (a *geminiExecutionAdapter) Log(message string) {
	logger.LegacyPrintf("service.antigravity_gateway", "%s", message)
}
func (a *geminiExecutionAdapter) StdLog(message string) { log.Printf("%s", message) }
func (a *geminiExecutionAdapter) Retry(ctx context.Context, in forwardcore.GeminiExecution) error {
	result, err := a.retry.AntigravityRetryLoop(a.params)
	if err == nil {
		a.response = result.Resp
	}
	return err
}
func (a *geminiExecutionAdapter) SwitchError(err error) (bool, bool) {
	if v, ok := IsAntigravityAccountSwitchError(err); ok {
		return v.IsStickySession, true
	}
	return false, false
}
func (a *geminiExecutionAdapter) Failover(status int, body []byte, retry, sticky bool) error {
	return &UpstreamFailoverError{StatusCode: status, ResponseBody: body, RetryableOnSameAccount: retry, ForceCacheBilling: sticky}
}
func (a *geminiExecutionAdapter) ClientCanceled() bool { return a.c.Request.Context().Err() != nil }
func (a *geminiExecutionAdapter) Recover(ctx context.Context, in forwardcore.GeminiExecution) (forwardcore.GeminiRecovery, error) {
	opts := antigravity.GeminiRecoveryOptions{
		Retry: func(body []byte) (*http.Response, error) {
			next := a.params
			next.Body = body
			value, err := a.retry.AntigravityRetryLoop(next)
			if err != nil {
				return nil, err
			}
			return value.Resp, nil
		},
		Do: a.retry.Options.Do,
		FallbackEnabled: func(ctx context.Context) bool {
			return a.s.settingService != nil && a.s.settingService.IsModelFallbackEnabled(ctx)
		},
		SignatureEnabled: func(ctx context.Context) bool {
			return a.s.settingService != nil && a.s.settingService.IsSignatureRectifierEnabled(ctx)
		},
		FallbackModel:   func(ctx context.Context) string { return a.s.settingService.GetFallbackModel(ctx, PlatformAntigravity) },
		IsModelNotFound: isModelNotFoundError, CleanSignatures: CleanGeminiNativeThoughtSignatures, ReadErrorBody: a.s.readUpstreamErrorBody, ErrorDetail: a.s.getUpstreamErrorDetail, Observe: a.retry.Options.Observe,
	}
	recovered, err := antigravity.RecoverGemini(ctx, antigravity.GeminiRecoveryInput{AccountID: a.account.ID, AccountName: a.account.Name, ProjectID: in.ProjectID, Model: in.Model, Action: in.UpstreamAction, AccessToken: a.token, Body: in.InjectedBody}, a.response, opts)
	if err == nil {
		a.response = recovered.Response
	}
	return forwardcore.GeminiRecovery{ErrorBody: recovered.ErrorBody, ContentType: recovered.ContentType}, err
}
func (a *geminiExecutionAdapter) RequestID(id string) { a.c.Header("x-request-id", id) }
func (a *geminiExecutionAdapter) Unwrap(body []byte) ([]byte, error) {
	return a.s.unwrapV1InternalResponse(body)
}
func (a *geminiExecutionAdapter) Health(ctx context.Context, status int, headers map[string][]string, body []byte, in forwardcore.GeminiExecution) {
	a.s.handleUpstreamError(ctx, a.prefix, a.account, status, headers, body, in.OriginalModel, in.GroupID, in.SessionHash, in.Sticky)
}
func (a *geminiExecutionAdapter) ErrorMessage(body []byte) string {
	return extractAntigravityErrorMessage(body)
}
func (a *geminiExecutionAdapter) Sanitize(message string) string {
	return sanitizeUpstreamErrorMessage(message)
}
func (a *geminiExecutionAdapter) Detail(body []byte) string { return a.s.getUpstreamErrorDetail(body) }
func (a *geminiExecutionAdapter) SetError(status int, message, detail string) {
	setOpsUpstreamError(a.c, status, message, detail)
}
func (a *geminiExecutionAdapter) GoogleConfigError(message string) bool {
	return isGoogleProjectConfigError(message)
}
func (a *geminiExecutionAdapter) Observe(n forwardcore.Notice) {
	appendOpsUpstreamError(a.c, OpsUpstreamErrorEvent{Platform: n.Platform, AccountID: n.AccountID, AccountName: n.AccountName, UpstreamStatusCode: n.UpstreamStatusCode, UpstreamRequestID: n.UpstreamRequestID, Kind: n.Kind, Message: n.Message, Detail: n.Detail})
}
func (a *geminiExecutionAdapter) ShouldFailover(status int) bool {
	return a.s.shouldFailoverUpstreamError(status)
}
func (a *geminiExecutionAdapter) TruncateBytes(body []byte, n int) string {
	return truncateForLog(body, n)
}
func (a *geminiExecutionAdapter) ErrorBody(status int, contentType string, body []byte) {
	gatewayhttp.WriteForwardGeminiErrorBody(a.c, status, contentType, body, func() { MarkResponseCommitted(a.c) })
}
func (a *geminiExecutionAdapter) Execute(ctx context.Context, in forwardcore.GeminiExecution, h forwardcore.GeminiHooks) (upstream.AttemptResult, error) {
	a.retry, a.params = a.s.antigravityRetryAdapter(antigravityRetryLoopParams{
		ctx: ctx, prefix: a.prefix, account: a.account, proxyURL: a.proxyURL, accessToken: a.token, action: in.UpstreamAction, body: in.Body, c: a.c, httpUpstream: a.s.httpUpstream, settingService: a.s.settingService, accountRepo: a.s.accountRepo, handleError: a.s.handleUpstreamError, requestedModel: in.OriginalModel, isStickySession: in.Sticky, groupID: in.GroupID, sessionHash: in.SessionHash,
	})
	target := &antigravity.Target{
		AccountID: a.account.ID, Model: in.Model, Mode: antigravity.ModeGeminiResponse, StartedAt: in.StartedAt, Response: a.s.antigravityResponseAdapter(a.c).Options, Enter: a.s.nativeAttemptActivity,
		Exchange: func(context.Context) (*http.Response, error) {
			if err := h.Exchange(); err != nil {
				return nil, err
			}
			return a.response, nil
		},
		BeforeResponse: func(ctx context.Context, resp *http.Response) (bool, error) {
			return h.Before(ctx, &forwardcore.ExchangeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, RequestID: resp.Header.Get("x-request-id")})
		},
		OutputError: h.OutputError,
	}
	return (antigravity.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolGeminiGenerateContent, Body: in.Body, ResponseModel: in.OriginalModel, Stream: in.Stream, Target: target}, gatewayhttp.ResponseSink{Writer: a.c.Writer})
}
func (a *geminiExecutionAdapter) IsImageModel(model string) bool {
	return isImageGenerationModel(model)
}
