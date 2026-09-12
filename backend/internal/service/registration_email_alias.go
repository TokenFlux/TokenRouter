// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

// NormalizeEmailForAliasDedup 委托所属模块的唯一实现。
func NormalizeEmailForAliasDedup(email string) string {
	return identity.NormalizeEmailForAliasDedup(email)
}

type EmailAliasProbe = identity.EmailAliasProbe

// EmailAliasDedupProbes 委托所属模块的唯一实现。
func EmailAliasDedupProbes(email string) []EmailAliasProbe {
	return identity.EmailAliasDedupProbes(email)
}
