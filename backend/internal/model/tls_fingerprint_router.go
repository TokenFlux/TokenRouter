// 本文件维护 model 的所属能力；兼容入口复用唯一实现。
package model

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
)

const TLSRouterMatchContains = egress.TLSRouterMatchContains

const TLSRouterMatchPrefix = egress.TLSRouterMatchPrefix

const TLSRouterMatchExact = egress.TLSRouterMatchExact

const TLSRouterMatchRegex = egress.TLSRouterMatchRegex

type TLSFingerprintRouterRule = egress.TLSFingerprintRouterRule

type TLSFingerprintRouter = egress.TLSFingerprintRouter

// NormalizeTLSRouterMatchType 委托所属模块的唯一实现。
func NormalizeTLSRouterMatchType(matchType string) string {
	return egress.NormalizeTLSRouterMatchType(matchType)
}
