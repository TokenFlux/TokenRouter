package handler

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
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
	return gatewayhttp.ContentModerationStatus(decision)
}

func contentModerationErrorCode(decision *service.ContentModerationDecision) string {
	return gatewayhttp.ContentModerationErrorCode(decision)
}

// clientRequestedModel 返回进入复合映射或 Key 重定向前的客户端模型。
func clientRequestedModel(c *gin.Context, fallback string) string {
	return gatewayhttp.ClientRequestedModel(c, fallback)
}

// clientRequestedUsageFields 统一生成包含客户端原始模型的渠道用量字段。
func clientRequestedUsageFields(c *gin.Context, mapping service.ChannelMappingResult, fallbackModel, upstreamModel string) service.ChannelUsageFields {
	return gatewayhttp.ClientRequestedUsageFields(c, routing.ChannelMappingResult(mapping), fallbackModel, upstreamModel)
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

func (h *OpenAIGatewayHandler) recordOpenAICyberWarningWithPromptExcerpt(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, account *service.Account, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string) bool {
	return h.recordOpenAICyberWarningWithSnapshot(c, reqLog, apiKey, account, model, statusCode, responseBody, warningText, promptExcerpt, currentOpenAICyberWarningSnapshot(c))
}

func (h *OpenAIGatewayHandler) recordOpenAICyberWarningWithSnapshot(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, account *service.Account, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string, snapshot service.ContentModerationInput) bool {
	if h == nil {
		return false
	}
	return gatewayhttp.RecordOpenAICyberWarningWithSnapshot(moderationHTTPEndpoints{}, nativeModerationPort(h.contentModerationService), c, reqLog, service.APIKeyView(apiKey), moderationAccountView(account), model, statusCode, responseBody, warningText, promptExcerpt, snapshot)
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
	return gatewayhttp.BuildOpenAICyberWarningInput(moderationHTTPEndpoints{}, c, service.APIKeyView(apiKey), moderationAccountView(account), model, statusCode, responseBody, warningText, promptExcerpt)
}

func currentOpenAICyberWarningPromptExcerpt(c *gin.Context) string {
	return gatewayhttp.CurrentOpenAICyberWarningPromptExcerpt(c)
}

func currentOpenAICyberWarningSnapshot(c *gin.Context) service.ContentModerationInput {
	return gatewayhttp.CurrentOpenAICyberWarningSnapshot(c)
}

func setOpenAICyberWarningRequestSnapshot(c *gin.Context, protocol string, body []byte) {
	gatewayhttp.SetOpenAICyberWarningRequestSnapshot(c, protocol, body)
}

func setOpenAICyberWarningPromptExcerpt(c *gin.Context, promptExcerpt string) {
	gatewayhttp.SetOpenAICyberWarningPromptExcerpt(c, promptExcerpt)
}

func runContentModeration(c *gin.Context, reqLog *zap.Logger, svc *service.ContentModerationService, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol string, model string, body []byte) *service.ContentModerationDecision {
	return gatewayhttp.RunContentModeration(moderationHTTPEndpoints{}, c, reqLog, nativeModerationPort(svc), service.APIKeyView(apiKey), subject, protocol, model, body)
}

func buildContentModerationInput(c *gin.Context, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol string, model string, body []byte) service.ContentModerationCheckInput {
	return gatewayhttp.BuildContentModerationInput(moderationHTTPEndpoints{}, c, service.APIKeyView(apiKey), subject, protocol, model, body)
}

// contentModerationIdentity 是网关进入风控前冻结的用户和归属快照。
type contentModerationIdentity = gatewayhttp.ContentModerationIdentity

// resolveContentModerationIdentity 将风控处置对象与付款归属拆开，避免团队成员触发规则时误封 Owner。
func resolveContentModerationIdentity(apiKey *service.APIKey, subject middleware2.AuthSubject) contentModerationIdentity {
	return gatewayhttp.ResolveContentModerationIdentity(service.APIKeyView(apiKey), subject)
}
