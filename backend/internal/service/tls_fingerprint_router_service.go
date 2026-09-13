// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

type TLSFingerprintRouterRepository = egress.TLSFingerprintRouterRepository

type TLSFingerprintRouterCache = egress.TLSFingerprintRouterCache

type TLSFingerprintRouterMatchResult = egress.TLSFingerprintRouterMatchResult

type TLSFingerprintRouterService = egress.TLSFingerprintRouterService

// NewTLSFingerprintRouterService 委托所属模块的唯一实现。
func NewTLSFingerprintRouterService(
	repo TLSFingerprintRouterRepository,
	cache TLSFingerprintRouterCache,
) *TLSFingerprintRouterService {
	return egress.NewTLSFingerprintRouterService(repo, cache, egress.Diagnostics{Logf: logger.LegacyPrintf})
}
