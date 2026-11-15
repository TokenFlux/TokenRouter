package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	dbusageexporttask "github.com/TokenFlux/TokenRouter/ent/usageexporttask"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usage_export_repo.go persists usage-export task state, atomic worker claims,
// progress counters, conditional terminal updates, and artifact expiration transitions.
// Task creation, listing, cancellation, and outcome transitions prefer an available
// Ent client, including any context-bound transaction client, while falling back to
// direct SQL when only a SQL executor is supplied in unit fixtures. Worker claims
// always run on the SQL executor so FOR UPDATE SKIP LOCKED selects and transitions
// one candidate row atomically without blocking competing workers.
//
// Reclaiming a stale running task resets exported_rows and artifact columns to zero
// before returning so the replacement worker rewrites a complete CSV from the first
// row. MarkTaskSucceeded and MarkTaskFailed both exclude canceled rows in their
// WHERE predicates so concurrent user cancellation cannot be overwritten by an
// in-flight worker finishing its final batch.

// Default stale threshold and sweep limits bound export repository queries when
// callers pass nonpositive bounds.
const (
	defaultStaleExportSeconds = 1800 // fallback stale-running claim threshold in seconds
	defaultExpiredListLimit   = 100  // fallback batch limit when listing expired tasks
)

// ExportStore persists usage-export task metadata across Ent and raw SQL executors.
type ExportStore struct {
	// client selects Ent for CRUD and conditional transitions when nonnil; a
	// context transaction client takes precedence over this default handle.
	client *dbent.Client // default Ent client for export task metadata
	// sql executes SKIP LOCKED worker claims and backs all repository methods
	// when client is nil.
	sql sqlExecutor // SQL executor for atomic claims and fallback queries
}

// NewUsageExportRepository constructs an ExportStore implementing
// usage.UsageExportRepository over the supplied Ent client and SQL pool.
func NewUsageExportRepository(client *dbent.Client, sqlDB *sql.DB) usage.UsageExportRepository {
	return NewExportStoreWithSQL(client, sqlDB)
}

// NewExportStoreWithSQL binds export task persistence and SKIP LOCKED claims to
// caller-owned Ent and SQL handles.
func NewExportStoreWithSQL(client *dbent.Client, sqlq sqlExecutor) *ExportStore {
	return &ExportStore{client: client, sql: sqlq}
}

// CreateTask inserts a pending export task and populates its ID and timestamps.
func (r *ExportStore) CreateTask(ctx context.Context, task *usage.UsageExportTask) error {
	if task == nil {
		return nil
	}
	if r.client != nil {
		return r.createTaskWithEnt(ctx, task)
	}
	return r.createTaskWithSQL(ctx, task)
}

// GetTask loads one persisted export task by ID or returns an error matching
// both usage.ErrExportTaskNotFound and sql.ErrNoRows when absent.
func (r *ExportStore) GetTask(ctx context.Context, taskID int64) (*usage.UsageExportTask, error) {
	if r.client != nil {
		return r.getTaskWithEnt(ctx, taskID)
	}
	query := `
		SELECT id, scope, status, filters, created_by, total_rows, exported_rows,
			file_name, file_path, file_size_bytes, error_message,
			canceled_by, canceled_at, started_at, finished_at, expires_at,
			created_at, updated_at
		FROM usage_export_tasks
		WHERE id = $1
	`
	rows, err := r.sql.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, errors.Join(usage.ErrExportTaskNotFound, sql.ErrNoRows)
	}
	task, err := scanUsageExportTaskRow(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &task, nil
}

// ListTasks returns scoped export tasks ordered newest first with exact total counts.
func (r *ExportStore) ListTasks(
	ctx context.Context,
	filter usage.UsageExportListFilter,
	params pagination.PaginationParams,
) ([]usage.UsageExportTask, *pagination.PaginationResult, error) {
	if r.client != nil {
		return r.listTasksWithEnt(ctx, filter, params)
	}

	conditions := []string{"scope = $1"}
	args := []any{filter.Scope}
	if filter.CreatedBy != nil {
		conditions = append(conditions, fmt.Sprintf("created_by = $%d", len(args)+1))
		args = append(args, *filter.CreatedBy)
	}
	whereClause := "WHERE " + strings.Join(conditions, " AND ")

	var total int64
	if err := scanSingleRow(ctx, r.sql, "SELECT COUNT(*) FROM usage_export_tasks "+whereClause, args, &total); err != nil {
		return nil, nil, err
	}
	if total == 0 {
		return []usage.UsageExportTask{}, paginationResultFromTotal(0, params), nil
	}

	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	listArgs := append(append([]any{}, args...), params.Limit(), params.Offset())
	query := fmt.Sprintf(`
		SELECT id, scope, status, filters, created_by, total_rows, exported_rows,
			file_name, file_path, file_size_bytes, error_message,
			canceled_by, canceled_at, started_at, finished_at, expires_at,
			created_at, updated_at
		FROM usage_export_tasks
		%s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, limitPos, offsetPos)

	rows, err := r.sql.QueryContext(ctx, query, listArgs...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	tasks := make([]usage.UsageExportTask, 0)
	for rows.Next() {
		task, scanErr := scanUsageExportTaskRow(rows)
		if scanErr != nil {
			return nil, nil, scanErr
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return tasks, paginationResultFromTotal(total, params), nil
}

// CountActiveTasks counts pending and running export tasks in scope, filtering
// by createdBy when positive.
func (r *ExportStore) CountActiveTasks(ctx context.Context, scope string, createdBy int64) (int64, error) {
	if r.client != nil {
		client := clientFromContext(ctx, r.client)
		q := client.UsageExportTask.Query().Where(
			dbusageexporttask.ScopeEQ(scope),
			dbusageexporttask.StatusIn(usage.UsageExportStatusPending, usage.UsageExportStatusRunning),
		)
		if createdBy > 0 {
			q = q.Where(dbusageexporttask.CreatedByEQ(createdBy))
		}
		cnt, err := q.Count(ctx)
		return int64(cnt), err
	}

	args := []any{scope, usage.UsageExportStatusPending, usage.UsageExportStatusRunning}
	query := "SELECT COUNT(*) FROM usage_export_tasks WHERE scope = $1 AND status IN ($2, $3)"
	if createdBy > 0 {
		args = append(args, createdBy)
		query += fmt.Sprintf(" AND created_by = $%d", len(args))
	}
	var count int64
	if err := scanSingleRow(ctx, r.sql, query, args, &count); err != nil {
		return 0, err
	}
	return count, nil
}

// ClaimNextPendingTask atomically selects the oldest pending or stale-running
// export task using FOR UPDATE SKIP LOCKED and resets its progress and artifact
// metadata for a clean rewrite. Returns (nil, nil) when no candidate is eligible.
func (r *ExportStore) ClaimNextPendingTask(ctx context.Context, staleRunningAfterSeconds int64) (*usage.UsageExportTask, error) {
	if staleRunningAfterSeconds <= 0 {
		staleRunningAfterSeconds = defaultStaleExportSeconds
	}
	// Claim and reset within one SKIP LOCKED statement so competing workers skip
	// locked candidates and stale running tasks restart cleanly from row zero.
	query := `
		WITH next AS (
			SELECT id
			FROM usage_export_tasks
			WHERE status = $1
				OR (
					status = $2
					AND started_at IS NOT NULL
					AND started_at < NOW() - ($3 * interval '1 second')
				)
			ORDER BY created_at ASC, id ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE usage_export_tasks AS tasks
		SET status = $4,
			exported_rows = 0,
			file_name = NULL,
			file_path = NULL,
			file_size_bytes = 0,
			started_at = NOW(),
			finished_at = NULL,
			expires_at = NULL,
			error_message = NULL,
			updated_at = NOW()
		FROM next
		WHERE tasks.id = next.id
		RETURNING tasks.id, tasks.scope, tasks.status, tasks.filters, tasks.created_by,
			tasks.total_rows, tasks.exported_rows, tasks.file_name, tasks.file_path,
			tasks.file_size_bytes, tasks.error_message, tasks.canceled_by, tasks.canceled_at,
			tasks.started_at, tasks.finished_at, tasks.expires_at, tasks.created_at, tasks.updated_at
	`
	rows, err := r.sql.QueryContext(
		ctx,
		query,
		usage.UsageExportStatusPending,
		usage.UsageExportStatusRunning,
		staleRunningAfterSeconds,
		usage.UsageExportStatusRunning,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	task, err := scanUsageExportTaskRow(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &task, nil
}

// GetTaskStatus reads the persisted status for taskID or returns an error
// matching usage.ErrExportTaskNotFound when the row does not exist.
func (r *ExportStore) GetTaskStatus(ctx context.Context, taskID int64) (string, error) {
	if r.client != nil {
		client := clientFromContext(ctx, r.client)
		row, err := client.UsageExportTask.Query().
			Where(dbusageexporttask.IDEQ(taskID)).
			Select(dbusageexporttask.FieldStatus).
			Only(ctx)
		if err != nil {
			if dbent.IsNotFound(err) {
				return "", errors.Join(usage.ErrExportTaskNotFound, sql.ErrNoRows)
			}
			return "", err
		}
		return row.Status, nil
	}

	var status string
	if err := scanSingleRow(ctx, r.sql, "SELECT status FROM usage_export_tasks WHERE id = $1", []any{taskID}, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.Join(usage.ErrExportTaskNotFound, err)
		}
		return "", err
	}
	return status, nil
}

// UpdateTaskProgress updates total_rows and exported_rows while taskID remains
// running, leaving started_at untouched so stale reclamation timing stays accurate.
func (r *ExportStore) UpdateTaskProgress(ctx context.Context, taskID int64, totalRows, exportedRows int64) error {
	if r.client != nil {
		client := clientFromContext(ctx, r.client)
		_, err := client.UsageExportTask.Update().
			Where(
				dbusageexporttask.IDEQ(taskID),
				dbusageexporttask.StatusEQ(usage.UsageExportStatusRunning),
			).
			SetTotalRows(totalRows).
			SetExportedRows(exportedRows).
			SetUpdatedAt(time.Now()).
			Save(ctx)
		return err
	}

	query := `
		UPDATE usage_export_tasks
		SET total_rows = $1,
			exported_rows = $2,
			updated_at = NOW()
		WHERE id = $3
			AND status = $4
	`
	_, err := r.sql.ExecContext(ctx, query, totalRows, exportedRows, taskID, usage.UsageExportStatusRunning)
	return err
}

// CancelTask conditionally transitions a pending or running export task to
// canceled and records the canceling actor and finish timestamp.
func (r *ExportStore) CancelTask(ctx context.Context, taskID int64, canceledBy int64) (bool, error) {
	if r.client != nil {
		client := clientFromContext(ctx, r.client)
		now := time.Now()
		affected, err := client.UsageExportTask.Update().
			Where(
				dbusageexporttask.IDEQ(taskID),
				dbusageexporttask.StatusIn(usage.UsageExportStatusPending, usage.UsageExportStatusRunning),
			).
			SetStatus(usage.UsageExportStatusCanceled).
			SetCanceledBy(canceledBy).
			SetCanceledAt(now).
			SetFinishedAt(now).
			ClearErrorMessage().
			SetUpdatedAt(now).
			Save(ctx)
		if err != nil {
			return false, err
		}
		return affected > 0, nil
	}

	query := `
		UPDATE usage_export_tasks
		SET status = $1,
			canceled_by = $3,
			canceled_at = NOW(),
			finished_at = NOW(),
			error_message = NULL,
			updated_at = NOW()
		WHERE id = $2
			AND status IN ($4, $5)
		RETURNING id
	`
	var id int64
	err := scanSingleRow(ctx, r.sql, query, []any{
		usage.UsageExportStatusCanceled,
		taskID,
		canceledBy,
		usage.UsageExportStatusPending,
		usage.UsageExportStatusRunning,
	}, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// MarkTaskSucceeded conditionally publishes succeeded status, artifact metadata,
// and expiration instant when taskID has not been canceled concurrently.
func (r *ExportStore) MarkTaskSucceeded(
	ctx context.Context,
	taskID int64,
	exportedRows int64,
	fileName, filePath string,
	fileSizeBytes int64,
	expiresAt time.Time,
) (bool, error) {
	if r.client != nil {
		client := clientFromContext(ctx, r.client)
		now := time.Now()
		// Guard terminal success against concurrent cancellation so a canceled task
		// never resurfaces as downloadable; false instructs the worker to delete filePath.
		affected, err := client.UsageExportTask.Update().
			Where(
				dbusageexporttask.IDEQ(taskID),
				dbusageexporttask.StatusNEQ(usage.UsageExportStatusCanceled),
			).
			SetStatus(usage.UsageExportStatusSucceeded).
			SetExportedRows(exportedRows).
			SetFileName(fileName).
			SetFilePath(filePath).
			SetFileSizeBytes(fileSizeBytes).
			SetExpiresAt(expiresAt).
			SetFinishedAt(now).
			ClearErrorMessage().
			SetUpdatedAt(now).
			Save(ctx)
		if err != nil {
			return false, err
		}
		return affected > 0, nil
	}

	query := `
		UPDATE usage_export_tasks
		SET status = $1,
			exported_rows = $3,
			file_name = $4,
			file_path = $5,
			file_size_bytes = $6,
			expires_at = $7,
			finished_at = NOW(),
			error_message = NULL,
			updated_at = NOW()
		WHERE id = $2
			AND status <> $8
		RETURNING id
	`
	var id int64
	err := scanSingleRow(ctx, r.sql, query, []any{
		usage.UsageExportStatusSucceeded,
		taskID,
		exportedRows,
		fileName,
		filePath,
		fileSizeBytes,
		expiresAt,
		usage.UsageExportStatusCanceled,
	}, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// MarkTaskFailed records failed status and diagnostic message when taskID has
// not already transitioned to canceled.
func (r *ExportStore) MarkTaskFailed(ctx context.Context, taskID int64, exportedRows int64, errorMsg string) error {
	if r.client != nil {
		client := clientFromContext(ctx, r.client)
		now := time.Now()
		_, err := client.UsageExportTask.Update().
			Where(
				dbusageexporttask.IDEQ(taskID),
				dbusageexporttask.StatusNEQ(usage.UsageExportStatusCanceled),
			).
			SetStatus(usage.UsageExportStatusFailed).
			SetExportedRows(exportedRows).
			SetErrorMessage(errorMsg).
			SetFinishedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		return err
	}

	query := `
		UPDATE usage_export_tasks
		SET status = $1,
			exported_rows = $3,
			error_message = $4,
			finished_at = NOW(),
			updated_at = NOW()
		WHERE id = $2
			AND status <> $5
	`
	_, err := r.sql.ExecContext(
		ctx,
		query,
		usage.UsageExportStatusFailed,
		taskID,
		exportedRows,
		errorMsg,
		usage.UsageExportStatusCanceled,
	)
	return err
}

// ListExpiredSucceededTasks returns up to limit succeeded tasks whose ExpiresAt
// is at or before now so retention sweeps can remove their CSV files.
func (r *ExportStore) ListExpiredSucceededTasks(ctx context.Context, now time.Time, limit int) ([]usage.UsageExportTask, error) {
	if limit <= 0 {
		limit = defaultExpiredListLimit
	}
	if r.client != nil {
		client := clientFromContext(ctx, r.client)
		rows, err := client.UsageExportTask.Query().
			Where(
				dbusageexporttask.StatusEQ(usage.UsageExportStatusSucceeded),
				dbusageexporttask.ExpiresAtNotNil(),
				dbusageexporttask.ExpiresAtLTE(now),
			).
			Order(dbent.Asc(dbusageexporttask.FieldExpiresAt), dbent.Asc(dbusageexporttask.FieldID)).
			Limit(limit).
			All(ctx)
		if err != nil {
			return nil, err
		}
		tasks := make([]usage.UsageExportTask, 0, len(rows))
		for _, row := range rows {
			task, convErr := usageExportTaskFromEnt(row)
			if convErr != nil {
				return nil, convErr
			}
			tasks = append(tasks, task)
		}
		return tasks, nil
	}

	query := `
		SELECT id, scope, status, filters, created_by, total_rows, exported_rows,
			file_name, file_path, file_size_bytes, error_message,
			canceled_by, canceled_at, started_at, finished_at, expires_at,
			created_at, updated_at
		FROM usage_export_tasks
		WHERE status = $1
			AND expires_at IS NOT NULL
			AND expires_at <= $2
		ORDER BY expires_at ASC, id ASC
		LIMIT $3
	`
	rows, err := r.sql.QueryContext(ctx, query, usage.UsageExportStatusSucceeded, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	tasks := make([]usage.UsageExportTask, 0)
	for rows.Next() {
		task, scanErr := scanUsageExportTaskRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tasks, nil
}

// MarkTaskExpired transitions a succeeded task to expired and clears its FilePath
// while retaining FileName and FileSizeBytes for historical UI display.
func (r *ExportStore) MarkTaskExpired(ctx context.Context, taskID int64) error {
	if r.client != nil {
		client := clientFromContext(ctx, r.client)
		_, err := client.UsageExportTask.Update().
			Where(
				dbusageexporttask.IDEQ(taskID),
				dbusageexporttask.StatusEQ(usage.UsageExportStatusSucceeded),
			).
			SetStatus(usage.UsageExportStatusExpired).
			ClearFilePath().
			SetUpdatedAt(time.Now()).
			Save(ctx)
		return err
	}

	query := `
		UPDATE usage_export_tasks
		SET status = $1,
			file_path = NULL,
			updated_at = NOW()
		WHERE id = $2
			AND status = $3
	`
	_, err := r.sql.ExecContext(ctx, query, usage.UsageExportStatusExpired, taskID, usage.UsageExportStatusSucceeded)
	return err
}

// createTaskWithEnt inserts task via Ent and populates ID and timestamps.
func (r *ExportStore) createTaskWithEnt(ctx context.Context, task *usage.UsageExportTask) error {
	client := clientFromContext(ctx, r.client)
	filtersJSON, err := json.Marshal(task.Filters)
	if err != nil {
		return fmt.Errorf("marshal export filters: %w", err)
	}
	created, err := client.UsageExportTask.Create().
		SetScope(task.Scope).
		SetStatus(task.Status).
		SetFilters(jsontext.Value(filtersJSON)).
		SetCreatedBy(task.CreatedBy).
		SetTotalRows(task.TotalRows).
		SetExportedRows(task.ExportedRows).
		Save(ctx)
	if err != nil {
		return err
	}
	task.ID = created.ID
	task.CreatedAt = created.CreatedAt
	task.UpdatedAt = created.UpdatedAt
	return nil
}

// createTaskWithSQL inserts task via SQL and scans generated ID and timestamps.
func (r *ExportStore) createTaskWithSQL(ctx context.Context, task *usage.UsageExportTask) error {
	filtersJSON, err := json.Marshal(task.Filters)
	if err != nil {
		return fmt.Errorf("marshal export filters: %w", err)
	}
	query := `
		INSERT INTO usage_export_tasks (
			scope,
			status,
			filters,
			created_by,
			total_rows,
			exported_rows
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at
	`
	return scanSingleRow(
		ctx,
		r.sql,
		query,
		[]any{task.Scope, task.Status, filtersJSON, task.CreatedBy, task.TotalRows, task.ExportedRows},
		&task.ID,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
}

// getTaskWithEnt queries one export task by ID via Ent.
func (r *ExportStore) getTaskWithEnt(ctx context.Context, taskID int64) (*usage.UsageExportTask, error) {
	client := clientFromContext(ctx, r.client)
	row, err := client.UsageExportTask.Query().
		Where(dbusageexporttask.IDEQ(taskID)).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, errors.Join(usage.ErrExportTaskNotFound, sql.ErrNoRows)
		}
		return nil, err
	}
	task, err := usageExportTaskFromEnt(row)
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// listTasksWithEnt queries paginated export tasks via Ent ordered newest first.
func (r *ExportStore) listTasksWithEnt(
	ctx context.Context,
	filter usage.UsageExportListFilter,
	params pagination.PaginationParams,
) ([]usage.UsageExportTask, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	query := client.UsageExportTask.Query().Where(dbusageexporttask.ScopeEQ(filter.Scope))
	if filter.CreatedBy != nil {
		query = query.Where(dbusageexporttask.CreatedByEQ(*filter.CreatedBy))
	}
	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, nil, err
	}
	if total == 0 {
		return []usage.UsageExportTask{}, paginationResultFromTotal(0, params), nil
	}
	rows, err := query.
		Order(dbent.Desc(dbusageexporttask.FieldCreatedAt), dbent.Desc(dbusageexporttask.FieldID)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}
	tasks := make([]usage.UsageExportTask, 0, len(rows))
	for _, row := range rows {
		task, convErr := usageExportTaskFromEnt(row)
		if convErr != nil {
			return nil, nil, convErr
		}
		tasks = append(tasks, task)
	}
	return tasks, paginationResultFromTotal(int64(total), params), nil
}

// scanUsageExportTaskRow decodes one 18-column usage_export_tasks SQL row.
func scanUsageExportTaskRow(rows *sql.Rows) (usage.UsageExportTask, error) {
	var (
		task        usage.UsageExportTask
		filtersJSON []byte
		fileName    sql.NullString
		filePath    sql.NullString
		errMsg      sql.NullString
		canceledBy  sql.NullInt64
		canceledAt  sql.NullTime
		startedAt   sql.NullTime
		finishedAt  sql.NullTime
		expiresAt   sql.NullTime
	)
	if err := rows.Scan(
		&task.ID,
		&task.Scope,
		&task.Status,
		&filtersJSON,
		&task.CreatedBy,
		&task.TotalRows,
		&task.ExportedRows,
		&fileName,
		&filePath,
		&task.FileSizeBytes,
		&errMsg,
		&canceledBy,
		&canceledAt,
		&startedAt,
		&finishedAt,
		&expiresAt,
		&task.CreatedAt,
		&task.UpdatedAt,
	); err != nil {
		return usage.UsageExportTask{}, err
	}
	if len(filtersJSON) > 0 {
		if err := json.Unmarshal(filtersJSON, &task.Filters); err != nil {
			return usage.UsageExportTask{}, fmt.Errorf("parse export filters: %w", err)
		}
	}
	if fileName.Valid {
		s := fileName.String
		task.FileName = &s
	}
	if filePath.Valid {
		s := filePath.String
		task.FilePath = &s
	}
	if errMsg.Valid {
		s := errMsg.String
		task.ErrorMsg = &s
	}
	if canceledBy.Valid {
		v := canceledBy.Int64
		task.CanceledBy = &v
	}
	if canceledAt.Valid {
		t := canceledAt.Time
		task.CanceledAt = &t
	}
	if startedAt.Valid {
		t := startedAt.Time
		task.StartedAt = &t
	}
	if finishedAt.Valid {
		t := finishedAt.Time
		task.FinishedAt = &t
	}
	if expiresAt.Valid {
		t := expiresAt.Time
		task.ExpiresAt = &t
	}
	return task, nil
}

// usageExportTaskFromEnt converts a nonnil Ent UsageExportTask row into the
// core UsageExportTask value and decodes its persisted filter JSON.
func usageExportTaskFromEnt(row *dbent.UsageExportTask) (usage.UsageExportTask, error) {
	task := usage.UsageExportTask{
		ID:            row.ID,
		Scope:         row.Scope,
		Status:        row.Status,
		CreatedBy:     row.CreatedBy,
		TotalRows:     row.TotalRows,
		ExportedRows:  row.ExportedRows,
		FileName:      row.FileName,
		FilePath:      row.FilePath,
		FileSizeBytes: row.FileSizeBytes,
		ErrorMsg:      row.ErrorMessage,
		CanceledBy:    row.CanceledBy,
		CanceledAt:    row.CanceledAt,
		StartedAt:     row.StartedAt,
		FinishedAt:    row.FinishedAt,
		ExpiresAt:     row.ExpiresAt,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	if len(row.Filters) > 0 {
		if err := json.Unmarshal(row.Filters, &task.Filters); err != nil {
			return usage.UsageExportTask{}, fmt.Errorf("parse export filters: %w", err)
		}
	}
	return task, nil
}
