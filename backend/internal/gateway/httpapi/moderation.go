package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unsafe"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// moderation.go builds moderation inputs and records cyber warnings with
// request identities and current-turn content evidence.
// Moderation risk identity can belong to a team actor while billing belongs to
// the Key owner. HTTP input construction keeps those identities separate from
// original client model attribution and actual provider execution, using the
// shared identity resolver before local checks or warning recording. Group IDs
// are copied while supplied body bytes remain retained input.
//
// Cyber warnings can be observed by event callbacks and later completion. A
// serial request/turn marker suppresses another record only after the service
// returns a warning; errors and nil results leave later observation available.
// Current-turn content snapshots supply preferred prompt evidence, and their
// collections are copied when passed to recording. Entry owners coordinate turn
// clearing and observation rather than relying on Gin marker access as a
// concurrent exactly-once primitive.

// These Gin keys retain Cyber recording state, current-turn content and an
// explicit prompt excerpt for warning attribution across event and completion observations.
const (
	CyberWarningRecordedKey        = "openai_cyber_warning_recorded"
	CyberWarningSnapshotKey        = "openai_cyber_warning_snapshot"
	CyberWarningPromptExcerptKey   = "openai_cyber_warning_prompt_excerpt"
	cyberWarningSnapshotBindingKey = "openai_cyber_warning_snapshot_binding"
)

// cyberWarningSnapshotBinding ties a retained current-turn moderation snapshot
// to the exact protocol and body slice header from which it was extracted.
type cyberWarningSnapshotBinding struct {
	// protocol, bodyData, and bodyLen must all match before BuildContentModerationInput
	// reuses snapshot, ensuring any intermediate body rewrite forces fresh extraction.
	protocol string                            // extraction protocol identifier
	bodyData *byte                             // original body backing pointer
	bodyLen  int                               // original body byte length
	snapshot moderation.ContentModerationInput // extracted current-turn content
}

// ModerationEndpoints reads normalized endpoint and forced-platform evidence
// for HTTP input.
type ModerationEndpoints interface {
	// Inbound returns the normalized endpoint for the supplied request so
	// moderation can correlate local checks and warning evidence. Input builders
	// use URL.Path when the returned string is empty.
	Inbound(*gin.Context) string
	// Forced returns platform text and whether typed override evidence is present.
	// Input construction trims a present value and replaces its provider field
	// even when that value is blank.
	Forced(*gin.Context) (string, bool)
}

// ModerationPort checks content and records or scopes upstream Cyber warning
// evidence.
type ModerationPort interface {
	// Check evaluates supplied protocol input and its risk/billing attribution
	// using the caller context. The HTTP adapter returns a successful decision and
	// logs errors before yielding an absent decision to its caller.
	Check(context.Context, moderation.ContentModerationCheckInput) (*moderation.ContentModerationDecision, error)
	// RecordCyberWarning records qualifying warning evidence using the request
	// context and supplied content snapshot. A nonnil result lets the HTTP adapter
	// mark serial deduplication; errors or nil warnings leave another observation
	// eligible.
	RecordCyberWarning(context.Context, moderation.ContentModerationCyberWarningInput) (*moderation.ContentModerationCyberWarning, error)
	// CyberWarningInScope checks whether supplied upstream warning evidence
	// qualifies for the configured recording scope. Callers use the boolean
	// separately from lookup errors before applying warning or session-block
	// behavior.
	CyberWarningInScope(context.Context, moderation.ContentModerationCyberWarningInput) (bool, error)
	// CyberSessionBlockGroupInScope checks whether a group is subject to Cyber
	// session blocking. The group pointer can be absent, and callers retain the
	// returned error separately from scope eligibility.
	CyberSessionBlockGroupInScope(context.Context, *int64) (bool, error)
}

// ContentModerationIdentity carries separately resolved risk actor, billing
// owner and team attribution.
type ContentModerationIdentity = moderationflow.Identity

// GatewayModerationEndpoints reads the request route and forced platform.
type GatewayModerationEndpoints struct{}

// ContentModerationStatus returns a decision's usable HTTP failure status or
// the shared 403 default. Nil decisions and values outside 400 through 599 use
// that default; callers separately decide whether the decision blocks their
// request.
func ContentModerationStatus(decision *moderation.ContentModerationDecision) int {
	if decision == nil || decision.StatusCode < 400 || decision.StatusCode > 599 {
		return http.StatusForbidden
	}
	return decision.StatusCode
}

// ContentModerationErrorCode returns the stable content_policy_violation code
// used for local denial.
func ContentModerationErrorCode(decision *moderation.ContentModerationDecision) string {
	return "content_policy_violation"
}

// ClientRequestedModel selects original client model attribution from request
// evidence, then a supplied fallback. Nil Gin or request state uses the trimmed
// fallback. A nonblank model-trace ClientModel wins before telemetry
// ClientModel, and all returned names are trimmed.
//
// Trace and telemetry describe client identity while dispatch can rewrite
// provider models. Choosing their client fields in this order lets pricing
// attribution remain tied to the inbound name even after the caller's current
// model variable describes an upstream request.
func ClientRequestedModel(c *gin.Context, fallback string) string {
	fallback = strings.TrimSpace(fallback)
	if c == nil || c.Request == nil {
		return fallback
	}
	if trace, ok := modeltrace.FromContext(c.Request.Context()); ok {
		if model := strings.TrimSpace(trace.ClientModel); model != "" {
			return model
		}
	}
	if model, ok := c.Request.Context().Value(telemetry.ClientModel).(string); ok {
		if model = strings.TrimSpace(model); model != "" {
			return model
		}
	}
	return fallback
}

// ClientRequestedUsageFields builds pricing attribution from the resolved
// client model and supplied upstream model. It delegates the mapping fields to
// ToUsageFields so requested identity remains separate from provider model
// rewrites recorded by execution.
func ClientRequestedUsageFields(c *gin.Context, mapping routing.GroupMappingResult, fallbackModel, upstreamModel string) routing.PricingUsageFields {
	return mapping.ToUsageFields(ClientRequestedModel(c, fallbackModel), upstreamModel)
}

// RecordOpenAICyberWarningWithSnapshot records one qualifying warning in a
// serial request or turn observation flow. Missing service/context or an
// already recorded marker returns false. The supplied content collections are
// copied before service recording, alongside separately resolved risk and
// billing identity.
//
// Recording error logs diagnostics and leaves the marker clear, allowing a
// later observation to try again. A nonnil returned warning sets the marker and
// returns true; nil with no error still leaves it clear. Callers serialize
// observation and clear turn state at their owning entry because the Gin
// read/write marker sequence does not establish concurrent exactly-once
// recording.
func RecordOpenAICyberWarningWithSnapshot(endpoints ModerationEndpoints, svc ModerationPort, c *gin.Context, reqLog *zap.Logger, apiKey *apikey.APIKey, provider *moderationflow.Provider, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string, snapshot moderation.ContentModerationInput) bool {
	if svc == nil || c == nil {
		return false
	}
	// Event observation and completion may report the same warning in a serial
	// turn. Skip later recording only after an earlier service result established the marker.
	if c.GetBool(CyberWarningRecordedKey) {
		return false
	}
	input := BuildOpenAICyberWarningInput(endpoints, c, apiKey, provider, model, statusCode, responseBody, warningText, promptExcerpt)
	input.Content = moderationflow.SnapshotContent(snapshot)
	warning, err := svc.RecordCyberWarning(c.Request.Context(), input)
	// A service error leaves no successful observation to publish; log it and
	// return an absent result for the owning flow to handle.
	if err != nil {
		if reqLog != nil {
			reqLog.Warn("content_moderation.cyber_warning_record_failed", zap.Error(err))
		}
		return false
	}
	// Publish recording evidence after the service returns a warning; failure or
	// an empty result leaves later serial observations able to retry recording.
	if warning != nil {
		c.Set(CyberWarningRecordedKey, true)
	}
	if warning != nil && reqLog != nil {
		reqLog.Info("content_moderation.cyber_warning_recorded",
			zap.Int64("warning_id", warning.ID),
			zap.Int64p("user_id", warning.UserID),
			zap.Int64p("billing_user_id", warning.BillingUserID),
			zap.Int64p("team_id", warning.TeamID),
			zap.Int64p("provider_id", warning.ProviderID),
			zap.Int("violation_count", warning.ViolationCount),
			zap.Bool("auto_banned", warning.AutoBanned),
		)
	}
	return warning != nil
}

// BuildOpenAICyberWarningInput captures warning attribution from a valid
// request and endpoint reader. It separates risk actor from billing owner,
// copies group ID storage and retains supplied response bytes. Blank normalized
// endpoints use URL.Path, while model, warning and prompt strings are trimmed.
func BuildOpenAICyberWarningInput(endpoints ModerationEndpoints, c *gin.Context, apiKey *apikey.APIKey, provider *moderationflow.Provider, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string) moderation.ContentModerationCyberWarningInput {
	identity := ResolveContentModerationIdentity(apiKey, authctx.AuthSubject{})
	input := moderation.ContentModerationCyberWarningInput{
		RequestID:      ContentModerationRequestID(c.Request.Context()),
		UserID:         identity.UserID,
		UserEmail:      identity.UserEmail,
		BillingUserID:  identity.BillingUserID,
		TeamID:         identity.TeamID,
		Endpoint:       endpoints.Inbound(c),
		Model:          strings.TrimSpace(model),
		UpstreamStatus: statusCode,
		ResponseBody:   responseBody,
		WarningText:    strings.TrimSpace(warningText),
		PromptExcerpt:  strings.TrimSpace(promptExcerpt),
	}
	if apiKey != nil {
		input.APIKeyID = apiKey.ID
		input.APIKeyName = apiKey.Name
		if apiKey.GroupID != nil {
			groupID := *apiKey.GroupID
			input.GroupID = &groupID
		}
		if apiKey.Group != nil {
			input.GroupName = apiKey.Group.Name
		}
	}
	if provider != nil {
		input.ProviderID = provider.ID
		input.ProviderName = provider.Name
	}
	// When normalized route attribution is absent, retain the actual request path
	// so the recorded check or warning still identifies its HTTP endpoint.
	if input.Endpoint == "" && c.Request != nil && c.Request.URL != nil {
		input.Endpoint = c.Request.URL.Path
	}
	return input
}

// CurrentOpenAICyberWarningPromptExcerpt selects prompt text from retained
// current-turn content before an explicit excerpt. Nil context returns empty. A
// nonempty content snapshot returns its derived excerpt even when that excerpt
// is empty, as can happen for content containing media alone.
//
// The explicit stored string is used only when the snapshot is empty, and its
// whitespace is trimmed. This precedence keeps the excerpt tied to the retained
// turn content instead of allowing an older explicit excerpt to replace a
// current snapshot with no text.
func CurrentOpenAICyberWarningPromptExcerpt(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if snapshot := CurrentOpenAICyberWarningSnapshot(c); !snapshot.IsEmpty() {
		return moderation.ExtractContentModerationPromptExcerptFromInput(snapshot)
	}
	if value, ok := c.Get(CyberWarningPromptExcerptKey); ok {
		if excerpt, ok := value.(string); ok {
			return strings.TrimSpace(excerpt)
		}
	}
	return ""
}

// CurrentOpenAICyberWarningSnapshot returns a copy of retained moderation
// content collections. Missing context, absent state or another stored type
// returns empty input; Images, Items and ImageItems storage is cloned by the
// shared snapshot helper.
func CurrentOpenAICyberWarningSnapshot(c *gin.Context) moderation.ContentModerationInput {
	if c == nil {
		return moderation.ContentModerationInput{}
	}
	if value, ok := c.Get(CyberWarningSnapshotKey); ok {
		if snapshot, ok := value.(moderation.ContentModerationInput); ok {
			return moderationflow.SnapshotContent(snapshot)
		}
	}
	return moderation.ContentModerationInput{}
}

// SetOpenAICyberWarningRequestSnapshot extracts current-turn moderation input
// and publishes it with a derived prompt excerpt. Nil contexts or empty bodies
// retain existing state, so the turn owner clears stale warning data separately
// before another request should replace it.
func SetOpenAICyberWarningRequestSnapshot(c *gin.Context, protocol string, body []byte) {
	if c == nil || len(body) == 0 {
		return
	}
	// Extract the same current-turn content used by local moderation, including
	// tool output and media, so warning recording can attach evidence beyond a text-only excerpt.
	extractStart := time.Now()
	snapshot := moderation.ExtractContentModerationInput(protocol, body)
	if c.Request != nil {
		timing.ObserveSubstage(c.Request.Context(), "moderation_extract_ms", time.Since(extractStart))
	}
	c.Set(CyberWarningSnapshotKey, snapshot)
	c.Set(cyberWarningSnapshotBindingKey, cyberWarningSnapshotBinding{
		protocol: protocol,
		bodyData: unsafe.SliceData(body),
		bodyLen:  len(body),
		snapshot: snapshot,
	})
	excerpt := moderation.ExtractContentModerationPromptExcerptFromInput(snapshot)
	SetOpenAICyberWarningPromptExcerpt(c, excerpt)
}

// SetOpenAICyberWarningPromptExcerpt stores a trimmed explicit warning excerpt
// in Gin context. Nil context skips publication, while blank text replaces the
// previous excerpt; a nonempty retained snapshot takes precedence even when its
// derived text is empty.
func SetOpenAICyberWarningPromptExcerpt(c *gin.Context, promptExcerpt string) {
	if c == nil {
		return
	}
	c.Set(CyberWarningPromptExcerptKey, strings.TrimSpace(promptExcerpt))
}

// RunContentModeration checks request input with separately resolved risk and
// billing attribution. Missing service or request state returns nil; a service
// error is logged and also returns nil, leaving the caller to decide handling
// for an absent decision. Successful decisions are returned with metadata
// diagnostics when logging is supplied.
func RunContentModeration(endpoints ModerationEndpoints, c *gin.Context, reqLog *zap.Logger, svc ModerationPort, apiKey *apikey.APIKey, subject authctx.AuthSubject, protocol string, model string, body []byte) *moderation.ContentModerationDecision {
	if svc == nil || c == nil || c.Request == nil {
		return nil
	}
	input := BuildContentModerationInput(endpoints, c, apiKey, subject, protocol, model, body)
	if reqLog != nil {
		reqLog.Info("content_moderation.gateway_check_start",
			zap.String("request_id", input.RequestID),
			zap.Int64("user_id", input.UserID),
			zap.Int64("billing_user_id", input.BillingUserID),
			zap.Int64p("team_id", input.TeamID),
			zap.Int64("api_key_id", input.APIKeyID),
			zap.String("api_key_name", input.APIKeyName),
			zap.Int64p("group_id", input.GroupID),
			zap.String("group_name", input.GroupName),
			zap.String("endpoint", input.Endpoint),
			zap.String("provider", input.Provider),
			zap.String("protocol", input.Protocol),
			zap.String("model", input.Model),
			zap.Int("body_bytes", len(body)),
		)
	}
	decision, err := svc.Check(c.Request.Context(), input)
	// A service error leaves no successful observation to publish; log it and
	// return an absent result for the owning flow to handle.
	if err != nil {
		if reqLog != nil {
			reqLog.Warn("content_moderation.check_failed", zap.Error(err))
		}
		return nil
	}
	if reqLog != nil && decision != nil {
		reqLog.Info("content_moderation.gateway_check_done",
			zap.String("request_id", input.RequestID),
			zap.Bool("allowed", decision.Allowed),
			zap.Bool("blocked", decision.Blocked),
			zap.Bool("flagged", decision.Flagged),
			zap.String("action", decision.Action),
			zap.Int("status_code", decision.StatusCode),
			zap.String("highest_category", decision.HighestCategory),
			zap.Float64("highest_score", decision.HighestScore),
		)
	}
	return decision
}

// BuildContentModerationInput captures moderation attribution using a valid
// request and endpoint reader. Key evidence resolves risk and billing
// identities and copies group ID storage, while body bytes are retained. A
// present forced-platform value replaces Provider after trimming, even when
// blank; missing endpoint text falls back to URL.Path.
func BuildContentModerationInput(endpoints ModerationEndpoints, c *gin.Context, apiKey *apikey.APIKey, subject authctx.AuthSubject, protocol string, model string, body []byte) moderation.ContentModerationCheckInput {
	identity := ResolveContentModerationIdentity(apiKey, subject)
	input := moderation.ContentModerationCheckInput{
		RequestID:     ContentModerationRequestID(c.Request.Context()),
		UserID:        identity.UserID,
		UserEmail:     identity.UserEmail,
		BillingUserID: identity.BillingUserID,
		TeamID:        identity.TeamID,
		Endpoint:      endpoints.Inbound(c),
		Provider:      ContentModerationProvider(apiKey),
		Model:         strings.TrimSpace(model),
		Protocol:      protocol,
		Body:          body,
	}
	if forcedPlatform, ok := endpoints.Forced(c); ok {
		input.Provider = strings.TrimSpace(forcedPlatform)
	}
	if apiKey != nil {
		input.APIKeyID = apiKey.ID
		input.APIKeyName = apiKey.Name
		if apiKey.GroupID != nil {
			groupID := *apiKey.GroupID
			input.GroupID = &groupID
		}
		if apiKey.Group != nil {
			input.GroupName = apiKey.Group.Name
		}
	}
	// When normalized route attribution is absent, retain the actual request path
	// so the recorded check or warning still identifies its HTTP endpoint.
	if input.Endpoint == "" && c.Request != nil && c.Request.URL != nil {
		input.Endpoint = c.Request.URL.Path
	}
	if c != nil && len(body) > 0 {
		if raw, ok := c.Get(cyberWarningSnapshotBindingKey); ok {
			// Reuse the request snapshot only when protocol and backing slice match, so any
			// intervening prompt or bootstrap rewrite triggers fresh extraction on the new bytes.
			if binding, ok := raw.(cyberWarningSnapshotBinding); ok &&
				binding.protocol == protocol &&
				binding.bodyLen == len(body) &&
				binding.bodyData == unsafe.SliceData(body) {
				copied := moderationflow.SnapshotContent(binding.snapshot)
				input.PreExtracted = &copied
			}
		}
	}
	return input
}

// ResolveContentModerationIdentity delegates independent risk actor and
// billing-owner resolution from Key and subject evidence. Missing keys retain
// subject identity; the shared resolver owns precedence and copied team ID
// storage for the returned value.
func ResolveContentModerationIdentity(key *apikey.APIKey, subject authctx.AuthSubject) ContentModerationIdentity {
	return moderationflow.ResolveIdentity(key, subject.UserID)
}

// CloneContentModerationID copies the pointed identifier into independently
// mutable storage. Nil input returns nil, and all pointed values, including
// nonpositive values, are copied unchanged so callers decide whether their
// identity is admissible.
func CloneContentModerationID(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// ContentModerationProvider returns an unspecified provider for ordinary API
// Key metadata.
func ContentModerationProvider(apiKey *apikey.APIKey) string {
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return ""
}

// ContentModerationRequestID returns the trimmed telemetry request correlation
// string. Nil contexts or values of another type return empty; the helper does
// not create identity, allowing callers to retain the request's existing
// correlation source.
func ContentModerationRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if requestID, ok := ctx.Value(telemetry.RequestID).(string); ok {
		return strings.TrimSpace(requestID)
	}
	return ""
}

// Inbound returns the normalized request endpoint used for gateway moderation
// attribution.
func (GatewayModerationEndpoints) Inbound(c *gin.Context) string { return GetInboundEndpoint(c) }

// Forced reads the Gin forced-platform value and its typed-presence flag. A
// stored empty string can remain present, so moderation input construction
// preserves the distinction between no override and a supplied blank override.
func (GatewayModerationEndpoints) Forced(c *gin.Context) (string, bool) {
	return keyhttp.GetForcePlatformFromContext(c)
}
