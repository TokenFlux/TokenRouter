//go:build integration

package identity_test

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// newKeyStoreFixture 保留测试的原 SQL/Ent 连接及批量用量读取，直接使用原生存储。
func newKeyStoreFixture(client *dbent.Client, exec postgresinfra.Executor) *keypostgres.KeyStore {
	return keypostgres.NewKeyStoreWithSQL(client, exec, func(ctx context.Context, ids []int64) (map[int64]float64, error) {
		return usagepostgres.ReadAPIKeyUsageTotals(ctx, exec, nil, ids)
	})
}
