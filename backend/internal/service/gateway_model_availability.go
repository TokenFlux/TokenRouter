package service

import (
	"context"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/config"
)

// ModelAvailabilityDiagnosis 描述请求模型是否被分组内任一持久可用账号支持。
// 持久可用指账号为 active 且启用 schedulable；诊断忽略限流、过载、临时不可调度和
// 运行时阻断等瞬时状态，供 handler 区分 404 model_not_found 与 503 service_unavailable。
type ModelAvailabilityDiagnosis struct {
	// HasAccountsInPool 表示查询平台下至少存在一个持久可用账号；
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

// DiagnoseModelAvailabilityForPlatform 通过专用持久配置查询检查指定平台的账号，
// 判断请求模型是否被配置支持。该查询绕过调度快照，忽略限流、过载、临时不可调度、
// 到期窗口、额度和运行时阻断等瞬时状态。
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

	if s.accountRepo == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	useMixed := platform == PlatformAnthropic || platform == PlatformGemini
	platforms := []string{platform}
	if useMixed {
		platforms = append(platforms, PlatformAntigravity)
	}

	queryGroupID := groupID
	includeGrouped := false
	if useMixed {
		// 保持通用调度器的池范围：混排时显式分组优先；无分组的 simple 模式扫描全部账号。
		if groupID == nil && s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
			includeGrouped = true
		}
	} else if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		queryGroupID = nil
		includeGrouped = true
	}

	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, queryGroupID, platforms, includeGrouped)
	if err != nil {
		// 查询失败时保守返回 503 分支，避免因为临时查询错误误判为 404。
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	diag := ModelAvailabilityDiagnosis{}
	routingModel := s.channelMappedModelForGroup(ctx, groupID, requestedModel)
	for i := range accounts {
		if useMixed && accounts[i].Platform == PlatformAntigravity && !accounts[i].IsMixedSchedulingEnabled() {
			continue
		}
		diag.HasAccountsInPool = true
		if s.isRoutingModelSupportedByAccountWithContext(ctx, &accounts[i], routingModel) {
			diag.HasModelSupport = true
			return diag
		}
	}
	return diag
}
