// 兼容入口只委托 Ops 的唯一实现，S15/S16 清理。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/ops"
)

type OpsIngressRejectAggregate = native.OpsIngressRejectAggregate
type OpsIngressRejectFilter = native.OpsIngressRejectFilter
type OpsIngressRejectList = native.OpsIngressRejectList
type OpsIngressRejectHealth = native.OpsIngressRejectHealth
type OpsIngressRejectRepository = native.OpsIngressRejectRepository
type OpsIngressRejectAggregator = native.OpsIngressRejectAggregator

func NewOpsIngressRejectAggregator(repo OpsIngressRejectRepository) *OpsIngressRejectAggregator {
	return native.NewOpsIngressRejectAggregator(repo)
}
