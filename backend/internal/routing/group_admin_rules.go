// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"context"
	"fmt"
	"sort"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// ClearOtherPlatformDefaultGroups 清理同平台的其他默认分组。
// 只有当当前分组准备成为默认分组时才会执行，避免无意义的额外更新。
func (s *GroupAdmin) ClearOtherPlatformDefaultGroups(ctx context.Context, platform string, excludeID int64, enableDefault bool) error {
	if !enableDefault {
		return nil
	}

	groups, err := s.groupRepo.ListActiveByPlatformLite(ctx, platform)
	if err != nil {
		return fmt.Errorf("list active groups by platform: %w", err)
	}
	for i := range groups {
		group := groups[i]
		if group.ID == excludeID || !group.IsDefault {
			continue
		}
		group.IsDefault = false
		if err := s.groupRepo.Update(ctx, &group); err != nil {
			return TranslateGroupDefaultConflict(err)
		}
	}
	return nil
}

// ValidateUnavailableFallbackGroup 校验分组不可用时的指定回退分组。
// 该回退会继承入口平台语义，因此必须指向同平台且当前可用的分组。
func (s *GroupAdmin) ValidateUnavailableFallbackGroup(ctx context.Context, currentGroupID int64, platform string, fallbackGroupID int64) error {
	if currentGroupID > 0 && currentGroupID == fallbackGroupID {
		return fmt.Errorf("cannot set self as unavailable fallback group")
	}
	fallbackGroup, err := s.groupRepo.GetByIDLite(ctx, fallbackGroupID)
	if err != nil {
		return fmt.Errorf("unavailable fallback group not found: %w", err)
	}
	if fallbackGroup.Platform != platform {
		return fmt.Errorf("unavailable fallback group must use the same platform")
	}
	if !fallbackGroup.IsActive() {
		return fmt.Errorf("unavailable fallback group must be active")
	}
	return nil
}

func ConfiguredModelsListCandidateIDs(accounts []GroupAccount, platform string) []string {
	modelSet := make(map[string]struct{})
	hasAnyConfiguredModels := false
	for _, acc := range accounts {
		if acc.Platform != platform {
			continue
		}
		requestModels := acc.Models
		if len(requestModels) == 0 {
			continue
		}
		hasAnyConfiguredModels = true
		for _, model := range requestModels {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			modelSet[model] = struct{}{}
		}
	}
	if !hasAnyConfiguredModels {
		return nil
	}

	// 候选项按字典序稳定输出，避免编辑分组时下拉列表随机抖动。
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

func FilterModelsListCandidates(candidates []string, selectedModels []string) []string {
	normalizedSelected := NormalizeGroupModelsListConfig(GroupModelsListConfig{
		Enabled: true,
		Models:  selectedModels,
	}).Models
	if len(normalizedSelected) == 0 {
		return nil
	}

	if len(candidates) == 0 {
		return normalizedSelected
	}

	allowed := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			allowed = append(allowed, candidate)
		}
	}

	// 按自定义模型列表顺序输出，确保探测下拉与管理员配置顺序一致。
	filtered := make([]string, 0, len(normalizedSelected))
	for _, model := range normalizedSelected {
		if ModelsListCandidateAllowsModel(allowed, model) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func ModelsListCandidateAllowsModel(availablePatterns []string, model string) bool {
	for _, pattern := range availablePatterns {
		if pattern == model {
			return true
		}
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(model, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

// NormalizeGroupDefaultState 统一处理默认分组的最终状态。
// 非 active 分组不保留默认标记，避免出现“默认但不可用”的歧义。
func NormalizeGroupDefaultState(group *Group) {
	if group == nil {
		return
	}
	if group.Status != StatusActive {
		group.IsDefault = false
	}
}

func TranslateGroupDefaultConflict(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "groups_platform_default_active_unique") {
		return infraerrors.Conflict("GROUP_DEFAULT_CONFLICT", "default group already exists for this platform").WithCause(err)
	}
	return err
}

func SanitizeGroupReasoningEffortPolicy(group *Group) {
	if group == nil {
		return
	}
	maxEffort, maxErr := NormalizeMaxReasoningEffortForPlatform(group.Platform, group.MaxReasoningEffort)
	mappings, mappingsErr := NormalizeReasoningEffortMappings(group.Platform, group.ReasoningEffortMappings)
	if maxErr != nil {
		maxEffort = ""
	}
	if mappingsErr != nil {
		mappings = []ReasoningEffortMapping{}
	}
	overLimit := NormalizeMaxReasoningEffortOverLimit(group.MaxReasoningEffortOverLimit)
	if overLimit == "" || (overLimit == ReasoningEffortOverLimitDeny && group.Platform != PlatformAnthropic && group.Platform != PlatformOpenAI) {
		overLimit = ReasoningEffortOverLimitDowngrade
	}
	group.MaxReasoningEffort = maxEffort
	group.MaxReasoningEffortOverLimit = overLimit
	group.ReasoningEffortMappings = mappings
}

func SanitizeGroupMessagesDispatchFields(g *Group) {
	if g == nil {
		return
	}
	// 弃用列只镜像 OpenAI 分组的 Messages 准入，供旧管理 API 字段保持一致。
	g.AllowMessagesDispatch = g.Platform == PlatformOpenAI && g.AllowsClientProtocol(protocol.ProtocolAnthropicMessages)
	if g.Platform == PlatformOpenAI {
		return
	}
	g.DefaultMappedModel = ""
	g.MessagesDispatchModelConfig = OpenAIMessagesDispatchModelConfig{}
}
