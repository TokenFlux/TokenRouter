package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// request_read_log.go classifies request body read failures and records
// bounded diagnostic fields with request-scoped logging.
// Body-read failures may contain user data in their error text. Read
// diagnostics use bounded category names, declared content length and content
// encoding, allowing size, decoding and disconnect failures to be investigated
// with no payload copied into these fields.

// LogRequestBodyReadFailure records a read-error category, declared content
// length and encoding category. Nil errors or loggers skip reporting; a nil
// request supplies unknown length and identity encoding. Error text and body
// contents are excluded from these fields.
func LogRequestBodyReadFailure(reqLog *zap.Logger, req *http.Request, err error) {
	if reqLog == nil || err == nil {
		return
	}

	contentLength := int64(-1)
	contentEncoding := "identity"
	if req != nil {
		contentLength = req.ContentLength
		contentEncoding = requestContentEncodingCategory(req.Header.Get("Content-Encoding"))
	}

	reqLog.Warn("read request body failed",
		zap.String("error_kind", requestBodyReadErrorKind(err)),
		zap.String("content_encoding", contentEncoding),
		zap.Int64("content_length", contentLength),
	)
}

// requestContentEncodingCategory maps trimmed case-insensitive encoding names
// to bounded diagnostic labels. Empty values select identity, x-gzip shares
// gzip, and unrecognized values become other so arbitrary header contents do
// not enter the log category.
func requestContentEncodingCategory(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "identity":
		return "identity"
	case "gzip", "x-gzip":
		return "gzip"
	case "zstd":
		return "zstd"
	case "deflate":
		return "deflate"
	// Group unknown encoding values under one label so arbitrary client header
	// text cannot become diagnostic field content.
	default:
		return "other"
	}
}

// requestBodyReadErrorKind classifies wrapped read errors into stable
// diagnostic labels. Size-limit errors precede decoding-message checks, then
// cancellation and connection errors, truncated bodies and transport errors;
// remaining failures use io_read, while nil returns none.
func requestBodyReadErrorKind(err error) string {
	if err == nil {
		return "none"
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return "max_bytes"
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "decode content-encoding") {
		if strings.Contains(lower, "unsupported content-encoding") {
			return "unsupported_content_encoding"
		}
		return "decode_content_encoding"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return "client_disconnect"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "truncated_body"
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return "transport"
	}
	// Use a stable catch-all category after known failure checks; read error
	// strings may include private request content.
	return "io_read"
}

// RequestLogger derives a logger with the supplied component and additional
// fields. A request context supplies the base when available; otherwise the
// global logger is used. An empty component leaves only the caller-provided
// fields.
func RequestLogger(c *gin.Context, component string, fields ...zap.Field) *zap.Logger {
	base := logger.L()
	if c != nil && c.Request != nil {
		base = logger.FromContext(c.Request.Context())
	}

	if component != "" {
		fields = append([]zap.Field{zap.String("component", component)}, fields...)
	}
	return base.With(fields...)
}

// BodyLimitLabel formats a byte limit for client error messages. Values of at
// least one binary megabyte use truncated integer MiB magnitude with the
// existing MB suffix; smaller values retain their byte magnitude.
func BodyLimitLabel(limit int64) string {
	// Truncate binary megabyte magnitude while retaining the existing MB
	// suffix.
	if limit >= 1024*1024 {
		return fmt.Sprintf("%dMB", limit/(1024*1024))
	}
	return fmt.Sprintf("%dB", limit)
}

// BodyTooLargeMessage returns the shared request-size error using
// BodyLimitLabel formatting.
func BodyTooLargeMessage(limit int64) string {
	return fmt.Sprintf("Request body too large, limit is %s", BodyLimitLabel(limit))
}
