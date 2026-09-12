// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

const MaxCompositeAPIKeyGroups = apikey.MaxCompositeAPIKeyGroups

const MaxCompositeKeyPrefixLen = apikey.MaxCompositeKeyPrefixLen

type APIKeyCompositeGroupInput = apikey.APIKeyCompositeGroupInput

// NormalizeCompositeKeyPrefix 委托 Key 模块的唯一实现。
func NormalizeCompositeKeyPrefix(prefix string) (display string, normalized string, ok bool) {
	return apikey.NormalizeCompositeKeyPrefix(prefix)
}

// SplitCompositeModel 委托 Key 模块的唯一实现。
func SplitCompositeModel(model string) (prefix string, modelID string, ok bool) {
	return apikey.SplitCompositeModel(model)
}

// ResolveCompositeModel 委托 Key 模块的唯一实现。
func (k *APIKey) ResolveCompositeModel(model string) (*APIKeyCompositeGroup, string, error) {
	coreKey := APIKeyView(k)
	kView := APIKeyView(k)
	v, model, e := coreKey.ResolveCompositeModel(model)
	ApplyAPIKeyView(k, coreKey)
	ApplyAPIKeyView(k, kView)
	if v == nil {
		return nil, model, e
	}
	out := APIKeyCompositeFromView(*v)
	return &out, model, e
}

// validateCompositeGroupInputs 委托 Key 模块的唯一实现。
func validateCompositeGroupInputs(inputs []APIKeyCompositeGroupInput) ([]APIKeyCompositeGroup, error) {
	v, e := apikey.KeyValidateCompositeGroupInputs(inputs)
	return keyBindingsFromView(v), e
}

// cloneCompositeBindings 委托 Key 模块的唯一实现。
func cloneCompositeBindings(bindings []APIKeyCompositeGroup) []APIKeyCompositeGroup {
	return keyBindingsFromView(apikey.KeyCloneCompositeBindings(keyBindingsToView(bindings)))
}
