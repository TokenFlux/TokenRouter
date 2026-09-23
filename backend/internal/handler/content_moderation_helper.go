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
	h.openAIAttemptSupport().RecordOpenAICyberWarning(c, reqLog, apiKey, account, model, warning.StatusCode, warning.ResponseBody, warning.Message)
}

func buildOpenAICyberWarningInput(c *gin.Context, apiKey *apikey.APIKey, account *gatewayprovider.ExecutionAccount, model string, statusCode int, responseBody []byte, warningText string, promptExcerpt string) moderation.ContentModerationCyberWarningInput {
	return gatewayhttp.BuildOpenAICyberWarningInput(gatewayhttp.GatewayModerationEndpoints{}, c, apikey.CopyAPIKey(apiKey), moderationAccountView(account), model, statusCode, responseBody, warningText, promptExcerpt)
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
