package app

import (
	"context"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/gin-gonic/gin"
)

// provideModelsHTTP 直接构造四个只读目录入口，不经过旧聚合 handler。
func provideModelsHTTP(catalogue *routing.RequestableCatalogue, reader *service.GeminiMessagesCompatService, activity *gatewayRequestActivity) *gatewayhttp.ModelsHandler {
	ports := gatewayhttp.ModelsPorts{
		ReadAccess: keyhttp.GetAPIKeyFromContext, ReadPlatform: keyhttp.GetForcePlatformFromContext,
		ReadBilling: func(c *gin.Context) (*billing.APIKeyBillingContext, bool) {
			return gatewayhttp.GetAPIKeyBillingContext(c)
		},
		SafeSegment: gemini.IsSafeGeminiModelPathSegment,
		SelectModel: func(ctx context.Context, id *int64) (gatewayhttp.GeminiModelReader, error) {
			value, err := reader.SelectAccountForAIStudioEndpoints(ctx, id)
			if err != nil {
				return nil, err
			}
			return geminiModelReadTarget{reader, value}, nil
		},
		CheckAntigravity: reader.HasAntigravityAccounts,
	}
	if catalogue != nil {
		ports.Catalogue = catalogue
	}
	result := gatewayhttp.NewModelsHandler(ports, gatewayprovider.ModelDisplayCatalogue{})
	if activity != nil {
		result.BindRequestActivity(activity.Enter)
	}
	return result
}

// geminiModelReadTarget 固化一次选择，仅允许读取模型资源；不额外回源或重新选择。
type geminiModelReadTarget struct {
	reader *service.GeminiMessagesCompatService
	value  *gatewayprovider.ExecutionAccount
}

func (p geminiModelReadTarget) Read(ctx context.Context, path string) (*gatewayhttp.ModelHTTPResponse, error) {
	value, err := p.reader.ForwardAIStudioGET(ctx, p.value, path)
	if value == nil {
		return nil, err
	}
	return &gatewayhttp.ModelHTTPResponse{StatusCode: value.StatusCode, Headers: value.Headers, Body: value.Body}, err
}
