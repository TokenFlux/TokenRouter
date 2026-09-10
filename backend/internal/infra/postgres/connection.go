// 本文件只打开 PostgreSQL 技术连接；迁移、密钥与业务初始化仍由外层编排。
package postgres

import (
	"database/sql"

	"github.com/lib/pq"
)

// Open 保持懒连接行为，调用方负责配置池、验证连接并最终关闭。
func Open(dsn string, enableTiming bool) (*sql.DB, error) {
	if !enableTiming {
		return sql.Open("postgres", dsn)
	}
	connector, err := pq.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(NewTimingConnector(connector)), nil
}
