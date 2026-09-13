package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	sqlutil "github.com/TokenFlux/TokenRouter/internal/infra/postgres"

	service "github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/lib/pq"
)

// Store 审计日志仓储（raw SQL，append-only）。
// 刻意不实现单条删除：审计日志只允许追加、按保留期批量清理、以及带 2FA 的全量清空。
type Store struct {
	db *sql.DB
}

// NewAuditLogRepository 创建审计日志仓储。
func NewAuditLogRepository(db *sql.DB) *Store {
	return &Store{db: db}
}

const auditLogInsertColumns = `created_at, actor_user_id, actor_email, actor_role, auth_method,
credential_masked, action, method, path, request_id, client_ip, user_agent,
request_body, status_code, latency_ms, extra`

func auditLogInsertValues(log *service.AuditLog) []any {
	createdAt := log.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	extraJSON := "{}"
	if len(log.Extra) > 0 {
		if encoded, err := json.Marshal(log.Extra); err == nil {
			extraJSON = string(encoded)
		}
	}
	return []any{
		createdAt.UTC(),
		sqlutil.NullPositiveInt64(log.ActorUserID),
		sqlutil.TruncateText(log.ActorEmail, 255),
		sqlutil.TruncateText(log.ActorRole, 32),
		sqlutil.TruncateText(log.AuthMethod, 32),
		sqlutil.TruncateText(log.CredentialMasked, 160),
		sqlutil.TruncateText(log.Action, 128),
		sqlutil.TruncateText(log.Method, 16),
		sqlutil.TruncateText(log.Path, 512),
		sqlutil.TruncateText(log.RequestID, 64),
		sqlutil.TruncateText(log.ClientIP, 64),
		sqlutil.TruncateText(log.UserAgent, 512),
		log.RequestBody,
		log.StatusCode,
		log.LatencyMs,
		extraJSON,
	}
}

func (r *Store) BatchInsert(ctx context.Context, logs []*service.AuditLog) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("nil audit log repository")
	}
	if len(logs) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn(
		"audit_logs",
		"created_at", "actor_user_id", "actor_email", "actor_role", "auth_method",
		"credential_masked", "action", "method", "path", "request_id", "client_ip", "user_agent",
		"request_body", "status_code", "latency_ms", "extra",
	))
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}

	var inserted int64
	for _, log := range logs {
		if log == nil {
			continue
		}
		if _, err := stmt.ExecContext(ctx, auditLogInsertValues(log)...); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return inserted, err
		}
		inserted++
	}

	if _, err := stmt.ExecContext(ctx); err != nil {
		_ = stmt.Close()
		_ = tx.Rollback()
		return inserted, err
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return inserted, err
	}
	if err := tx.Commit(); err != nil {
		return inserted, err
	}
	return inserted, nil
}

func (r *Store) Insert(ctx context.Context, log *service.AuditLog) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil audit log repository")
	}
	if log == nil {
		return fmt.Errorf("nil audit log")
	}
	query := `INSERT INTO audit_logs (` + auditLogInsertColumns + `)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`
	_, err := r.db.ExecContext(ctx, query, auditLogInsertValues(log)...)
	return err
}

func buildAuditLogsWhere(filter *service.AuditLogFilter) (string, []any) {
	clauses := make([]string, 0, 10)
	args := make([]any, 0, 10)
	clauses = append(clauses, "1=1")

	if filter.StartTime != nil {
		args = append(args, filter.StartTime.UTC())
		clauses = append(clauses, "l.created_at >= $"+strconv.Itoa(len(args)))
	}
	if filter.EndTime != nil {
		args = append(args, filter.EndTime.UTC())
		clauses = append(clauses, "l.created_at <= $"+strconv.Itoa(len(args)))
	}
	if filter.ActorUserID != nil {
		args = append(args, *filter.ActorUserID)
		clauses = append(clauses, "l.actor_user_id = $"+strconv.Itoa(len(args)))
	}
	if v := strings.TrimSpace(filter.ActorEmail); v != "" {
		args = append(args, "%"+sqlutil.EscapeLikePattern(v)+"%")
		clauses = append(clauses, "l.actor_email ILIKE $"+strconv.Itoa(len(args)))
	}
	if v := strings.TrimSpace(filter.AuthMethod); v != "" {
		args = append(args, v)
		clauses = append(clauses, "l.auth_method = $"+strconv.Itoa(len(args)))
	}
	if v := strings.TrimSpace(filter.Action); v != "" {
		args = append(args, "%"+sqlutil.EscapeLikePattern(v)+"%")
		clauses = append(clauses, "l.action ILIKE $"+strconv.Itoa(len(args)))
	}
	if v := strings.TrimSpace(filter.Method); v != "" {
		args = append(args, strings.ToUpper(v))
		clauses = append(clauses, "l.method = $"+strconv.Itoa(len(args)))
	}
	if v := strings.TrimSpace(filter.ClientIP); v != "" {
		args = append(args, v)
		clauses = append(clauses, "l.client_ip = $"+strconv.Itoa(len(args)))
	}
	if filter.Success != nil {
		if *filter.Success {
			clauses = append(clauses, "l.status_code < 400")
		} else {
			clauses = append(clauses, "l.status_code >= 400")
		}
	}
	if v := strings.TrimSpace(filter.Query); v != "" {
		args = append(args, "%"+sqlutil.EscapeLikePattern(v)+"%")
		idx := strconv.Itoa(len(args))
		clauses = append(clauses, "(l.path ILIKE $"+idx+" OR l.action ILIKE $"+idx+" OR l.actor_email ILIKE $"+idx+")")
	}

	return "WHERE " + strings.Join(clauses, " AND "), args
}

const auditLogSelectColumns = `
  l.id,
  l.created_at,
  l.actor_user_id,
  COALESCE(l.actor_email, ''),
  COALESCE(l.actor_role, ''),
  COALESCE(l.auth_method, ''),
  COALESCE(l.credential_masked, ''),
  COALESCE(l.action, ''),
  COALESCE(l.method, ''),
  COALESCE(l.path, ''),
  COALESCE(l.request_id, ''),
  COALESCE(l.client_ip, ''),
  COALESCE(l.user_agent, ''),
  COALESCE(l.request_body, ''),
  l.status_code,
  l.latency_ms,
  COALESCE(l.extra::text, '{}')`

func scanAuditLogRow(scan func(dest ...any) error) (*service.AuditLog, error) {
	item := &service.AuditLog{}
	var actorUserID sql.NullInt64
	var extraRaw string
	if err := scan(
		&item.ID,
		&item.CreatedAt,
		&actorUserID,
		&item.ActorEmail,
		&item.ActorRole,
		&item.AuthMethod,
		&item.CredentialMasked,
		&item.Action,
		&item.Method,
		&item.Path,
		&item.RequestID,
		&item.ClientIP,
		&item.UserAgent,
		&item.RequestBody,
		&item.StatusCode,
		&item.LatencyMs,
		&extraRaw,
	); err != nil {
		return nil, err
	}
	if actorUserID.Valid {
		v := actorUserID.Int64
		item.ActorUserID = &v
	}
	extraRaw = strings.TrimSpace(extraRaw)
	if extraRaw != "" && extraRaw != "null" && extraRaw != "{}" {
		extra := make(map[string]any)
		if err := json.Unmarshal([]byte(extraRaw), &extra); err == nil {
			item.Extra = extra
		}
	}
	return item, nil
}

func (r *Store) List(ctx context.Context, filter *service.AuditLogFilter) (*service.AuditLogList, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil audit log repository")
	}
	if filter == nil {
		filter = &service.AuditLogFilter{}
	}

	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}

	where, args := buildAuditLogsWhere(filter)
	countSQL := "SELECT COUNT(*) FROM audit_logs l " + where
	var total int
	if err := r.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	argsWithLimit := append(args, pageSize, offset)
	query := "SELECT" + auditLogSelectColumns + "\nFROM audit_logs l\n" + where + `
ORDER BY l.created_at DESC, l.id DESC
LIMIT $` + strconv.Itoa(len(args)+1) + ` OFFSET $` + strconv.Itoa(len(args)+2)

	rows, err := r.db.QueryContext(ctx, query, argsWithLimit...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	logs := make([]*service.AuditLog, 0, pageSize)
	for rows.Next() {
		item, err := scanAuditLogRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		// 列表页不返回 body，降低载荷；详情接口返回完整记录。
		item.RequestBody = ""
		logs = append(logs, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &service.AuditLogList{
		Logs:     logs,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (r *Store) GetByID(ctx context.Context, id int64) (*service.AuditLog, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil audit log repository")
	}
	query := "SELECT" + auditLogSelectColumns + "\nFROM audit_logs l WHERE l.id = $1"
	row := r.db.QueryRowContext(ctx, query, id)
	item, err := scanAuditLogRow(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrAuditLogNotFound
		}
		return nil, err
	}
	return item, nil
}

func (r *Store) Count(ctx context.Context) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("nil audit log repository")
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs").Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (r *Store) TruncateAll(ctx context.Context) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil audit log repository")
	}
	_, err := r.db.ExecContext(ctx, "TRUNCATE TABLE audit_logs")
	return err
}

func (r *Store) DeleteBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("nil audit log repository")
	}
	if batchSize <= 0 {
		batchSize = 5000
	}
	res, err := r.db.ExecContext(ctx, `
WITH batch AS (
  SELECT id FROM audit_logs WHERE created_at < $1 ORDER BY id LIMIT $2
)
DELETE FROM audit_logs WHERE id IN (SELECT id FROM batch)`, cutoff.UTC(), batchSize)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ClearWithTrace 把原记录清空与必须持久的留痕放在同一 PostgreSQL 事务中。
func (r *Store) ClearWithTrace(ctx context.Context, trace *service.AuditLog) (int64, error) {
	if trace == nil {
		return 0, fmt.Errorf("audit clear trace is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "LOCK TABLE audit_logs IN ACCESS EXCLUSIVE MODE"); err != nil {
		return 0, err
	}
	var count int64
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs").Scan(&count); err != nil {
		return 0, fmt.Errorf("count audit logs: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "TRUNCATE TABLE audit_logs"); err != nil {
		return 0, fmt.Errorf("truncate audit logs: %w", err)
	}
	copied := *trace
	copied.Extra = make(map[string]any, len(trace.Extra)+1)
	for k, v := range trace.Extra {
		copied.Extra[k] = v
	}
	copied.Extra["deleted_rows"] = count
	query := `INSERT INTO audit_logs (` + auditLogInsertColumns + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`
	if _, err = tx.ExecContext(ctx, query, auditLogInsertValues(&copied)...); err != nil {
		return 0, fmt.Errorf("persist audit clear trace: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
