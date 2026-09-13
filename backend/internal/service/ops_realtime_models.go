// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/ops"

type PlatformConcurrencyInfo = native.PlatformConcurrencyInfo
type GroupConcurrencyInfo = native.GroupConcurrencyInfo
type AccountConcurrencyInfo = native.AccountConcurrencyInfo
type UserConcurrencyInfo = native.UserConcurrencyInfo
type PlatformAvailability = native.PlatformAvailability
type GroupAvailability = native.GroupAvailability
type AccountAvailability = native.AccountAvailability
