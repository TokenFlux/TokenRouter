package service

import (
	"context"
	"errors"
)

// isContextDoneError 判断错误是否由调用方取消或上下文超时触发。
func isContextDoneError(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if ctx == nil {
		return false
	}
	ctxErr := ctx.Err()
	// 数据库驱动可能返回自有错误文本，此时以 ctx 状态判断是否由取消/超时触发。
	return errors.Is(ctxErr, context.Canceled) || errors.Is(ctxErr, context.DeadlineExceeded)
}

// contextDoneError 返回对上游更有意义的上下文错误，兜底保留原始错误。
