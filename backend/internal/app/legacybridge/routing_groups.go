// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	context "context"
	policy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// GroupDefaultModels 和 GroupValidationWeights 保留平台目录与 S07 设置的原读取时机。
func GroupDefaultModels(platform string) []string {
	return service.DefaultGroupModelCandidates(platform)
}
func NormalizeGroupMappedModel(model string) string {
	return service.NormalizeOpenAICompatRequestedModel(model)
}
func GroupValidationWeights(settings *service.SettingService) func(context.Context) (policy.ScoreWeights, error) {
	return func(ctx context.Context) (policy.ScoreWeights, error) {
		value, err := service.GroupValidationWeights(ctx, settings)
		return policy.ScoreWeights(value), err
	}
}
