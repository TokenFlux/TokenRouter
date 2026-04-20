//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration111_MigratesLegacySubscriptionData(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)

	restoreLegacySubscriptionColumns(t, tx)

	suffix := time.Now().UnixNano()

	var userID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, balance, concurrency, status)
VALUES ($1, $2, 'user', 0, 5, 'active')
RETURNING id
`, "migration-user-"+time.Unix(0, suffix).UTC().Format("20060102150405.000000")+"@example.com", "hash").Scan(&userID))

	group1ID := insertLegacyGroup(t, tx, "migration-group-1", suffix, 100, 500, 1000, 30)
	group2ID := insertLegacyGroup(t, tx, "migration-group-2", suffix, 10, 20, 30, 14)

	plan1ID := insertLegacyPlan(t, tx, "migration-plan-unique", suffix, &group1ID, 30, 39)
	plan2AID := insertLegacyPlan(t, tx, "migration-plan-ambiguous-a", suffix, &group2ID, 14, 19)
	plan2BID := insertLegacyPlan(t, tx, "migration-plan-ambiguous-b", suffix, &group2ID, 14, 29)

	start1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	expires1 := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	dailyWindow1 := time.Date(2026, 3, 15, 8, 0, 0, 0, time.UTC)
	weeklyWindow1 := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)
	monthlyWindow1 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

	var legacySub1ID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO user_subscriptions (
    user_id, plan_id, group_id, starts_at, expires_at, status,
    daily_window_start, weekly_window_start, monthly_window_start,
    daily_usage_usd, weekly_usage_usd, monthly_usage_usd, notes
) VALUES (
    $1, NULL, $2, $3, $4, 'active',
    $5, $6, $7,
    $8, $9, $10, $11
)
RETURNING id
`, userID, group1ID, start1, expires1, dailyWindow1, weeklyWindow1, monthlyWindow1, 25.0, 110.0, 450.0, "legacy unique").Scan(&legacySub1ID))

	start2 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	expires2 := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	dailyWindow2 := time.Date(2026, 4, 5, 9, 0, 0, 0, time.UTC)
	weeklyWindow2 := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	monthlyWindow2 := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	var legacySub2ID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO user_subscriptions (
    user_id, plan_id, group_id, starts_at, expires_at, status,
    daily_window_start, weekly_window_start, monthly_window_start,
    daily_usage_usd, weekly_usage_usd, monthly_usage_usd, notes
) VALUES (
    $1, NULL, $2, $3, $4, 'active',
    $5, $6, $7,
    $8, $9, $10, $11
)
RETURNING id
`, userID, group2ID, start2, expires2, dailyWindow2, weeklyWindow2, monthlyWindow2, 2.5, 5.5, 8.5, "legacy ambiguous").Scan(&legacySub2ID))

	var redeemCodeID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO redeem_codes (code, type, value, status, max_uses, used_count, notes, group_id, validity_days)
VALUES ($1, 'subscription', 0, 'unused', 1, 0, 'legacy redeem', $2, 14)
RETURNING id
`, "MIGRATE-REDEEM-"+time.Unix(0, suffix).UTC().Format("150405000000"), group2ID).Scan(&redeemCodeID))

	var orderID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO payment_orders (
    user_id, user_email, user_name, amount, pay_amount, fee_rate, recharge_code,
    out_trade_no, payment_type, payment_trade_no, order_type, status,
    expires_at, client_ip, src_host, subscription_group_id, subscription_days
) VALUES (
    $1, $2, $3, 59, 59, 0, $4,
    $5, 'alipay', '', 'subscription', 'PAID',
    $6, '127.0.0.1', 'migration-test', $7, 14
)
RETURNING id
`, userID, "legacy-order@example.com", "legacy-order-user", "RC-"+time.Unix(0, suffix).UTC().Format("150405000000"), "OTN-"+time.Unix(0, suffix).UTC().Format("150405000000"), time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC), group2ID).Scan(&orderID))

	var legacySub3ID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO user_subscriptions (
    user_id, plan_id, group_id, starts_at, expires_at, status,
    daily_window_start, weekly_window_start, monthly_window_start,
    daily_usage_usd, weekly_usage_usd, monthly_usage_usd, notes
) VALUES (
    $1, NULL, $2, $3, $4, 'active',
    $5, $6, $7,
    $8, $9, $10, $11
)
RETURNING id
`, userID, group2ID, start2, expires2, dailyWindow2, weeklyWindow2, monthlyWindow2, 1.5, 2.5, 3.5, "payment order "+int64ToString(orderID)).Scan(&legacySub3ID))

	oldDefaultSubscriptions := `[{"group_id":` + int64ToString(group1ID) + `,"validity_days":30},{"group_id":` + int64ToString(group2ID) + `,"validity_days":14},{"group_id":` + int64ToString(group2ID) + `,"validity_days":14}]`
	_, err := tx.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at)
VALUES ('default_subscriptions', $1, NOW())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
`, oldDefaultSubscriptions)
	require.NoError(t, err)

	oldTargeting := `{"any_of":[{"all_of":[{"type":"subscription","operator":"in","group_ids":[` + int64ToString(group1ID) + `,` + int64ToString(group2ID) + `]}]}]}`
	var announcementID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO announcements (title, content, status, notify_mode, targeting, created_at, updated_at)
VALUES ($1, 'legacy targeting', 'active', 'silent', $2::jsonb, NOW(), NOW())
RETURNING id
`, "migration-announcement-"+time.Unix(0, suffix).UTC().Format("150405000000"), oldTargeting).Scan(&announcementID))

	migrationSQL, err := migrations.FS.ReadFile("111_global_plan_quota_packs.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)

	var (
		sub1PlanID                                        int64
		sub1Status                                        string
		gotStart1, gotExpires1                            time.Time
		gotDailyWindow1, gotWeeklyWindow1                 time.Time
		gotMonthlyWindow1                                 time.Time
		gotDailyLimit1, gotWeeklyLimit1, gotMonthlyLimit1 float64
		gotDailyUsage1, gotWeeklyUsage1, gotMonthlyUsage1 float64
	)
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT
    plan_id,
    status,
    starts_at,
    expires_at,
    daily_window_start,
    weekly_window_start,
    monthly_window_start,
    daily_limit_usd,
    weekly_limit_usd,
    monthly_limit_usd,
    daily_usage_usd,
    weekly_usage_usd,
    monthly_usage_usd
FROM user_subscriptions
WHERE id = $1
`, legacySub1ID).Scan(
		&sub1PlanID,
		&sub1Status,
		&gotStart1,
		&gotExpires1,
		&gotDailyWindow1,
		&gotWeeklyWindow1,
		&gotMonthlyWindow1,
		&gotDailyLimit1,
		&gotWeeklyLimit1,
		&gotMonthlyLimit1,
		&gotDailyUsage1,
		&gotWeeklyUsage1,
		&gotMonthlyUsage1,
	))
	require.Equal(t, plan1ID, sub1PlanID)
	require.Equal(t, "active", sub1Status)
	require.Equal(t, start1, gotStart1)
	require.Equal(t, expires1, gotExpires1)
	require.Equal(t, dailyWindow1, gotDailyWindow1)
	require.Equal(t, weeklyWindow1, gotWeeklyWindow1)
	require.Equal(t, monthlyWindow1, gotMonthlyWindow1)
	require.Equal(t, 100.0, gotDailyLimit1)
	require.Equal(t, 500.0, gotWeeklyLimit1)
	require.Equal(t, 1000.0, gotMonthlyLimit1)
	require.Equal(t, 25.0, gotDailyUsage1)
	require.Equal(t, 110.0, gotWeeklyUsage1)
	require.Equal(t, 450.0, gotMonthlyUsage1)

	var (
		sub2PlanID                                        int64
		gotStart2, gotExpires2                            time.Time
		gotDailyWindow2, gotWeeklyWindow2                 time.Time
		gotMonthlyWindow2                                 time.Time
		gotDailyLimit2, gotWeeklyLimit2, gotMonthlyLimit2 float64
		gotDailyUsage2, gotWeeklyUsage2, gotMonthlyUsage2 float64
	)
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT
    plan_id,
    starts_at,
    expires_at,
    daily_window_start,
    weekly_window_start,
    monthly_window_start,
    daily_limit_usd,
    weekly_limit_usd,
    monthly_limit_usd,
    daily_usage_usd,
    weekly_usage_usd,
    monthly_usage_usd
FROM user_subscriptions
WHERE id = $1
`, legacySub2ID).Scan(
		&sub2PlanID,
		&gotStart2,
		&gotExpires2,
		&gotDailyWindow2,
		&gotWeeklyWindow2,
		&gotMonthlyWindow2,
		&gotDailyLimit2,
		&gotWeeklyLimit2,
		&gotMonthlyLimit2,
		&gotDailyUsage2,
		&gotWeeklyUsage2,
		&gotMonthlyUsage2,
	))
	require.NotEqual(t, plan2AID, sub2PlanID)
	require.NotEqual(t, plan2BID, sub2PlanID)
	require.Equal(t, start2, gotStart2)
	require.Equal(t, expires2, gotExpires2)
	require.Equal(t, dailyWindow2, gotDailyWindow2)
	require.Equal(t, weeklyWindow2, gotWeeklyWindow2)
	require.Equal(t, monthlyWindow2, gotMonthlyWindow2)
	require.Equal(t, 10.0, gotDailyLimit2)
	require.Equal(t, 20.0, gotWeeklyLimit2)
	require.Equal(t, 30.0, gotMonthlyLimit2)
	require.Equal(t, 2.5, gotDailyUsage2)
	require.Equal(t, 5.5, gotWeeklyUsage2)
	require.Equal(t, 8.5, gotMonthlyUsage2)

	var hiddenForSale bool
	var hiddenValidityDays int
	var hiddenDailyLimit, hiddenWeeklyLimit, hiddenMonthlyLimit float64
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT for_sale, validity_days, daily_limit_usd, weekly_limit_usd, monthly_limit_usd
FROM subscription_plans
WHERE id = $1
`, sub2PlanID).Scan(&hiddenForSale, &hiddenValidityDays, &hiddenDailyLimit, &hiddenWeeklyLimit, &hiddenMonthlyLimit))
	require.False(t, hiddenForSale)
	require.Equal(t, 14, hiddenValidityDays)
	require.Equal(t, 10.0, hiddenDailyLimit)
	require.Equal(t, 20.0, hiddenWeeklyLimit)
	require.Equal(t, 30.0, hiddenMonthlyLimit)

	var redeemPlanID int64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT plan_id FROM redeem_codes WHERE id = $1`, redeemCodeID).Scan(&redeemPlanID))
	require.Equal(t, sub2PlanID, redeemPlanID)

	var orderPlanID int64
	var snapshotRaw []byte
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT plan_id, plan_snapshot FROM payment_orders WHERE id = $1`, orderID).Scan(&orderPlanID, &snapshotRaw))
	require.Equal(t, sub2PlanID, orderPlanID)

	var sourceOrderID sql.NullInt64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT source_order_id FROM user_subscriptions WHERE id = $1`, legacySub3ID).Scan(&sourceOrderID))
	require.True(t, sourceOrderID.Valid)
	require.Equal(t, orderID, sourceOrderID.Int64)

	var snapshot struct {
		Name            string   `json:"name"`
		Price           float64  `json:"price"`
		ValidityDays    int      `json:"validity_days"`
		DailyLimitUSD   *float64 `json:"daily_limit_usd"`
		WeeklyLimitUSD  *float64 `json:"weekly_limit_usd"`
		MonthlyLimitUSD *float64 `json:"monthly_limit_usd"`
	}
	require.NoError(t, json.Unmarshal(snapshotRaw, &snapshot))
	require.NotEmpty(t, snapshot.Name)
	require.Equal(t, 59.0, snapshot.Price)
	require.Equal(t, 14, snapshot.ValidityDays)
	require.NotNil(t, snapshot.DailyLimitUSD)
	require.NotNil(t, snapshot.WeeklyLimitUSD)
	require.NotNil(t, snapshot.MonthlyLimitUSD)
	require.Equal(t, 10.0, *snapshot.DailyLimitUSD)
	require.Equal(t, 20.0, *snapshot.WeeklyLimitUSD)
	require.Equal(t, 30.0, *snapshot.MonthlyLimitUSD)

	var settingsValue string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'default_subscriptions'`).Scan(&settingsValue))
	var defaultSubscriptions []struct {
		PlanID int64 `json:"plan_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(settingsValue), &defaultSubscriptions))
	require.Len(t, defaultSubscriptions, 2)
	require.ElementsMatch(t, []int64{plan1ID, sub2PlanID}, []int64{defaultSubscriptions[0].PlanID, defaultSubscriptions[1].PlanID})

	var targetingRaw []byte
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT targeting FROM announcements WHERE id = $1`, announcementID).Scan(&targetingRaw))
	require.NotContains(t, strings.ToLower(string(targetingRaw)), "group_ids")

	var targeting struct {
		AnyOf []struct {
			AllOf []struct {
				Type    string  `json:"type"`
				PlanIDs []int64 `json:"plan_ids"`
			} `json:"all_of"`
		} `json:"any_of"`
	}
	require.NoError(t, json.Unmarshal(targetingRaw, &targeting))
	require.Len(t, targeting.AnyOf, 1)
	require.Len(t, targeting.AnyOf[0].AllOf, 1)
	require.Equal(t, "subscription", targeting.AnyOf[0].AllOf[0].Type)
	require.ElementsMatch(t, []int64{plan1ID, plan2AID, plan2BID, sub2PlanID}, targeting.AnyOf[0].AllOf[0].PlanIDs)
}

func restoreLegacySubscriptionColumns(t *testing.T, tx *sql.Tx) {
	t.Helper()

	_, err := tx.ExecContext(context.Background(), `
ALTER TABLE groups ADD COLUMN IF NOT EXISTS subscription_type VARCHAR(20) NOT NULL DEFAULT 'standard';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS daily_limit_usd DECIMAL(20,8);
ALTER TABLE groups ADD COLUMN IF NOT EXISTS weekly_limit_usd DECIMAL(20,8);
ALTER TABLE groups ADD COLUMN IF NOT EXISTS monthly_limit_usd DECIMAL(20,8);
ALTER TABLE groups ADD COLUMN IF NOT EXISTS default_validity_days INT NOT NULL DEFAULT 30;

ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS group_id BIGINT;

ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS group_id BIGINT;
ALTER TABLE user_subscriptions ALTER COLUMN plan_id DROP NOT NULL;

ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS subscription_group_id BIGINT;
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS subscription_days INT;

ALTER TABLE redeem_codes ADD COLUMN IF NOT EXISTS group_id BIGINT;
ALTER TABLE redeem_codes ADD COLUMN IF NOT EXISTS validity_days INT NOT NULL DEFAULT 30;
`)
	require.NoError(t, err)
}

func insertLegacyGroup(t *testing.T, tx *sql.Tx, base string, suffix int64, daily, weekly, monthly float64, validityDays int) int64 {
	t.Helper()

	var id int64
	require.NoError(t, tx.QueryRowContext(context.Background(), `
INSERT INTO groups (
    name, description, rate_multiplier, is_exclusive, status, platform,
    subscription_type, daily_limit_usd, weekly_limit_usd, monthly_limit_usd, default_validity_days
) VALUES (
    $1, 'migration test group', 1, FALSE, 'active', 'openai',
    'subscription', $2, $3, $4, $5
)
RETURNING id
`, base+"-"+int64ToString(suffix), daily, weekly, monthly, validityDays).Scan(&id))
	return id
}

func insertLegacyPlan(t *testing.T, tx *sql.Tx, base string, suffix int64, groupID *int64, validityDays int, price float64) int64 {
	t.Helper()

	var id int64
	require.NoError(t, tx.QueryRowContext(context.Background(), `
INSERT INTO subscription_plans (
    group_id, name, description, price, validity_days, validity_unit,
    features, product_name, for_sale, sort_order
) VALUES (
    $1, $2, 'migration test plan', $3, $4, 'day',
    '', '', TRUE, 0
)
RETURNING id
`, groupID, base+"-"+int64ToString(suffix), price, validityDays).Scan(&id))
	return id
}

func int64ToString(v int64) string {
	return strconv.FormatInt(v, 10)
}
