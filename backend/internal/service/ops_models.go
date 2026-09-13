// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/ops"

type OpsSystemLog = native.OpsSystemLog
type OpsRequestTiming = native.OpsRequestTiming

func OpsRequestTimingFromExtra(extra map[string]any) *OpsRequestTiming {
	return native.OpsRequestTimingFromExtra(extra)
}

type OpsErrorLog = native.OpsErrorLog
type OpsErrorLogDetail = native.OpsErrorLogDetail
type OpsErrorLogFilter = native.OpsErrorLogFilter
type OpsErrorLogList = native.OpsErrorLogList
