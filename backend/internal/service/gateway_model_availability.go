// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"

	config "github.com/TokenFlux/TokenRouter/internal/config"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

func (s *GatewayService) DiagnoseModelAvailabilityForPlatform(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	platform string,
) routing.ModelAvailabilityDiagnosis {
	return s.modelAvailability().DiagnoseGeneral(ctx, groupID, requestedModel, platform)
}

func (s *GatewayService) modelAvailability() *routing.ModelAvailability {
	if s == nil {
		return nil
	}
	return &routing.ModelAvailability{Simple: s.cfg != nil && s.cfg.RunMode == config.RunModeSimple, Read: legacyAvailabilityReader(s.accountRepo, s.isRoutingModelSupportedByAccountWithContext), MapModel: s.channelMappedModelForGroup}
}
