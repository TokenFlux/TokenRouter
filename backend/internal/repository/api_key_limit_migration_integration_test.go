//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/BrandonVee/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration213_AddsUserAPIKeyLimitWithDataAndConstraints(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)

	_, err := tx.ExecContext(ctx, `CREATE TEMP TABLE users (id BIGSERIAL PRIMARY KEY) ON COMMIT DROP`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO users DEFAULT VALUES`)
	require.NoError(t, err)

	migrationSQL, err := migrations.FS.ReadFile("213_add_user_api_key_limit.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)

	var migratedValue int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT api_key_limit FROM users WHERE id = 1`).Scan(&migratedValue))
	require.Equal(t, 100, migratedValue)

	var defaultValue int
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users DEFAULT VALUES RETURNING api_key_limit`).Scan(&defaultValue))
	require.Equal(t, 100, defaultValue)

	_, err = tx.ExecContext(ctx, `SAVEPOINT api_key_limit_negative`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO users (api_key_limit) VALUES (-1)`)
	require.Error(t, err)
	_, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT api_key_limit_negative`)
	require.NoError(t, rollbackErr)

	_, err = tx.ExecContext(ctx, `SAVEPOINT api_key_limit_null`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO users (api_key_limit) VALUES (NULL)`)
	require.Error(t, err)
	_, rollbackErr = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT api_key_limit_null`)
	require.NoError(t, rollbackErr)
}
