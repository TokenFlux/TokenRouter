// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	maps "maps"
)

// DiscardDeprecatedAccountExtra 静默移除旧客户端可能继续提交的废弃账号扩展键。
func DiscardDeprecatedAccountExtra(extra map[string]any) {
	NormalizeLegacyOpenAIAccountExtra(extra)
	DiscardDeprecatedExtra(extra)
}

// NormalizeDeprecatedAccountExtraUpdate 规范化整份替换语义的账号 Extra 更新。
// 第二个返回值表示是否仍应执行替换：显式空对象保留清空语义，只有废弃键的对象视为未提供更新。
func NormalizeDeprecatedAccountExtraUpdate(extra map[string]any) (map[string]any, bool) {
	if extra == nil {
		return nil, false
	}
	normalized := maps.Clone(extra)
	DiscardDeprecatedAccountExtra(normalized)
	if len(extra) > 0 && len(normalized) == 0 {
		return nil, false
	}
	return normalized, true
}
