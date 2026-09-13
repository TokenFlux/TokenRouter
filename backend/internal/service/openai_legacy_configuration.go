// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func normalizeLegacyOpenAIAccountExtra(extra map[string]any) {
	acctcore.NormalizeLegacyOpenAIAccountExtra(extra)
}
