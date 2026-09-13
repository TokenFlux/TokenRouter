// 本文件集中存储适配层共用的稳定参数与文本格式处理。
package postgres

import (
	"database/sql"
	"strings"
)

func NullPositiveInt64(v *int64) any {
	if v == nil || *v <= 0 {
		return nil
	}
	return *v
}
func TruncateText(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	runes := []rune(s)
	for len(string(runes)) > max && len(runes) > 0 {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}
func EscapeLikePattern(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func NullableInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}
