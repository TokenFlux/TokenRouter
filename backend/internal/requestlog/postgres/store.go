package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/requestlog"
)

// store.go persists versioned request snapshots and joins retained diagnostic
// records by request identity.
// Request snapshots are stored by local ID with nanosecond UpdatedAt revisions,
// while retained usage, error, and audit rows may outlive their summary.
// Snapshot upserts retain earlier start and parent identity, combine aliases,
// and keep saved failure or cancellation states when a later update arrives.
//
// Diagnostic lookup resolves candidate IDs once and shares that result with
// each source's ownership-filtered query. Exact local matches disable
// external-collision expansion, while historical rows without an indexed
// summary create Legacy entries using their actual IDs. Source queries run
// sequentially with their own row limits, so returned details are not one
// database snapshot or one globally sorted page. Task rows then update matching
// alias-linked details in place.
// Provider name reads use the IDs selected by the request service and include
// soft-deleted rows because those providers still identify retained attempts.

// findUsageSQL selects visible usage identities before loading their diagnostic
// payloads, preserving one result per stored row across overlapping ID sources.
const findUsageSQL = `WITH selected_usage AS MATERIALIZED (
 SELECT id,created_at FROM (
  SELECT id,created_at,user_id,team_id FROM usage_logs WHERE request_id=ANY($1::text[])
  UNION
  SELECT id,created_at,user_id,team_id FROM usage_logs WHERE NOT $5 AND upstream_request_id=$4
  UNION
  SELECT id,created_at,user_id,team_id FROM usage_logs
   WHERE system_usage IS NOT NULL AND system_usage->>'parent_request_id'=$4
 ) matches
 WHERE ($3 OR user_id=$2 OR team_id IN
  (SELECT team_id FROM team_memberships WHERE user_id=$2 AND role='owner' AND left_at IS NULL))
 ORDER BY created_at DESC LIMIT 101
)
SELECT u.id,COALESCE(u.request_id,''),u.created_at,COALESCE(u.user_id,0),COALESCE(u.team_id,0),COALESCE(u.api_key_id,0),
 COALESCE(u.requested_model,u.model),u.input_tokens,u.output_tokens,u.actual_cost,u.duration_ms,u.timings,u.request_source,u.system_usage,
 CASE WHEN u.system_usage->>'cost_state' = 'unknown' THEN NULL ELSE COALESCE(u.provider_stats_cost,u.total_cost)*COALESCE(u.provider_rate_multiplier,1) END
 FROM selected_usage JOIN usage_logs u ON u.id=selected_usage.id
 ORDER BY selected_usage.created_at DESC`

// Store persists versioned request snapshots and reads retained
// ownership-filtered diagnostic records.
type Store struct {
	// db is the caller-owned SQL pool used for writes and sequential detail
	// queries. Keep it open through Service.Stop and later synchronous
	// observations.
	db *sql.DB
}

// NewStore retains the caller-owned SQL pool for request persistence and
// lookup. The pool must be nonnil, and application shutdown closes it after
// request-record draining has finished.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Save writes all supplied snapshots in one transaction and commits after every
// encoded upsert succeeds. Callers provide normalized records with increasing
// UpdatedAt values; UnixNano supplies the revision so submicrosecond updates
// remain distinguishable in PostgreSQL.
//
// A request's existing start and nonempty parent survive updates, and
// relational ownership keeps previous values when incoming IDs are zero. JSON
// merges aliases and protects saved failed or canceled state, while a running
// update cannot replace a finished state's value. Older or equal revisions are
// ignored. Encoding errors and SQLSTATE class 22 from upsert execution wrap
// ErrInvalidRecord; setup and commit failures propagate. Deferred rollback or
// statement-close errors do not replace the selected result.
func (s *Store) Save(ctx context.Context, records []telemetry.RequestRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Rollback any transaction that exits before commit; suppress the deferred
	// rollback result so the encode, execution, or commit outcome remains
	// authoritative.
	defer func() { _ = tx.Rollback() }()
	// Use nanosecond revision comparison to reject stale updates while SQL retains
	// start and parent identity and merges aliases within the same upsert.
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO request_records
 (request_id,parent_request_id,user_id,team_id,started_at,updated_at,aliases,record,revision)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
 ON CONFLICT(request_id) DO UPDATE SET parent_request_id=COALESCE(NULLIF(request_records.parent_request_id,''),EXCLUDED.parent_request_id),
 user_id=COALESCE(NULLIF(EXCLUDED.user_id,0),request_records.user_id),team_id=COALESCE(NULLIF(EXCLUDED.team_id,0),request_records.team_id),updated_at=EXCLUDED.updated_at,revision=EXCLUDED.revision,
 aliases=ARRAY(SELECT DISTINCT unnest(request_records.aliases || EXCLUDED.aliases)),
 record=(request_records.record || (EXCLUDED.record - 'started_at' - 'aliases')) || jsonb_build_object(
 'started_at', request_records.record->'started_at',
 'parent_request_id', COALESCE(NULLIF(request_records.parent_request_id,''),EXCLUDED.parent_request_id),
 'aliases', (SELECT COALESCE(jsonb_agg(DISTINCT item),'[]'::jsonb) FROM jsonb_array_elements(COALESCE(request_records.record->'aliases','[]'::jsonb) || COALESCE(EXCLUDED.record->'aliases','[]'::jsonb)) item),
 'state', CASE WHEN request_records.record->>'state' IN ('failed','canceled') OR (EXCLUDED.record->>'state'='running' AND request_records.record ? 'finished_at') THEN request_records.record->>'state' ELSE EXCLUDED.record->>'state' END)
 WHERE request_records.revision < EXCLUDED.revision`)
	if err != nil {
		return err
	}
	// Release the transaction statement after all upserts, suppressing close
	// errors so they do not replace the selected transaction result.
	defer func() { _ = stmt.Close() }()
	for _, record := range records {
		body, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("%w: %w", requestlog.ErrInvalidRecord, err)
		}
		// Keep related-request links in JSON for relationship expansion, while
		// excluding them from the external alias array used to match independent
		// identifiers.
		aliases := make([]string, 0, len(record.Aliases))
		for _, alias := range record.Aliases {
			if alias.Kind != "related" {
				aliases = append(aliases, alias.Value)
			}
		}
		if _, err = stmt.ExecContext(ctx, record.RequestID, record.ParentRequestID, record.UserID, record.TeamID,
			record.StartedAt, record.UpdatedAt, pq.Array(aliases), body, record.UpdatedAt.UnixNano()); err != nil {
			// Classify PostgreSQL data exceptions as permanent content errors so
			// Service.Flush can retry records separately and discard only a confirmed
			// invalid version.
			if pgErr, ok := errors.AsType[*pq.Error](err); ok && pgErr.Code.Class() == "22" {
				return fmt.Errorf("%w: %w", requestlog.ErrInvalidRecord, err)
			}
			return err
		}
	}
	// Commit after every upsert succeeded so a failed record cannot leave earlier
	// members of this snapshot batch persisted by this transaction.
	return tx.Commit()
}

// Find resolves id once, then reads accessible summaries and retained
// references using that candidate array. admin is trusted caller authority;
// ordinary callers receive direct or active team-owner summary and usage
// records, while error rows use their direct user ownership.
//
// The exact-local flag disables external upstream or client-ID compatibility
// matches that could widen an exact request query. Summary rows close before
// historical source queries, avoiding a retained cursor while another
// connection is needed. Each source limits its own newest rows to one hundred
// one; historical entries append after summaries and task queries subsequently
// overwrite matching state. Reads run outside a shared transaction, so
// concurrent persistence can change later source observations.
func (s *Store) Find(ctx context.Context, id string, userID int64, admin bool) ([]requestlog.Detail, error) {
	// Resolve candidates and the exact-local match flag once, then pass both through
	// source queries so external collisions cannot widen an exact summary lookup.
	var ids []string
	var exact bool
	if err := s.db.QueryRowContext(ctx, `SELECT ARRAY(SELECT id FROM request_lookup_ids($1)),
 EXISTS(SELECT 1 FROM request_records WHERE request_id=btrim($1))`, id).Scan(pq.Array(&ids), &exact); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM request_records r
 WHERE (r.request_id = ANY($1::text[])
 OR (r.parent_request_id <> '' AND r.parent_request_id = ANY($1::text[])))
 AND ($3 OR (r.user_id > 0 AND (r.user_id=$2 OR r.team_id IN
 (SELECT team_id FROM team_memberships WHERE user_id=$2 AND role='owner' AND left_at IS NULL))))
 ORDER BY r.started_at DESC LIMIT 101`, pq.Array(ids), userID, admin)
	if err != nil {
		return nil, err
	}
	result := make([]requestlog.Detail, 0)
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			// Close the summary cursor before returning its scan or decode error;
			// suppress close errors so the original source failure remains selected.
			_ = rows.Close()
			return nil, err
		}
		var detail requestlog.Detail
		if err = json.Unmarshal(body, &detail.RequestRecord); err != nil {
			// Close the summary cursor before returning its scan or decode error;
			// suppress close errors so the original source failure remains selected.
			_ = rows.Close()
			return nil, err
		}
		result = append(result, detail)
	}
	// Close summary rows before starting the historical queries, releasing their
	// connection while keeping the recorded iteration error as the selected
	// outcome.
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	// Index stored summaries before attaching retained sources by their actual
	// request IDs; missing indexed summaries are appended as inferred Legacy
	// values.
	index := make(map[string]int, len(result))
	for i := range result {
		index[result[i].RequestID] = i
	}
	if err = s.findUsage(ctx, ids, id, userID, admin, exact, &result, index); err != nil {
		return nil, err
	}
	if err = s.findErrors(ctx, ids, userID, admin, exact, &result, index); err != nil {
		return nil, err
	}
	if admin {
		if err = s.findAudit(ctx, ids, &result, index); err != nil {
			return nil, err
		}
	}
	if err = s.findTasks(ctx, result, userID, admin); err != nil {
		return nil, err
	}
	return result, nil
}

// FindProviderNames reads only the configured names for the supplied IDs.
// The caller supplies IDs from administrator-visible diagnostics. Soft-deleted
// providers can still name historical attempts, and absent rows leave no entry
// for their ID.
func (s *Store) FindProviderNames(ctx context.Context, ids []int64) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM providers WHERE id = ANY($1::bigint[])`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	// Release the cursor if scanning fails before exhaustion so later request
	// lookups can reuse the connection.
	defer func() { _ = rows.Close() }()
	names := make(map[int64]string, len(ids))
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}
	return names, rows.Err()
}

// findUsage appends up to one hundred one accessible usage references using the
// resolved candidate IDs. Direct ownership or active team ownership grants
// ordinary access, while admin bypasses that filter and exact lookup suppresses
// the raw upstream-ID compatibility match.
//
// An indexed summary keeps its request outcome when usage is attached. A
// missing indexed summary gets a Legacy value with the usage row's actual
// request ID and usage_recorded state, which does not establish HTTP success.
// Requested model wins when nonnull. ensureDetail leaves its input index
// unchanged, so repeated historical rows without a stored summary can create
// separate details. The parent-run branch explicitly requires system metadata
// to match the partial parent index used for internal-request searches.
//
// Matching branches union narrow rows containing the usage primary key, keeping
// different charges with the same request ID distinct. Ownership filtering and
// the shared newest-row limit precede payload retrieval in one SQL snapshot.
// Materializing that bounded selection prevents the payload join from widening
// the lookup, while sorting after the union keeps a missing parent-index
// statistic from favoring a full time-index scan within a limited branch.
func (s *Store) findUsage(ctx context.Context, ids []string, searchID string, userID int64, admin, exact bool, result *[]requestlog.Detail, index map[string]int) error {
	rows, err := s.db.QueryContext(ctx, findUsageSQL, pq.Array(ids), userID, admin, searchID, exact)
	if err != nil {
		return err
	}
	// Close this source's cursor on success or iteration failure; a deferred close
	// error does not replace the selected query or scan outcome.
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var record telemetry.RequestRecord
		var usage requestlog.Usage
		var timingsJSON []byte
		var usageDuration sql.NullInt64
		var systemJSON []byte
		var providerCost sql.NullFloat64
		if err = rows.Scan(&usage.ID, &record.RequestID, &record.StartedAt, &record.UserID, &record.TeamID, &record.APIKeyID,
			&usage.Model, &usage.InputTokens, &usage.OutputTokens, &usage.ActualCost, &usageDuration, &timingsJSON, &usage.RequestSource, &systemJSON, &providerCost); err != nil {
			return err
		}
		if admin && providerCost.Valid {
			usage.ProviderCost = &providerCost.Float64
		}
		if len(systemJSON) > 0 {
			if err := json.Unmarshal(systemJSON, &usage.SystemUsage); err != nil {
				return err
			}
			if usage.SystemUsage != nil {
				record.ParentRequestID = usage.SystemUsage.ParentRequestID
			}
		}
		if len(timingsJSON) > 0 {
			if err := json.Unmarshal(timingsJSON, &usage.Timings); err != nil {
				return fmt.Errorf("decode request usage timings: %w", err)
			}
		}
		if usageDuration.Valid {
			elapsed := usageDuration.Int64
			usage.DurationMs = &elapsed
		}
		usage.TTFTMs = usage.Timings.TTFTMs()
		usage.TPS = usage.Timings.TPS(int(usage.OutputTokens))
		record.Model = usage.Model
		record.State = "usage_recorded"
		i := ensureDetail(result, index, record)
		(*result)[i].Usage = append((*result)[i].Usage, usage)
	}
	return rows.Err()
}

// findErrors appends newest direct-user or administrator-visible error
// references using the resolved IDs. exact disables compatibility expansion
// through client_request_id, preserving the local query's identified scope.
//
// Rows with a stored indexed summary add references without replacing that
// summary's state. Missing summaries create Legacy failed details using the
// saved request ID or client-ID fallback and the error status. The shared index
// is not extended by ensureDetail, so multiple retained rows for an absent
// summary can append multiple inferred details. Scan and row iteration errors
// propagate.
func (s *Store) findErrors(ctx context.Context, ids []string, userID int64, admin, exact bool, result *[]requestlog.Detail, index map[string]int) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id,COALESCE(NULLIF(request_id,''),client_request_id,''),created_at,
 COALESCE(user_id,0),COALESCE(api_key_id,0),COALESCE(model,''),COALESCE(status_code,0),COALESCE(error_phase,'')
 FROM ops_error_logs WHERE (request_id = ANY($1::text[]) OR (NOT $4 AND client_request_id = ANY($1::text[])))
 AND ($3 OR user_id=$2) ORDER BY created_at DESC LIMIT 101`, pq.Array(ids), userID, admin, exact)
	if err != nil {
		return err
	}
	// Close this source's cursor on success or iteration failure; a deferred close
	// error does not replace the selected query or scan outcome.
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var record telemetry.RequestRecord
		var failure requestlog.Failure
		if err = rows.Scan(&failure.ID, &record.RequestID, &record.StartedAt, &record.UserID, &record.APIKeyID, &record.Model, &failure.Status, &failure.Phase); err != nil {
			return err
		}
		record.State = "failed"
		record.Status = failure.Status
		i := ensureDetail(result, index, record)
		(*result)[i].Errors = append((*result)[i].Errors, failure)
	}
	return rows.Err()
}

// findAudit attaches newest audit references for resolved request IDs and
// requires its caller to establish administrator authority. Find calls it only
// when admin is true; the query itself supplies no per-user ownership filter.
//
// Indexed summaries retain their state and receive the audit ID. Missing
// summaries create Legacy details whose state is inferred from the audit HTTP
// status, with statuses at least four hundred marked failed. ensureDetail does
// not index appended historical values, so repeated absent-summary rows can
// produce separate inferred details. Each query contributes at most one hundred
// one rows.
func (s *Store) findAudit(ctx context.Context, ids []string, result *[]requestlog.Detail, index map[string]int) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id,request_id,created_at,method,path,status_code FROM audit_logs
 WHERE request_id = ANY($1::text[]) ORDER BY created_at DESC LIMIT 101`, pq.Array(ids))
	if err != nil {
		return err
	}
	// Close this source's cursor on success or iteration failure; a deferred close
	// error does not replace the selected query or scan outcome.
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var record telemetry.RequestRecord
		var auditID int64
		if err = rows.Scan(&auditID, &record.RequestID, &record.StartedAt, &record.Method, &record.Path, &record.Status); err != nil {
			return err
		}
		record.State = "completed"
		// Use an unsuccessful audit HTTP status to mark an inferred summary failed;
		// existing indexed request summaries keep their own recorded state.
		if record.Status >= 400 {
			record.State = "failed"
		}
		i := ensureDetail(result, index, record)
		(*result)[i].AuditIDs = append((*result)[i].AuditIDs, auditID)
	}
	return rows.Err()
}

// ensureDetail returns a position already present in index or appends a new
// Legacy detail from record. result must be nonnil, and the supplied index
// identifies persisted summaries; the helper leaves that map unchanged.
//
// Using actual source request IDs keeps unrelated historical requests from
// sharing the searched external alias as their identity. Because appended
// entries are not inserted into index, a later historical row with the same
// absent-summary ID appends another detail. Callers can therefore attach to
// stored summaries but should not infer complete grouping of synthesized
// entries from this helper.
func ensureDetail(result *[]requestlog.Detail, index map[string]int, record telemetry.RequestRecord) int {
	if i, ok := index[record.RequestID]; ok {
		return i
	}
	i := len(*result)
	// Append under the source record's actual identity, leaving index unchanged; a
	// later absent-summary row with this ID will create another detail.
	*result = append(*result, requestlog.Detail{RequestRecord: record, Legacy: true})
	return i
}

// Cleanup deletes summaries with started_at strictly before cutoff in
// ten-thousand-row statements until fewer rows are removed. Statements commit
// independently, so a later error leaves earlier deletions applied; other
// diagnostic tables retain their own lifetimes.
func (s *Store) Cleanup(ctx context.Context, cutoff time.Time) error {
	for {
		// Commit each deletion batch independently so context or storage failure
		// stops subsequent work while earlier summary removals remain applied.
		result, err := s.db.ExecContext(ctx, `DELETE FROM request_records WHERE request_id IN
        (SELECT request_id FROM request_records WHERE started_at<$1 ORDER BY started_at LIMIT 10000)`, cutoff)
		if err != nil {
			return fmt.Errorf("delete expired requests: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		// Stop when this statement removed fewer than its batch limit.
		if count < 10000 {
			return nil
		}
	}
}

// findTasks updates details in place from currently visible creative and
// image-batch tasks referenced by task aliases. Ordinary creative access
// requires direct user ownership; image batches also permit active team owners,
// and admin bypasses each task filter.
//
// Every returned task row updates all detail positions registered for its ID.
// Task states map to completed, failed, canceled, or running; a nonnull finish
// time replaces FinishedAt and derives milliseconds from that detail's
// StartedAt. Missing tasks or null finish times retain existing time fields.
// Creative tasks are read before image batches, so batch observations can
// replace creative observations for a detail linked to both. Within either
// source, multiple task aliases overwrite in returned row order, which the
// queries do not order. Rows close before the next task-source query.
func (s *Store) findTasks(ctx context.Context, details []requestlog.Detail, userID int64, admin bool) error {
	creativeIDs, batchIDs := []string{}, []string{}
	positions := make(map[string][]int)
	for i := range details {
		for _, alias := range details[i].Aliases {
			if alias.Kind != "task" {
				continue
			}
			positions[alias.Value] = append(positions[alias.Value], i)
			if strings.HasPrefix(alias.Value, "crun_") {
				creativeIDs = append(creativeIDs, alias.Value)
			}
			if strings.HasPrefix(alias.Value, "imgbatch_") {
				batchIDs = append(batchIDs, alias.Value)
			}
		}
	}
	queries := []struct {
		// ids and sql pair one task source's candidate list with its
		// ownership-filtered query. Empty IDs skip that query, and returned rows use
		// positions for in-place detail updates.
		ids []string // candidate task identifiers
		sql string   // ownership-filtered task source query
	}{
		{creativeIDs, `SELECT run_id,status,COALESCE(completed_at,cancelled_at) FROM creative_runs WHERE run_id=ANY($1) AND ($3 OR user_id=$2)`},
		{batchIDs, `SELECT batch_id,status,finished_at FROM batch_image_jobs WHERE batch_id=ANY($1) AND ($3 OR user_id=$2 OR team_id IN (SELECT team_id FROM team_memberships WHERE user_id=$2 AND role='owner' AND left_at IS NULL))`},
	}
	for _, query := range queries {
		if len(query.ids) == 0 {
			continue
		}
		rows, err := s.db.QueryContext(ctx, query.sql, pq.Array(query.ids), userID, admin)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, state string
			var finished sql.NullTime
			if err = rows.Scan(&id, &state, &finished); err != nil {
				// Release task rows before returning the scan failure, suppressing close
				// errors so the failing source outcome is still returned to Find.
				_ = rows.Close()
				return err
			}
			// Apply each authorized task observation to all alias-linked positions;
			// multiple task rows can successively replace one detail's state and finish
			// metadata.
			for _, i := range positions[id] {
				// Translate each task domain's terminal labels into the common request
				// outcome strings, leaving unrecognized or active statuses represented as
				// running.
				switch state {
				case "succeeded", "completed", "acked", "output_deleted":
					details[i].State = "completed"
				case "failed", "expired", "result_lost":
					details[i].State = "failed"
				case "cancelled", "canceled":
					details[i].State = "canceled"
				default:
					details[i].State = "running"
				}
				// Derive elapsed milliseconds from this detail's request start only when
				// task storage supplies a finish timestamp; missing finish metadata leaves
				// existing values intact.
				if finished.Valid {
					at := finished.Time
					details[i].FinishedAt = &at
					details[i].DurationMs = at.Sub(details[i].StartedAt).Milliseconds()
				}
			}
		}
		// Release this task cursor before another task-table query begins; retain its
		// iteration error while suppressing a separate close result.
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
