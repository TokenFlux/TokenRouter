// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
)

// clientFromContext 从 context 中获取事务 client，如果不存在则返回默认 client。
//
// 这个辅助函数支持 repository 方法在事务上下文中工作：
// - 如果 context 中存在事务（通过 ent.NewTxContext 设置），返回事务的 client
// - 否则返回传入的默认 client
//
// 使用示例：
//
//	func (r *someRepo) SomeMethod(ctx context.Context) error {
//	    client := clientFromContext(ctx, r.client)
//	    return client.SomeEntity.Create().Save(ctx)
//	}
func clientFromContext(ctx context.Context, defaultClient *dbent.Client) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return defaultClient
}
