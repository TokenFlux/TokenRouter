// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	sql "database/sql"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	time "time"
)

func (r *MemberUsageStore) ResetMemberUsage(ctx context.Context, teamID, userID int64, resetDaily, resetWeekly, resetMonthly bool, now time.Time) error {
	calendar := timezone.NewCalendar(timezone.Location())
	if r.calendar != nil {
		calendar = *r.calendar
	}
	dailyStart := calendar.StartOfDay(now)
	weeklyStart := calendar.StartOfWeek(now)
	monthlyStart := calendar.StartOfMonth(now)
	result, err := r.db.ExecContext(ctx, `
		UPDATE team_memberships SET
			daily_usage_usd = CASE WHEN $3 THEN 0 ELSE daily_usage_usd END,
			weekly_usage_usd = CASE WHEN $4 THEN 0 ELSE weekly_usage_usd END,
			monthly_usage_usd = CASE WHEN $5 THEN 0 ELSE monthly_usage_usd END,
			daily_window_start = CASE WHEN $3 THEN $6 ELSE daily_window_start END,
			weekly_window_start = CASE WHEN $4 THEN $7 ELSE weekly_window_start END,
			monthly_window_start = CASE WHEN $5 THEN $8 ELSE monthly_window_start END,
			updated_at = NOW()
		WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL AND role = 'member'`,
		teamID, userID, resetDaily, resetWeekly, resetMonthly, dailyStart, weeklyStart, monthlyStart)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return billing.ErrTeamMembershipRequired
	}
	return nil
}

// MemberUsageStore 仅写消费累计与窗口，不改变成员角色及限额配置。
type MemberUsageStore struct {
	db       *sql.DB
	calendar *timezone.Calendar
}

func NewMemberUsageStore(db *sql.DB, calendar *timezone.Calendar) *MemberUsageStore {
	return &MemberUsageStore{db: db, calendar: calendar}
}
