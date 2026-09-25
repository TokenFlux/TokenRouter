//go:build integration

package promotion_test

import (
	"context"
	"fmt"
	"testing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	promotionpostgres "github.com/TokenFlux/TokenRouter/internal/promotion/postgres"

	"github.com/stretchr/testify/require"
)

// 转账流水失败必须回滚同事务的清零、余额和累计充值，不能只保证计提原子性。
func TestAffiliateTransferLedgerFailureRollsBackFunds(t *testing.T) {
	ctx := context.Background()
	client, integrationDB := testStore(t)
	user, err := client.User.Create().SetEmail("s12-transfer@example.com").SetPasswordHash("hash").SetBalance(3).SetTotalRecharged(7).Save(ctx)
	require.NoError(t, err)
	repo := promotionpostgres.NewAffiliateRepository(client, func(tx *dbent.Tx) promotionpostgres.TransferBalance { return billingpostgres.BalanceInTx(tx) })
	_, err = repo.EnsureUserAffiliate(ctx, user.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "UPDATE user_affiliates SET aff_quota=10, aff_history_quota=10 WHERE user_id=$1", user.ID)
	require.NoError(t, err)
	name := fmt.Sprintf("s12_transfer_failure_%d", user.ID)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.user_id=%d AND NEW.action='transfer' THEN IF (SELECT balance FROM users WHERE id=%d)<>13 OR (SELECT aff_quota FROM user_affiliates WHERE user_id=%d)<>0 THEN RAISE EXCEPTION 'transfer was not visible in same transaction'; END IF; RAISE EXCEPTION 's12 forced ledger failure'; END IF; RETURN NEW; END; $$`, name, user.ID, user.ID, user.ID))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON user_affiliate_ledger; DROP FUNCTION IF EXISTS %s()", name, name))
		require.NoError(t, e)
	})
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON user_affiliate_ledger FOR EACH ROW EXECUTE FUNCTION %s()", name, name))
	require.NoError(t, err)
	_, _, err = repo.TransferQuotaToBalance(ctx, user.ID)
	require.ErrorContains(t, err, "s12 forced ledger failure")
	current, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 3.0, current.Balance)
	require.Equal(t, 7.0, current.TotalRecharged)
	var quota float64
	var records int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT aff_quota FROM user_affiliates WHERE user_id=$1", user.ID).Scan(&quota))
	require.Equal(t, 10.0, quota)
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_affiliate_ledger WHERE user_id=$1 AND action='transfer'", user.ID).Scan(&records))
	require.Zero(t, records)
}
