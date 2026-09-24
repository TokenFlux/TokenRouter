package httpapi

import (
	"context"
	"io"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/gin-gonic/gin"
)

// GoogleOutput 只负责本次 HTTP 输出；平台准备器通过同步端口写入。
type GoogleOutput struct{ Context *gin.Context }

func (o GoogleOutput) RequestContext() context.Context { return o.Context.Request.Context() }
func (o GoogleOutput) GetHeader(key string) string {
	if o.Context == nil {
		return ""
	}
	return o.Context.GetHeader(key)
}
func (o GoogleOutput) Header(key, value string) { o.Context.Header(key, value) }

// WriteHeaders 在 HTTP 边界应用既有响应头过滤器。
func (o GoogleOutput) WriteHeaders(dst, src http.Header, filter *egress.CompiledHeaderFilter) {
	egressprovider.WriteFilteredHeaders(dst, src, filter)
}

// Raw 保留静态 upstream 透传错误的状态、Content-Type 和写入顺序。
func (o GoogleOutput) Raw(status int, content string, body []byte) {
	o.Context.Header("Content-Type", content)
	o.Context.Status(status)
	_, _ = o.Context.Writer.Write(body)
}
func (o GoogleOutput) Sink() upstream.OutputSink { return ResponseSink{Writer: o.Context.Writer} }
func (o GoogleOutput) Commit()                   { MarkResponseCommitted(o.Context) }
func (o GoogleOutput) Count(n int)               { o.Context.JSON(200, map[string]any{"totalTokens": n}) }
func (o GoogleOutput) GeminiErrorBody(status int, content string, body []byte) {
	WriteForwardGeminiErrorBody(o.Context, status, content, body, o.Commit)
}
func (o GoogleOutput) ReadBody(body io.Reader, limit int64) ([]byte, error) {
	return ReadUpstreamResponseBody(body, limit, o.Context, OpenAIResponseTooLarge)
}
func (o GoogleOutput) Observe(value ops.OpsUpstreamErrorEvent) {
	AppendOpsUpstreamError(o.Context, value)
}
func (o GoogleOutput) SetError(status int, message, detail string) {
	SetOpsUpstreamError(o.Context, status, message, detail)
}
func (o GoogleOutput) FeatureDenied() {
	MarkOpsClientBusinessLimited(o.Context, OpsClientBusinessLimitedReasonLocalFeatureGate)
}

// GoogleBoundary 保留两个平台的错误形状，平台选择对应方法，不共享可变业务状态。
type GoogleBoundary struct {
	*GeminiOutput
	Antigravity    *AntigravityOutput
	UseAntigravity bool
}

func (o GoogleBoundary) ClaudeError(status int, kind, message string) error {
	if o.UseAntigravity {
		return o.Antigravity.ClaudeError(status, kind, message)
	}
	return o.GeminiOutput.ClaudeError(status, kind, message)
}
func (o GoogleBoundary) GoogleError(status int, message string) error {
	if o.UseAntigravity {
		return o.Antigravity.GoogleError(status, message)
	}
	return o.GeminiOutput.GoogleError(status, message)
}
func (o GoogleBoundary) AntigravityCompatError(status int, kind, message string) error {
	return o.Antigravity.AntigravityCompatError(status, kind, message)
}
func (o GoogleBoundary) MappedAntigravityCompatError(a *provider.ExecutionAccount, status int, id string, body []byte) error {
	return o.Antigravity.MappedAntigravityCompatError(a, status, id, body)
}
func (o GoogleBoundary) MappedClaudeError(a *provider.ExecutionAccount, status int, id string, body []byte) error {
	return o.Antigravity.MappedClaudeError(a, status, id, body)
}
func (o GoogleBoundary) MapAntigravityCollectionError(err error) error {
	return o.Antigravity.MapAntigravityCollectionError(err)
}
func NewGoogleBoundary(c *gin.Context, options googleforward.Options, antigravity bool) GoogleBoundary {
	common := GoogleOutput{Context: c}
	return GoogleBoundary{
		GeminiOutput:   &GeminiOutput{GoogleOutput: common, Options: options},
		Antigravity:    &AntigravityOutput{GoogleOutput: common, Options: options},
		UseAntigravity: antigravity,
	}
}
