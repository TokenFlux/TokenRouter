//go:build integration

package apikey_test

import (
	"database/sql"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// 并发创建合同保留真实提交；使用隔离数据库避免共享账号数据。
func keyDatabase(t *testing.T) (*sql.DB, *dbent.Client) {
	t.Helper()
	db := postgrescontainer.New(t)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return db, client
}
