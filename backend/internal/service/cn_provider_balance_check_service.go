// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

const CNUsageMonitorSnapshotExtraKey = acctcore.CNUsageMonitorSnapshotExtraKey
const cnUsageMonitorSnapshotVersion = acctcore.CNUsageMonitorSnapshotVersion

type CNUsageMonitorError = acctcore.CNUsageMonitorError
type CNUsageMonitorSnapshot = acctcore.CNUsageMonitorSnapshot

func cnUsageBalanceBelowThreshold(result *UpstreamUsageQueryResult, threshold float64) (bool, bool) {
	return acctcore.CNUsageBalanceBelowThreshold(result, threshold)
}
func cnUsageMonitorReason(identityHash string) string {
	return acctcore.CNUsageMonitorReason(identityHash)
}
