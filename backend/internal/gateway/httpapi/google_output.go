package httpapi

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// google_output.go connects Google platform execution to synchronous HTTP
// output, headers, and request observations.
// Gemini and Antigravity execution write synchronously through the active Gin
// response. Their shared HTTP adapter exposes request headers, filtered
// upstream headers and Ops observation, while the selected error adapter
// determines the client error envelope for each platform.

// GoogleOutput writes synchronous platform output to the HTTP response.
type GoogleOutput struct {
	Context *gin.Context // active HTTP request
}

// GoogleBoundary selects Gemini or Antigravity output methods and their respective error formats.
type GoogleBoundary struct {
	*GeminiOutput                    // default Gemini error methods
	Antigravity   *AntigravityOutput // Antigravity error methods
	// UseAntigravity selects ClaudeError and GoogleError. Methods explicitly
	// naming Antigravity always use Antigravity regardless of this selector.
	UseAntigravity bool
}

// RequestContext returns the active HTTP request context to upstream
// preparation.
func (o GoogleOutput) RequestContext() context.Context { return o.Context.Request.Context() }

// APIKeyID returns the authenticated Key ID for Gemini tool replay scoping.
func (o GoogleOutput) APIKeyID() int64 { return APIKeyIDFromContext(o.Context) }

// GetHeader reads an inbound HTTP header from the current request. A nil
// Context returns an empty string, allowing callers that gather optional
// header signals to omit them.
func (o GoogleOutput) GetHeader(key string) string {
	if o.Context == nil {
		return ""
	}
	return o.Context.GetHeader(key)
}

// Header sets a response header on the active Gin context.
func (o GoogleOutput) Header(key, value string) { o.Context.Header(key, value) }

// WriteHeaders copies upstream headers through the supplied compiled egress
// filter. Nil handling and the filter's default header selection come from
// WriteFilteredHeaders, keeping platform output consistent with other HTTP
// adapters.
func (o GoogleOutput) WriteHeaders(dst, src http.Header, filter *egress.CompiledHeaderFilter) {
	egressprovider.WriteFilteredHeaders(dst, src, filter)
}

// Raw sets content type and status, then writes the supplied bytes. It
// discards the write result; callers that need delivery-error reporting use
// the synchronous Sink interface instead.
func (o GoogleOutput) Raw(status int, content string, body []byte) {
	o.Context.Header("Content-Type", content)
	o.Context.Status(status)
	// Raw has no error return; client write failure leaves delivery diagnosis
	// to the request's output state and the platform execution that owns presentation.
	_, _ = o.Context.Writer.Write(body)
}

// Sink exposes the current response writer through the synchronous upstream
// interface.
func (o GoogleOutput) Sink() upstream.OutputSink { return NewResponseSink(o.Context) }

// Commit records the protocol-response marker used by outer error handling.
// It changes request state without writing or flushing bytes, so the marker
// alone does not establish client delivery.
func (o GoogleOutput) Commit() { MarkResponseCommitted(o.Context) }

// Count writes the local Gemini token total as a JSON response.
func (o GoogleOutput) Count(n int) { o.Context.JSON(200, map[string]any{"totalTokens": n}) }

// GeminiErrorBody marks the request committed before writing the supplied
// native error bytes. Outer error handling uses that marker to suppress another
// response even when the client write fails.
func (o GoogleOutput) GeminiErrorBody(status int, content string, body []byte) {
	if !o.Context.Writer.Written() {
		if beginGoogleError(o.Context) {
			o.Context.Data(status, content, body)
		}
		return
	}
	writeGoogleErrorBody(o.Context, status, body)
}

// ReadBody performs a bounded upstream read with OpenAI-format oversize
// output. The reader remains caller-owned, and the returned error determines
// whether preparation or outer orchestration can continue.
func (o GoogleOutput) ReadBody(body io.Reader, limit int64) ([]byte, error) {
	return ReadUpstreamResponseBody(body, limit, o.Context, OpenAIResponseTooLarge)
}

// Observe appends one platform error observation to this HTTP request.
func (o GoogleOutput) Observe(value ops.OpsUpstreamErrorEvent) {
	AppendOpsUpstreamError(o.Context, value)
}

// SetError stores the latest upstream failure details for request diagnostics.
func (o GoogleOutput) SetError(status int, message, detail string) {
	SetOpsUpstreamError(o.Context, status, message, detail)
}

// FeatureDenied marks local feature admission as an Ops business limitation.
func (o GoogleOutput) FeatureDenied() {
	MarkOpsClientBusinessLimited(o.Context, OpsClientBusinessLimitedReasonLocalFeatureGate)
}

// NewGoogleBoundary creates Gemini and Antigravity output sharing the supplied
// Gin context and fixed options. The selector controls general Claude and
// Google errors; platform-specific methods remain available on their
// respective adapters.
func NewGoogleBoundary(c *gin.Context, options googleforward.Options, antigravity bool) GoogleBoundary {
	common := GoogleOutput{Context: c}
	return GoogleBoundary{
		GeminiOutput:   &GeminiOutput{GoogleOutput: common, Options: options},
		Antigravity:    &AntigravityOutput{GoogleOutput: common, Options: options},
		UseAntigravity: antigravity,
	}
}

// ClaudeError writes a Messages envelope using the selected platform adapter.
// Each implementation marks the response committed and returns a message error
// after synchronous presentation to the client.
func (o GoogleBoundary) ClaudeError(status int, kind, message string) error {
	if o.UseAntigravity {
		return o.Antigravity.ClaudeError(status, kind, message)
	}
	return o.GeminiOutput.ClaudeError(status, kind, message)
}

// GoogleError writes a native Google error using the selected platform
// adapter. Gemini and Antigravity have different status-name mappings, so this
// method retains the selection made for the attempt.
func (o GoogleBoundary) GoogleError(status int, message string) error {
	if o.UseAntigravity {
		return o.Antigravity.GoogleError(status, message)
	}
	return o.GeminiOutput.GoogleError(status, message)
}

// AntigravityCompatError delegates OpenAI-compatible error output to the
// Antigravity adapter. This named operation uses that adapter regardless of
// UseAntigravity and returns its committed presentation error to the caller.
func (o GoogleBoundary) AntigravityCompatError(status int, kind, message string) error {
	return o.Antigravity.AntigravityCompatError(status, kind, message)
}

// MappedAntigravityCompatError records and presents Antigravity upstream
// diagnostics in the compatible envelope. This named operation uses
// Antigravity output regardless of UseAntigravity, preserving its safe message
// and status rules.
func (o GoogleBoundary) MappedAntigravityCompatError(a *gatewayadapter.ExecutionProvider, status int, id string, body []byte) error {
	return o.Antigravity.MappedAntigravityCompatError(a, status, id, body)
}

// MappedClaudeError delegates upstream failure presentation to the Antigravity
// Messages adapter. It uses that adapter regardless of UseAntigravity,
// retaining Antigravity rule matching and its client message defaults.
func (o GoogleBoundary) MappedClaudeError(a *gatewayadapter.ExecutionProvider, status int, id string, body []byte) error {
	return o.Antigravity.MappedClaudeError(a, status, id, body)
}

// MapAntigravityCollectionError delegates collected-stream error
// classification to Antigravity output. Failover and cancellation errors can
// pass through; other errors may write a compatible response, so callers
// handle the returned error.
func (o GoogleBoundary) MapAntigravityCollectionError(err error) error {
	return o.Antigravity.MapAntigravityCollectionError(err)
}
