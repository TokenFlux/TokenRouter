package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	middleware2 "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *GatewayHandler) checkContentModeration(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol string, model string, body []byte) *service.ContentModerationDecision {
	if h == nil || h.contentModerationService == nil {
		return nil
	}
	return runContentModeration(c, reqLog, h.contentModerationService, apiKey, subject, protocol, model, body)
}

func contentModerationStatus(decision *service.ContentModerationDecision) int {
	if decision == nil || decision.StatusCode < 400 || decision.StatusCode > 599 {
		return http.StatusForbidden
	}
	return decision.StatusCode
}

func contentModerationErrorCode(decision *service.ContentModerationDecision) string {
	return "content_policy_violation"
}

// clientRequestedModel 返回进入复合映射或 Key 重定向前的客户端模型。
func clientRequestedModel(c *gin.Context, fallback string) string {
	fallback = strings.TrimSpace(fallback)
	if c == nil || c.Request == nil {
		return fallback
	}
	if trace, ok := service.APIKeyModelRedirectTraceFromContext(c.Request.Context()); ok {
		if model := strings.TrimSpace(trace.ClientModel); model != "" {
			return model
		}
	}
	if model, ok := c.Request.Context().Value(ctxkey.ClientModel).(string); ok {
		if model = strings.TrimSpace(model); model != "" {
			return model
		}
	}
	return fallback
}

// clientRequestedUsageFields 统一生成包含客户端原始模型的渠道用量字段。
func clientRequestedUsageFields(c *gin.Context, mapping service.ChannelMappingResult, fallbackModel, upstreamModel string) service.ChannelUsageFields {
	return mapping.ToUsageFields(clientRequestedModel(c, fallbackModel), upstreamModel)
}

func (h *OpenAIGatewayHandler) checkContentModeration(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol string, model string, body []byte) *service.ContentModerationDecision {
	if h == nil || h.contentModerationService == nil {
		return nil
	}
	return runContentModeration(c, reqLog, h.contentModerationService, apiKey, subject, protocol, model, body)
}

func (h *OpenAIGatewayHandler) recordOpenAICyberWarning(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, account *service.Account, model string, statusCode int, responseBody []byte, warningText string) {
	h.recordOpenAICyberWarningWithPromptExcerpt(c, reqLog, apiKey, account, model, statusCode, responseBody, warningText, currentOpenAICyberWarningPromptExcerpt(c))
}

const openAICyberWarningRecordedKey = "openai_cyber_warning_recorded"

const openAICyberWarningSnapshotKey = "openai_cyber_warning_snapshot"

func (h *OpenAIGatewayHandler) recordOpenAICyberWarningWithPromptExcerpt(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, account *service.Account, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string) bool {
	return h.recordOpenAICyberWarningWithSnapshot(c, reqLog, apiKey, account, model, statusCode, responseBody, warningText, promptExcerpt, currentOpenAICyberWarningSnapshot(c))
}

func (h *OpenAIGatewayHandler) recordOpenAICyberWarningWithSnapshot(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, account *service.Account, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string, snapshot service.ContentModerationInput) bool {
	if h == nil || h.contentModerationService == nil || c == nil {
		return false
	}
	// WS 上游事件回调和 turn 收尾都可能观察到同一个 cyber_policy 终止事件，这里按请求/turn 去重。
	if c.GetBool(openAICyberWarningRecordedKey) {
		return false
	}
	input := buildOpenAICyberWarningInput(c, apiKey, account, model, statusCode, responseBody, warningText, promptExcerpt)
	input.Content = snapshot
	warning, err := h.contentModerationService.RecordCyberWarning(c.Request.Context(), input)
	if err != nil {
		if reqLog != nil {
			reqLog.Warn("content_moderation.cyber_warning_record_failed", zap.Error(err))
		}
		return false
	}
	if warning != nil {
		c.Set(openAICyberWarningRecordedKey, true)
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

// recordOpenAIForwardResultCyberWarning 记录成功转发结果中携带的上游 cyber 风控警告。
func (h *OpenAIGatewayHandler) recordOpenAIForwardResultCyberWarning(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, account *service.Account, fallbackModel string, result *service.OpenAIForwardResult) {
	if result == nil || result.UpstreamWarning == nil {
		return
	}
	model := strings.TrimSpace(result.Model)
	if model == "" {
		model = strings.TrimSpace(fallbackModel)
	}
	warning := result.UpstreamWarning
	h.recordOpenAICyberWarning(c, reqLog, apiKey, account, model, warning.StatusCode, warning.ResponseBody, warning.Message)
}

// recordOpenAIForwardErrorCyberWarning 记录错误链中携带的上游 cyber 风控警告。
func (h *OpenAIGatewayHandler) recordOpenAIForwardErrorCyberWarning(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, account *service.Account, model string, statusCode int, err error) bool {
	warning, ok := service.ExtractOpenAIUpstreamWarning(err)
	if !ok || warning == nil {
		return false
	}
	if warning.StatusCode > 0 {
		statusCode = warning.StatusCode
	}
	h.recordOpenAICyberWarning(c, reqLog, apiKey, account, model, statusCode, warning.ResponseBody, warning.Message)
	return true
}

func buildOpenAICyberWarningInput(c *gin.Context, apiKey *service.APIKey, account *service.Account, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string) service.ContentModerationCyberWarningInput {
	identity := resolveContentModerationIdentity(apiKey, middleware2.AuthSubject{})
	input := service.ContentModerationCyberWarningInput{
		RequestID:      contentModerationRequestID(c.Request.Context()),
		UserID:         identity.UserID,
		UserEmail:      identity.UserEmail,
		BillingUserID:  identity.BillingUserID,
		TeamID:         identity.TeamID,
		Endpoint:       GetInboundEndpoint(c),
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

func currentOpenAICyberWarningPromptExcerpt(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if snapshot := currentOpenAICyberWarningSnapshot(c); !snapshot.IsEmpty() {
		return service.ExtractContentModerationPromptExcerptFromInput(snapshot)
	}
	if value, ok := c.Get(openAICyberWarningPromptExcerptKey); ok {
		if excerpt, ok := value.(string); ok {
			return strings.TrimSpace(excerpt)
		}
	}
	return ""
}

func currentOpenAICyberWarningSnapshot(c *gin.Context) service.ContentModerationInput {
	if c == nil {
		return service.ContentModerationInput{}
	}
	if value, ok := c.Get(openAICyberWarningSnapshotKey); ok {
		if snapshot, ok := value.(service.ContentModerationInput); ok {
			return snapshot
		}
	}
	return service.ContentModerationInput{}
}

func setOpenAICyberWarningRequestSnapshot(c *gin.Context, protocol string, body []byte) {
	if c == nil || len(body) == 0 {
		return
	}
	// Cyber 与本地审核复用同一份结构化当前轮快照，确保工具输出和图片上下文不会丢失。
	snapshot := service.ExtractContentModerationInput(protocol, body)
	c.Set(openAICyberWarningSnapshotKey, snapshot)
	excerpt := service.ExtractContentModerationPromptExcerptFromInput(snapshot)
	setOpenAICyberWarningPromptExcerpt(c, excerpt)
}

func setOpenAICyberWarningPromptExcerpt(c *gin.Context, promptExcerpt string) {
	if c == nil {
		return
	}
	c.Set(openAICyberWarningPromptExcerptKey, strings.TrimSpace(promptExcerpt))
}

func runContentModeration(c *gin.Context, reqLog *zap.Logger, svc *service.ContentModerationService, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol string, model string, body []byte) *service.ContentModerationDecision {
	if svc == nil || c == nil || c.Request == nil {
		return nil
	}
	input := buildContentModerationInput(c, apiKey, subject, protocol, model, body)
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

func buildContentModerationInput(c *gin.Context, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol string, model string, body []byte) service.ContentModerationCheckInput {
	identity := resolveContentModerationIdentity(apiKey, subject)
	input := service.ContentModerationCheckInput{
		RequestID:     contentModerationRequestID(c.Request.Context()),
		UserID:        identity.UserID,
		UserEmail:     identity.UserEmail,
		BillingUserID: identity.BillingUserID,
		TeamID:        identity.TeamID,
		Endpoint:      GetInboundEndpoint(c),
		Provider:      contentModerationProvider(apiKey),
		Model:         strings.TrimSpace(model),
		Protocol:      protocol,
		Body:          body,
	}
	if forcedPlatform, ok := middleware2.GetForcePlatformFromContext(c); ok {
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

// contentModerationIdentity 是网关进入风控前冻结的用户和归属快照。
type contentModerationIdentity struct {
	UserID        int64
	UserEmail     string
	BillingUserID int64
	TeamID        *int64
}

// resolveContentModerationIdentity 将风控处置对象与付款归属拆开，避免团队成员触发规则时误封 Owner。
func resolveContentModerationIdentity(apiKey *service.APIKey, subject middleware2.AuthSubject) contentModerationIdentity {
	identity := contentModerationIdentity{
		UserID:        subject.UserID,
		BillingUserID: subject.UserID,
	}
	if apiKey == nil {
		return identity
	}

	if apiKey.UserID > 0 {
		identity.UserID = apiKey.UserID
		if identity.BillingUserID <= 0 {
			identity.BillingUserID = apiKey.UserID
		}
	}
	if apiKey.User != nil && apiKey.User.ID > 0 {
		identity.BillingUserID = apiKey.User.ID
		if apiKey.User.ID == identity.UserID {
			identity.UserEmail = strings.TrimSpace(apiKey.User.Email)
		}
	}
	if apiKey.ActorUser != nil && apiKey.ActorUser.ID > 0 {
		identity.UserID = apiKey.ActorUser.ID
		identity.UserEmail = strings.TrimSpace(apiKey.ActorUser.Email)
	} else if apiKey.TeamMembership != nil && apiKey.TeamMembership.UserID == identity.UserID {
		identity.UserEmail = strings.TrimSpace(apiKey.TeamMembership.Email)
	}
	identity.TeamID = cloneContentModerationID(apiKey.TeamID)
	return identity
}

// cloneContentModerationID 隔离 API Key 上的可变指针，避免异步审计读取到后续修改。
func cloneContentModerationID(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func contentModerationProvider(apiKey *service.APIKey) string {
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return strings.TrimSpace(apiKey.Group.Platform)
}

func contentModerationRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if requestID, ok := ctx.Value(ctxkey.RequestID).(string); ok {
		return strings.TrimSpace(requestID)
	}
	return ""
}
