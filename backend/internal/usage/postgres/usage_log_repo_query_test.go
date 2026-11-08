package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usage_log_repo_query_test.go verifies usage query filters and positional row decoding.
//
// Raw usage decoding must keep positional fields aligned while transport
// compatibility and display-model selection interpret older records. SQL-mock
// cases check filter precedence, request-ID lookup, internal-model predicates,
// and personal scopes; typed scanner fixtures exercise nullable metadata without
// relying on driver conversions.

// usageLogScannerStub supplies already typed positional values to raw-row
// decoding tests.
type usageLogScannerStub struct {
	// values retains the test's ordered storage without driver conversion. Each
	// element's type must match Scan's writable destination before reflection
	// assigns it.
	values []any
}

// TestCoalesceTrimmedString supplies null, whitespace-only, and populated
// display strings. It checks missing/blank text uses the fallback and a valid
// populated value reaches the result.
func TestCoalesceTrimmedString(t *testing.T) {
	require.Equal(t, "fallback", coalesceTrimmedString(sql.NullString{}, "fallback"))
	require.Equal(t, "fallback", coalesceTrimmedString(sql.NullString{Valid: true, String: "   "}, "fallback"))
	require.Equal(t, "value", coalesceTrimmedString(sql.NullString{Valid: true, String: "value"}, "fallback"))
}

// TestUsageLogRepositoryListWithFiltersRequestTypePriority supplies an explicit
// WebSocket type with conflicting stream selection. It checks counted and listed
// SQL use request-type compatibility without an extra stream argument.
func TestUsageLogRepositoryListWithFiltersRequestTypePriority(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &Store{sql: db}

	requestType := int16(usage.RequestTypeWSV2)
	stream := false
	filters := usage.UsageLogFilters{
		RequestType: &requestType,
		Stream:      &stream,
		ExactTotal:  true,
	}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM usage_logs WHERE \\(request_type = \\$1 OR \\(request_type = 0 AND openai_ws_mode = TRUE\\)\\)").
		WithArgs(requestType).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery("SELECT .* FROM usage_logs WHERE \\(request_type = \\$1 OR \\(request_type = 0 AND openai_ws_mode = TRUE\\)\\) ORDER BY id DESC LIMIT \\$2 OFFSET \\$3").
		WithArgs(requestType, 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	logs, page, err := repo.ListWithFilters(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 20}, filters)
	require.NoError(t, err)
	require.Empty(t, logs)
	require.NotNil(t, page)
	require.Equal(t, int64(0), page.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUsageLogRepositoryListWithFiltersRequestID supplies padded request
// identity in a broad listing. It checks trimmed lookup input, the
// exact-local/upstream predicate, and fast pagination's extra-row limit.
func TestUsageLogRepositoryListWithFiltersRequestID(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &Store{sql: db}

	filters := usage.UsageLogFilters{RequestID: " req-0123 "}

	mock.ExpectQuery("SELECT .* FROM usage_logs WHERE "+regexp.QuoteMeta("(request_id = ANY(ARRAY(SELECT id FROM request_lookup_ids($1))) OR (upstream_request_id = $1 AND NOT EXISTS (SELECT 1 FROM request_records exact_request WHERE exact_request.request_id=$1)) OR (system_usage IS NOT NULL AND system_usage->>'parent_request_id' = $1)) ORDER BY id DESC LIMIT $2 OFFSET $3")).
		WithArgs("req-0123", 21, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	logs, page, err := repo.ListWithFilters(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 20}, filters)
	require.NoError(t, err)
	require.Empty(t, logs)
	require.NotNil(t, page)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUsageLogRepositoryListWithFiltersInternalModelSource selects
// requested/internal model filtering. It checks the generated compatibility
// expression and bound model value on the fast listing query.
func TestUsageLogRepositoryListWithFiltersInternalModelSource(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &Store{sql: db}

	filters := usage.UsageLogFilters{
		Model:             "gpt-5",
		ModelFilterSource: usage.ModelSourceRequested,
	}

	mock.ExpectQuery("SELECT .* FROM usage_logs WHERE COALESCE\\(NULLIF\\(TRIM\\(model\\), ''\\), NULLIF\\(TRIM\\(requested_model\\), ''\\), ''\\) = \\$1 ORDER BY id DESC LIMIT \\$2 OFFSET \\$3").
		WithArgs("gpt-5", 21, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	logs, page, err := repo.ListWithFilters(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 20}, filters)
	require.NoError(t, err)
	require.Empty(t, logs)
	require.NotNil(t, page)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUsageLogRepositoryListPersonalOnlyExcludesTeamUsage selects one actor with
// personal-only scope. It checks both count and list predicates require a null
// team ID and decode an empty page.
func TestUsageLogRepositoryListPersonalOnlyExcludesTeamUsage(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &Store{sql: db}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM usage_logs WHERE user_id = \\$1 AND team_id IS NULL").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery("SELECT .* FROM usage_logs WHERE user_id = \\$1 AND team_id IS NULL ORDER BY id DESC LIMIT \\$2 OFFSET \\$3").
		WithArgs(int64(7), 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	logs, page, err := repo.ListWithFilters(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 20}, usage.UsageLogFilters{
		UserID:       7,
		PersonalOnly: true,
	})
	require.NoError(t, err)
	require.Empty(t, logs)
	require.Zero(t, page.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestBuildUsageLogScopeSource supplies a current team owner and existing SQL
// argument. It checks disjoint actor/team branches, alias placement, empty
// external predicate, and appended parameter order.
func TestBuildUsageLogScopeSource(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &Store{sql: db}
	mock.ExpectQuery("(?s)SELECT \\(.*FROM team_memberships.*tm.user_id = \\$1.*tm.role = 'owner'").
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(int64(99)))

	source, condition, args, err := repo.buildUsageLogScopeSource(context.Background(), []any{"existing"}, 42, true, "ul")
	require.NoError(t, err)
	require.Empty(t, condition)
	require.Contains(t, source, "SELECT * FROM usage_logs WHERE user_id = $2")
	require.Contains(t, source, "SELECT * FROM usage_logs WHERE team_id = $3 AND user_id <> $2")
	require.Contains(t, source, ") ul")
	require.Equal(t, []any{"existing", int64(42), int64(99)}, args)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestScanUsageLogRequestTypeAndLegacyFallback provides typed rows with image
// metadata, service tiers, explicit transport, and legacy flags. It checks
// decoded media/tier fields and request-type/legacy normalization for each
// fixture.
func TestScanUsageLogRequestTypeAndLegacyFallback(t *testing.T) {
	t.Run("image_size_metadata_is_scanned", func(t *testing.T) {
		now := time.Now().UTC()
		log, err := scanUsageLog(usageLogScannerStub{values: []any{
			int64(4),
			int64(13),
			int64(13),
			sql.NullInt64{},
			int64(23),
			int64(33),
			sql.NullString{Valid: true, String: "req-image-metadata"},
			"gpt-image-2",
			sql.NullString{Valid: true, String: "gpt-image-2"},
			sql.NullString{},
			sql.NullInt64{},
			sql.NullInt64{},
			0, 0, 0, 0, 0, 0,
			0, 0.0, // Image output tokens and cost
			0, 0.0, // Image input tokens and cost
			0.0, 0.0, 0.0, 0.0, 0.8, 0.8,
			0.0, 0.0, []byte("[]"),
			1.0,
			sql.NullFloat64{},
			int16(usage.BillingTypeBalance),
			int16(usage.RequestTypeSync),
			false,
			false,
			sql.NullInt64{},
			[]byte(nil),
			sql.NullString{},
			sql.NullString{},
			2,
			0, 0, // Animation frames and explicit image-cache tokens.
			sql.NullString{Valid: true, String: "4K"},
			sql.NullString{Valid: true, String: "1024x1024"},
			sql.NullString{Valid: true, String: "3840x2160"},
			sql.NullString{Valid: true, String: "output"},
			sql.NullString{Valid: true, String: `{"4K":2}`},
			0,                // video_count
			sql.NullString{}, // video_resolution
			sql.NullInt64{},  // video_duration_seconds
			sql.NullString{},
			sql.NullString{},
			sql.NullString{},
			sql.NullString{},
			false,
			false,
			sql.NullInt64{},
			sql.NullString{},
			sql.NullString{},
			sql.NullString{},
			sql.NullFloat64{},
			sql.NullString{}, // upstream_request_id
			sql.NullString{},
			now,
			sql.NullString{}, // requested_reasoning_effort
			false,            // native_compaction_v2
			"unknown",        // platform snapshot
			sql.NullString{},
			sql.NullBool{},
			"user", []byte(nil),
		}})
		require.NoError(t, err)
		require.Equal(t, 2, log.ImageCount)
		require.NotNil(t, log.ImageSize)
		require.Equal(t, "4K", *log.ImageSize)
		require.NotNil(t, log.ImageInputSize)
		require.Equal(t, "1024x1024", *log.ImageInputSize)
		require.NotNil(t, log.ImageOutputSize)
		require.Equal(t, "3840x2160", *log.ImageOutputSize)
		require.NotNil(t, log.ImageSizeSource)
		require.Equal(t, "output", *log.ImageSizeSource)
		require.Equal(t, map[string]int{"4K": 2}, log.ImageSizeBreakdown)
	})

	t.Run("request_type_ws_v2_overrides_legacy", func(t *testing.T) {
		now := time.Now().UTC()
		log, err := scanUsageLog(usageLogScannerStub{values: []any{
			int64(1),        // id
			int64(10),       // user_id
			int64(10),       // billing_user_id
			sql.NullInt64{}, // team_id
			int64(20),       // api_key_id
			int64(30),       // provider_id
			sql.NullString{Valid: true, String: "req-1"},
			"gpt-5", // model
			sql.NullString{Valid: true, String: "gpt-5"}, // requested_model
			sql.NullString{},  // upstream_model
			sql.NullInt64{},   // group_id
			sql.NullInt64{},   // subscription_id
			1,                 // input_tokens
			2,                 // output_tokens
			3,                 // cache_creation_tokens
			4,                 // cache_read_tokens
			5,                 // cache_creation_5m_tokens
			6,                 // cache_creation_1h_tokens
			0,                 // image_output_tokens
			0.0,               // image_output_cost
			0,                 // image_input_tokens
			0.0,               // image_input_cost
			0.1,               // input_cost
			0.2,               // output_cost
			0.3,               // cache_creation_cost
			0.4,               // cache_read_cost
			1.0,               // total_cost
			0.9,               // actual_cost
			0.0,               // subscription_amount_usd
			0.0,               // balance_amount_usd
			[]byte("[]"),      // billing_allocations
			1.0,               // rate_multiplier
			sql.NullFloat64{}, // provider_rate_multiplier
			int16(usage.BillingTypeBalance),
			int16(usage.RequestTypeWSV2),
			false, // legacy stream
			false, // legacy openai ws
			sql.NullInt64{},
			[]byte(nil),
			sql.NullString{},
			sql.NullString{},
			0,
			0, 0, // Animation frames and explicit image-cache tokens.
			sql.NullString{},
			sql.NullString{}, // image_input_size
			sql.NullString{}, // image_output_size
			sql.NullString{}, // image_size_source
			sql.NullString{}, // image_size_breakdown
			0,                // video_count
			sql.NullString{}, // video_resolution
			sql.NullInt64{},  // video_duration_seconds
			sql.NullString{Valid: true, String: "priority"},
			sql.NullString{},
			sql.NullString{},
			sql.NullString{},
			false,
			false,
			sql.NullInt64{},   // pricing_config_id
			sql.NullString{},  // model_mapping_chain
			sql.NullString{},  // billing_tier
			sql.NullString{},  // billing_mode
			sql.NullFloat64{}, // provider_stats_cost
			sql.NullString{},  // upstream_request_id
			sql.NullString{},  // session_id
			now,
			sql.NullString{}, // requested_reasoning_effort
			false,            // native_compaction_v2
			"unknown",        // platform snapshot
			sql.NullString{},
			sql.NullBool{},
			"user", []byte(nil),
		}})
		require.NoError(t, err)
		require.NotNil(t, log.ServiceTier)
		require.Equal(t, "priority", *log.ServiceTier)
		require.Equal(t, usage.RequestTypeWSV2, log.RequestType)
		require.True(t, log.Stream)
		require.True(t, log.OpenAIWSMode)
	})

	t.Run("request_type_unknown_falls_back_to_legacy", func(t *testing.T) {
		now := time.Now().UTC()
		log, err := scanUsageLog(usageLogScannerStub{values: []any{
			int64(2),
			int64(11),
			int64(11),
			sql.NullInt64{},
			int64(21),
			int64(31),
			sql.NullString{Valid: true, String: "req-2"},
			"gpt-5",
			sql.NullString{Valid: true, String: "gpt-5"},
			sql.NullString{},
			sql.NullInt64{},
			sql.NullInt64{},
			1, 2, 3, 4, 5, 6,
			0, 0.0, // Image output tokens and cost
			0, 0.0, // Image input tokens and cost
			0.1, 0.2, 0.3, 0.4, 1.0, 0.9,
			0.0, 0.0, []byte("[]"),
			1.0,
			sql.NullFloat64{},
			int16(usage.BillingTypeBalance),
			int16(usage.RequestTypeUnknown),
			true,
			false,
			sql.NullInt64{},
			[]byte(nil),
			sql.NullString{},
			sql.NullString{},
			0,
			0, 0, // Animation frames and explicit image-cache tokens.
			sql.NullString{},
			sql.NullString{}, // image_input_size
			sql.NullString{}, // image_output_size
			sql.NullString{}, // image_size_source
			sql.NullString{}, // image_size_breakdown
			0,                // video_count
			sql.NullString{}, // video_resolution
			sql.NullInt64{},  // video_duration_seconds
			sql.NullString{Valid: true, String: "flex"},
			sql.NullString{},
			sql.NullString{},
			sql.NullString{},
			false,
			false,
			sql.NullInt64{},   // pricing_config_id
			sql.NullString{},  // model_mapping_chain
			sql.NullString{},  // billing_tier
			sql.NullString{},  // billing_mode
			sql.NullFloat64{}, // provider_stats_cost
			sql.NullString{},  // upstream_request_id
			sql.NullString{},  // session_id
			now,
			sql.NullString{}, // requested_reasoning_effort
			false,            // native_compaction_v2
			"unknown",        // platform snapshot
			sql.NullString{},
			sql.NullBool{},
			"user", []byte(nil),
		}})
		require.NoError(t, err)
		require.NotNil(t, log.ServiceTier)
		require.Equal(t, "flex", *log.ServiceTier)
		require.Equal(t, usage.RequestTypeStream, log.RequestType)
		require.True(t, log.Stream)
		require.False(t, log.OpenAIWSMode)
	})

	t.Run("service_tier_is_scanned", func(t *testing.T) {
		now := time.Now().UTC()
		log, err := scanUsageLog(usageLogScannerStub{values: []any{
			int64(3),
			int64(12),
			int64(12),
			sql.NullInt64{},
			int64(22),
			int64(32),
			sql.NullString{Valid: true, String: "req-3"},
			"gpt-5.4",
			sql.NullString{Valid: true, String: "gpt-5.4"},
			sql.NullString{},
			sql.NullInt64{},
			sql.NullInt64{},
			1, 2, 3, 4, 5, 6,
			0, 0.0, // Image output tokens and cost
			0, 0.0, // Image input tokens and cost
			0.1, 0.2, 0.3, 0.4, 1.0, 0.9,
			0.0, 0.0, []byte("[]"),
			1.0,
			sql.NullFloat64{},
			int16(usage.BillingTypeBalance),
			int16(usage.RequestTypeSync),
			false,
			false,
			sql.NullInt64{},
			[]byte(nil),
			sql.NullString{},
			sql.NullString{},
			0,
			0, 0, // Animation frames and explicit image-cache tokens.
			sql.NullString{},
			sql.NullString{}, // image_input_size
			sql.NullString{}, // image_output_size
			sql.NullString{}, // image_size_source
			sql.NullString{}, // image_size_breakdown
			0,                // video_count
			sql.NullString{}, // video_resolution
			sql.NullInt64{},  // video_duration_seconds
			sql.NullString{Valid: true, String: "priority"},
			sql.NullString{},
			sql.NullString{},
			sql.NullString{},
			false,
			false,
			sql.NullInt64{},   // pricing_config_id
			sql.NullString{},  // model_mapping_chain
			sql.NullString{},  // billing_tier
			sql.NullString{},  // billing_mode
			sql.NullFloat64{}, // provider_stats_cost
			sql.NullString{},  // upstream_request_id
			sql.NullString{},  // session_id
			now,
			sql.NullString{}, // requested_reasoning_effort
			false,            // native_compaction_v2
			"unknown",        // platform snapshot
			sql.NullString{},
			sql.NullBool{},
			"user", []byte(nil),
		}})
		require.NoError(t, err)
		require.NotNil(t, log.ServiceTier)
		require.Equal(t, "priority", *log.ServiceTier)
	})
}

// Scan assigns already typed fixture values into the positional destinations.
// Count mismatch or a nonpointer destination returns an error; callers supply
// writable nonnil pointers with matching types, because reflection can panic for
// unusable destinations.
func (s usageLogScannerStub) Scan(dest ...any) error {
	if len(dest) != len(s.values) {
		return fmt.Errorf("scan arg count mismatch: got %d want %d", len(dest), len(s.values))
	}
	for i := range dest {
		dv := reflect.ValueOf(dest[i])
		if dv.Kind() != reflect.Pointer {
			return fmt.Errorf("dest[%d] is not pointer", i)
		}
		// The fixture already carries destination-compatible types. Reflection assigns
		// them directly, so this stub exercises column wiring rather than SQL
		// conversions.
		if reflect.TypeOf(s.values[i]) == dv.Elem().Type() {
			dv.Elem().Set(reflect.ValueOf(s.values[i]))
			continue
		}
		if scanner, ok := dest[i].(sql.Scanner); ok {
			if err := scanner.Scan(s.values[i]); err != nil {
				return err
			}
			continue
		}
		dv.Elem().Set(reflect.ValueOf(s.values[i]))
	}
	return nil
}
