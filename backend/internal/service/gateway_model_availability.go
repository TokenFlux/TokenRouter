package service

import (
	"context"
	"strings"
)

// ModelAvailabilityDiagnosis 描述请求模型是否被分组内任一账号静态配置支持。
// 这里忽略限流、额度自动暂停、运行时阻断等临时状态，供 handler 在“无可用账号”
// 错误路径区分 404 model_not_found 与 503 service_unavailable。
type ModelAvailabilityDiagnosis struct {
	// HasAccountsInPool 表示查询平台下至少存在一个可调度账号；
	// Anthropic/Gemini 路径还会包含参与混排的 Antigravity 账号。
	HasAccountsInPool bool
	// HasModelSupport 表示至少有一个账号的模型映射允许请求模型。
	HasModelSupport bool
}

// ModelAvailabilityDiagnoser 由可诊断模型静态可用性的网关服务实现。
// GatewayService 与 OpenAIGatewayService 都实现该接口，方便 handler 复用同一分类器。
type ModelAvailabilityDiagnoser interface {
	DiagnoseModelAvailabilityForPlatform(
		ctx context.Context,
		groupID *int64,
		requestedModel string,
		platform string,
	) ModelAvailabilityDiagnosis
}

// DiagnoseModelAvailabilityForPlatform 检查指定平台的可调度账号，判断请求模型是否被配置支持。
// 它刻意忽略限流、额度、运行时阻断等临时状态，只回答“静态配置上是否能服务”。
//
// 该方法用于错误路径：内部失败或输入无法诊断时返回 {true,true}，
// 让调用方保守地继续走 503 分支，避免误报 404。
func (s *GatewayService) DiagnoseModelAvailabilityForPlatform(
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
		// 空模型无法判断 model_not_found，交给调用方回落到 503。
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	if strings.TrimSpace(platform) == "" {
		// 没有平台时无法限定查询范围，保守回落到 503 分支。
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	// hasForcePlatform=false 使 Anthropic/Gemini 也纳入混排的 Antigravity 账号，
	// 与实际选择账号时的候选池保持一致。
	accounts, _, err := s.listSchedulableAccounts(ctx, groupID, platform, false)
	if err != nil {
		// 查询失败时保守返回 503 分支，避免因为临时查询错误误判为 404。
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	diag := ModelAvailabilityDiagnosis{}
	for i := range accounts {
		diag.HasAccountsInPool = true
		if s.isModelSupportedByAccountWithContext(ctx, &accounts[i], requestedModel) {
			diag.HasModelSupport = true
			return diag
		}
	}
	return diag
}
