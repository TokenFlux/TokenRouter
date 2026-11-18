package httpapi

import (
	"strconv"

	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
)

// request_parse_log.go validates OpenAI-compatible stream fields and logs
// malformed request bodies using bounded, escaped excerpts and the full byte
// length.
// Invalid JSON can result from malformed values or a body cut off in transit.
// Parse diagnostics retain the byte length and bounded beginning and ending
// excerpts, escaping their contents so each field remains one log value.

// Request body parsing constants bound logged excerpts and provide consistent
// client stream-validation error text across OpenAI-compatible HTTP entries.
const (
	parseFailureSnippetLen        = 256
	InvalidStreamFieldTypeMessage = "invalid stream field type"
)

// LogRequestBodyParseFailure logs parsing diagnostics with escaped body
// excerpts and total byte length. A nil logger skips reporting; a nil error
// asks requeststate to diagnose malformed JSON. Large bodies retain separate
// bounded beginning and ending excerpts.
func LogRequestBodyParseFailure(reqLog *zap.Logger, body []byte, err error) {
	if reqLog == nil {
		return
	}
	if err == nil {
		err = requeststate.DescribeInvalidJSON(body)
	}

	head := body
	var tail []byte
	// Keep both ends when the full body exceeds the excerpt budget; the length
	// field still reports the original byte count.
	if len(body) > parseFailureSnippetLen {
		head = body[:parseFailureSnippetLen]
		tail = body[len(body)-parseFailureSnippetLen:]
	}

	fields := []zap.Field{
		zap.Error(err),
		zap.Int("body_len", len(body)),
		zap.String("body_head", SanitizeBodySnippet(head)),
	}
	if len(tail) > 0 {
		fields = append(fields, zap.String("body_tail", SanitizeBodySnippet(tail)))
	}
	reqLog.Warn("parse request body failed", fields...)
}

// SanitizeBodySnippet returns a quoted string with escaped controls and invalid
// UTF-8.
func SanitizeBodySnippet(b []byte) string {
	return strconv.Quote(string(b))
}

// ParseOpenAICompatibleStream returns client stream intent and whether its
// JSON field has a supported type. A missing field means false with
// valid=true; explicit null, strings and numbers return false with
// valid=false.
func ParseOpenAICompatibleStream(body []byte) (bool, bool) {
	v := gjson.GetBytes(body, "stream")
	if v.Exists() && v.Type != gjson.True && v.Type != gjson.False {
		return false, false
	}
	return v.Bool(), true
}
