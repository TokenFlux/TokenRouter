package postgres

import (
	"database/sql"
	"errors"
	"strings"
)

// IsNoRows 保留 Ent upsert 对 SQL 无行错误的历史识别。
func IsNoRows(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "no rows in result set")
}
