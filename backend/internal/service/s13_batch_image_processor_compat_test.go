//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
)

const defaultBatchImageProcessorRequeue = batchimage.DefaultBatchImageProcessorRequeue

func isBatchImageProcessorDoneStatus(status string) bool {
	return batchimage.IsBatchImageProcessorDoneStatus(status)
}
func batchImageDerefString(v *string) string       { return batchimage.BatchImageDerefString(v) }
func batchImageStringPtr(v string) *string         { return batchimage.BatchImageStringPtr(v) }
func batchImageOptionalStringPtr(v string) *string { return batchimage.BatchImageOptionalStringPtr(v) }
