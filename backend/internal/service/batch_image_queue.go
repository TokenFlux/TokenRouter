// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/batchimage"

var ErrBatchImageLeaseLost = native.ErrBatchImageLeaseLost
var ErrBatchImageQueueEmpty = native.ErrBatchImageQueueEmpty
var ErrBatchImageAlreadyQueued = native.ErrBatchImageAlreadyQueued
var ErrBatchImageLockNotAcquired = native.ErrBatchImageLockNotAcquired
var ErrInvalidBatchImageQueuePayload = native.ErrInvalidBatchImageQueuePayload

type ReservedBatchImageJob = native.ReservedBatchImageJob
type BatchImageJobLock = native.BatchImageJobLock
type BatchImageQueue = native.BatchImageQueue
type BatchImageService = native.BatchImageService

func NewBatchImageService(repo BatchImageRepository, queue BatchImageQueue) *BatchImageService {
	return native.NewBatchImageService(repo, queue)
}
func IsValidBatchImageID(batchID string) bool { return native.IsValidBatchImageID(batchID) }
