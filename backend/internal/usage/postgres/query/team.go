// 查询参与函数复用调用者的连接与主查询，保持批量及分页前排序。
package query

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	infra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/team"
)

func ListUsageLogs(ctx context.Context, db TeamExecutor, teamID int64, query team.TeamUsageQuery) ([]team.TeamUsageLogItem, int64, error) {
	where, args := TeamUsageWhere(teamID, query)
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs ul WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	args = append(args, query.Limit, query.Offset)
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT ul.id, ul.user_id, COALESCE(u.email, ''), ul.api_key_id, COALESCE(k.name, ''),
		       ul.request_id, COALESCE(NULLIF(ul.requested_model, ''), ul.model), ul.actual_cost,
		       ul.input_tokens, ul.output_tokens, ul.created_at
		FROM usage_logs ul
		LEFT JOIN users u ON u.id = ul.user_id
		LEFT JOIN api_keys k ON k.id = ul.api_key_id
		WHERE %s ORDER BY ul.created_at DESC, ul.id DESC LIMIT $%d OFFSET $%d`, where, limitPos, offsetPos), args...)
	if err != nil {
		return nil, 0, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	items := make([]team.TeamUsageLogItem, 0, query.Limit)
	for rows.Next() {
		var item team.TeamUsageLogItem
		if err := rows.Scan(&item.ID, &item.ActorUserID, &item.ActorEmail, &item.APIKeyID, &item.APIKeyName, &item.RequestID, &item.Model, &item.ActualCost, &item.InputTokens, &item.OutputTokens, &item.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
func ListMemberUsageSeries(ctx context.Context, db TeamExecutor, teamID int64, query team.TeamUsageQuery) ([]team.TeamMemberUsageSeries, error) {
	args := []any{teamID, query.From, query.To, timezone.Name()}
	membershipActorFilter := ""
	usageActorFilter := ""
	if query.ActorUserID != nil && *query.ActorUserID > 0 {
		args = append(args, *query.ActorUserID)
		position := len(args)
		membershipActorFilter = fmt.Sprintf(" AND m.user_id = $%d", position)
		usageActorFilter = fmt.Sprintf(" AND ul.user_id = $%d", position)
	}
	rows, err := db.QueryContext(ctx, `
		WITH actors AS (
			SELECT m.user_id
			FROM team_memberships m
			WHERE m.team_id = $1 AND m.left_at IS NULL`+membershipActorFilter+`
			UNION
			SELECT ul.user_id
			FROM usage_logs ul
			WHERE ul.team_id = $1 AND ul.created_at >= $2 AND ul.created_at < $3`+usageActorFilter+`
		), daily AS (
			SELECT ul.user_id,
			       TO_CHAR(ul.created_at AT TIME ZONE $4, 'YYYY-MM-DD') AS usage_date,
			       COALESCE(SUM(ul.actual_cost), 0) AS actual_cost,
			       COUNT(*) AS request_count,
			       COALESCE(SUM(ul.input_tokens), 0) AS input_tokens,
			       COALESCE(SUM(ul.output_tokens), 0) AS output_tokens
			FROM usage_logs ul
			WHERE ul.team_id = $1 AND ul.created_at >= $2 AND ul.created_at < $3`+usageActorFilter+`
			GROUP BY ul.user_id, usage_date
		)
		SELECT a.user_id,
		       COALESCE(NULLIF(u.username, ''), NULLIF(u.email, ''), 'User #' || a.user_id::text),
		       CASE WHEN EXISTS (
			   SELECT 1 FROM team_memberships current_m
			   WHERE current_m.team_id = $1 AND current_m.user_id = a.user_id AND current_m.left_at IS NULL
		       ) THEN 'active' ELSE 'left' END,
		       d.usage_date, COALESCE(d.actual_cost, 0), COALESCE(d.request_count, 0),
		       COALESCE(d.input_tokens, 0), COALESCE(d.output_tokens, 0)
		FROM actors a
		LEFT JOIN users u ON u.id = a.user_id
		LEFT JOIN daily d ON d.user_id = a.user_id
		ORDER BY a.user_id, d.usage_date`, args...)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	items := make([]team.TeamMemberUsageSeries, 0)
	indexByUser := make(map[int64]int)
	for rows.Next() {
		var userID int64
		var displayName, status string
		var date sql.NullString
		var actualCost float64
		var requestCount, inputTokens, outputTokens int64
		if err := rows.Scan(&userID, &displayName, &status, &date, &actualCost, &requestCount, &inputTokens, &outputTokens); err != nil {
			return nil, err
		}
		index, exists := indexByUser[userID]
		if !exists {
			index = len(items)
			indexByUser[userID] = index
			items = append(items, team.TeamMemberUsageSeries{
				ActorUserID: userID,
				DisplayName: displayName,
				Status:      status,
				Summary:     team.TeamUsageSummary{Daily: make([]team.TeamUsageDaily, 0)},
			})
		}
		item := &items[index]
		if date.Valid {
			item.Summary.Daily = append(item.Summary.Daily, team.TeamUsageDaily{Date: date.String, ActualCost: actualCost, RequestCount: requestCount})
			item.Summary.ActualCost += actualCost
			item.Summary.RequestCount += requestCount
			item.Summary.InputTokens += inputTokens
			item.Summary.OutputTokens += outputTokens
		}
	}
	return items, rows.Err()
}
func GetUsageSummary(ctx context.Context, db TeamExecutor, teamID int64, query team.TeamUsageQuery) (*team.TeamUsageSummary, error) {
	where, args := TeamUsageWhere(teamID, query)
	summary := &team.TeamUsageSummary{Daily: make([]team.TeamUsageDaily, 0)}
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(ul.actual_cost), 0), COUNT(*), COALESCE(SUM(ul.input_tokens), 0), COALESCE(SUM(ul.output_tokens), 0) FROM usage_logs ul WHERE `+where, args...).
		Scan(&summary.ActualCost, &summary.RequestCount, &summary.InputTokens, &summary.OutputTokens)
	if err != nil {
		return nil, err
	}
	args = append(args, timezone.Name())
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT TO_CHAR(ul.created_at AT TIME ZONE $%d, 'YYYY-MM-DD'), COALESCE(SUM(ul.actual_cost), 0), COUNT(*)
		FROM usage_logs ul WHERE %s
		GROUP BY 1 ORDER BY 1`, len(args), where), args...)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item team.TeamUsageDaily
		if err := rows.Scan(&item.Date, &item.ActualCost, &item.RequestCount); err != nil {
			return nil, err
		}
		summary.Daily = append(summary.Daily, item)
	}
	return summary, rows.Err()
}
func TeamUsageWhere(teamID int64, query team.TeamUsageQuery) (string, []any) {
	conditions := []string{"ul.team_id = $1", "ul.created_at >= $2", "ul.created_at < $3"}
	args := []any{teamID, query.From, query.To}
	if query.ActorUserID != nil && *query.ActorUserID > 0 {
		args = append(args, *query.ActorUserID)
		conditions = append(conditions, fmt.Sprintf("ul.user_id = $%d", len(args)))
	}
	if query.APIKeyID != nil && *query.APIKeyID > 0 {
		args = append(args, *query.APIKeyID)
		conditions = append(conditions, fmt.Sprintf("ul.api_key_id = $%d", len(args)))
	}
	return strings.Join(conditions, " AND "), args
}

// TeamExecutor 直接复用团队调用方的 SQL 连接。
type TeamExecutor interface {
	infra.Executor
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
