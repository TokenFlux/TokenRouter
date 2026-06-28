package service

import (
	"context"
	"strings"
)

// DiagnoseModelAvailabilityForPlatform 判断请求模型是否被分组内指定 OpenAI 兼容平台账号配置支持。
// platform 用于限定候选池，避免 OpenAI 与 Grok 等兼容平台互相污染诊断结果。
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

	accounts, err := s.listSchedulableAccounts(ctx, groupID, platform)
	if err != nil {
		// 查询失败时保守返回 503 分支，避免临时查询错误误判为 404 model_not_found。
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	diag := ModelAvailabilityDiagnosis{}
	for i := range accounts {
		diag.HasAccountsInPool = true
		// 与账号选择时的候选过滤保持一致：空 model_mapping 表示允许全部模型；
		// 否则必须命中显式映射或通配符映射。
		if accounts[i].IsModelSupported(requestedModel) {
			diag.HasModelSupport = true
			return diag
		}
	}
	return diag
}
