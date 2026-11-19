package googleforward

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// antigravity_attempt.go binds native Gemini execution hooks to Antigravity
// request, recovery, and response services.
// Native Gemini execution through Antigravity stores its current response on
// the same adapter used by exchange and recovery hooks. Credentials and proxy
// selection precede retry binding, and successful recovery replaces that
// response before the executor adapts output and closes its final body.

// geminiExecutionAdapter retains native attempt inputs and its selected
// Antigravity response.
type geminiExecutionAdapter struct {
	// s, c, and provider are borrowed for this synchronous native attempt.
	// Execute initializes retry and params before core exchange hooks call
	// Retry and Recover against the saved response.
	//
	// shared Antigravity forwarding services
	s *Antigravity
	c *attempt // current attempt presentation state
	// selected execution provider state
	provider *gatewayprovider.ExecutionProvider
	// Credential stores token and Transport selects proxyURL before Execute
	// binds retry inputs; prefix is assigned by ForwardGemini for this
	// request diagnostics.
	//
	// token OAuth credential; proxyURL exchange proxy; prefix attempt log
	// text
	token, proxyURL, prefix string
	// bound provider retry adapter
	retry *antigravity.RetryAdapter
	// saved retry-loop request inputs
	params antigravity.RetryInput
	// response changes after successful Retry and Recover; the platform
	// executor owns closing its final successful handle after response
	// processing.
	//
	// selected upstream response handle
	response *http.Response
}

// GoogleError delegates native error presentation to the current attempt HTTP
// adapter.
func (a *geminiExecutionAdapter) GoogleError(status int, message string) error {
	return a.c.GoogleError(status, message)
}

// ImageInputSize delegates image-size extraction from the supplied native
// request bytes.
func (a *geminiExecutionAdapter) ImageInputSize(body []byte) string {
	return a.s.extractImageInputSize(body)
}

// ImageTier normalizes a supplied image-size hint for completion billing
// metadata.
func (a *geminiExecutionAdapter) ImageTier(size string) string {
	return media.NormalizeImageSizeTier(size)
}

// ZeroCount presents a zero local token count through native HTTP output.
func (a *geminiExecutionAdapter) ZeroCount() { a.c.Count(0) }

// MappedModel returns the selected provider Antigravity mapping for the request
// model.
func (a *geminiExecutionAdapter) MappedModel(model string) string {
	return a.s.getMappedModel(a.provider, model)
}

// ModelParameters resolves the final model attributes and applies native Gemini
// controls. Resolution or JSON update errors return to the core flow before
// wrapping or sending the request.
func (a *geminiExecutionAdapter) ModelParameters(ctx context.Context, model string, body []byte) ([]byte, error) {
	resolved, err := gatewayprovider.ResolveModelAttributes(ctx, a.provider, model, a.s.Attributes, "gemini_generate_content")
	if err != nil {
		return nil, err
	}
	return applyGeminiModelParameters(body, body, resolved, false)
}

// FeatureDenied records local feature denial through the current attempt HTTP
// adapter.
func (a *geminiExecutionAdapter) FeatureDenied() {
	a.c.FeatureDenied()
}

// Credential resolves and stores the selected provider OAuth token with its
// error. Callers inspect the error before Execute binds the saved token into
// the upstream retry inputs.
func (a *geminiExecutionAdapter) Credential(ctx context.Context) error {
	token, err := gatewayprovider.ExecutionToken(ctx, a.s.Tokens, a.provider)
	a.token = token
	return err
}

// ProjectID resolves the selected provider project before v1internal wrapping.
// Missing project data returns ErrProjectIDRequired, which the core flow
// recognizes for local Google configuration output instead of sending an empty
// project.
func (a *geminiExecutionAdapter) ProjectID() (string, error) {
	return resolveAntigravityProjectID(a.provider)
}

// Transport stores the attached provider proxy when both proxy references
// exist. ForwardGemini creates fresh adapter state for each call, so an absent
// proxy retains its initial empty selection.
func (a *geminiExecutionAdapter) Transport() {
	if a.provider.Record.ProxyID != nil && a.provider.Record.Proxy != nil {
		a.proxyURL = a.provider.Record.Proxy.URL()
	}
}

// InjectIdentity delegates the Antigravity identity patch into native Gemini
// request bytes.
func (a *geminiExecutionAdapter) InjectIdentity(body []byte) ([]byte, error) {
	return antigravity.InjectIdentityPatchToGeminiRequest(body)
}

// CleanSchema delegates native Gemini schema compatibility cleanup to
// Antigravity request handling.
func (a *geminiExecutionAdapter) CleanSchema(body []byte) ([]byte, error) {
	return antigravity.CleanGeminiRequest(body)
}

// Wrap delegates v1internal project and model envelope construction for native
// bytes.
func (a *geminiExecutionAdapter) Wrap(project, model string, body []byte) ([]byte, error) {
	return antigravity.WrapV1InternalRequest(project, model, body)
}

// ProjectRequired recognizes the Antigravity missing-project sentinel through
// the supplied error chain.
func (a *geminiExecutionAdapter) ProjectRequired(err error) bool {
	return errors.Is(err, antigravity.ErrProjectIDRequired)
}

// Log writes supplied native attempt diagnostics under the Antigravity gateway
// component.
func (a *geminiExecutionAdapter) Log(message string) {
	logging.LegacyPrintf("service.antigravity_gateway", "%s", message)
}

// StdLog writes supplied native attempt diagnostics through the standard
// logger.
func (a *geminiExecutionAdapter) StdLog(message string) {
	logging.LegacyPrintf("gateway.antigravity", "%s", message)
}

// Retry runs the bound retry inputs and saves their response on success.
// Execute initializes that binding first; ctx and execution input are accepted
// while the saved params supply the actual retry context.
func (a *geminiExecutionAdapter) Retry(ctx context.Context, in forwardcore.GeminiExecution) error {
	result, err := a.retry.AntigravityRetryLoop(a.params)
	if err == nil {
		a.response = result.Resp
	}
	return err
}

// SwitchError extracts sticky provider-switch state and reports whether that
// signal exists. An unrelated error returns two false values, allowing core
// forwarding to select its remaining failure handling.
func (a *geminiExecutionAdapter) SwitchError(err error) (bool, bool) {
	if v, ok := antigravity.IsAntigravityProviderSwitchError(err); ok {
		return v.IsStickySession, true
	}
	return false, false
}

// Failover constructs an outer provider-loop error with retry and sticky
// billing hints. The body bytes remain shared with their supplied input, and
// the caller decides subsequent provider selection.
func (a *geminiExecutionAdapter) Failover(status int, body []byte, retry, sticky bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: status, ResponseBody: body, RetryableOnSameProvider: retry, ForceCacheBilling: sticky}
}

// ClientCanceled reports whether the active inbound HTTP request context has
// ended.
func (a *geminiExecutionAdapter) ClientCanceled() bool { return a.c.RequestContext().Err() != nil }

// Recover delegates missing-model and signature recovery with current route and
// gateway switches. A successful result replaces the saved response; retained
// error bytes and content type may still describe an earlier failure.
func (a *geminiExecutionAdapter) Recover(ctx context.Context, in forwardcore.GeminiExecution) (forwardcore.GeminiRecovery, error) {
	opts := antigravity.GeminiRecoveryOptions{
		Prepare: func(model string, body []byte) ([]byte, error) { return a.ModelParameters(ctx, model, body) },
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
			return a.s.Routing != nil && a.s.Routing.IsModelFallbackEnabled(ctx)
		},

		SignatureEnabled: func(ctx context.Context) bool {
			return a.s.Gateway != nil && a.s.Gateway.IsSignatureRectifierEnabled(ctx)
		},

		FallbackModel: func(ctx context.Context) string {
			return a.s.Routing.GetFallbackModel(ctx, capability.PlatformAntigravity)
		},

		IsModelNotFound: upstream.IsModelNotFoundOrBare404,

		CleanSignatures: func(body []byte) []byte {
			return protocolgemini.CleanNativeThoughtSignatures(body, bridge.DummyThoughtSignature)
		},
		ReadErrorBody: a.s.readUpstreamErrorBody,
		ErrorDetail:   a.s.getUpstreamErrorDetail,
		Observe:       a.retry.Options.Observe,
	}
	recovered, err := antigravity.RecoverGemini(ctx, antigravity.GeminiRecoveryInput{
		ProviderID:   a.provider.Record.ID,
		ProviderName: a.provider.Record.Name,
		ProjectID:    in.ProjectID,
		Model:        in.Model,
		Action:       in.UpstreamAction,
		AccessToken:  a.token,
		Body:         in.InjectedBody,
	}, a.response, opts)
	if err == nil {
		a.response = recovered.Response
	}
	return forwardcore.GeminiRecovery{ErrorBody: recovered.ErrorBody, ContentType: recovered.ContentType}, err
}

// RequestID exposes the upstream correlation identifier through the client
// response header.
func (a *geminiExecutionAdapter) RequestID(id string) { a.c.Header("x-request-id", id) }

// Unwrap delegates single v1internal response envelope extraction for native
// output bytes.
func (a *geminiExecutionAdapter) Unwrap(body []byte) ([]byte, error) {
	return a.s.unwrapV1InternalResponse(body)
}

// Health delegates provider observation using original model, group, and sticky
// session context.
func (a *geminiExecutionAdapter) Health(ctx context.Context, status int, headers map[string][]string, body []byte, in forwardcore.GeminiExecution) {
	a.s.handleUpstreamError(ctx, a.prefix, a.provider, status, headers, body, in.OriginalModel, in.GroupID, in.SessionHash, in.Sticky)
}

// ErrorMessage extracts the Google platform message from upstream error
// response bytes.
func (a *geminiExecutionAdapter) ErrorMessage(body []byte) string {
	return google.ExtractPlatformMessage(body)
}

// Sanitize removes sensitive upstream query values from the supplied diagnostic
// message.
func (a *geminiExecutionAdapter) Sanitize(message string) string {
	return logredact.SanitizeUpstreamQueries(message)
}

// Detail returns diagnostic body text under current Antigravity logging cap and
// permission.
func (a *geminiExecutionAdapter) Detail(body []byte) string { return a.s.getUpstreamErrorDetail(body) }

// SetError records native failure status and diagnostics in the HTTP adapter.
func (a *geminiExecutionAdapter) SetError(status int, message, detail string) {
	a.c.SetError(status, message, detail)
}

// GoogleConfigError recognizes project configuration failures in the supplied
// upstream message.
func (a *geminiExecutionAdapter) GoogleConfigError(message string) bool {
	return upstream.IsGoogleProjectConfigError(message)
}

// Observe translates a core failure notice into HTTP upstream diagnostic event
// fields.
func (a *geminiExecutionAdapter) Observe(n forwardcore.Notice) {
	a.c.Observe(ops.OpsUpstreamErrorEvent{
		Platform:           n.Platform,
		ProviderID:         n.ProviderID,
		ProviderName:       n.ProviderName,
		UpstreamStatusCode: n.UpstreamStatusCode,
		UpstreamRequestID:  n.UpstreamRequestID,
		Kind:               n.Kind,
		Message:            n.Message,
		Detail:             n.Detail,
	})
}

// ShouldFailover recognizes upstream statuses eligible for outer Antigravity
// provider switching.
func (a *geminiExecutionAdapter) ShouldFailover(status int) bool {
	return a.s.shouldFailoverUpstreamError(status)
}

// TruncateBytes delegates bounded one-line diagnostics for supplied upstream
// error bytes.
func (a *geminiExecutionAdapter) TruncateBytes(body []byte, n int) string {
	return logredact.TruncateLine(body, n)
}

// ErrorBody delegates supplied native error bytes and content type to HTTP
// presentation. The adapter marks semantic commitment before writing, and
// forwarding uses that marker to suppress another terminal response.
func (a *geminiExecutionAdapter) ErrorBody(status int, contentType string, body []byte) {
	a.c.GeminiErrorBody(status, contentType, body)
}

// Execute binds the prepared credential, proxy, session, and request bytes for
// platform retry. Core hooks perform exchange and response inspection
// synchronously, and the Antigravity executor closes the final successful
// response body.
func (a *geminiExecutionAdapter) Execute(ctx context.Context, in forwardcore.GeminiExecution, h forwardcore.GeminiHooks) (upstream.AttemptResult, error) {
	a.retry, a.params = a.s.antigravityRetryAdapter(antigravityRetryLoopParams{
		ctx:             ctx,
		prefix:          a.prefix,
		provider:        a.provider,
		proxyURL:        a.proxyURL,
		accessToken:     a.token,
		action:          in.UpstreamAction,
		body:            in.Body,
		c:               a.c,
		httpUpstream:    a.s.Transport,
		providerRepo:    a.s.Store,
		handleError:     a.s.handleUpstreamError,
		requestedModel:  in.OriginalModel,
		isStickySession: in.Sticky,
		groupID:         in.GroupID,
		sessionHash:     in.SessionHash,
	})
	target := &antigravity.Target{
		ProviderID: a.provider.Record.ID,
		Model:      in.Model,
		Mode:       antigravity.ModeGeminiResponse,
		StartedAt:  in.StartedAt,
		Response:   a.s.antigravityResponseAdapter(a.c).Options,
		Enter:      a.s.Enter,

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
	result, err := (antigravity.Executor{}).Execute(ctx, upstream.AttemptInput{
		Protocol:      protocol.ProtocolGeminiGenerateContent,
		Body:          in.Body,
		ResponseModel: in.OriginalModel,
		Stream:        in.Stream,
		Target:        target,
	}, a.c.Sink())
	result.ObservedImages = max(result.ObservedImages, a.c.Images)
	var streamError *upstream.GeminiStreamError
	if errors.As(err, &streamError) && ctx.Err() == nil && !result.ClientDisconnect {
		body := streamError.Body
		if len(body) == 0 {
			body, _ = json.Marshal(map[string]any{"error": map[string]any{"code": streamError.Status, "message": streamError.Message}})
		}
		a.Health(ctx, streamError.Status, result.UpstreamHeaders, body, in)
		committed := true
		if state, ok := a.c.Sink().(interface{ OutputState() upstream.OutputHead }); ok {
			committed = state.OutputState().ContentCommitted
		}
		a.c.SetError(streamError.Status, streamError.Message, a.Detail(body))
		a.Observe(forwardcore.Notice{
			Platform: a.provider.Record.Platform, ProviderID: a.provider.Record.ID, ProviderName: a.provider.Record.Name,
			UpstreamStatusCode: streamError.Status, UpstreamRequestID: result.RequestID, Kind: "stream_error", Message: streamError.Message,
		})
		// Existing provider policy can retry only before real output. Once bytes
		// have reached the client, preserve the failed attempt and end its native stream.
		if !committed && a.ShouldFailover(streamError.Status) {
			return result, a.Failover(streamError.Status, body, false, false)
		}
		err = errors.Join(err, a.c.GoogleError(streamError.Status, streamError.Message))
	}
	return result, err
}
