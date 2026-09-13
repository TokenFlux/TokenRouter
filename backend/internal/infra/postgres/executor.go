// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	sql "database/sql"
)

// Executor 接收调用方已经选择的连接或事务，不创建或结束事务。
type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
