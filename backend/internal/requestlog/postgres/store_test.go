package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/requestlog"
)

// store_test.go checks permanent snapshot classification and the arguments
// shared by sequential detail queries. They isolate transaction rollback,
// unencodable timestamps, administrator versus ordinary lookup, and exact-local
// flags without executing PostgreSQL's lookup function or planner.

// TestSaveClassifiesInvalidRecords injects wrapped PostgreSQL SQLSTATEs through
// mocked upsert execution. Class-22 failures retain their cause and add
// ErrInvalidRecord; other classes remain retryable, with rollback expectations
// satisfied.
func TestSaveClassifiesInvalidRecords(t *testing.T) {
	for _, test := range []struct {
		// code selects the wrapped SQLSTATE and invalid selects whether Save must add
		// ErrInvalidRecord. The cases distinguish data exceptions from connection,
		// constraint, and serialization outcomes.
		code    string // injected PostgreSQL error code
		invalid bool   // expected permanent-content classification
	}{
		{"22P05", true},
		{"22001", true},
		{"08006", false},
		{"23514", false},
		{"40001", false},
	} {
		t.Run(test.code, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			// Close the fixture database after checking expected SQL operations; the
			// deferred close result is suppressed and does not change asserted
			// transaction or lookup outcomes.
			defer func() { _ = db.Close() }()
			failure := fmt.Errorf("write: %w", &pq.Error{Code: pq.ErrorCode(test.code)})
			mock.ExpectBegin()
			mock.ExpectPrepare("INSERT INTO request_records").ExpectExec().WillReturnError(failure)
			mock.ExpectRollback()
			err = NewStore(db).Save(t.Context(), []telemetry.RequestRecord{{RequestID: "id"}})
			require.ErrorIs(t, err, failure)
			if test.invalid {
				require.ErrorIs(t, err, requestlog.ErrInvalidRecord)
			} else {
				require.NotErrorIs(t, err, requestlog.ErrInvalidRecord)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestSaveRejectsUnencodableRecord passes a timestamp beyond JSON's supported
// year range into a mocked transaction. Save reports ErrInvalidRecord and rolls
// back before an upsert execution is expected.
func TestSaveRejectsUnencodableRecord(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close the mock pool after the scenario finishes, suppressing its deferred
	// error while explicit SQL expectations and returned error assertions
	// determine the result.
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectPrepare("INSERT INTO request_records")
	mock.ExpectRollback()
	err = NewStore(db).Save(t.Context(), []telemetry.RequestRecord{{RequestID: "id", StartedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}})
	require.ErrorIs(t, err, requestlog.ErrInvalidRecord)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFindResolvesIDsOnce returns a fixed candidate array for
// administrator/user and exact/external cases. Every subsequent mocked source
// receives the same IDs and its expected flags, with audits queried for
// administrators.
func TestFindResolvesIDsOnce(t *testing.T) {
	for _, test := range []struct {
		// name labels the combination of admin and exact used by each lookup
		// expectation. Admin selects audit attachment, and exact disables
		// source-specific external-ID compatibility matching.
		name  string // lookup policy scenario label
		admin bool   // trusted administrator access flag
		exact bool   // resolved local-match lookup flag
	}{
		{"user external", false, false},
		{"admin external", true, false},
		{"user exact", false, true},
		{"admin exact", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			// Close the fixture database after checking expected SQL operations; the
			// deferred close result is suppressed and does not change asserted
			// transaction or lookup outcomes.
			defer func() { _ = db.Close() }()
			ids := pq.Array([]string{"local-request", "client:legacy"})
			mock.ExpectQuery(`SELECT ARRAY\(SELECT id FROM request_lookup_ids\(\$1\)\)`).
				WithArgs("external-id").WillReturnRows(sqlmock.NewRows([]string{"ids", "exact"}).AddRow(`{"local-request","client:legacy"}`, test.exact))
			mock.ExpectQuery(`SELECT record FROM request_records`).WithArgs(ids, int64(42), test.admin).
				WillReturnRows(sqlmock.NewRows([]string{"record"}))
			mock.ExpectQuery(`WITH selected_usage AS MATERIALIZED`).WithArgs(ids, int64(42), test.admin, "external-id", test.exact).
				WillReturnRows(sqlmock.NewRows([]string{"id"}))
			mock.ExpectQuery(`SELECT .* FROM ops_error_logs`).WithArgs(ids, int64(42), test.admin, test.exact).
				WillReturnRows(sqlmock.NewRows([]string{"id"}))
			if test.admin {
				mock.ExpectQuery(`SELECT .* FROM audit_logs`).WithArgs(ids).
					WillReturnRows(sqlmock.NewRows([]string{"id"}))
			}
			items, err := NewStore(db).Find(t.Context(), "external-id", 42, test.admin)
			require.NoError(t, err)
			require.Empty(t, items)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestFindStopsWhenLookupIsCanceled makes the initial candidate-resolution
// query return context cancellation. Find returns that cause and satisfies the
// mock with no subsequent source query expected.
func TestFindStopsWhenLookupIsCanceled(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close the mock pool after the scenario finishes, suppressing its deferred
	// error while explicit SQL expectations and returned error assertions
	// determine the result.
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`SELECT ARRAY`).WithArgs("id").WillReturnError(context.Canceled)
	_, err = NewStore(db).Find(t.Context(), "id", 42, false)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFindUsagePerformanceUsesOneBatch reads two logical requests through one usage
// query. Each row keeps its own attempt, selected latency and throughput while the
// enclosing request lifecycle remains unchanged and receives no performance copy.
func TestFindUsagePerformanceUsesOneBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { mock.ExpectClose(); require.NoError(t, db.Close()) }()
	p := timing.NewPerformance(timing.FirstEvent, 100)
	p.Stream, p.Complete, p.UsageComplete = true, true, true
	p.Values["upstream_first_event_ms"] = 150
	p.Values["upstream_first_output_ms"] = 300
	p.Values["upstream_last_output_ms"] = 800
	p.Values["upstream_output_event_count"] = 2
	p.Values["attempt_number"] = 2
	first, err := json.Marshal(p)
	require.NoError(t, err)
	p.Mode = timing.FirstOutput
	p.Values["attempt_number"] = 1
	second, err := json.Marshal(p)
	require.NoError(t, err)
	ids := []string{"first", "second"}
	rows := sqlmock.NewRows([]string{"id", "request_id", "created_at", "user_id", "team_id", "api_key_id", "model", "input_tokens", "output_tokens", "actual_cost", "duration_ms", "timings", "request_source", "system_usage", "provider_cost"}).
		AddRow(1, "first", time.Now(), 101, 0, 201, "model", 20, 10, 0.5, 1000, first, "user", nil, 0.5).
		AddRow(2, "second", time.Now(), 101, 0, 201, "model", 20, 10, 0.5, 1000, second, "user", nil, 0.5)
	mock.ExpectQuery("WITH selected_usage AS MATERIALIZED").WithArgs(pq.Array(ids), int64(101), true, "first", true).WillReturnRows(rows)
	result := []requestlog.Detail{
		{RequestRecord: telemetry.RequestRecord{RequestID: "first", DurationMs: 3000}},
		{RequestRecord: telemetry.RequestRecord{RequestID: "second", DurationMs: 4000}},
	}
	require.NoError(t, NewStore(db).findUsage(t.Context(), ids, "first", 101, true, true, &result, map[string]int{"first": 0, "second": 1}))
	require.NoError(t, mock.ExpectationsWereMet())
	require.Equal(t, 50, *result[0].Usage[0].TTFTMs)
	require.Equal(t, 200, *result[1].Usage[0].TTFTMs)
	require.Equal(t, 14.3, *result[0].Usage[0].TPS)
	require.Equal(t, result[0].Usage[0].TPS, result[1].Usage[0].TPS)
	require.Equal(t, int64(2), result[0].Usage[0].Timings.Values["attempt_number"])
	require.Equal(t, int64(1000), *result[0].Usage[0].DurationMs)
	require.Equal(t, int64(3000), result[0].DurationMs)
	require.Nil(t, result[0].FailureTimings)
}

// TestRequestUsageRestrictsProviderCost reads the same customer record as an
// administrator and as its owner. Provider expense must remain administrator-only
// even though both callers can inspect their permitted request and user charge.
func TestRequestUsageRestrictsProviderCost(t *testing.T) {
	for _, admin := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		ids := []string{"cost-visibility"}
		rows := sqlmock.NewRows([]string{"id", "request_id", "created_at", "user_id", "team_id", "api_key_id", "model", "input_tokens", "output_tokens", "actual_cost", "duration_ms", "timings", "request_source", "system_usage", "provider_cost"}).AddRow(1, ids[0], time.Now(), 101, 0, 201, "model", 20, 10, 0.5, 1000, nil, "user", nil, 0.25)
		mock.ExpectQuery("WITH selected_usage AS MATERIALIZED").WithArgs(pq.Array(ids), int64(101), admin, ids[0], true).WillReturnRows(rows)
		result := []requestlog.Detail{{RequestRecord: telemetry.RequestRecord{RequestID: ids[0]}}}
		require.NoError(t, NewStore(db).findUsage(t.Context(), ids, ids[0], 101, admin, true, &result, map[string]int{ids[0]: 0}))
		require.NoError(t, mock.ExpectationsWereMet())
		if admin {
			require.NotNil(t, result[0].Usage[0].ProviderCost)
		} else {
			require.Nil(t, result[0].Usage[0].ProviderCost)
		}
	}
}
