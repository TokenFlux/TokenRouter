// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"strings"
)

// 废弃探测键仅在输入边界清理；路由、调度和公开类型不得引用这些状态。
var DeprecatedOpenAIAccountExtraKeys = [...]string{
	"openai_responses_probe_status", "openai_responses_supported",
	"openai_compact_supported", "openai_compact_checked_at",
	"openai_compact_last_status", "openai_compact_last_error",
	"openai_native_compaction_v2_supported", "openai_native_compaction_v2_checked_at",
	"openai_native_compaction_v2_last_status", "openai_native_compaction_v2_last_error",
}

// NormalizeLegacyOpenAIAccountExtra 收拢账号和导入模板的历史兼容处理。
// 只规范化已提供的开关，保留模板缺省语义；旧 auto 与其它遗留值沿用默认开启。
// @project-doc docs/interfaces/openai_upstream.md#openai_account_configuration
func NormalizeLegacyOpenAIAccountExtra(extra map[string]any) {
	for _, key := range DeprecatedOpenAIAccountExtraKeys {
		delete(extra, key)
	}
	for _, key := range []string{"openai_compact_mode", OpenAINativeCompactionV2ModeExtraKey} {
		if raw, exists := extra[key]; exists {
			mode, _ := raw.(string)
			if strings.EqualFold(strings.TrimSpace(mode), OpenAICompactModeForceOff) {
				extra[key] = OpenAICompactModeForceOff
			} else {
				extra[key] = OpenAICompactModeForceOn
			}
		}
	}
}
