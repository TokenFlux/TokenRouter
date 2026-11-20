package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// cyber.go rejects blocked cyber sessions and records upstream policy evidence
// for warning and completion processing.
// Cyber handling distinguishes a locally blocked session from newly observed
// upstream policy evidence. Session rejection checks availability, runtime
// settings, and group scope before lookup; a found block writes the selected
// client error form and submits a dedicated Ops observation.
//
// Policy recording can write a supplied block plan before warning scope checks
// or request deduplication. In-scope evidence then sets the handled marker,
// attempts warning persistence, and asks moderationflow.Runtime to snapshot
// completion data for background work. These decisions are separate: a handled
// marker reports dispatch preparation, while task acceptance and persisted
// warning success retain their own behavior in the bound dependencies.

// Cyber response formats choose the local block envelope, and the recorded key
// marks policy handling already performed for this HTTP request or WS turn.
const (
	CyberBlockResponses CyberBlockFormat = iota // Responses local error envelope
	CyberBlockChat                              // Chat local error envelope
	CyberBlockAnthropic                         // Messages local error envelope

	CyberPolicyRecordedKey = "ops_cyber_recorded" // policy handling deduplication key
)

// CyberBlockFormat selects the client error shape for local session rejection.
type CyberBlockFormat int

// CyberBackend supplies request evidence, session lookup, and protocol-aware failure output.
type CyberBackend interface {
	// Available reports whether session blocking dependencies are bound for this entry.
	Available() bool
	// Enabled reads current runtime enablement before session lookup is attempted.
	Enabled(context.Context) bool
	// Find returns the blocked session key for the current request, or empty.
	// The backend owns explicit identity, scoped transcript checks, and lookup
	// failure behavior; the handler calls it after availability and group admission.
	Find(context.Context, int64, *gin.Context, []byte) string
	// Mark supplies current upstream policy evidence, returning nil when absent.
	// RecordPolicy calls it before checking the HTTP context, so implementations
	// tolerate a nil context. Returned fields remain readable during recording and dispatch preparation.
	Mark(*gin.Context) *moderationflow.Mark
	// StopKeepalive waits for heartbeat takeover and reports whether heartbeat framing began.
	// A true result selects stream-error handling before local JSON rejection;
	// request cleanup continues to own restoration of the heartbeat writer wrapper.
	StopKeepalive(*gin.Context) bool
	// MarkStream records intended policy failure details after streaming status was selected.
	MarkStream(*gin.Context, string, string, int)
	// FailedSSE attempts the Responses terminal error path and reports output ownership.
	// True ends further response writing. False permits the handler's local JSON
	// branch, using the error shape requested by CyberBlockFormat.
	FailedSSE(*gin.Context, string, string) bool
	// UpstreamEndpoint resolves the observed endpoint for the supplied provider platform.
	UpstreamEndpoint(*gin.Context, string) string
}

// CyberHandler coordinates local session rejection and upstream policy completion observations.
type CyberHandler struct {
	backend CyberBackend // request evidence and output operations
	// moderator supplies scope and warning persistence. Nil bypasses warning
	// scope checks in RecordPolicy but makes GroupInScope reject local session checks.
	moderator ModerationPort
	endpoints ModerationEndpoints // inbound endpoint observation
	// runtime owns block writing, task submission, completion, and Ops dependencies.
	// Assembly supplies usable Tasks when RecordPolicy can reach background dispatch.
	runtime moderationflow.Runtime
}

// CyberPolicyCall supplies policy identity, block intent, and optional completion input.
type CyberPolicyCall struct {
	// Key and Provider are read during immediate warning and metadata construction.
	// Callers keep these pointed-to objects stable until RecordPolicy returns; background completion receives runtime snapshots separately.
	Key      *apikey.APIKey           // request API-key identity
	Provider *moderationflow.Provider // selected upstream provider
	// Usage is completion input supplied by the caller. Runtime.Dispatch copies
	// its mutable snapshot before submitting work that may outlive the HTTP request.
	Usage          *completion.Input
	Model          string // requested model observation
	ForwardErrored bool   // enable cyber usage completion
	// BlockKey supplies the deferred single-key block, while Plan contains early
	// block keys. HasPlan selects Plan.ScopeKey over BlockKey before deduplication and warning scope checks.
	BlockKey string                   // deferred session block key
	Plan     moderationflow.BlockPlan // early scope and block keys
	HasPlan  bool                     // supplied block plan applies
}

// NewCyberHandler binds cyber request operations and the supplied moderation runtime.
// Assembly owns runtime resource lifecycle and provides a usable backend.
// The optional moderator controls scope checks and warning persistence; runtime
// task submission is required when policy recording reaches Dispatch.
func NewCyberHandler(b CyberBackend, m ModerationPort, e ModerationEndpoints, r moderationflow.Runtime) *CyberHandler {
	return &CyberHandler{b, m, e, r}
}

// GroupInScope checks whether the current key's group permits session blocking.
// Missing handler, moderator, request, or key returns false. Scope-read errors
// are logged and return false, leaving this adapter's local block check unclaimed.
func (h *CyberHandler) GroupInScope(c *gin.Context, key *apikey.APIKey) bool {
	if h == nil || h.moderator == nil || c == nil || c.Request == nil || key == nil {
		return false
	}
	scope, err := h.moderator.CyberSessionBlockGroupInScope(c.Request.Context(), key.GroupID)
	if err != nil {
		RequestLogger(c, "handler.openai_gateway.cyber_session_block").Warn("content_moderation.cyber_session_block_scope_check_failed", zap.Error(err))
		// A failed scope read establishes no applicable block scope. Log the
		// failure and leave local session rejection unclaimed for this request.
		return false
	}
	return scope
}

// RejectSession checks an eligible request for an already blocked session.
// Callers provide a request-bearing context and the desired client error form.
// Missing dependencies, disabled runtime, absent group scope, or no matching key
// returns false, allowing the caller's ordinary request preparation to continue.
//
// A found block stops heartbeats before choosing output. Started Compact
// heartbeat framing first attempts a Responses failure event with intended
// forbidden status; another already committed event stream emits a terminal SSE
// error in the requested client format, while an uncommitted writer receives
// the local JSON shape. Dedicated Ops submission follows every claimed
// rejection path. True reports handling of that block response, leaving further
// forwarding to the caller to stop.
func (h *CyberHandler) RejectSession(c *gin.Context, key *apikey.APIKey, body []byte, model string, format CyberBlockFormat) bool {
	if h == nil || !h.backend.Available() || key == nil || c == nil {
		return false
	}
	if !h.backend.Enabled(c.Request.Context()) || !h.GroupInScope(c, key) {
		return false
	}
	blockKey := h.backend.Find(c.Request.Context(), key.ID, c, body)
	if blockKey == "" {
		return false
	}
	message := moderationflow.SessionBlockedClientMessage
	// After heartbeat framing begins, record the intended forbidden failure before
	// terminal output so Ops can observe that error even when the selected HTTP status remains successful.
	if h.backend.StopKeepalive(c) {
		h.backend.MarkStream(c, "permission_error", message, 403)
		if h.backend.FailedSSE(c, "permission_error", message) {
			h.EnqueueBlocked(c, key, model, blockKey)
			return true
		}
	}
	if CommittedSSE(c) {
		if format == CyberBlockAnthropic {
			DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, http.StatusForbidden, "permission_error", message, true)
		} else {
			DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, http.StatusForbidden, "permission_error", "session_blocked_by_cyber_policy", message, true, false)
		}
		h.EnqueueBlocked(c, key, model, blockKey)
		return true
	}
	if format == CyberBlockAnthropic {
		c.JSON(http.StatusForbidden, gin.H{"type": "error", "error": gin.H{"type": "permission_error", "message": message}})
	} else {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "code": "session_blocked_by_cyber_policy", "message": message}})
	}
	h.EnqueueBlocked(c, key, model, blockKey)
	return true
}

// EnqueueBlocked submits a dedicated Ops observation for local session rejection.
// Missing handler, Ops writer, or HTTP context is ignored. The request's
// dedicated-error marker is set before enqueueing so later error observation can
// recognize that this rejection was already offered to the Ops writer.
func (h *CyberHandler) EnqueueBlocked(c *gin.Context, key *apikey.APIKey, model, blockKey string) {
	if h == nil || h.runtime.Ops == nil || c == nil {
		return
	}
	MarkOpsDedicatedErrorRecorded(c)
	h.runtime.Ops.Enqueue(moderationflow.BuildSessionBlockedOpsEntry(h.Meta(c, key, nil, model, "openai", false, blockKey)))
}

// RecordPolicy handles current upstream policy evidence and prepares completion dispatch.
// Callers provide an initialized handler and stable call data. No marker or
// context returns false; a prior handled marker returns true after any supplied
// early block plan has been processed again.
//
// A supplied plan reaches early block processing before warning scope validation
// and deduplication. Scope rejection or failure returns false with any completed
// early block action retained.
// Admitted evidence sets the handled marker before warning persistence and
// runtime dispatch, preventing a later request callback from repeating those
// operations. True reports that handling decision, not successful persistence
// or accepted background execution. Callers serialize recording and retain the
// same turn until snapshots are captured; its check and handled-marker write
// form separate operations.
func (h *CyberHandler) RecordPolicy(c *gin.Context, in CyberPolicyCall) bool {
	mark := h.backend.Mark(c)
	if mark == nil || c == nil {
		return false
	}
	blockKey := strings.TrimSpace(in.BlockKey)
	// The supplied plan carries immediate block keys as well as scope identity.
	// Apply it before warning scope and deduplication so those later decisions do not erase this earlier block action.
	if in.HasPlan {
		blockKey = in.Plan.ScopeKey
		h.runtime.MarkBeforeScope(in.Plan)
	}
	if c.GetBool(CyberPolicyRecordedKey) {
		return true
	}
	platform := "openai"
	if in.Provider != nil && strings.TrimSpace(in.Provider.Platform) != "" {
		platform = in.Provider.Platform
	}
	meta := h.Meta(c, in.Key, in.Provider, in.Model, platform, GetOpsStream(c), blockKey)
	excerpt := CurrentOpenAICyberWarningPromptExcerpt(c)
	warning := BuildOpenAICyberWarningInput(h.endpoints, c, in.Key, in.Provider, in.Model, mark.UpstreamStatus, []byte(mark.Body), mark.Message, excerpt)
	if h.moderator != nil {
		scope, err := h.moderator.CyberWarningInScope(c.Request.Context(), warning)
		if err != nil {
			// Earlier block processing has already run. A failed warning-scope read
			// leaves that work intact and returns unhandled before setting deduplication or submitting completion.
			RequestLogger(c, "handler.openai_gateway.cyber_policy").Warn("content_moderation.cyber_policy_scope_check_failed", zap.Error(err))
			return false
		}
		if !scope {
			return false
		}
	}
	// Set request deduplication before warning persistence and task submission.
	// Those dependencies may fail independently; later callbacks still see this request's handling decision.
	c.Set(CyberPolicyRecordedKey, true)
	RecordOpenAICyberWarningWithSnapshot(h.endpoints, h.moderator, c, RequestLogger(c, "handler.openai_gateway.cyber_policy"), in.Key, in.Provider, in.Model, mark.UpstreamStatus, []byte(mark.Body), mark.Message, excerpt, CurrentOpenAICyberWarningSnapshot(c))
	// Runtime captures Usage, GroupID, and selected correlation context before task
	// creation. The task receives Meta and Mark values without closing over this Gin context.
	h.runtime.Dispatch(c.Request.Context(), moderationflow.PolicyCompletion{Usage: in.Usage, Meta: meta, Mark: *mark, ForwardErrored: in.ForwardErrored, BlockKey: blockKey})
	return true
}

// Meta captures scalar request and provider observations for cyber Ops work.
// Callers supply a request-bearing context. The current API-key user ID is
// recorded as provided, and GroupID receives a copied pointer; warning identity
// is resolved independently by the moderation input builder.
func (h *CyberHandler) Meta(c *gin.Context, key *apikey.APIKey, provider *moderationflow.Provider, model, platform string, stream bool, blockKey string) moderationflow.OpsMeta {
	meta := moderationflow.OpsMeta{RequestID: telemetry.RequestIDValue(c.Request.Context()), ClientRequestID: telemetry.RequestIDValue(c.Request.Context()), Platform: platform, Model: strings.TrimSpace(model), Stream: stream, InboundEndpoint: h.endpoints.Inbound(c), UpstreamEndpoint: h.backend.UpstreamEndpoint(c, platform), UserAgent: c.GetHeader("User-Agent"), ClientIP: clientip.GetClientIP(c), CreatedAt: time.Now(), SessionBlockKey: blockKey}
	if c.Request != nil && c.Request.URL != nil {
		meta.RequestPath = c.Request.URL.Path
	}
	if key != nil {
		meta.APIKeyID = key.ID
		meta.APIKeyPrefix = key.Key
		// Limit credential identification in Ops metadata to its first eight bytes.
		if len(meta.APIKeyPrefix) > 8 {
			meta.APIKeyPrefix = meta.APIKeyPrefix[:8]
		}
		meta.UserID = key.UserID
		meta.GroupID = CloneContentModerationID(key.GroupID)
	}
	if provider != nil {
		meta.ProviderID = provider.ID
	}
	return meta
}

// ClearCyberTurnState clears evidence and handling flags after a WebSocket turn.
// A nil context is ignored. Callers supply clearMark and finish current-turn
// observation or snapshotting before reset so the next turn can publish and
// process its own independent policy evidence.
func ClearCyberTurnState(c *gin.Context, clearMark func(*gin.Context)) {
	if c == nil {
		return
	}
	clearMark(c)
	c.Set(CyberPolicyRecordedKey, false)
	c.Set(CyberWarningRecordedKey, false)
}
