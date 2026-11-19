package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	openaierrors "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// compact_request.go normalizes remote compaction requests and records their
// client intent, session seed, and outcome.
// Remote compaction can retain the ordinary Responses stream or become a unary
// Compact request, depending on its path, trigger, and parsed stream intent.
// Native V2 keeps the Responses route and receives a negotiation marker. Other
// body-triggered compaction on bare Responses paths is promoted to Compact;
// existing response subpaths keep their own routing behavior.
//
// Promotion captures session and client-output intent before the Compact codec
// removes unsupported fields. That saved request state supplies later upstream
// session construction and any SSE bridge for the client. Outcome logging also
// checks the request's stream-error observation, because a heartbeat can commit
// HTTP 200 before compaction fails and leave the HTTP status alone misleading.

// OpenAICompactSessionSeedKey stores a captured prompt-cache seed before Compact
// normalization removes that field, allowing later request construction to retain its session input.
const OpenAICompactSessionSeedKey = "openai_compact_session_seed"

// IsOpenAIResponsesCompactPath identifies the legacy compact endpoint and allowed subpaths.
// It compares the trimmed Responses path suffix with compact or its descendants.
// Missing request context produces no matching suffix, leaving ordinary Responses
// paths available for body-triggered detection.
func IsOpenAIResponsesCompactPath(c *gin.Context) bool {
	suffix := strings.TrimSpace(OpenAIResponsesRequestPathSuffix(c))
	return suffix == "/compact" || strings.HasPrefix(suffix, "/compact/")
}

// ResolveOpenAICompactSessionID selects session_id, conversation_id, then the
// saved prompt-cache seed for legacy Compact construction. A nonnil context
// carries a request; header candidates are trimmed, and a blank or mistyped
// saved seed is skipped.
//
// Compact normalization removes prompt_cache_key from the body, so the captured
// context seed supplies that identity after header candidates have been checked.
// With no usable candidate, including a nil context, each call generates a fresh
// UUID. Callers retain the returned value when repeated construction needs to
// use one generated session.
func ResolveOpenAICompactSessionID(c *gin.Context) string {
	if c != nil {
		if sessionID := strings.TrimSpace(c.GetHeader("session_id")); sessionID != "" {
			return sessionID
		}
		if conversationID := strings.TrimSpace(c.GetHeader("conversation_id")); conversationID != "" {
			return conversationID
		}
		if seed, ok := c.Get(OpenAICompactSessionSeedKey); ok {
			if seedStr, ok := seed.(string); ok && strings.TrimSpace(seedStr) != "" {
				return strings.TrimSpace(seedStr)
			}
		}
	}
	// Normalization may remove prompt_cache_key before upstream construction. If
	// no captured seed or client session remains, give this call a generated session value.
	return uuid.NewString()
}

// IsBareOpenAIResponsesPath matches Responses endpoints without subpaths such as /compact.
// Body-triggered compact promotion applies here; /responses/{id}/... subpaths keep their own handling.
// Nil request data returns false. Surrounding whitespace and trailing slashes
// are removed before matching the supported versioned, unversioned, OpenAI, and Codex aliases.
func IsBareOpenAIResponsesPath(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	normalizedPath := strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/")
	switch normalizedPath {
	case EndpointResponses, "/openai/v1/responses", "/responses", "/backend-api/codex/responses":
		return true
	default:
		return false
	}
}

// IsOpenAIRemoteCompactionV2Request reports native streaming compaction intent
// from body fields. A boolean true stream and an input item whose type is
// compaction_trigger both need to be present. Missing, false, and nonboolean
// stream values cannot qualify, even when a trigger is present.
//
// HTTP handling separately limits this recognition to bare Responses paths and
// installs negotiation state afterward. The field queries do not establish that
// the complete body is valid JSON, so structural validation remains a separate
// caller step. Beta-header values do not participate in this body decision.
func IsOpenAIRemoteCompactionV2Request(body []byte) bool {
	stream, valid := ParseOpenAICompatibleStream(body)
	return valid && stream && protocolopenai.HasCompactionTriggerInInput(body)
}

// normalizeOpenAIResponsesCompactRequest selects streaming Responses for native
// V2 and the Compact bridge for other body triggers. It returns the normalized
// body; false means an error response was written and the caller must stop.
// Callers supply a request-bearing context and logger. Existing Compact paths
// use the unary codec, while body promotion is limited to bare Responses paths.
//
// Trigger ordering is normalized before deciding native V2; an ordering error
// is logged and leaves the body available for the remaining decision. Legacy
// promotion changes the path and saves session and streaming intent before
// fields are removed. A later codec failure can therefore leave those markers
// already installed, but writes a bad-request response and returns false so
// the caller never forwards that incomplete preparation.
func (h *OpenAITextHandler) normalizeOpenAIResponsesCompactRequest(c *gin.Context, reqLog *zap.Logger, body []byte) ([]byte, bool) {
	isCompactRequest := IsOpenAIResponsesCompactPath(c)
	if !isCompactRequest && IsBareOpenAIResponsesPath(c) && protocolopenai.HasCompactionTriggerInInput(body) {
		if normalized, changed, err := protocolopenai.NormalizeCompactionTriggerInputOrder(body); err != nil {
			// Trigger ordering is a compatibility preparation step. Keep the captured
			// body on failure so native recognition or Compact validation can still handle its input.
			reqLog.Warn("codex.remote_compact.trigger_order_normalization_failed", zap.Error(err))
		} else if changed {
			body = normalized
		}
		if IsOpenAIRemoteCompactionV2Request(body) {
			// Native V2 stays on Responses and needs its outbound beta feature.
			// Record negotiation here before ordinary forwarding builds the upstream headers.
			MarkOpenAINativeCompactionV2(c)
			return body, true
		}
		// Legacy promotion chooses the unary endpoint while retaining client stream
		// intent separately, allowing output to bridge the result after stream is removed from input.
		c.Request.URL.Path = strings.TrimRight(c.Request.URL.Path, "/") + "/compact"
		isCompactRequest = true
		clientStream := gjson.GetBytes(body, "stream").Bool()
		if clientStream {
			MarkOpenAICompactClientStream(c)
		}
		reqLog.Info("codex.remote_compact.detected_body_signal", zap.Bool("client_stream", clientStream))
	}
	if !isCompactRequest {
		return body, true
	}
	if compactSeed := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); compactSeed != "" {
		// The unary codec removes prompt_cache_key. Save its session seed first
		// so later upstream request construction can still select this client-supplied identity.
		c.Set(OpenAICompactSessionSeedKey, compactSeed)
	}
	normalizedCompactBody, normalizedCompact, compactErr := openaierrors.NormalizeOpenAICompactRequestBody(body)
	if compactErr != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to normalize compact request body")
		return nil, false
	}
	if normalizedCompact {
		body = normalizedCompactBody
	}
	return body, true
}

// LogRemoteCompactOutcome reports legacy Compact status and request observations.
// Non-Compact paths are skipped. startedAt anchors nonnegative elapsed
// milliseconds, and available request, model, provider, and upstream-ID values
// are included in the structured event.
//
// A 2xx status initially counts as success, but a recorded Ops stream error
// changes the outcome to failed. This accounts for heartbeats committing HTTP
// 200 before a terminal error is emitted. Success logs at info and failure
// at warn, so consumers use compact_outcome together with the retained HTTP status.
func (h *OpenAITextHandler) LogRemoteCompactOutcome(c *gin.Context, startedAt time.Time) {
	if !IsOpenAIResponsesCompactPath(c) {
		return
	}

	var (
		ctx    = context.Background()
		path   string
		status int
	)
	if c != nil {
		if c.Request != nil {
			ctx = c.Request.Context()
			if c.Request.URL != nil {
				path = strings.TrimSpace(c.Request.URL.Path)
			}
		}
		if c.Writer != nil {
			status = c.Writer.Status()
		}
	}

	outcome := "failed"
	if status >= 200 && status < 300 {
		outcome = "succeeded"
	}
	// Heartbeats can commit HTTP 200 before an upstream failure. Consult the
	// recorded stream error so that committed status cannot turn this diagnostic into success.
	if outcome == "succeeded" && c != nil {
		if _, hasStreamErr := GetOpsStreamError(c); hasStreamErr {
			outcome = "failed"
		}
	}
	// Clamp elapsed milliseconds when the supplied start lies in the future.
	latencyMs := max(time.Since(startedAt).Milliseconds(), 0)

	fields := []zap.Field{
		zap.String("component", "handler.openai_gateway.responses"),
		zap.Bool("remote_compact", true),
		zap.String("compact_outcome", outcome),
		zap.Int("status_code", status),
		zap.Int64("latency_ms", latencyMs),
		zap.String("path", path),
		zap.Bool("force_codex_cli", h != nil && h.options.ForceCodexCLI),
	}

	if c != nil {
		if userAgent := strings.TrimSpace(c.GetHeader("User-Agent")); userAgent != "" {
			fields = append(fields, zap.String("request_user_agent", userAgent))
		}
		if model := strings.TrimSpace(GetOpsModel(c)); model != "" {
			fields = append(fields, zap.String("request_model", model))
		}
		if providerID, ok := GetOpsProviderID(c); ok {
			fields = append(fields, zap.Int64("provider_id", providerID))
		}
		if c.Writer != nil {
			if upstreamRequestID := strings.TrimSpace(c.Writer.Header().Get("x-request-id")); upstreamRequestID != "" {
				fields = append(fields, zap.String("upstream_request_id", upstreamRequestID))
			} else if upstreamRequestID := strings.TrimSpace(c.Writer.Header().Get("X-Request-Id")); upstreamRequestID != "" {
				fields = append(fields, zap.String("upstream_request_id", upstreamRequestID))
			}
		}
	}

	log := logging.FromContext(ctx).With(fields...)
	if outcome == "succeeded" {
		log.Info("codex.remote_compact.succeeded")
		return
	}
	log.Warn("codex.remote_compact.failed")
}
