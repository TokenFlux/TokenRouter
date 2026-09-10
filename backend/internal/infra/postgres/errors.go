// 本文件只识别 PostgreSQL 技术错误；业务错误映射留在存储 Adapter。
package postgres

import (
	"errors"
	"strings"

	"github.com/lib/pq"
)

func IsUniqueConstraintViolation(err error) bool {
	if err == nil {
		return false
	}

	// 优先检测 PostgreSQL 特定错误码（最精确）。
	// 错误码 23505 对应 unique_violation。
	// 参考：https://www.postgresql.org/docs/current/errcodes-appendix.html
	var pgErr *pq.Error
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}

	// 回退到错误消息检测（兼容其他场景）。
	// 这些关键词覆盖了 PostgreSQL、MySQL 等主流数据库的错误消息。
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate entry")
}

func IsDeadlock(err error) bool {
	return SQLState(err) == "40P01"
}

func SQLState(err error) string {
	if err == nil {
		return ""
	}
	var pgErr *pq.Error
	if !errors.As(err, &pgErr) || pgErr == nil {
		return ""
	}
	return string(pgErr.Code)
}
