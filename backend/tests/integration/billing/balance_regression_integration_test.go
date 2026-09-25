//go:build integration

package billing_test

import (
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	context "context"

	fmt "fmt"

	require "github.com/stretchr/testify/require"

	testing "testing"

	time "time"
)

// TestS04SetBalanceReturnsLockedOldValue 用真实行锁固定结算先于 set 提交，核对返回的实际旧值。
func TestS04SetBalanceReturnsLockedOldValue(t *testing.T) {
	ctx := context.Background()
	client := committedEntitlementClient(t)
	user := mustCreateUser(t, client, &identity.User{Email: fmt.Sprintf("s04-set-%d@example.com", time.Now().UnixNano()), Balance: 100})
	repo := postgres.NewUserStore(client, integrationDB)
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, "UPDATE users SET balance = balance - 20 WHERE id = $1", user.ID)
	require.NoError(t, err)
	type outcome struct {
		change identity.BalanceChange
		err    error
	}
	done := make(chan outcome, 1)
	go func() { c, e := repo.SetBalance(ctx, user.ID, 200); done <- outcome{c, e} }()
	require.Eventually(t, func() bool {
		var waiting int
		err := integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE query LIKE '%UPDATE users AS u%' AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()").Scan(&waiting)
		return err == nil && waiting > 0
	}, 5*time.Second, 10*time.Millisecond, "set 已读取旧快照并阻塞在正在结算的用户行")
	require.NoError(t, tx.Commit())
	got := <-done
	require.NoError(t, got.err)
	require.Equal(t, float64(80), got.change.Old, "审计增量必须依据锁解除后的真实旧余额")
	require.Equal(t, float64(200), got.change.New)
}
