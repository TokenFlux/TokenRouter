package ent

import "context"

// WithoutTx 返回一个剥离了所附 *Tx 的 ctx 副本，使调用方可以在基础 client
// 的独立事务或 autocommit 路径执行 best-effort 副作用，而不加入外层事务。
//
// PostgreSQL 语义：事务内任一语句失败会把整个事务标记为 aborted，后续语句都会被拒绝，
// 直到 ROLLBACK。因此注册默认平台配额快照这类 fail-open 副作用必须和主注册事务隔离。
func WithoutTx(ctx context.Context) context.Context {
	if TxFromContext(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, txCtxKey{}, (*Tx)(nil))
}
