// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	sql "database/sql"
	errors "errors"
	fmt "fmt"
	time "time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	team "github.com/TokenFlux/TokenRouter/internal/team"
)

func (r *TeamKeys) ListTeamKeyStrings(ctx context.Context, teamID int64) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key FROM api_keys WHERE team_id = $1 AND deleted_at IS NULL`, teamID)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (r *TeamKeys) ListTeamKeys(ctx context.Context, teamID int64, actorUserID *int64) ([]team.TeamAPIKeyItem, error) {
	args := []any{teamID}
	actorCondition, args := TeamKeyActorCondition(actorUserID, args)
	rows, err := r.db.QueryContext(ctx, `
		SELECT k.id, k.user_id, COALESCE(u.email, ''), k.name, k.key, k.status, k.team_owner_disabled, k.group_id,
		       COALESCE(g.name, ''), k.last_used_at, k.created_at
		FROM api_keys k
		LEFT JOIN users u ON u.id = k.user_id
		LEFT JOIN groups g ON g.id = k.group_id
		WHERE k.team_id = $1 AND k.deleted_at IS NULL`+actorCondition+`
		ORDER BY k.created_at DESC, k.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	items := make([]team.TeamAPIKeyItem, 0)
	for rows.Next() {
		var item team.TeamAPIKeyItem
		var groupID sql.NullInt64
		var lastUsedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.UserID, &item.UserEmail, &item.Name, &item.Key, &item.Status, &item.OwnerDisabled, &groupID, &item.GroupName, &lastUsedAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.GroupID = batchImageNullInt64Ptr(groupID)
		item.LastUsedAt = batchImageNullTimePtr(lastUsedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TeamKeys) DisableTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error) {
	args := []any{teamID, keyID}
	actorCondition, args := TeamKeyActorCondition(actorUserID, args)
	var key string
	err := r.db.QueryRowContext(ctx, `
		UPDATE api_keys k SET status = 'disabled', team_owner_disabled = TRUE, updated_at = NOW()
		WHERE k.team_id = $1 AND k.id = $2 AND k.deleted_at IS NULL`+actorCondition+`
		RETURNING k.key`, args...).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", apikey.ErrAPIKeyNotFound
	}
	return key, err
}

func (r *TeamKeys) EnableTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error) {
	args := []any{teamID, keyID}
	actorCondition, args := TeamKeyActorCondition(actorUserID, args)
	var key string
	err := r.db.QueryRowContext(ctx, `
		UPDATE api_keys k SET status = 'active', team_owner_disabled = FALSE, updated_at = NOW()
		WHERE k.team_id = $1 AND k.id = $2 AND k.deleted_at IS NULL`+actorCondition+`
		RETURNING k.key`, args...).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", apikey.ErrAPIKeyNotFound
	}
	return key, err
}

func (r *TeamKeys) DeleteTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	args := []any{teamID, keyID}
	actorCondition, args := TeamKeyActorCondition(actorUserID, args)
	var key string
	err = tx.QueryRowContext(ctx, `SELECT k.key FROM api_keys k WHERE k.team_id = $1 AND k.id = $2 AND k.deleted_at IS NULL`+actorCondition+` FOR UPDATE`, args...).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", apikey.ErrAPIKeyNotFound
	}
	if err != nil {
		return "", err
	}
	tombstone := fmt.Sprintf("__deleted__%d__%d", keyID, time.Now().UnixNano())
	if _, err = tx.ExecContext(ctx, `UPDATE api_keys SET key = $2, deleted_at = NOW(), updated_at = NOW() WHERE id = $1`, keyID, tombstone); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return key, nil
}

func TeamKeyActorCondition(actorUserID *int64, args []any) (string, []any) {
	if actorUserID == nil || *actorUserID <= 0 {
		return "", args
	}
	args = append(args, *actorUserID)
	return fmt.Sprintf(" AND k.user_id = $%d", len(args)), args
}
func batchImageNullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func batchImageNullTimePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}

// TeamKeys 拥有团队 Key 的 SQL 操作；成员事务必须显式传入同一连接。
type TeamKeys struct{ db *sql.DB }

func NewTeamKeys(db *sql.DB) *TeamKeys { return &TeamKeys{db: db} }

// DisableMemberInTx 参与成员移除，不取得或结束事务。
func (k *TeamKeys) DisableMemberInTx(ctx context.Context, tx *sql.Tx, teamID, userID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE api_keys SET status = 'disabled', updated_at = $3 WHERE team_id = $1 AND user_id = $2 AND deleted_at IS NULL`, teamID, userID, now)
	return err
}

// DisableHistoricalMemberInTx 保留重新加入时旧 Membership Key 不自动恢复的边界。
func (k *TeamKeys) DisableHistoricalMemberInTx(ctx context.Context, tx *sql.Tx, teamID, userID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE api_keys SET status = 'disabled', updated_at = $3 WHERE team_id = $1 AND user_id = $2 AND created_at < $3 AND deleted_at IS NULL`, teamID, userID, now)
	return err
}
func (k *TeamKeys) DisableTeamInTx(ctx context.Context, tx *sql.Tx, teamID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE api_keys SET status = 'disabled', updated_at = $2 WHERE team_id = $1 AND deleted_at IS NULL`, teamID, now)
	return err
}
