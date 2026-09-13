// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/ops"
)

type OpsQueryMode = native.OpsQueryMode

const OpsQueryModeAuto = native.OpsQueryModeAuto
const OpsQueryModeRaw = native.OpsQueryModeRaw

var ErrOpsPreaggregatedNotPopulated = native.ErrOpsPreaggregatedNotPopulated
