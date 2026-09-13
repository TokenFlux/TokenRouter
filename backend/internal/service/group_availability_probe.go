// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

const GroupAvailabilityProbeStatusSuccess = routing.GroupAvailabilityProbeStatusSuccess

const GroupAvailabilityProbeStatusFailed = routing.GroupAvailabilityProbeStatusFailed

type GroupAvailabilityProbeDueGroup = routing.GroupAvailabilityProbeDueGroup

type GroupAvailabilityProbeResult = routing.GroupAvailabilityProbeResult

type GroupAvailabilityBucket = routing.GroupAvailabilityBucket

type GroupAvailabilitySummary = routing.GroupAvailabilitySummary

type GroupAvailabilityProbeRepository = routing.GroupAvailabilityProbeRepository
