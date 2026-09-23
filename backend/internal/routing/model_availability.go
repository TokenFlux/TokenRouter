// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	context "context"
	strings "strings"
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

// ModelAvailabilityDiagnoser 提供模型静态可用性的窄读取能力，供入口复用同一分类器。
type ModelAvailabilityDiagnoser interface {
	DiagnoseModelAvailabilityForPlatform(
		ctx context.Context,
		groupID *int64,
		requestedModel string,
		platform string,
	) ModelAvailabilityDiagnosis
}

// DiagnoseGeneral 通过专用持久配置查询检查指定平台的账号，
// 判断请求模型是否被配置支持。该查询绕过调度快照，忽略限流、过载、临时不可调度、
// 到期窗口、额度和运行时阻断等瞬时状态。
//
// 该方法用于错误路径：内部失败或输入无法诊断时返回 {true,true}，
// 让调用方保守地继续走 503 分支，避免误报 404。
func (s *ModelAvailability) DiagnoseGeneral(
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

	if s.Read == nil {
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
		if groupID == nil && s.Simple {
			includeGrouped = true
		}
	} else if s.Simple {
		queryGroupID = nil
		includeGrouped = true
	}

	accounts, err := s.Read(ctx, queryGroupID, platforms, includeGrouped)
	if err != nil {
		// 查询失败时保守返回 503 分支，避免因为临时查询错误误判为 404。
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	diag := ModelAvailabilityDiagnosis{}
	routingModel := s.MapModel(ctx, groupID, requestedModel)
	for i := range accounts {
		if useMixed && accounts[i].Platform == PlatformAntigravity && !accounts[i].MixedScheduling {
			continue
		}
		diag.HasAccountsInPool = true
		if accounts[i].Supports(ctx, routingModel) {
			diag.HasModelSupport = true
			return diag
		}
	}
	return diag
}

// DiagnoseCompatible 判断请求模型是否被分组内指定 OpenAI 兼容平台账号配置支持。
// platform 用于限定候选池，避免 OpenAI 与 Grok 等兼容平台互相污染诊断结果。
// 诊断使用持久配置查询，绕过调度快照并忽略瞬时运行状态。
//
// 该方法用于错误路径：内部失败、空模型或 nil service 时返回 {true,true}，
// 让调用方保守地继续走 503 分支。
func (s *ModelAvailability) DiagnoseCompatible(
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
	routingModel := s.MapModel(ctx, groupID, requestedModel)
	return s.DiagnoseCompatibleRouting(ctx, groupID, routingModel, platform)
}

// DiagnoseCompatibleRouting 直接诊断已经完成渠道及分组映射的账号层模型。
// Messages 错误路径使用该入口，避免把 D 再次当作客户端模型执行渠道映射。
func (s *ModelAvailability) DiagnoseCompatibleRouting(
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
	if s.Read == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	platform = NormalizeOpenAICompatiblePlatform(platform)
	queryGroupID := groupID
	includeGrouped := false
	if s.Simple {
		queryGroupID = nil
		includeGrouped = true
	}
	accounts, err := s.Read(
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
		if accounts[i].Supports(ctx, routingModel) {
			diag.HasModelSupport = true
			return diag
		}
	}
	return diag
}

// AvailabilityAccount 只暴露持久资格投影和模型判断端口，不持有可任意读取的凭据。
type AvailabilityAccount struct {
	Platform        string
	MixedScheduling bool
	Supports        func(context.Context, string) bool
}
type AvailabilityReader func(context.Context, *int64, []string, bool) ([]AvailabilityAccount, error)

// ModelAvailability 用已有查询边界区分永久模型缺失和暂时容量不足；自身无缓存。
type ModelAvailability struct {
	Simple   bool
	Read     AvailabilityReader
	MapModel func(context.Context, *int64, string) string
}

// ModelAvailabilityDiagnoserFunc 将已绑定诊断意图交给消费者，不新增查询或缓存。
type ModelAvailabilityDiagnoserFunc func(context.Context, *int64, string, string) ModelAvailabilityDiagnosis

func (f ModelAvailabilityDiagnoserFunc) DiagnoseModelAvailabilityForPlatform(ctx context.Context, group *int64, model, platform string) ModelAvailabilityDiagnosis {
	return f(ctx, group, model, platform)
}
