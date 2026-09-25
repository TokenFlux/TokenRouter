package openaiattempt

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
)

// 兼容错误结构保持同一 HTTP 类型。
type noAccountErrorClassification = gatewayhttp.SelectionErrorResponse

func classifySelectionFailureError(err error, fallback noAccountErrorClassification) noAccountErrorClassification {
	return gatewayhttp.RefineSelectionError(err, fallback)
}

// 旧 Key 只投影最终分组，诊断与 HTTP 映射由所属模块完成。
func classifyNoAccountError(
	ctx context.Context,
	diag routing.ModelAvailabilityDiagnoser,
	apiKey *apikey.APIKey,
	routingModel string,
	displayModel string,
	platform string,
) noAccountErrorClassification {
	var groupID *int64
	if apiKey != nil {
		groupID = apiKey.GroupID
	}
	return gatewayhttp.ClassifySelectionError(ctx, diag, groupID, routingModel, displayModel, platform)
}

// ClassifyNoAccountErrorFromGin 复用 gin.Context 上的 request context，简化 handler 调用点。
func ClassifyNoAccountErrorFromGin(
	c *gin.Context,
	diag routing.ModelAvailabilityDiagnoser,
	apiKey *apikey.APIKey,
	routingModel string,
	displayModel string,
	platform string,
) noAccountErrorClassification {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	classification := classifyNoAccountError(ctx, diag, apiKey, routingModel, displayModel, platform)
	if classification.ModelNotFound {
		gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalModelConfiguration)
	}
	return classification
}

// ClassifyOpenAICompatibleNoAccountErrorFromGin 按 API Key 分组平台诊断 OpenAI 兼容请求。
func ClassifyOpenAICompatibleNoAccountErrorFromGin(
	c *gin.Context,
	diag routing.ModelAvailabilityDiagnoser,
	apiKey *apikey.APIKey,
	routingModel string,
	displayModel string,
) noAccountErrorClassification {
	return ClassifyNoAccountErrorFromGin(
		c,
		diag,
		apiKey,
		routingModel,
		displayModel,
		gatewayhttp.OpenAICompatibleRequestPlatform(apiKey),
	)
}
