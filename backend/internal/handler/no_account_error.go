package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// noAccountErrorClassification 描述账号选择失败时应返回给客户端的错误。
//
//   - 404 model_not_found：分组里有账号，但没有任何账号配置支持请求模型。
//     这通常是模型配置、拼写或上游不支持的问题，返回 404 让客户端看到真实原因。
//
//   - 503 api_error：有账号可能支持该模型但暂时不可用，或分组当前没有账号。
//     这类问题保留 503，因为退避后重试或管理员补账号后仍可能恢复。
type noAccountErrorClassification struct {
	Status        int
	ErrType       string
	Message       string
	ModelNotFound bool // true 表示本次应返回 404 model_not_found
}

// classifyNoAccountError 在“无可用账号”场景下区分 404 model_not_found 与 503 api_error。
//
// 选择层不会明确告诉调用方账号池为空的具体原因：限流和模型不支持都可能包装成
// ErrNoAvailableAccounts。因此这里重新检查账号池配置，只看 model_mapping 等静态配置，
// 忽略限流、额度暂停等临时状态，确保只有“改账号模型配置才可能成功”的场景返回 404。
//
// routingModel 是账号选择实际比较的模型名（可能已经过分组模型分发映射）；
// displayModel 是调用方原始请求模型，仅用于客户端错误消息，避免泄露内部映射细节。
//
// platform 是请求实际路由到的平台。Anthropic/Gemini 路径还会纳入混排的 Antigravity
// 账号，因此必须传入正确平台，避免把临时 503 误判为 404。
func classifyNoAccountError(
	ctx context.Context,
	diag service.ModelAvailabilityDiagnoser,
	apiKey *service.APIKey,
	routingModel string,
	displayModel string,
	platform string,
) noAccountErrorClassification {
	fallback := noAccountErrorClassification{
		Status:  http.StatusServiceUnavailable,
		ErrType: "api_error",
		Message: "Service temporarily unavailable",
	}

	routingModel = strings.TrimSpace(routingModel)
	displayModel = strings.TrimSpace(displayModel)
	if displayModel == "" {
		displayModel = routingModel
	}
	if diag == nil || apiKey == nil || apiKey.GroupID == nil || routingModel == "" {
		return fallback
	}

	result := diag.DiagnoseModelAvailabilityForPlatform(ctx, apiKey.GroupID, routingModel, platform)
	if result.HasAccountsInPool && !result.HasModelSupport {
		return noAccountErrorClassification{
			Status:        http.StatusNotFound,
			ErrType:       "model_not_found",
			Message:       fmt.Sprintf("Model %q is not supported by any configured account in this group", displayModel),
			ModelNotFound: true,
		}
	}
	return fallback
}

// classifyNoAccountErrorFromGin 复用 gin.Context 上的 request context，简化 handler 调用点。
func classifyNoAccountErrorFromGin(
	c *gin.Context,
	diag service.ModelAvailabilityDiagnoser,
	apiKey *service.APIKey,
	routingModel string,
	displayModel string,
	platform string,
) noAccountErrorClassification {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	return classifyNoAccountError(ctx, diag, apiKey, routingModel, displayModel, platform)
}
