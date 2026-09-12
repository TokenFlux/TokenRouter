// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"strings"
)

func scanSingleRow(ctx context.Context, q sqlQueryer, query string, args []any, dest ...any) error {
	return postgresinfra.ScanSingleRow(ctx, q, query, args, dest...)
}

// escapeLikePattern 转义 LIKE/ILIKE 通配符（\ % _），避免用户输入被当作通配符。
// Postgres 默认以反斜杠为转义符，无需额外 ESCAPE 子句。
func escapeLikePattern(s string) string {
	return likePatternReplacer.Replace(s)
}

var likePatternReplacer = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
