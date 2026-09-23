package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/service"
)

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
