package handler

import (
	"context"
	"fmt"
	"strings"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
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

// classifyNoAccountErrorFromGin 复用 gin.Context 上的 request context，简化 handler 调用点。
func classifyNoAccountErrorFromGin(
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

// classifyOpenAICompatibleNoAccountErrorFromGin 按 API Key 分组平台诊断 OpenAI 兼容请求。
func classifyOpenAICompatibleNoAccountErrorFromGin(
	c *gin.Context,
	diag routing.ModelAvailabilityDiagnoser,
	apiKey *apikey.APIKey,
	routingModel string,
	displayModel string,
) noAccountErrorClassification {
	return classifyNoAccountErrorFromGin(
		c,
		diag,
		apiKey,
		routingModel,
		displayModel,
		openAICompatibleRequestPlatform(apiKey),
	)
}

// openAIResolvedRoutingModelDiagnoser 让通用错误分类器直接诊断账号层模型，避免重复渠道映射。
type openAIResolvedRoutingModelDiagnoser struct {
	service *service.OpenAIGatewayService
}

func (d openAIResolvedRoutingModelDiagnoser) DiagnoseModelAvailabilityForPlatform(
	ctx context.Context,
	groupID *int64,
	routingModel string,
	platform string,
) routing.ModelAvailabilityDiagnosis {
	if d.service == nil {
		return routing.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	return d.service.DiagnoseRoutingModelAvailabilityForPlatform(ctx, groupID, routingModel, platform)
}

// classifyOpenAICompatibleResolvedRoutingNoAccountErrorFromGin 用 D 诊断能力，用 R 输出错误信息。
func classifyOpenAICompatibleResolvedRoutingNoAccountErrorFromGin(
	c *gin.Context,
	gatewayService *service.OpenAIGatewayService,
	apiKey *apikey.APIKey,
	routingModel string,
	displayModel string,
) noAccountErrorClassification {
	return classifyOpenAICompatibleNoAccountErrorFromGin(
		c,
		openAIResolvedRoutingModelDiagnoser{service: gatewayService},
		apiKey,
		routingModel,
		displayModel,
	)
}

// openAICompatibleSelectionErrorForLog 将 Grok 选择失败日志中的平台名称改为实际平台。
func openAICompatibleSelectionErrorForLog(err error, platform string) error {
	if err == nil || platform != capability.PlatformGrok {
		return err
	}
	message := strings.ReplaceAll(err.Error(), "OpenAI accounts", "Grok accounts")
	if message == err.Error() {
		return err
	}
	return fmt.Errorf("%s", message)
}
