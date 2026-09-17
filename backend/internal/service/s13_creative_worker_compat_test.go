//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"context"

	native "github.com/TokenFlux/TokenRouter/internal/creative"
)

const defaultCreativeConcurrencyRequeueDelay = native.DefaultCreativeConcurrencyRequeueDelay

func (w *CreativeRunWorker) process(ctx context.Context, id string) (CreativeProcessResult, error) {
	return w.Process(ctx, id)
}
