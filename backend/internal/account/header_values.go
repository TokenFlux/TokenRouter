// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
)

// HeaderOverrides 由账号决定适用性，名称/值安全规则由 egress 唯一执行。
// 结果不持有 credentials 内的 map，也不在读路径更新共享缓存字段。
func (a *Record) HeaderOverrides() map[string]string {
	if !a.IsHeaderOverrideEnabled() {
		return nil
	}
	return egress.ResolveHeaderOverrides(StringMappingFromRaw(a.Credentials["header_overrides"]))
}
