package handler

import (
	"strings"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/moderation"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) checkContentModeration(c *gin.Context, reqLog *zap.Logger, apiKey *apikey.APIKey, subject authctx.AuthSubject, protocol string, model string, body []byte) *moderation.ContentModerationDecision {
	if h == nil || h.contentModerationService == nil {
		return nil
	}
	return runContentModeration(c, reqLog, h.contentModerationService, apiKey, subject, protocol, model, body)
}

func (h *OpenAIGatewayHandler) recordOpenAICyberWarning(c *gin.Context, reqLog *zap.Logger, apiKey *apikey.APIKey, account *gatewayprovider.ExecutionAccount, model string, statusCode int, responseBody []byte, warningText string) {
	h.recordOpenAICyberWarningWithPromptExcerpt(c, reqLog, apiKey, account, model, statusCode, responseBody, warningText, gatewayhttp.CurrentOpenAICyberWarningPromptExcerpt(c))
}

func (h *OpenAIGatewayHandler) recordOpenAICyberWarningWithPromptExcerpt(c *gin.Context, reqLog *zap.Logger, apiKey *apikey.APIKey, account *gatewayprovider.ExecutionAccount, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string) bool {
	return h.recordOpenAICyberWarningWithSnapshot(c, reqLog, apiKey, account, model, statusCode, responseBody, warningText, promptExcerpt, gatewayhttp.CurrentOpenAICyberWarningSnapshot(c))
}

func (h *OpenAIGatewayHandler) recordOpenAICyberWarningWithSnapshot(c *gin.Context, reqLog *zap.Logger, apiKey *apikey.APIKey, account *gatewayprovider.ExecutionAccount, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string, snapshot moderation.ContentModerationInput) bool {
	if h == nil {
		return false
	}
	return gatewayhttp.RecordOpenAICyberWarningWithSnapshot(gatewayhttp.GatewayModerationEndpoints{}, nativeModerationPort(h.contentModerationService), c, reqLog, apikey.CopyAPIKey(apiKey), moderationAccountView(account), model, statusCode, responseBody, warningText, promptExcerpt, snapshot)
}

// recordOpenAIForwardResultCyberWarning 记录成功转发结果中携带的上游 cyber 风控警告。
func (h *OpenAIGatewayHandler) recordOpenAIForwardResultCyberWarning(c *gin.Context, reqLog *zap.Logger, apiKey *apikey.APIKey, account *gatewayprovider.ExecutionAccount, fallbackModel string, result *forwardcore.OpenAIResult) {
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
func (h *OpenAIGatewayHandler) recordOpenAIForwardErrorCyberWarning(c *gin.Context, reqLog *zap.Logger, apiKey *apikey.APIKey, account *gatewayprovider.ExecutionAccount, model string, statusCode int, err error) bool {
	warning, ok := forwardcore.WarningFromError(err)
	if !ok || warning == nil {
		return false
	}
	if warning.StatusCode > 0 {
		statusCode = warning.StatusCode
	}
	h.recordOpenAICyberWarning(c, reqLog, apiKey, account, model, statusCode, warning.ResponseBody, warning.Message)
	return true
}

func buildOpenAICyberWarningInput(c *gin.Context, apiKey *apikey.APIKey, account *gatewayprovider.ExecutionAccount, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string) moderation.ContentModerationCyberWarningInput {
	return gatewayhttp.BuildOpenAICyberWarningInput(gatewayhttp.GatewayModerationEndpoints{}, c, apikey.CopyAPIKey(apiKey), moderationAccountView(account), model, statusCode, responseBody, warningText, promptExcerpt)
}

func runContentModeration(c *gin.Context, reqLog *zap.Logger, svc *moderation.ContentModerationService, apiKey *apikey.APIKey, subject authctx.AuthSubject, protocol string, model string, body []byte) *moderation.ContentModerationDecision {
	return gatewayhttp.RunContentModeration(gatewayhttp.GatewayModerationEndpoints{}, c, reqLog, nativeModerationPort(svc), apikey.CopyAPIKey(apiKey), subject, protocol, model, body)
}

func buildContentModerationInput(c *gin.Context, apiKey *apikey.APIKey, subject authctx.AuthSubject, protocol string, model string, body []byte) moderation.ContentModerationCheckInput {
	return gatewayhttp.BuildContentModerationInput(gatewayhttp.GatewayModerationEndpoints{}, c, apikey.CopyAPIKey(apiKey), subject, protocol, model, body)
}

// contentModerationIdentity 是网关进入风控前冻结的用户和归属快照。
type contentModerationIdentity = gatewayhttp.ContentModerationIdentity

// resolveContentModerationIdentity 将风控处置对象与付款归属拆开，避免团队成员触发规则时误封 Owner。
func resolveContentModerationIdentity(apiKey *apikey.APIKey, subject authctx.AuthSubject) contentModerationIdentity {
	return gatewayhttp.ResolveContentModerationIdentity(apikey.CopyAPIKey(apiKey), subject)
}
