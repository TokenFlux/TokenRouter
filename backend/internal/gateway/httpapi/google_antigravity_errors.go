package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// google_antigravity_errors.go presents Antigravity failures in the client
// protocol and records upstream diagnostic evidence.
// Messages error output applies administrator rules after recording
// upstream diagnostics, while compatible output uses its platform-safe message
// selection and status mapping. Native Google output retains a smaller status-
// name table.
//
// Error observation keeps provider and upstream request identity separate from
// the client envelope. Mapped Messages output records those observations
// before rule matching and returns a diagnostic error after presentation.
// Collected compatible-stream failures preserve failover and cancellation
// signals for orchestration; parse, timeout and line-size failures instead
// receive committed protocol error output when this adapter handles them.

// AntigravityOutput holds the current HTTP request and fixed output options.
type AntigravityOutput struct {
	GoogleOutput                       // synchronous request output
	Options      googleforward.Options // fixed diagnostic output settings
}

// ClaudeError marks final presentation committed and writes an Anthropic error
// envelope, or an Anthropic SSE error frame when event-stream framing is
// already committed. It returns the supplied message as an error so request
// orchestration can still report failure after synchronous client output.
func (s *AntigravityOutput) ClaudeError(status int, errType, message string) error {
	c := s.Context

	MarkResponseCommitted(c)
	WriteAnthropicStreamError(c, status, errType, "", message, CommittedSSE(c), MarkOpsStreamError)
	return fmt.Errorf("%s", message)
}

// MappedClaudeError records sanitized upstream diagnostics and presents an
// Antigravity failure as a Messages error. The caller supplies a selected
// provider, response status and upstream request identity. Commit state is
// installed before diagnostic recording and client output.
//
// A matching response rule determines status, type and message. Otherwise HTTP
// status selects platform defaults; bad requests use Antigravity's safe
// passthrough selector, while authentication, permission and other failures
// use their respective messages. Committed event streams emit a terminal
// Anthropic SSE error frame instead of JSON, and the returned error keeps the
// upstream status and sanitized diagnostic message for the request owner.
func (s *AntigravityOutput) MappedClaudeError(provider *gatewayprovider.ExecutionProvider, upstreamStatus int, upstreamRequestID string, body []byte) error {
	c := s.Context

	MarkResponseCommitted(c)
	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	logBody, maxBytes := s.Options.LogConfig()
	upstreamDetail := s.Options.ErrorDetail(body)
	SetOpsUpstreamError(c, upstreamStatus, upstreamMsg, upstreamDetail)
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform: provider.Record.Platform,

		ProviderID: provider.Record.ID,

		ProviderName: provider.Record.Name,

		UpstreamStatusCode: upstreamStatus,

		UpstreamRequestID: upstreamRequestID,

		Kind: "http_error",

		Message: upstreamMsg,

		Detail: upstreamDetail,
	})

	// Enabled diagnostic logging keeps the bounded upstream body separate from
	// the safe client message chosen by response rules or Antigravity defaults.
	if logBody {
		logging.LegacyPrintf("service.antigravity_gateway", "[antigravity-Forward] upstream_error status=%d body=%s", upstreamStatus, logredact.TruncateLine(body, maxBytes))
	}

	if ptStatus, ptErrType, ptErrMsg, matched := ApplyErrorPassthroughRule(
		c, provider.Record.Platform, upstreamStatus, body,
		0, "", "",
	); matched {
		WriteAnthropicStreamError(c, ptStatus, ptErrType, "", ptErrMsg, CommittedSSE(c), MarkOpsStreamError)
		if upstreamMsg == "" {
			return fmt.Errorf("upstream error: %d", upstreamStatus)
		}
		return fmt.Errorf("upstream error: %d message=%s", upstreamStatus, upstreamMsg)
	}

	var statusCode int
	var errType, errMsg string

	switch upstreamStatus {
	case 400:
		statusCode = http.StatusBadRequest
		errType = "invalid_request_error"
		errMsg = antigravity.GetPassthroughOrDefault(upstreamMsg, "Invalid request")
	case 401:
		statusCode = http.StatusBadGateway
		errType = "authentication_error"
		errMsg = "Upstream authentication failed"
	case 403:
		statusCode = http.StatusBadGateway
		errType = "permission_error"
		errMsg = "Upstream access forbidden"
	case 429:
		statusCode = http.StatusTooManyRequests
		errType = "rate_limit_error"
		errMsg = "Upstream rate limit exceeded"
	case 529:
		statusCode = http.StatusServiceUnavailable
		errType = "overloaded_error"
		errMsg = "Upstream service overloaded"
	default:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream request failed"
	}

	WriteAnthropicStreamError(c, statusCode, errType, "", errMsg, CommittedSSE(c), MarkOpsStreamError)
	if upstreamMsg == "" {
		return fmt.Errorf("upstream error: %d", upstreamStatus)
	}
	return fmt.Errorf("upstream error: %d message=%s", upstreamStatus, upstreamMsg)
}

// GoogleError commits and writes Antigravity's native Google error envelope.
// Its explicit status table maps unknown HTTP values to UNKNOWN, and the
// supplied message is returned as an error after output.
func (s *AntigravityOutput) GoogleError(status int, message string) error {
	c := s.Context

	MarkResponseCommitted(c)
	statusStr := "UNKNOWN"
	switch status {
	case 400:
		statusStr = "INVALID_ARGUMENT"
	case 404:
		statusStr = "NOT_FOUND"
	case 429:
		statusStr = "RESOURCE_EXHAUSTED"
	case 500:
		statusStr = "INTERNAL"
	case 502, 503:
		statusStr = "UNAVAILABLE"
	}

	body, _ := json.Marshal(gin.H{
		"error": gin.H{
			"code":    status,
			"message": message,
			"status":  statusStr,
		},
	})
	writeGoogleErrorBody(c, status, body)
	return fmt.Errorf("%s", message)
}

// AntigravityCompatError commits and writes an OpenAI-compatible error with
// nil param and code fields, or emits a terminal SSE error when event-stream
// framing is already committed. The returned ordinary error contains the
// message, so final presentation and execution failure remain visible
// independently.
func (s *AntigravityOutput) AntigravityCompatError(
	status int,
	errType string,
	message string,
) error {
	c := s.Context

	MarkResponseCommitted(c)
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().StreamError(c, status, errType, message, true)
		return errors.New(message)
	}
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
			"param":   nil,
			"code":    nil,
		},
	})
	return errors.New(message)
}

// MappedAntigravityCompatError records the sanitized Google-platform message
// and provider identity before compatible error output. It applies MapStatus
// and Antigravity's safe passthrough selector, marks the response committed,
// emits SSE when event-stream framing is already committed, and returns the
// diagnostic failure.
func (s *AntigravityOutput) MappedAntigravityCompatError(
	provider *gatewayprovider.ExecutionProvider,
	upstreamStatus int,
	upstreamRequestID string,
	body []byte,
) error {
	c := s.Context

	MarkResponseCommitted(c)
	message := logredact.SanitizeUpstreamQueries(strings.TrimSpace(google.ExtractPlatformMessage(body)))
	SetOpsUpstreamError(c, upstreamStatus, message, s.Options.ErrorDetail(body))
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform: provider.Record.Platform,

		ProviderID: provider.Record.ID,

		ProviderName: provider.Record.Name,

		UpstreamStatusCode: upstreamStatus,

		UpstreamRequestID: upstreamRequestID,

		Kind: "http_error",

		Message: message,
	})
	mappedStatus := protocolforward.MapStatus(upstreamStatus)
	clientMessage := antigravity.GetPassthroughOrDefault(message, "Upstream request failed")
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().StreamError(c, mappedStatus, "upstream_error", clientMessage, true)
		return fmt.Errorf("upstream error: %d %s", upstreamStatus, message)
	}
	c.JSON(mappedStatus, gin.H{
		"error": gin.H{
			"message": clientMessage,

			"type": "upstream_error",

			"param": nil,

			"code": nil,
		},
	})
	return fmt.Errorf("upstream error: %d %s", upstreamStatus, message)
}

// MapAntigravityCollectionError maps a nonnil collected-stream failure to
// compatible client output. Failover, cancellation and deadline errors pass
// through for orchestration; interval timeout, scanner overflow and other
// parse failures write their specific committed envelope.
func (s *AntigravityOutput) MapAntigravityCollectionError(err error) error {
	// Preserve outer-loop and cancellation decisions before presenting parse failures,
	// because writing an error response here would close their available retry path.
	if _, ok := errors.AsType[*protocolforward.UpstreamFailoverError](err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if strings.Contains(err.Error(), "stream data interval timeout") {
		return s.AntigravityCompatError(http.StatusBadGateway, "upstream_timeout", "Upstream stream data interval timeout")
	}
	if errors.Is(err, bufio.ErrTooLong) {
		return s.AntigravityCompatError(http.StatusBadGateway, "response_too_large", "Upstream response line too long")
	}
	return s.AntigravityCompatError(http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
}
