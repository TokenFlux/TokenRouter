package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// anthropic_failover_error.go writes terminal Anthropic errors after provider
// recovery is exhausted. An exhausted upstream attempt needs a terminal client
// error even when its provider uses credentials unrelated to the client's
// gateway key. Default Anthropic mapping turns upstream authentication and
// server failures into gateway errors while retaining distinct rate-limit and
// overload classifications.
//
// Silent rejection takes precedence over administrator rules. Other response
// bodies can match a rule that chooses the client status and message before
// default mapping runs. Framing follows the caller's output state: an untouched
// response can use an HTTP error, while a started stream needs an appended
// protocol error, including a failed Responses event for converted Responses.

// WriteAnthropicFailover writes an exhausted attempt's terminal client failure.
// Callers provide a nonnil failure, usable context, and silent detector, and set
// streamStarted from their output state. They decide whether another attempt is
// eligible before invoking this terminal writer.
//
// Silent detection selects the supplied silentMessage before rule matching.
// A matched rule can retain or replace the upstream status and extracted message;
// the result still uses the terminal protocol's error envelope. Unmatched errors
// record upstream details for Ops before default Anthropic mapping selects a
// client-safe classification. Started Responses streams receive response.failed
// through the shared protocol writer.
func WriteAnthropicFailover(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError, platform string, streamStarted bool, rules ErrorRuleMatcher, silent func([]byte) bool, silentMessage string) {
	statusCode := failoverErr.StatusCode
	responseBody := failoverErr.ResponseBody
	// The supplied silent message also becomes the Ops summary on this path.
	// Return before rule matching so passthrough cannot substitute upstream error details.
	if silent(responseBody) {
		SetOpsUpstreamError(c, statusCode, silentMessage, "")
		writeAnthropicFailure(c, http.StatusBadGateway, "upstream_error", silentMessage, streamStarted)
		return
	}

	// Apply the first configured match before default translation; its monitoring
	// flag belongs to this request even when the rule keeps the upstream status.
	if rules != nil && len(responseBody) > 0 {
		if rule := rules.MatchRule(platform, statusCode, responseBody); rule != nil {
			respCode := statusCode
			if !rule.PassthroughCode && rule.ResponseCode != nil {
				respCode = *rule.ResponseCode
			}

			msg := upstream.ExtractErrorMessage(responseBody)
			if !rule.PassthroughBody && rule.CustomMessage != nil {
				msg = *rule.CustomMessage
			}

			if rule.SkipMonitoring {
				MarkOpsSkipMonitoring(c)
			}

			writeAnthropicFailure(c, respCode, "upstream_error", msg, streamStarted)
			return
		}
	}

	// Record the upstream status and message for Ops error logs.
	upstreamMsg := upstream.ExtractErrorMessage(responseBody)
	SetOpsUpstreamError(c, statusCode, upstreamMsg, "")

	status, errType, errMsg := AnthropicUpstreamError(statusCode)
	writeAnthropicFailure(c, status, errType, errMsg, streamStarted)
}

// AnthropicUpstreamError maps an upstream status to a safe Messages error.
// Rate limits remain retryable client-visible statuses, while credential and
// general server failures become gateway errors. Unknown statuses use the
// generic upstream-failure message.
func AnthropicUpstreamError(statusCode int) (int, string, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "upstream_error", "Upstream authentication failed, please contact administrator"
	case 403:
		return http.StatusBadGateway, "upstream_error", "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "rate_limit_error", "Upstream rate limit exceeded, please retry later"
	case 529:
		return http.StatusServiceUnavailable, "overloaded_error", "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "upstream_error", "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "upstream_error", "Upstream request failed"
	}
}

// writeAnthropicFailure delegates terminal protocol output and stream-error recording.
// Before output starts it writes a normal HTTP error; after output starts it
// appends the protocol's error event, with Responses-aware stream handling, and
// lets MarkOpsStreamError record that failure.
func writeAnthropicFailure(c *gin.Context, status int, kind, message string, started bool) {
	WriteAnthropicStreamError(c, status, kind, "", message, started, MarkOpsStreamError)
}
