// 扫描兼容入口不创建事务，沿用调用方传入的 QueryContext。
package repository

import (
	"context"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

type sqlQueryer = postgresinfra.Queryer

func scanSingleRow(ctx context.Context, q sqlQueryer, query string, args []any, dest ...any) error {
	return postgresinfra.ScanSingleRow(ctx, q, query, args, dest...)
}
