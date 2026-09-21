// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"

	config "github.com/TokenFlux/TokenRouter/internal/config"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

func (s *OpenAIGatewayService) DiagnoseModelAvailabilityForPlatform(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	platform string,
) routing.ModelAvailabilityDiagnosis {
	return s.modelAvailability().DiagnoseCompatible(ctx, groupID, requestedModel, platform)
}

func (s *OpenAIGatewayService) DiagnoseRoutingModelAvailabilityForPlatform(
	ctx context.Context,
	groupID *int64,
	routingModel string,
	platform string,
) routing.ModelAvailabilityDiagnosis {
	return s.modelAvailability().DiagnoseCompatibleRouting(ctx, groupID, routingModel, platform)
}

func (s *OpenAIGatewayService) modelAvailability() *routing.ModelAvailability {
	if s == nil {
		return nil
	}
	return &routing.ModelAvailability{Simple: s.cfg != nil && s.cfg.RunMode == config.RunModeSimple, Read: legacyAvailabilityReader(s.accountRepo, openAIAccountSupportsRoutingModel), MapModel: s.resolveChannelRoutingModel}
}
