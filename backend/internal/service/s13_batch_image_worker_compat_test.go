//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/batchimage"
)

const defaultBatchImageWorkerRequeueDelay = native.DefaultBatchImageWorkerRequeueDelay
const defaultBatchImageWorkerReserveBlockTimeout = native.DefaultBatchImageWorkerReserveBlockTimeout
