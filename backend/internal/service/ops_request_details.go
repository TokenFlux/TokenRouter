// 兼容入口只委托 Ops 的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/ops"

type OpsRequestKind = native.OpsRequestKind

const OpsRequestKindSuccess = native.OpsRequestKindSuccess
const OpsRequestKindError = native.OpsRequestKindError

type OpsRequestDetail = native.OpsRequestDetail
type OpsRequestDetailFilter = native.OpsRequestDetailFilter
type OpsRequestDetailList = native.OpsRequestDetailList
