package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type contentModerationRepository struct {
	db *sql.DB
}

type sqlQueryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func NewContentModerationRepository(db *sql.DB) service.ContentModerationRepository {
	return &contentModerationRepository{db: db}
}

func (r *contentModerationRepository) CreateLog(ctx context.Context, log *service.ContentModerationLog) error {
	if log == nil {
		return nil
	}
	categoryScores, err := json.Marshal(log.CategoryScores)
	if err != nil {
		return fmt.Errorf("marshal moderation category scores: %w", err)
	}
	thresholdSnapshot, err := json.Marshal(log.ThresholdSnapshot)
	if err != nil {
		return fmt.Errorf("marshal moderation thresholds: %w", err)
	}
	var userID any
	if log.UserID != nil {
		userID = *log.UserID
	}
	var apiKeyID any
	if log.APIKeyID != nil {
		apiKeyID = *log.APIKeyID
	}
	var groupID any
	if log.GroupID != nil {
		groupID = *log.GroupID
	}
	var latency any
	if log.UpstreamLatencyMS != nil {
		latency = *log.UpstreamLatencyMS
	}
	err = r.db.QueryRowContext(ctx, `
INSERT INTO content_moderation_logs (
    request_id, user_id, user_email, api_key_id, api_key_name, group_id, group_name,
    endpoint, provider, model, mode, action, flagged, highest_category, highest_score,
    category_scores, threshold_snapshot, input_excerpt, upstream_latency_ms, error,
    violation_count, auto_banned, email_sent, queue_delay_ms, matched_keyword
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13, $14, $15,
    $16::jsonb, $17::jsonb, $18, $19, $20,
    $21, $22, $23, $24, $25
) RETURNING id, created_at`,
		log.RequestID, userID, log.UserEmail, apiKeyID, log.APIKeyName, groupID, log.GroupName,
		log.Endpoint, log.Provider, log.Model, log.Mode, log.Action, log.Flagged, log.HighestCategory, log.HighestScore,
		string(categoryScores), string(thresholdSnapshot), log.InputExcerpt, latency, log.Error,
		log.ViolationCount, log.AutoBanned, log.EmailSent, nullableIntPtr(log.QueueDelayMS), log.MatchedKeyword,
	).Scan(&log.ID, &log.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert content moderation log: %w", err)
	}
	return nil
}

func (r *contentModerationRepository) ListLogs(ctx context.Context, filter service.ContentModerationLogFilter) ([]service.ContentModerationLog, *pagination.PaginationResult, error) {
	where, args := buildContentModerationLogWhere(filter)
	whereSQL := "WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM content_moderation_logs l "+whereSQL, args...).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("count content moderation logs: %w", err)
	}

	params := filter.Pagination
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 20
	}
	if params.PageSize > 100 {
		params.PageSize = 100
	}
	queryArgs := append([]any{}, args...)
	queryArgs = append(queryArgs, params.Limit(), params.Offset())
	rows, err := r.db.QueryContext(ctx, `
SELECT
    l.id, l.request_id, l.user_id, l.user_email, l.api_key_id, l.api_key_name, l.group_id, l.group_name,
    l.endpoint, l.provider, l.model, l.mode, l.action, l.flagged, l.highest_category, l.highest_score,
    l.category_scores, l.threshold_snapshot, l.input_excerpt, l.upstream_latency_ms, l.error,
    l.violation_count, l.auto_banned, l.email_sent, COALESCE(u.status, ''), l.queue_delay_ms, l.matched_keyword, l.created_at
FROM content_moderation_logs l
LEFT JOIN users u ON u.id = l.user_id `+whereSQL+`
ORDER BY l.created_at DESC, l.id DESC
LIMIT $`+fmt.Sprint(len(queryArgs)-1)+` OFFSET $`+fmt.Sprint(len(queryArgs)),
		queryArgs...,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("list content moderation logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.ContentModerationLog, 0)
	for rows.Next() {
		var item service.ContentModerationLog
		var userID, apiKeyID, groupID, latency, queueDelay sql.NullInt64
		var scoresRaw, thresholdsRaw []byte
		if err := rows.Scan(
			&item.ID,
			&item.RequestID,
			&userID,
			&item.UserEmail,
			&apiKeyID,
			&item.APIKeyName,
			&groupID,
			&item.GroupName,
			&item.Endpoint,
			&item.Provider,
			&item.Model,
			&item.Mode,
			&item.Action,
			&item.Flagged,
			&item.HighestCategory,
			&item.HighestScore,
			&scoresRaw,
			&thresholdsRaw,
			&item.InputExcerpt,
			&latency,
			&item.Error,
			&item.ViolationCount,
			&item.AutoBanned,
			&item.EmailSent,
			&item.UserStatus,
			&queueDelay,
			&item.MatchedKeyword,
			&item.CreatedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("scan content moderation log: %w", err)
		}
		if userID.Valid {
			v := userID.Int64
			item.UserID = &v
		}
		if apiKeyID.Valid {
			v := apiKeyID.Int64
			item.APIKeyID = &v
		}
		if groupID.Valid {
			v := groupID.Int64
			item.GroupID = &v
		}
		if latency.Valid {
			v := int(latency.Int64)
			item.UpstreamLatencyMS = &v
		}
		if queueDelay.Valid {
			v := int(queueDelay.Int64)
			item.QueueDelayMS = &v
		}
		item.CategoryScores = map[string]float64{}
		_ = json.Unmarshal(scoresRaw, &item.CategoryScores)
		item.ThresholdSnapshot = map[string]float64{}
		_ = json.Unmarshal(thresholdsRaw, &item.ThresholdSnapshot)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate content moderation logs: %w", err)
	}
	return items, paginationResultFromTotal(total, params), nil
}

func (r *contentModerationRepository) CountFlaggedByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	if userID <= 0 {
		return 0, nil
	}
	var count int
	err := r.db.QueryRowContext(ctx, `
WITH last_auto_ban AS (
    SELECT MAX(created_at) AS at
    FROM content_moderation_logs
    WHERE user_id = $1 AND auto_banned = TRUE
)
SELECT COUNT(*)
FROM content_moderation_logs
WHERE user_id = $1
  AND flagged = TRUE
  AND action <> 'hash_block'
  AND created_at >= $2
  AND created_at > COALESCE((SELECT at FROM last_auto_ban), '-infinity'::timestamptz)
`, userID, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count user content moderation flagged logs: %w", err)
	}
	return count, nil
}

func (r *contentModerationRepository) CreateCyberWarning(ctx context.Context, warning *service.ContentModerationCyberWarning) error {
	if warning == nil {
		return nil
	}
	if warning.ViolationCount <= 0 {
		warning.ViolationCount = 1
	}
	return insertContentModerationCyberWarning(ctx, r.db, warning)
}

func (r *contentModerationRepository) CreateCyberWarningAndApplyUserBan(ctx context.Context, warning *service.ContentModerationCyberWarning, policy service.ContentModerationCyberWarningPolicy) (bool, error) {
	if warning == nil {
		return false, nil
	}
	if warning.ViolationCount <= 0 {
		warning.ViolationCount = 1
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin content moderation cyber warning tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	userStatus, userLocked, err := lockCyberWarningUserTx(ctx, tx, warning)
	if err != nil {
		return false, err
	}
	if err := insertContentModerationCyberWarning(ctx, tx, warning); err != nil {
		return false, err
	}

	autoBanJustApplied, err := applyCyberWarningUserBanTx(ctx, tx, warning, policy, userStatus, userLocked)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit content moderation cyber warning tx: %w", err)
	}
	return autoBanJustApplied, nil
}

func insertContentModerationCyberWarning(ctx context.Context, q sqlQueryRower, warning *service.ContentModerationCyberWarning) error {
	var userID any
	if warning.UserID != nil {
		userID = *warning.UserID
	}
	var apiKeyID any
	if warning.APIKeyID != nil {
		apiKeyID = *warning.APIKeyID
	}
	var groupID any
	if warning.GroupID != nil {
		groupID = *warning.GroupID
	}
	var accountID any
	if warning.AccountID != nil {
		accountID = *warning.AccountID
	}
	err := q.QueryRowContext(ctx, `
INSERT INTO content_moderation_cyber_warnings (
    request_id, user_id, user_email, api_key_id, api_key_name, group_id, group_name,
    account_id, account_name, endpoint, model, upstream_status, warning_text, prompt_excerpt,
    violation_count, auto_banned, email_sent
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13, $14,
    $15, $16, $17
) RETURNING id, created_at`,
		warning.RequestID, userID, warning.UserEmail, apiKeyID, warning.APIKeyName, groupID, warning.GroupName,
		accountID, warning.AccountName, warning.Endpoint, warning.Model, warning.UpstreamStatus, warning.WarningText, warning.PromptExcerpt,
		warning.ViolationCount, warning.AutoBanned, warning.EmailSent,
	).Scan(&warning.ID, &warning.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert content moderation cyber warning: %w", err)
	}
	return nil
}

func lockCyberWarningUserTx(ctx context.Context, tx *sql.Tx, warning *service.ContentModerationCyberWarning) (string, bool, error) {
	if warning == nil || warning.UserID == nil || *warning.UserID <= 0 {
		return "", false, nil
	}
	userID := *warning.UserID
	userStatus := ""
	// 先锁用户行再插入 warning，避免多个事务先持有外键检查锁再升级为 FOR UPDATE 造成死锁。
	err := tx.QueryRowContext(ctx, `SELECT status FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&userStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("lock cyber warning user: %w", err)
	}
	return userStatus, true, nil
}

func applyCyberWarningUserBanTx(ctx context.Context, tx *sql.Tx, warning *service.ContentModerationCyberWarning, policy service.ContentModerationCyberWarningPolicy, userStatus string, userLocked bool) (bool, error) {
	if warning == nil || warning.UserID == nil || *warning.UserID <= 0 || !userLocked {
		return false, nil
	}
	userID := *warning.UserID

	count := 1
	if policy.WindowHours > 0 {
		since := time.Now().Add(-time.Duration(policy.WindowHours) * time.Hour)
		err := tx.QueryRowContext(ctx, `
WITH last_auto_ban AS (
    SELECT MAX(created_at) AS at
    FROM content_moderation_cyber_warnings
    WHERE user_id = $1 AND auto_banned = TRUE AND id <> $3
)
SELECT COUNT(*)
FROM content_moderation_cyber_warnings
WHERE user_id = $1
  AND created_at >= $2
  AND created_at > COALESCE((SELECT at FROM last_auto_ban), '-infinity'::timestamptz)
`, userID, since, warning.ID).Scan(&count)
		if err != nil {
			return false, fmt.Errorf("count cyber warnings in tx: %w", err)
		}
	}

	warning.ViolationCount = count
	autoBanJustApplied := false
	if policy.AutoBanEnabled && policy.BanThreshold > 0 && count >= policy.BanThreshold {
		warning.AutoBanned = true
		if userStatus != service.StatusDisabled {
			result, err := tx.ExecContext(ctx, `
UPDATE users
SET status = $2, updated_at = NOW()
WHERE id = $1 AND status <> $2
`, userID, service.StatusDisabled)
			if err != nil {
				return false, fmt.Errorf("disable cyber warning user: %w", err)
			}
			if rows, err := result.RowsAffected(); err == nil && rows > 0 {
				autoBanJustApplied = true
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE content_moderation_cyber_warnings
SET violation_count = $2, auto_banned = $3
WHERE id = $1
`, warning.ID, warning.ViolationCount, warning.AutoBanned); err != nil {
		return false, fmt.Errorf("update cyber warning policy fields: %w", err)
	}
	return autoBanJustApplied, nil
}

func (r *contentModerationRepository) ListCyberWarnings(ctx context.Context, filter service.ContentModerationCyberWarningFilter) ([]service.ContentModerationCyberWarning, *pagination.PaginationResult, error) {
	where, args := buildContentModerationCyberWhere(filter)
	whereSQL := "WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM content_moderation_cyber_warnings w "+whereSQL, args...).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("count content moderation cyber warnings: %w", err)
	}

	params := filter.Pagination
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 20
	}
	if params.PageSize > 100 {
		params.PageSize = 100
	}
	queryArgs := append([]any{}, args...)
	queryArgs = append(queryArgs, params.Limit(), params.Offset())
	rows, err := r.db.QueryContext(ctx, `
SELECT
    w.id, w.request_id, w.user_id, w.user_email, w.api_key_id, w.api_key_name, w.group_id, w.group_name,
    w.account_id, w.account_name, w.endpoint, w.model, w.upstream_status, w.warning_text, w.prompt_excerpt,
    w.violation_count, w.auto_banned, w.email_sent, COALESCE(u.status, ''), w.created_at
FROM content_moderation_cyber_warnings w
LEFT JOIN users u ON u.id = w.user_id `+whereSQL+`
ORDER BY w.created_at DESC, w.id DESC
LIMIT $`+fmt.Sprint(len(queryArgs)-1)+` OFFSET $`+fmt.Sprint(len(queryArgs)),
		queryArgs...,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("list content moderation cyber warnings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.ContentModerationCyberWarning, 0)
	for rows.Next() {
		var item service.ContentModerationCyberWarning
		var userID, apiKeyID, groupID, accountID sql.NullInt64
		if err := rows.Scan(
			&item.ID,
			&item.RequestID,
			&userID,
			&item.UserEmail,
			&apiKeyID,
			&item.APIKeyName,
			&groupID,
			&item.GroupName,
			&accountID,
			&item.AccountName,
			&item.Endpoint,
			&item.Model,
			&item.UpstreamStatus,
			&item.WarningText,
			&item.PromptExcerpt,
			&item.ViolationCount,
			&item.AutoBanned,
			&item.EmailSent,
			&item.UserStatus,
			&item.CreatedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("scan content moderation cyber warning: %w", err)
		}
		if userID.Valid {
			v := userID.Int64
			item.UserID = &v
		}
		if apiKeyID.Valid {
			v := apiKeyID.Int64
			item.APIKeyID = &v
		}
		if groupID.Valid {
			v := groupID.Int64
			item.GroupID = &v
		}
		if accountID.Valid {
			v := accountID.Int64
			item.AccountID = &v
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate content moderation cyber warnings: %w", err)
	}
	return items, paginationResultFromTotal(total, params), nil
}

func (r *contentModerationRepository) CountCyberWarningsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	if userID <= 0 {
		return 0, nil
	}
	var count int
	err := r.db.QueryRowContext(ctx, `
WITH last_auto_ban AS (
    SELECT MAX(created_at) AS at
    FROM content_moderation_cyber_warnings
    WHERE user_id = $1 AND auto_banned = TRUE
)
SELECT COUNT(*)
FROM content_moderation_cyber_warnings
WHERE user_id = $1
  AND created_at >= $2
  AND created_at > COALESCE((SELECT at FROM last_auto_ban), '-infinity'::timestamptz)
`, userID, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count user content moderation cyber warnings: %w", err)
	}
	return count, nil
}

func (r *contentModerationRepository) MarkCyberWarningEmailSent(ctx context.Context, id int64) error {
	if id <= 0 {
		return nil
	}
	if _, err := r.db.ExecContext(ctx, `
UPDATE content_moderation_cyber_warnings
SET email_sent = TRUE
WHERE id = $1
`, id); err != nil {
		return fmt.Errorf("mark content moderation cyber warning email sent: %w", err)
	}
	return nil
}

func (r *contentModerationRepository) GetCyberSummary(ctx context.Context, filter service.ContentModerationCyberWarningFilter) (*service.ContentModerationCyberSummary, error) {
	where, args := buildContentModerationCyberWhere(filter)
	whereSQL := "WHERE " + strings.Join(where, " AND ")
	result := &service.ContentModerationCyberSummary{}
	if err := r.db.QueryRowContext(ctx, `
SELECT
    COUNT(*),
    COUNT(DISTINCT NULLIF(request_id, '')),
    COUNT(DISTINCT user_id),
    COUNT(DISTINCT account_id)
FROM content_moderation_cyber_warnings w `+whereSQL, args...).Scan(
		&result.Events,
		&result.Requests,
		&result.Users,
		&result.Accounts,
	); err != nil {
		return nil, fmt.Errorf("summary content moderation cyber warnings: %w", err)
	}
	userRows, err := r.db.QueryContext(ctx, `
SELECT
    COUNT(*) AS count,
    w.user_id,
    COALESCE(NULLIF(MAX(w.user_email), ''), '无法关联用户') AS user_email,
    COALESCE(string_agg(DISTINCT CASE WHEN w.api_key_id IS NOT NULL THEN w.api_key_id::text || ' / ' || COALESCE(NULLIF(w.api_key_name, ''), '未命名') END, ', '), '-') AS api_keys,
    to_char(MAX(w.created_at AT TIME ZONE 'Asia/Shanghai'), 'YYYY-MM-DD HH24:MI:SS') AS last_seen
FROM content_moderation_cyber_warnings w `+whereSQL+`
GROUP BY w.user_id
ORDER BY count DESC, last_seen DESC
LIMIT 100`, args...)
	if err != nil {
		return nil, fmt.Errorf("summary content moderation cyber warnings by user: %w", err)
	}
	defer func() { _ = userRows.Close() }()
	for userRows.Next() {
		var item service.ContentModerationCyberUserSummary
		var userID sql.NullInt64
		if err := userRows.Scan(&item.Count, &userID, &item.UserEmail, &item.APIKeys, &item.LastSeen); err != nil {
			return nil, fmt.Errorf("scan content moderation cyber user summary: %w", err)
		}
		if userID.Valid {
			v := userID.Int64
			item.UserID = &v
		}
		result.ByUser = append(result.ByUser, item)
	}
	if err := userRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate content moderation cyber user summary: %w", err)
	}

	accountRows, err := r.db.QueryContext(ctx, `
SELECT
    COUNT(*) AS count,
    w.account_id,
    COALESCE(NULLIF(MAX(w.account_name), ''), 'unknown') AS account_name,
    COUNT(DISTINCT w.user_id) AS users,
    to_char(MAX(w.created_at AT TIME ZONE 'Asia/Shanghai'), 'YYYY-MM-DD HH24:MI:SS') AS last_seen
FROM content_moderation_cyber_warnings w `+whereSQL+`
GROUP BY w.account_id
ORDER BY count DESC, last_seen DESC
LIMIT 100`, args...)
	if err != nil {
		return nil, fmt.Errorf("summary content moderation cyber warnings by account: %w", err)
	}
	defer func() { _ = accountRows.Close() }()
	for accountRows.Next() {
		var item service.ContentModerationCyberAccountSummary
		var accountID sql.NullInt64
		if err := accountRows.Scan(&item.Count, &accountID, &item.AccountName, &item.Users, &item.LastSeen); err != nil {
			return nil, fmt.Errorf("scan content moderation cyber account summary: %w", err)
		}
		if accountID.Valid {
			v := accountID.Int64
			item.AccountID = &v
		}
		result.ByAccount = append(result.ByAccount, item)
	}
	if err := accountRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate content moderation cyber account summary: %w", err)
	}
	return result, nil
}

func (r *contentModerationRepository) CleanupExpiredLogs(ctx context.Context, hitBefore time.Time, nonHitBefore time.Time) (*service.ContentModerationCleanupResult, error) {
	result := &service.ContentModerationCleanupResult{FinishedAt: time.Now()}
	if r == nil || r.db == nil {
		return result, nil
	}
	hitExec, err := r.db.ExecContext(ctx, `
DELETE FROM content_moderation_logs
WHERE flagged = TRUE AND created_at < $1
`, hitBefore)
	if err != nil {
		return nil, fmt.Errorf("delete expired hit content moderation logs: %w", err)
	}
	result.DeletedHit, _ = hitExec.RowsAffected()

	nonHitExec, err := r.db.ExecContext(ctx, `
DELETE FROM content_moderation_logs
WHERE flagged = FALSE AND created_at < $1
`, nonHitBefore)
	if err != nil {
		return nil, fmt.Errorf("delete expired non-hit content moderation logs: %w", err)
	}
	result.DeletedNonHit, _ = nonHitExec.RowsAffected()

	result.FinishedAt = time.Now()
	return result, nil
}

func nullableIntPtr(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func buildContentModerationLogWhere(filter service.ContentModerationLogFilter) ([]string, []any) {
	where := []string{"l.id IS NOT NULL"}
	args := make([]any, 0)
	add := func(expr string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(expr, len(args)))
	}
	switch strings.ToLower(strings.TrimSpace(filter.Result)) {
	case "hit", "flagged":
		where = append(where, "l.flagged = TRUE")
	case "blocked", "block":
		where = append(where, "l.action IN ('block', 'keyword_block', 'hash_block')")
	case "pass", "allow":
		where = append(where, "l.flagged = FALSE AND l.error = ''")
	case "error":
		where = append(where, "l.error <> ''")
	}
	if filter.GroupID != nil {
		add("l.group_id = $%d", *filter.GroupID)
	}
	if endpoint := strings.TrimSpace(filter.Endpoint); endpoint != "" {
		add("l.endpoint = $%d", endpoint)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		like := "%" + search + "%"
		args = append(args, like, like, like, like, like)
		idx := len(args) - 4
		where = append(where, fmt.Sprintf("(l.request_id ILIKE $%d OR l.user_email ILIKE $%d OR l.api_key_name ILIKE $%d OR l.model ILIKE $%d OR l.input_excerpt ILIKE $%d)", idx, idx+1, idx+2, idx+3, idx+4))
	}
	if filter.From != nil && !filter.From.IsZero() {
		add("l.created_at >= $%d", *filter.From)
	}
	if filter.To != nil && !filter.To.IsZero() {
		add("l.created_at <= $%d", *filter.To)
	}
	return where, args
}

func buildContentModerationCyberWhere(filter service.ContentModerationCyberWarningFilter) ([]string, []any) {
	where := []string{"w.id IS NOT NULL"}
	args := make([]any, 0)
	add := func(expr string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(expr, len(args)))
	}
	if filter.UserID != nil {
		add("w.user_id = $%d", *filter.UserID)
	}
	if filter.AccountID != nil {
		add("w.account_id = $%d", *filter.AccountID)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		like := "%" + search + "%"
		args = append(args, like, like, like, like, like, like, like)
		idx := len(args) - 6
		where = append(where, fmt.Sprintf("(w.request_id ILIKE $%d OR w.user_email ILIKE $%d OR w.api_key_name ILIKE $%d OR w.account_name ILIKE $%d OR w.model ILIKE $%d OR w.warning_text ILIKE $%d OR w.prompt_excerpt ILIKE $%d)", idx, idx+1, idx+2, idx+3, idx+4, idx+5, idx+6))
	}
	if filter.From != nil && !filter.From.IsZero() {
		add("w.created_at >= $%d", *filter.From)
	}
	if filter.To != nil && !filter.To.IsZero() {
		add("w.created_at <= $%d", *filter.To)
	}
	return where, args
}
