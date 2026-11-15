package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	dbusageexporttask "github.com/TokenFlux/TokenRouter/ent/usageexporttask"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usage_export_repo.go persists usage-export task state, quota-serialized task
// admission, atomic worker claims, conditional terminal updates, and expiration sweeps.
// Task creation serializes concurrent active-quota checks per (scope, created_by)
// inside a database transaction with pg_advisory_xact_lock on PostgreSQL so two
// simultaneous requests cannot both observe spare quota and exceed maxActive.
//
// Worker claims run on the SQL executor with FOR UPDATE SKIP LOCKED, resetting
// exported_rows and artifact columns to zero when reclaiming crash-abandoned tasks,
// while graceful shutdown calls RequeueTask to return running tasks directly to
// pending. MarkTaskSucceeded and MarkTaskFailed both exclude canceled rows so
// concurrent user cancellation cannot be overwritten by an in-flight worker.

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
	// quotaMu serializes active-quota checks on non-PostgreSQL unit-test drivers
	// such as SQLite where pg_advisory_xact_lock is unavailable.
	quotaMu sync.Mutex // local quota serialization lock for non-PostgreSQL drivers
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
//
// When maxActive is positive, CreateTask serializes concurrent admissions for
// (task.Scope, task.CreatedBy) inside a transaction, counts existing pending and
// running rows after acquiring the lock, and returns usage.ErrExportActiveLimitExceeded
// without inserting if the active count already meets or exceeds maxActive.
func (r *ExportStore) CreateTask(ctx context.Context, task *usage.UsageExportTask, maxActive int) error {
	if task == nil {
		return nil
	}
	filtersJSON, err := json.Marshal(task.Filters)
	if err != nil {
		return fmt.Errorf("marshal export filters: %w", err)
	}
	if r.client != nil {
		return r.createTaskWithEnt(ctx, task, filtersJSON, maxActive)
	}
	return r.createTaskWithSQL(ctx, task, filtersJSON, maxActive)
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

// ListTaskPage reads one paginated slice of scoped export tasks, all active
// task IDs in that scope, and any requested off-page watchIDs inside one
// read-only REPEATABLE READ snapshot transaction so task statuses and active_ids
// never diverge when a background worker completes a task mid-poll.
func (r *ExportStore) ListTaskPage(
	ctx context.Context,
	filter usage.UsageExportListFilter,
	params pagination.PaginationParams,
	watchIDs []int64,
) (*usage.UsageExportTaskListResult, error) {
	if r.client != nil {
		return r.listTaskPageWithEnt(ctx, filter, params, watchIDs)
	}
	if sqlDB, ok := r.sql.(*sql.DB); ok {
		tx, err := sqlDB.BeginTx(ctx, &sql.TxOptions{
			Isolation: sql.LevelRepeatableRead,
			ReadOnly:  true,
		})
		if err != nil {
			return nil, err
		}
		res, err := listTaskPageWithSQL(ctx, tx, filter, params, watchIDs)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return res, nil
	}
	return listTaskPageWithSQL(ctx, r.sql, filter, params, watchIDs)
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
	return countActiveTasksSQL(ctx, r.sql, scope, createdBy)
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

// RequeueTask conditionally resets a running export task back to pending with
// cleared claim and artifact fields during graceful service shutdown, leaving
// canceled or already completed rows untouched.
func (r *ExportStore) RequeueTask(ctx context.Context, taskID int64) error {
	if r.client != nil {
		client := clientFromContext(ctx, r.client)
		_, err := client.UsageExportTask.Update().
			Where(
				dbusageexporttask.IDEQ(taskID),
				dbusageexporttask.StatusEQ(usage.UsageExportStatusRunning),
			).
			SetStatus(usage.UsageExportStatusPending).
			SetExportedRows(0).
			SetFileSizeBytes(0).
			ClearFileName().
			ClearFilePath().
			ClearStartedAt().
			ClearFinishedAt().
			ClearExpiresAt().
			ClearErrorMessage().
			SetUpdatedAt(time.Now()).
			Save(ctx)
		return err
	}

	query := `
		UPDATE usage_export_tasks
		SET status = $1,
			exported_rows = 0,
			file_name = NULL,
			file_path = NULL,
			file_size_bytes = 0,
			started_at = NULL,
			finished_at = NULL,
			expires_at = NULL,
			error_message = NULL,
			updated_at = NOW()
		WHERE id = $2
			AND status = $3
	`
	_, err := r.sql.ExecContext(ctx, query, usage.UsageExportStatusPending, taskID, usage.UsageExportStatusRunning)
	return err
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

// createTaskWithEnt inserts task via Ent, enforcing maxActive inside a
// transaction-scoped advisory lock when maxActive is positive.
func (r *ExportStore) createTaskWithEnt(
	ctx context.Context,
	task *usage.UsageExportTask,
	filtersJSON []byte,
	maxActive int,
) error {
	if maxActive <= 0 {
		return insertUsageExportTaskEnt(ctx, clientFromContext(ctx, r.client), task, filtersJSON)
	}

	isPostgres := r.client.Driver().Dialect() == dialect.Postgres
	if !isPostgres {
		// SQLite unit-test drivers lack pg_advisory_xact_lock and share one writer;
		// serialize local admissions so quota checks and inserts stay atomic in tests.
		r.quotaMu.Lock()
		defer r.quotaMu.Unlock()
	}

	insertGuarded := func(txClient *dbent.Client) error {
		if isPostgres {
			lockKey := usageExportQuotaAdvisoryLockKey(task.Scope, task.CreatedBy)
			var lockRows entsql.Rows
			if err := txClient.Driver().Query(ctx, "SELECT pg_advisory_xact_lock($1)", []any{lockKey}, &lockRows); err != nil {
				return fmt.Errorf("acquire export quota lock: %w", err)
			}
			// Advisory lock ownership stays bound to the active database transaction
			// after closing the empty SELECT result set.
			_ = lockRows.Close()
		}

		query := txClient.UsageExportTask.Query().Where(
			dbusageexporttask.ScopeEQ(task.Scope),
			dbusageexporttask.StatusIn(usage.UsageExportStatusPending, usage.UsageExportStatusRunning),
		)
		if task.CreatedBy > 0 {
			query = query.Where(dbusageexporttask.CreatedByEQ(task.CreatedBy))
		}
		activeCount, err := query.Count(ctx)
		if err != nil {
			return err
		}
		if activeCount >= maxActive {
			return usage.ErrExportActiveLimitExceeded
		}
		return insertUsageExportTaskEnt(ctx, txClient, task, filtersJSON)
	}

	if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		return insertGuarded(existingTx.Client())
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	if err := insertGuarded(tx.Client()); err != nil {
		// Roll back the owned quota transaction on limit rejection or insert error
		// so the transaction-scoped advisory lock releases immediately.
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// insertUsageExportTaskEnt persists one UsageExportTask row on client and copies
// database-assigned ID and timestamps back into task.
func insertUsageExportTaskEnt(
	ctx context.Context,
	client *dbent.Client,
	task *usage.UsageExportTask,
	filtersJSON []byte,
) error {
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

// createTaskWithSQL inserts task via SQL, enforcing maxActive inside a SQL
// transaction when backed by *sql.DB and maxActive is positive.
func (r *ExportStore) createTaskWithSQL(
	ctx context.Context,
	task *usage.UsageExportTask,
	filtersJSON []byte,
	maxActive int,
) error {
	if maxActive <= 0 {
		return insertUsageExportTaskSQL(ctx, r.sql, task, filtersJSON)
	}

	if sqlDB, ok := r.sql.(*sql.DB); ok {
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		lockKey := usageExportQuotaAdvisoryLockKey(task.Scope, task.CreatedBy)
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("acquire export quota lock: %w", err)
		}
		count, err := countActiveTasksSQL(ctx, tx, task.Scope, task.CreatedBy)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if count >= int64(maxActive) {
			_ = tx.Rollback()
			return usage.ErrExportActiveLimitExceeded
		}
		if err := insertUsageExportTaskSQL(ctx, tx, task, filtersJSON); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}

	r.quotaMu.Lock()
	defer r.quotaMu.Unlock()
	count, err := countActiveTasksSQL(ctx, r.sql, task.Scope, task.CreatedBy)
	if err != nil {
		return err
	}
	if count >= int64(maxActive) {
		return usage.ErrExportActiveLimitExceeded
	}
	return insertUsageExportTaskSQL(ctx, r.sql, task, filtersJSON)
}

// countActiveTasksSQL counts pending and running export tasks on exec for scope and createdBy.
func countActiveTasksSQL(ctx context.Context, exec sqlExecutor, scope string, createdBy int64) (int64, error) {
	args := []any{scope, usage.UsageExportStatusPending, usage.UsageExportStatusRunning}
	query := "SELECT COUNT(*) FROM usage_export_tasks WHERE scope = $1 AND status IN ($2, $3)"
	if createdBy > 0 {
		args = append(args, createdBy)
		query += fmt.Sprintf(" AND created_by = $%d", len(args))
	}
	var count int64
	if err := scanSingleRow(ctx, exec, query, args, &count); err != nil {
		return 0, err
	}
	return count, nil
}

// insertUsageExportTaskSQL inserts one usage_export_tasks row on exec and scans
// its generated ID and timestamps into task.
func insertUsageExportTaskSQL(
	ctx context.Context,
	exec sqlExecutor,
	task *usage.UsageExportTask,
	filtersJSON []byte,
) error {
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
		exec,
		query,
		[]any{task.Scope, task.Status, filtersJSON, task.CreatedBy, task.TotalRows, task.ExportedRows},
		&task.ID,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
}

// usageExportQuotaAdvisoryLockKey hashes scope and createdBy with 64-bit FNV-1a
// so PostgreSQL pg_advisory_xact_lock serializes quota checks per actor without
// truncating 64-bit user identifiers or colliding across user and admin scopes.
func usageExportQuotaAdvisoryLockKey(scope string, createdBy int64) int64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte("usage_export_quota:"))
	_, _ = hasher.Write([]byte(scope))
	_, _ = hasher.Write([]byte(":"))
	var idBuf [20]byte
	_, _ = hasher.Write(strconv.AppendInt(idBuf[:0], createdBy, 10))
	return int64(hasher.Sum64())
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

// listTaskPageWithEnt executes paginated task, active-ID, and watched-task reads
// inside a single read-only snapshot transaction on Ent.
func (r *ExportStore) listTaskPageWithEnt(
	ctx context.Context,
	filter usage.UsageExportListFilter,
	params pagination.PaginationParams,
	watchIDs []int64,
) (*usage.UsageExportTaskListResult, error) {
	if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		return queryExportTaskPageEnt(ctx, existingTx.Client(), filter, params, watchIDs)
	}

	isPostgres := r.client.Driver().Dialect() == dialect.Postgres
	txOpts := &sql.TxOptions{ReadOnly: true}
	if isPostgres {
		// Pin one MVCC snapshot across count, page rows, active IDs, and watched rows
		// so concurrent worker completion cannot appear in active_ids before items.
		txOpts.Isolation = sql.LevelRepeatableRead
	} else {
		r.quotaMu.Lock()
		defer r.quotaMu.Unlock()
	}

	tx, err := r.client.BeginTx(ctx, txOpts)
	if err != nil {
		return nil, err
	}
	res, err := queryExportTaskPageEnt(ctx, tx.Client(), filter, params, watchIDs)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// queryExportTaskPageEnt reads paginated items, active IDs, and off-page watched
// rows from client within the caller's snapshot transaction.
func queryExportTaskPageEnt(
	ctx context.Context,
	client *dbent.Client,
	filter usage.UsageExportListFilter,
	params pagination.PaginationParams,
	watchIDs []int64,
) (*usage.UsageExportTaskListResult, error) {
	baseQuery := client.UsageExportTask.Query().Where(dbusageexporttask.ScopeEQ(filter.Scope))
	if filter.CreatedBy != nil {
		baseQuery = baseQuery.Where(dbusageexporttask.CreatedByEQ(*filter.CreatedBy))
	}

	total, err := baseQuery.Clone().Count(ctx)
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return &usage.UsageExportTaskListResult{
			Items:      []usage.UsageExportTask{},
			Pagination: paginationResultFromTotal(0, params),
			ActiveIDs:  []int64{},
		}, nil
	}

	rows, err := baseQuery.Clone().
		Order(dbent.Desc(dbusageexporttask.FieldCreatedAt), dbent.Desc(dbusageexporttask.FieldID)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]usage.UsageExportTask, 0, len(rows))
	onPage := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		task, convErr := usageExportTaskFromEnt(row)
		if convErr != nil {
			return nil, convErr
		}
		items = append(items, task)
		onPage[task.ID] = struct{}{}
	}

	activeIDs, err := baseQuery.Clone().
		Where(dbusageexporttask.StatusIn(usage.UsageExportStatusPending, usage.UsageExportStatusRunning)).
		Order(dbent.Desc(dbusageexporttask.FieldCreatedAt), dbent.Desc(dbusageexporttask.FieldID)).
		IDs(ctx)
	if err != nil {
		return nil, err
	}
	if activeIDs == nil {
		activeIDs = []int64{}
	}

	offPageWatchIDs := excludeOnPageTaskIDs(watchIDs, onPage)
	var watchedItems []usage.UsageExportTask
	if len(offPageWatchIDs) > 0 {
		watchedRows, watchErr := baseQuery.Clone().
			Where(dbusageexporttask.IDIn(offPageWatchIDs...)).
			Order(dbent.Desc(dbusageexporttask.FieldCreatedAt), dbent.Desc(dbusageexporttask.FieldID)).
			All(ctx)
		if watchErr != nil {
			return nil, watchErr
		}
		watchedItems = make([]usage.UsageExportTask, 0, len(watchedRows))
		for _, row := range watchedRows {
			task, convErr := usageExportTaskFromEnt(row)
			if convErr != nil {
				return nil, convErr
			}
			watchedItems = append(watchedItems, task)
		}
	}

	return &usage.UsageExportTaskListResult{
		Items:        items,
		Pagination:   paginationResultFromTotal(int64(total), params),
		ActiveCount:  int64(len(activeIDs)),
		ActiveIDs:    activeIDs,
		WatchedItems: watchedItems,
	}, nil
}

// listTaskPageWithSQL reads paginated items, active IDs, and off-page watched
// rows on exec for raw-SQL callers.
func listTaskPageWithSQL(
	ctx context.Context,
	exec sqlExecutor,
	filter usage.UsageExportListFilter,
	params pagination.PaginationParams,
	watchIDs []int64,
) (*usage.UsageExportTaskListResult, error) {
	conditions := []string{"scope = $1"}
	args := []any{filter.Scope}
	if filter.CreatedBy != nil {
		conditions = append(conditions, fmt.Sprintf("created_by = $%d", len(args)+1))
		args = append(args, *filter.CreatedBy)
	}
	whereClause := "WHERE " + strings.Join(conditions, " AND ")

	var total int64
	if err := scanSingleRow(ctx, exec, "SELECT COUNT(*) FROM usage_export_tasks "+whereClause, args, &total); err != nil {
		return nil, err
	}
	if total == 0 {
		return &usage.UsageExportTaskListResult{
			Items:      []usage.UsageExportTask{},
			Pagination: paginationResultFromTotal(0, params),
			ActiveIDs:  []int64{},
		}, nil
	}

	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	listArgs := append(append([]any{}, args...), params.Limit(), params.Offset())
	pageQuery := fmt.Sprintf(`
		SELECT id, scope, status, filters, created_by, total_rows, exported_rows,
			file_name, file_path, file_size_bytes, error_message,
			canceled_by, canceled_at, started_at, finished_at, expires_at,
			created_at, updated_at
		FROM usage_export_tasks
		%s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, limitPos, offsetPos)

	pageRows, err := exec.QueryContext(ctx, pageQuery, listArgs...)
	if err != nil {
		return nil, err
	}
	items, err := scanUsageExportTaskSlice(pageRows)
	if err != nil {
		return nil, err
	}

	onPage := make(map[int64]struct{}, len(items))
	for i := range items {
		onPage[items[i].ID] = struct{}{}
	}

	activeArgs := append(append([]any{}, args...), usage.UsageExportStatusPending, usage.UsageExportStatusRunning)
	activeQuery := fmt.Sprintf(
		"SELECT id FROM usage_export_tasks %s AND status IN ($%d, $%d) ORDER BY created_at DESC, id DESC",
		whereClause,
		len(args)+1,
		len(args)+2,
	)
	activeRows, err := exec.QueryContext(ctx, activeQuery, activeArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = activeRows.Close() }()

	activeIDs := make([]int64, 0)
	for activeRows.Next() {
		var id int64
		if scanErr := activeRows.Scan(&id); scanErr != nil {
			return nil, scanErr
		}
		activeIDs = append(activeIDs, id)
	}
	if err := activeRows.Err(); err != nil {
		return nil, err
	}

	offPageWatchIDs := excludeOnPageTaskIDs(watchIDs, onPage)
	var watchedItems []usage.UsageExportTask
	if len(offPageWatchIDs) > 0 {
		watchArgs := append([]any{}, args...)
		placeholders := make([]string, 0, len(offPageWatchIDs))
		for _, id := range offPageWatchIDs {
			watchArgs = append(watchArgs, id)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(watchArgs)))
		}
		watchQuery := fmt.Sprintf(`
			SELECT id, scope, status, filters, created_by, total_rows, exported_rows,
				file_name, file_path, file_size_bytes, error_message,
				canceled_by, canceled_at, started_at, finished_at, expires_at,
				created_at, updated_at
			FROM usage_export_tasks
			%s AND id IN (%s)
			ORDER BY created_at DESC, id DESC
		`, whereClause, strings.Join(placeholders, ", "))
		watchRows, watchErr := exec.QueryContext(ctx, watchQuery, watchArgs...)
		if watchErr != nil {
			return nil, watchErr
		}
		watchedItems, err = scanUsageExportTaskSlice(watchRows)
		if err != nil {
			return nil, err
		}
	}

	return &usage.UsageExportTaskListResult{
		Items:        items,
		Pagination:   paginationResultFromTotal(total, params),
		ActiveCount:  int64(len(activeIDs)),
		ActiveIDs:    activeIDs,
		WatchedItems: watchedItems,
	}, nil
}

// excludeOnPageTaskIDs returns positive watchIDs absent from onPage without duplicates.
func excludeOnPageTaskIDs(watchIDs []int64, onPage map[int64]struct{}) []int64 {
	if len(watchIDs) == 0 {
		return nil
	}
	out := make([]int64, 0, len(watchIDs))
	seen := make(map[int64]struct{}, len(watchIDs))
	for _, id := range watchIDs {
		if id <= 0 {
			continue
		}
		if _, present := onPage[id]; present {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// scanUsageExportTaskSlice decodes all rows from rows and closes rows before returning.
func scanUsageExportTaskSlice(rows *sql.Rows) ([]usage.UsageExportTask, error) {
	defer func() { _ = rows.Close() }()
	tasks := make([]usage.UsageExportTask, 0)
	for rows.Next() {
		task, err := scanUsageExportTaskRow(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tasks, nil
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
