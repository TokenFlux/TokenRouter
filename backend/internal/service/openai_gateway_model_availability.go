package service

import (
	"context"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/config"
)

// DiagnoseModelAvailabilityForPlatform 判断请求模型是否被分组内指定 OpenAI 兼容平台账号配置支持。
// platform 用于限定候选池，避免 OpenAI 与 Grok 等兼容平台互相污染诊断结果。
// 诊断使用持久配置查询，绕过调度快照并忽略瞬时运行状态。
//
// 该方法用于错误路径：内部失败、空模型或 nil service 时返回 {true,true}，
// 让调用方保守地继续走 503 分支。
func (s *OpenAIGatewayService) DiagnoseModelAvailabilityForPlatform(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	platform string,
) ModelAvailabilityDiagnosis {
	if s == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	routingModel := s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	return s.DiagnoseRoutingModelAvailabilityForPlatform(ctx, groupID, routingModel, platform)
}

// DiagnoseRoutingModelAvailabilityForPlatform 直接诊断已经完成渠道及分组映射的账号层模型。
// Messages 错误路径使用该入口，避免把 D 再次当作客户端模型执行渠道映射。
func (s *OpenAIGatewayService) DiagnoseRoutingModelAvailabilityForPlatform(
	ctx context.Context,
	groupID *int64,
	routingModel string,
	platform string,
) ModelAvailabilityDiagnosis {
	if s == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	routingModel = strings.TrimSpace(routingModel)
	if routingModel == "" {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	if s.accountRepo == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	platform = normalizeOpenAICompatiblePlatform(platform)
	queryGroupID := groupID
	includeGrouped := false
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		queryGroupID = nil
		includeGrouped = true
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(
		ctx,
		queryGroupID,
		[]string{platform},
		includeGrouped,
	)
	if err != nil {
		// 查询失败时保守返回 503 分支，避免临时查询错误误判为 404 model_not_found。
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	diag := ModelAvailabilityDiagnosis{}
	for i := range accounts {
		diag.HasAccountsInPool = true
		// 与账号选择时的候选过滤保持一致：空 model_mapping 表示允许全部模型；
		// 否则必须命中显式映射或通配符映射。
		if openAIAccountSupportsRoutingModel(ctx, &accounts[i], routingModel) {
			diag.HasModelSupport = true
			return diag
		}
	}
	return diag
}
