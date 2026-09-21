//go:build integration

package repository

import (
	"context"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

type s06FailConfigurationOutbox struct{ AccountEventBinding }

func (s06FailConfigurationOutbox) Write(ctx context.Context, exec postgresinfra.Executor, _ accountpostgres.AccountEvent, _, _ *int64, _ any) error {
	// 故障在真实事务连接上发生，验证配置 SQL 不会先行提交。
	_, err := exec.ExecContext(ctx, "INSERT INTO s06_missing_outbox_fixture DEFAULT VALUES")
	return err
}
