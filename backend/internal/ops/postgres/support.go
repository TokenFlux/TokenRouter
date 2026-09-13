// 标量 SQL 转换复用基础层定义，保持原 NULL 语义。
package postgres

import (
	"database/sql"
	"strconv"

	infra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

func itoa(v int) string                { return strconv.Itoa(v) }
func nullInt64(v *int64) sql.NullInt64 { return infra.NullableInt64(v) }
