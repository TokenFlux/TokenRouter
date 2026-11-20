package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// openai_response_diagnostics.go captures Codex rejection diagnostics and
// writes structured upstream client errors.
// Codex client-admission diagnostics describe the rejected request using a
// fixed header whitelist and a hashed prompt-cache key. The captured query
// remains the request's trimmed RawQuery, while final deterministic client
// errors use the separately supplied message and selected upstream
// classification fields.

// Rejection diagnostics cap each whitelisted header value. Client error
// envelopes use these fallback strings when upstream classification or
// message fields are absent.
const (
	// codexCLIOnlyHeaderValueMaxBytes bounds one recorded header excerpt so
	// rejection diagnostics can retain selected values with a finite byte limit.
	codexCLIOnlyHeaderValueMaxBytes = 256

	// openAIUpstreamClientErrorFallbackType and
	// openAIUpstreamClientErrorFallbackMessage supply envelope defaults when the
	// selected upstream type or supplied client message is empty.
	// openAIUpstreamClientErrorFallbackType names the default rejection category.
	openAIUpstreamClientErrorFallbackType = "invalid_request_error"

	// openAIUpstreamClientErrorFallbackMessage is used when the upstream message is empty.
	openAIUpstreamClientErrorFallbackMessage = "Upstream rejected the request"
)

// codexCLIOnlyDebugHeaderWhitelist selects client and routing headers for
// rejection diagnostics;
// SnapshotCodexRejectedHeaders trims values and bounds each captured excerpt.
var codexCLIOnlyDebugHeaderWhitelist = []string{
	"User-Agent",
	"Content-Type",
	"Accept",
	"Accept-Language",
	"OpenAI-Beta",
	"Originator",
	"Session_ID",
	"Conversation_ID",
	"X-Request-ID",
	"X-Client-Request-ID",
	"X-Forwarded-For",
	"X-Real-IP",
}

// AppendCodexRejectedRequestFields appends request metadata, selected headers
// and body size to diagnostic fields. Missing context or request leaves fields
// unchanged. Prompt-cache keys are hashed, but RawQuery and selected header
// values retain request text after trimming, so callers account for that
// content when choosing log destinations.
func AppendCodexRejectedRequestFields(fields []zap.Field, c *gin.Context, body []byte) []zap.Field {
	if c == nil || c.Request == nil {
		return fields
	}

	req := c.Request
	requestModel, requestStream, promptCacheKey := requeststate.OpenAIRequestMetaFromBody(body)
	fields = append(fields,
		zap.String("request_method", strings.TrimSpace(req.Method)),
		zap.String("request_path", strings.TrimSpace(req.URL.Path)),
		zap.String("request_query", strings.TrimSpace(req.URL.RawQuery)),
		zap.String("request_host", strings.TrimSpace(req.Host)),
		zap.String("request_client_ip", strings.TrimSpace(clientip.GetClientIP(c))),
		zap.String("request_remote_addr", strings.TrimSpace(req.RemoteAddr)),
		zap.String("request_user_agent", strings.TrimSpace(req.Header.Get("User-Agent"))),
		zap.String("request_content_type", strings.TrimSpace(req.Header.Get("Content-Type"))),
		zap.Int64("request_content_length", req.ContentLength),
		zap.Bool("request_stream", requestStream),
	)
	if requestModel != "" {
		fields = append(fields, zap.String("request_model", requestModel))
	}
	if promptCacheKey != "" {
		fields = append(fields, zap.String("request_prompt_cache_key_sha256", upstream.HashSensitiveValueForLog(promptCacheKey)))
	}

	if headers := SnapshotCodexRejectedHeaders(req.Header); len(headers) > 0 {
		fields = append(fields, zap.Any("request_headers", headers))
	}
	fields = append(fields, zap.Int("request_body_size", len(body)))
	return fields
}

// SnapshotCodexRejectedHeaders captures trimmed, nonempty whitelisted header
// values under lowercase names. Nil or empty headers return nil; each value is
// UTF-8 truncated to codexCLIOnlyHeaderValueMaxBytes, and the returned map
// owns its entries.
func SnapshotCodexRejectedHeaders(header http.Header) map[string]string {
	if len(header) == 0 {
		return nil
	}
	result := make(map[string]string, len(codexCLIOnlyDebugHeaderWhitelist))
	for _, key := range codexCLIOnlyDebugHeaderWhitelist {
		value := strings.TrimSpace(header.Get(key))
		if value == "" {
			continue
		}
		// Bound each whitelisted value while retaining complete UTF-8 characters.
		result[strings.ToLower(key)] = logredact.TruncateUTF8(value, codexCLIOnlyHeaderValueMaxBytes)
	}
	return result
}

// IsOpenAIDeterministicClientError reports whether a 400 was classified as
// nonretryable. The caller supplies the relevant failover decision, because
// status alone cannot distinguish request rejection from provider or pool
// recovery.
func IsOpenAIDeterministicClientError(statusCode int, shouldFailover bool) bool {
	return statusCode == http.StatusBadRequest && !shouldFailover
}

// WriteOpenAIUpstreamClientError writes a deterministic client error from
// selected upstream fields and upstreamMsg, switching to a terminal SSE error
// event when event-stream framing is already committed. Callers sanitize
// sensitive data in the message before entry; the upstream body supplies type,
// code and param, and absent classification or message selects the standard
// fallback strings.
func WriteOpenAIUpstreamClientError(c *gin.Context, statusCode int, body []byte, upstreamMsg string) {
	errType := openAIUpstreamClientErrorFallbackType
	if parsedType := strings.TrimSpace(gjson.GetBytes(body, "error.type").String()); parsedType != "" {
		errType = parsedType
	}
	code := strings.TrimSpace(upstream.ExtractErrorCode(body))
	message := strings.TrimSpace(upstreamMsg)
	if message == "" {
		message = openAIUpstreamClientErrorFallbackMessage
	}
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, statusCode, errType, code, message, true, false)
		return
	}
	errorPayload := gin.H{"type": errType}
	if code != "" {
		errorPayload["code"] = code
	}
	if param := strings.TrimSpace(gjson.GetBytes(body, "error.param").String()); param != "" {
		errorPayload["param"] = param
	}
	errorPayload["message"] = message

	c.JSON(statusCode, gin.H{"error": errorPayload})
}
