//go:build unit

// 原私有测试入口只委托已迁实现，生产构建不保留无消费者的包装。
package service

import (
	context "context"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func withOAuthRefreshRequestPath(ctx context.Context) context.Context {
	return acctcore.WithRefreshRequestPath(ctx)
}
