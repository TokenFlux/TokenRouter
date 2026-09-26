// 审核 HTTP 适配冻结请求投影并记录观测，处置和计数始终委托 moderation。
package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ModerationEndpoints interface {
	Inbound(*gin.Context) string
	Forced(*gin.Context) (string, bool)
}
type ModerationPort interface {
	Check(context.Context, moderation.ContentModerationCheckInput) (*moderation.ContentModerationDecision, error)
	RecordCyberWarning(context.Context, moderation.ContentModerationCyberWarningInput) (*moderation.ContentModerationCyberWarning, error)
	CyberWarningInScope(context.Context, moderation.ContentModerationCyberWarningInput) (bool, error)
	CyberSessionBlockGroupInScope(context.Context, *int64) (bool, error)
}

const (
	CyberWarningRecordedKey      = "openai_cyber_warning_recorded"
	CyberWarningSnapshotKey      = "openai_cyber_warning_snapshot"
	CyberWarningPromptExcerptKey = "openai_cyber_warning_prompt_excerpt"
)

func ContentModerationStatus(decision *moderation.ContentModerationDecision) int {
	if decision == nil || decision.StatusCode < 400 || decision.StatusCode > 599 {
		return http.StatusForbidden
	}
	return decision.StatusCode
}

func ContentModerationErrorCode(decision *moderation.ContentModerationDecision) string {
	return "content_policy_violation"
}

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

func ClientRequestedUsageFields(c *gin.Context, mapping routing.GroupMappingResult, fallbackModel, upstreamModel string) routing.PricingUsageFields {
	return mapping.ToUsageFields(ClientRequestedModel(c, fallbackModel), upstreamModel)
}

func RecordOpenAICyberWarningWithSnapshot(endpoints ModerationEndpoints, svc ModerationPort, c *gin.Context, reqLog *zap.Logger, apiKey *apikey.APIKey, account *moderationflow.Account, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string, snapshot moderation.ContentModerationInput) bool {
	if svc == nil || c == nil {
		return false
	}
	// WS 上游事件回调和 turn 收尾都可能观察到同一个 cyber_policy 终止事件，这里按请求/turn 去重。
	if c.GetBool(CyberWarningRecordedKey) {
		return false
	}
	input := BuildOpenAICyberWarningInput(endpoints, c, apiKey, account, model, statusCode, responseBody, warningText, promptExcerpt)
	input.Content = moderationflow.SnapshotContent(snapshot)
	warning, err := svc.RecordCyberWarning(c.Request.Context(), input)
	if err != nil {
		if reqLog != nil {
			reqLog.Warn("content_moderation.cyber_warning_record_failed", zap.Error(err))
		}
		return false
	}
	if warning != nil {
		c.Set(CyberWarningRecordedKey, true)
	}
	if warning != nil && reqLog != nil {
		reqLog.Info("content_moderation.cyber_warning_recorded",
			zap.Int64("warning_id", warning.ID),
			zap.Int64p("user_id", warning.UserID),
			zap.Int64p("billing_user_id", warning.BillingUserID),
			zap.Int64p("team_id", warning.TeamID),
			zap.Int64p("account_id", warning.AccountID),
			zap.Int("violation_count", warning.ViolationCount),
			zap.Bool("auto_banned", warning.AutoBanned),
		)
	}
	return warning != nil
}

func BuildOpenAICyberWarningInput(endpoints ModerationEndpoints, c *gin.Context, apiKey *apikey.APIKey, account *moderationflow.Account, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string) moderation.ContentModerationCyberWarningInput {
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
	if account != nil {
		input.AccountID = account.ID
		input.AccountName = account.Name
	}
	if input.Endpoint == "" && c.Request != nil && c.Request.URL != nil {
		input.Endpoint = c.Request.URL.Path
	}
	return input
}

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

func SetOpenAICyberWarningRequestSnapshot(c *gin.Context, protocol string, body []byte) {
	if c == nil || len(body) == 0 {
		return
	}
	// Cyber 与本地审核复用同一份结构化当前轮快照，确保工具输出和图片上下文不会丢失。
	snapshot := moderation.ExtractContentModerationInput(protocol, body)
	c.Set(CyberWarningSnapshotKey, snapshot)
	excerpt := moderation.ExtractContentModerationPromptExcerptFromInput(snapshot)
	SetOpenAICyberWarningPromptExcerpt(c, excerpt)
}

func SetOpenAICyberWarningPromptExcerpt(c *gin.Context, promptExcerpt string) {
	if c == nil {
		return
	}
	c.Set(CyberWarningPromptExcerptKey, strings.TrimSpace(promptExcerpt))
}

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
	if input.Endpoint == "" && c.Request != nil && c.Request.URL != nil {
		input.Endpoint = c.Request.URL.Path
	}
	return input
}

type ContentModerationIdentity = moderationflow.Identity

func ResolveContentModerationIdentity(key *apikey.APIKey, subject authctx.AuthSubject) ContentModerationIdentity {
	return moderationflow.ResolveIdentity(key, subject.UserID)
}

func CloneContentModerationID(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func ContentModerationProvider(apiKey *apikey.APIKey) string {
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return strings.TrimSpace(apiKey.Group.Platform)
}

func ContentModerationRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if requestID, ok := ctx.Value(telemetry.RequestID).(string); ok {
		return strings.TrimSpace(requestID)
	}
	return ""
}
