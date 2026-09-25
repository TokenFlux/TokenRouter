// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

const (
	// MaxCompositeAPIKeyGroups 限制认证缓存和模型列表聚合的最大规模。
	MaxCompositeAPIKeyGroups = 20
	MaxCompositeKeyPrefixLen = 32
)

var KeyCompositeKeyPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// APIKeyCompositeGroupInput 是创建和更新复合映射时的公共输入。
type APIKeyCompositeGroupInput struct {
	GroupID int64  `json:"group_id"`
	Prefix  string `json:"prefix"`
}

// NormalizeCompositeKeyPrefix 规范化并校验用户输入的分组前缀。
func NormalizeCompositeKeyPrefix(prefix string) (display string, normalized string, ok bool) {
	display = strings.TrimSpace(prefix)
	if display == "" || len(display) > MaxCompositeKeyPrefixLen || !KeyCompositeKeyPrefixPattern.MatchString(display) {
		return "", "", false
	}
	return display, strings.ToLower(display), true
}

// SplitCompositeModel 按第一个斜杠拆分复合模型，真实模型 ID 可以继续包含斜杠。
func SplitCompositeModel(model string) (prefix string, modelID string, ok bool) {
	model = strings.TrimSpace(model)
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	prefix = strings.TrimSpace(parts[0])
	modelID = strings.TrimSpace(parts[1])
	if prefix == "" || modelID == "" {
		return "", "", false
	}
	return prefix, modelID, true
}

// ResolveCompositeModel 根据大小写不敏感前缀选择分组并返回真实模型 ID。
func (k *APIKey) ResolveCompositeModel(model string) (*APIKeyCompositeGroup, string, error) {
	if k == nil || !k.IsComposite {
		return nil, model, nil
	}
	prefix, modelID, ok := SplitCompositeModel(model)
	if !ok {
		return nil, "", ErrCompositeKeyPrefixRequired
	}
	_, normalized, valid := NormalizeCompositeKeyPrefix(prefix)
	if !valid {
		return nil, "", ErrCompositeKeyPrefixInvalid
	}
	for i := range k.CompositeGroups {
		binding := &k.CompositeGroups[i]
		if binding.NormalizedPrefix == normalized {
			return binding, modelID, nil
		}
	}
	return nil, "", ErrCompositeKeyPrefixNotFound
}

// KeyValidateCompositeGroupInputs 只处理与数据库无关的结构校验并保留用户顺序。
func KeyValidateCompositeGroupInputs(inputs []APIKeyCompositeGroupInput) ([]APIKeyCompositeGroup, error) {
	if len(inputs) == 0 {
		return nil, ErrCompositeKeyGroupsRequired
	}
	if len(inputs) > MaxCompositeAPIKeyGroups {
		return nil, ErrCompositeKeyTooManyGroups
	}
	groups := make([]APIKeyCompositeGroup, 0, len(inputs))
	seenGroups := make(map[int64]struct{}, len(inputs))
	seenPrefixes := make(map[string]struct{}, len(inputs))
	for index, input := range inputs {
		if input.GroupID <= 0 {
			return nil, ErrGroupNotAllowed
		}
		if _, exists := seenGroups[input.GroupID]; exists {
			return nil, ErrCompositeKeyGroupDuplicate
		}
		display, normalized, ok := NormalizeCompositeKeyPrefix(input.Prefix)
		if !ok {
			return nil, ErrCompositeKeyPrefixInvalid
		}
		if _, exists := seenPrefixes[normalized]; exists {
			return nil, ErrCompositeKeyPrefixDuplicate
		}
		seenGroups[input.GroupID] = struct{}{}
		seenPrefixes[normalized] = struct{}{}
		groups = append(groups, APIKeyCompositeGroup{
			GroupID:          input.GroupID,
			Prefix:           display,
			NormalizedPrefix: normalized,
			SortOrder:        index,
		})
	}
	return groups, nil
}

// KeyPrepareCompositeGroups 校验复合 Key 的分组权限并补齐分组快照。
func (s *APIKeyService) KeyPrepareCompositeGroups(
	ctx context.Context,
	user *User,
	inputs []APIKeyCompositeGroupInput,
) ([]APIKeyCompositeGroup, error) {
	bindings, err := KeyValidateCompositeGroupInputs(inputs)
	if err != nil {
		return nil, err
	}
	for i := range bindings {
		binding := &bindings[i]
		group, err := s.groupRepo.GetByID(ctx, binding.GroupID)
		if err != nil {
			return nil, err
		}
		if !s.KeyCanUserBindGroup(ctx, user, group) {
			return nil, ErrGroupNotAllowed
		}
		binding.Group = group
	}
	sort.SliceStable(bindings, func(i, j int) bool { return bindings[i].SortOrder < bindings[j].SortOrder })
	return bindings, nil
}

// KeyCloneCompositeBindings 避免认证请求对缓存快照中的切片原地修改。
func KeyCloneCompositeBindings(bindings []APIKeyCompositeGroup) []APIKeyCompositeGroup {
	if len(bindings) == 0 {
		return nil
	}
	out := make([]APIKeyCompositeGroup, len(bindings))
	copy(out, bindings)
	return out
}
