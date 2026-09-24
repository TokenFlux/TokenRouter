package httpapi

import (
	"io"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MessageForwardBoundary 独占当前 HTTP 写入和观察，不持有账号、设置或重试规则。
type MessageForwardBoundary struct {
	context *gin.Context
	filter  *egress.CompiledHeaderFilter
}

func NewMessageForwardBoundary(c *gin.Context, filter *egress.CompiledHeaderFilter) *MessageForwardBoundary {
	return &MessageForwardBoundary{context: c, filter: filter}
}

func (b *MessageForwardBoundary) Present() bool { return b != nil && b.context != nil }

func (b *MessageForwardBoundary) RequestPresent() bool {
	return b.Present() && b.context.Request != nil
}

func (b *MessageForwardBoundary) HasHeaderFilter() bool { return b.filter != nil }

func (b *MessageForwardBoundary) RequestHeaders() http.Header {
	if !b.RequestPresent() {
		return http.Header{}
	}
	return b.context.Request.Header
}

func (b *MessageForwardBoundary) Sink() upstream.OutputSink {
	return ResponseSink{Writer: b.context.Writer}
}

func (b *MessageForwardBoundary) SearchOutput() searchtools.Output {
	return SearchOutput{Context: b.context}
}

// ConversionOutput 复用现有输出器，工具恢复只读取本次尝试的状态。
func (b *MessageForwardBoundary) ConversionOutput(responses bool, state *messageforward.AttemptState) forward.Output {
	return ForwardConversionOutput{
		Context: b.context, Filter: b.filter, Responses: responses,
		Reverse: func(body []byte) []byte {
			return anthropic.RestoreToolNamesInBytes(body, state.ToolNames)
		},
		Commit: b.Commit,
		Diagnostic: func(level, message string, err error, requestID, event string) {
			fields := []zap.Field{zap.String("request_id", requestID)}
			if err != nil {
				fields = append(fields, zap.Error(err))
			}
			if event != "" {
				fields = append(fields, zap.String("event_type", event))
			}
			if level == "info" {
				logging.L().Info(message, fields...)
			} else {
				logging.L().Warn(message, fields...)
			}
		},
	}
}

func (b *MessageForwardBoundary) ReadResponseBody(reader io.Reader, limit int64, kind messageforward.BodyKind) ([]byte, error) {
	tooLarge := AnthropicResponseTooLarge
	if kind == messageforward.CountBody {
		tooLarge = func(c *gin.Context) {
			WriteForwardCountError(c, http.StatusBadGateway, "upstream_error", "Upstream response too large")
		}
	}
	return ReadUpstreamResponseBody(reader, limit, b.context, tooLarge)
}

func (b *MessageForwardBoundary) Size() int     { return b.context.Writer.Size() }
func (b *MessageForwardBoundary) Written() bool { return b.context.Writer.Written() }
func (b *MessageForwardBoundary) Commit()       { MarkResponseCommitted(b.context) }

func (b *MessageForwardBoundary) GenericError() {
	WriteForwardMessageGenericError(b.context, b.Commit)
}

func (b *MessageForwardBoundary) MessageError(status int, kind, message string) {
	(AnthropicForwardErrorOutput{Context: b.context}).Message(status, kind, message)
}

func (b *MessageForwardBoundary) RawError(status int, body []byte) {
	(AnthropicForwardErrorOutput{Context: b.context}).Raw(status, body)
}

func (b *MessageForwardBoundary) CountError(status int, kind, message string) {
	WriteForwardCountError(b.context, status, kind, message)
}

func (b *MessageForwardBoundary) CountSuccess(status int, headers map[string][]string, body []byte, passthrough bool) {
	if passthrough {
		WriteAnthropicPassthroughHeaders(b.context.Writer.Header(), headers, b.filter)
	}
	WriteForwardCountSuccess(b.context, status, headers, body, passthrough)
}

func (b *MessageForwardBoundary) MarkPassthrough() {
	if b.Present() {
		b.context.Set("anthropic_passthrough", true)
	}
}

func (b *MessageForwardBoundary) ServiceTier() string {
	return ObservedUpstreamResponseServiceTier(b.context)
}

func (b *MessageForwardBoundary) SetError(status int, message, detail string) {
	SetOpsUpstreamError(b.context, status, message, detail)
}

func (b *MessageForwardBoundary) Observe(notice forward.Notice) {
	AppendOpsUpstreamError(b.context, ops.OpsUpstreamErrorEvent{
		UpstreamURL: notice.UpstreamURL, Passthrough: notice.Passthrough,
		Platform: notice.Platform, AccountID: notice.AccountID, AccountName: notice.AccountName,
		UpstreamStatusCode: notice.UpstreamStatusCode, UpstreamRequestID: notice.UpstreamRequestID,
		Kind: notice.Kind, Message: notice.Message, Detail: notice.Detail,
	})
}

func (b *MessageForwardBoundary) MatchRule(platform string, status int, body []byte) *errorpolicy.ErrorPassthroughRule {
	rules := BoundErrorPassthroughService(b.context)
	if rules == nil {
		return nil
	}
	return rules.MatchRule(platform, status, body)
}

func (b *MessageForwardBoundary) SkipMonitoring() {
	b.context.Set(OpsSkipPassthroughKey, true)
}

func (b *MessageForwardBoundary) WriteHeaders(dst, src http.Header, passthrough bool) {
	if passthrough {
		WriteAnthropicPassthroughHeaders(dst, src, b.filter)
		return
	}
	egressprovider.WriteFilteredHeaders(dst, src, b.filter)
}

var _ messageforward.HTTPBoundary = (*MessageForwardBoundary)(nil)
