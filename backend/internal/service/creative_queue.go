// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/creative"

var ErrCreativeQueueEmpty = native.ErrCreativeQueueEmpty
var ErrCreativeAlreadyQueued = native.ErrCreativeAlreadyQueued
var ErrCreativeLockNotAcquired = native.ErrCreativeLockNotAcquired
var ErrCreativeLeaseLost = native.ErrCreativeLeaseLost
var ErrInvalidCreativeQueuePayload = native.ErrInvalidCreativeQueuePayload

type ReservedCreativeRun = native.ReservedCreativeRun
type CreativeRunJobLock = native.CreativeRunJobLock
type CreativeRunJobLockRefresher = native.CreativeRunJobLockRefresher
type CreativeRunQueue = native.CreativeRunQueue
type CreativeTransientStore = native.CreativeTransientStore
