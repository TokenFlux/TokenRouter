package creative

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// CreativeOperationOrder 保证设置、候选和公开目录中的能力顺序稳定。
var CreativeOperationOrder = []string{
	CreativeOperationGenerate,
	CreativeOperationEdit,
	CreativeOperationInpaint,
}

// CreativeModelSetting 是管理员配置的创作台分组、模型和能力白名单项。
type CreativeModelSetting struct {
	GroupID    int64    `json:"group_id"`
	Model      string   `json:"model"`
	Operations []string `json:"operations"`
}

// CreativeModelCandidate 是管理端可选择的当前生图模型候选。
type CreativeModelCandidate struct {
	GroupID    int64    `json:"group_id"`
	GroupName  string   `json:"group_name"`
	Platform   string   `json:"platform"`
	Model      string   `json:"model"`
	Operations []string `json:"operations"`
}

// NormalizeCreativeModelSettingsForSave 按当前候选模型能力保留操作，历史不可解析的模型留待管理员处理。
func (s *Public) NormalizeCreativeModelSettingsForSave(ctx context.Context, input []CreativeModelSetting) ([]CreativeModelSetting, error) {
	normalized, err := NormalizeCreativeModelSettings(input)
	if err != nil || s == nil || s.GroupRepo == nil {
		return normalized, err
	}
	out := make([]CreativeModelSetting, 0, len(normalized))
	queries := make(map[int64]*creativeModelQuery)
	for _, item := range normalized {
		query, loaded := queries[item.GroupID]
		if !loaded {
			group, lookupErr := s.GroupRepo.GetByIDLite(ctx, item.GroupID)
			if lookupErr == nil && group != nil {
				var routeErr error
				query, routeErr = s.prepareCreativeModels(ctx, group)
				if routeErr != nil {
					return nil, routeErr
				}
			}
			queries[item.GroupID] = query
		}
		if route, ok := query.resolve(ctx, item.Model); ok {
			item.Operations = intersectCreativeOperations(item.Operations, route.Operations)
		}
		if len(item.Operations) > 0 {
			out = append(out, item)
		}
	}
	return out, nil
}

// NormalizeCreativeModelSettings 校验并规范化管理员配置。
func NormalizeCreativeModelSettings(input []CreativeModelSetting) ([]CreativeModelSetting, error) {
	out := make([]CreativeModelSetting, 0, len(input))
	seenModels := make(map[string]struct{}, len(input))
	for index, item := range input {
		if item.GroupID <= 0 {
			return nil, fmt.Errorf("creative model setting %d group_id must be positive", index)
		}
		model := strings.TrimSpace(item.Model)
		if model == "" {
			return nil, fmt.Errorf("creative model setting %d model is required", index)
		}

		configured := make(map[string]struct{}, len(item.Operations))
		for _, operation := range item.Operations {
			operation = strings.ToLower(strings.TrimSpace(operation))
			switch operation {
			case CreativeOperationGenerate, CreativeOperationEdit, CreativeOperationInpaint:
				configured[operation] = struct{}{}
			default:
				return nil, fmt.Errorf("creative model setting %d operation %q is invalid", index, operation)
			}
		}
		if len(configured) == 0 {
			return nil, fmt.Errorf("creative model setting %d must contain at least one operation", index)
		}

		modelKey := fmt.Sprintf("%d:%s", item.GroupID, model)
		if _, exists := seenModels[modelKey]; exists {
			return nil, fmt.Errorf("creative model setting for group %d model %q is duplicated", item.GroupID, model)
		}
		seenModels[modelKey] = struct{}{}

		operations := make([]string, 0, len(configured))
		for _, operation := range CreativeOperationOrder {
			if _, ok := configured[operation]; ok {
				operations = append(operations, operation)
			}
		}
		out = append(out, CreativeModelSetting{
			GroupID:    item.GroupID,
			Model:      model,
			Operations: operations,
		})
	}
	return out, nil
}

// MarshalCreativeModelSettings 校验后生成稳定 JSON，供 settings 表持久化。
func MarshalCreativeModelSettings(input []CreativeModelSetting) (string, []CreativeModelSetting, error) {
	normalized, err := NormalizeCreativeModelSettings(input)
	if err != nil {
		return "", nil, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", nil, fmt.Errorf("marshal creative model settings: %w", err)
	}
	return string(raw), normalized, nil
}

// CreativeModelSettingsIndex 将配置转换为精确的分组+模型索引。
func CreativeModelSettingsIndex(settings []CreativeModelSetting) map[string][]string {
	index := make(map[string][]string, len(settings))
	for _, setting := range settings {
		key := fmt.Sprintf("%d:%s", setting.GroupID, strings.TrimSpace(setting.Model))
		index[key] = append([]string(nil), setting.Operations...)
	}
	return index
}

// CreativeOperationsForModel 计算配置能力与平台能力的交集。
func CreativeOperationsForModel(settings map[string][]string, groupID int64, model string, supported []string) ([]string, bool) {
	configured, ok := settings[fmt.Sprintf("%d:%s", groupID, strings.TrimSpace(model))]
	if !ok {
		return nil, false
	}
	supportedSet := make(map[string]struct{}, len(supported))
	for _, operation := range supported {
		supportedSet[operation] = struct{}{}
	}
	operations := make([]string, 0, len(configured))
	for _, operation := range CreativeOperationOrder {
		if ContainsCreativeOperation(configured, operation) {
			if _, supported := supportedSet[operation]; supported {
				operations = append(operations, operation)
			}
		}
	}
	return operations, true
}

func ContainsCreativeOperation(values []string, target string) bool {
	return slices.Contains(values, target)
}
