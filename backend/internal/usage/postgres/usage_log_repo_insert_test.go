package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usage_log_repo_insert_test.go verifies usage insert columns, values, and retry behavior.
//
// Usage inserts share a positional schema across single-row, result-aware batch,
// and best-effort SQL. Static shape checks inspect columns, placeholder
// numbering, casts, and bound values, while SQL mocks exercise deadlock recovery
// and verify that diagnostic fields reach their intended persistence positions.

// Best-effort SQL patterns distinguish batch execution from its individual
// recovery statement, allowing mock expectations to prove which recovery path
// ran.
const (
	usageLogBestEffortBatchSQL  = `(?s)^\s*WITH input .*INSERT INTO usage_logs`
	usageLogBestEffortSingleSQL = `(?s)^\s*INSERT INTO usage_logs`
)

// Insert-shape patterns extract static column/value lists and numbered
// placeholders so schema additions can be checked against positional type
// metadata.
var (
	usageLogStaticInsertShapeRe = regexp.MustCompile(`(?s)INSERT INTO usage_logs \((.*?)\) VALUES \((.*?)\)`)
	usageLogPlaceholderRe       = regexp.MustCompile(`\$(\d+)`)
)

// TestFlushBestEffortBatch_RetriesDeadlockBeforeFallback injects two batch
// deadlocks followed by success. It checks the third batch attempt completes the
// acknowledgement without an individual fallback expectation.
func TestFlushBestEffortBatch_RetriesDeadlockBeforeFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		// Release the mock database after the flush assertion or an early test exit;
		// this cleanup ignores Close errors and leaves SQL expectations to the test.
		_ = db.Close()
	}()

	for attempt := 1; attempt <= 3; attempt++ {
		expectation := mock.ExpectExec(usageLogBestEffortBatchSQL)
		if attempt < 3 {
			expectation.WillReturnError(&pq.Error{Code: "40P01"})
			continue
		}
		expectation.WillReturnResult(sqlmock.NewResult(0, 1))
	}

	req := newUsageLogBestEffortRequestForTest()
	repo := &Store{}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req})

	require.NoError(t, <-req.resultCh)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFlushBestEffortBatch_NonDeadlockUsesSingleFallbackImmediately injects a
// non-deadlock batch failure. It checks one individual insert follows
// immediately and the waiting request receives a successful acknowledgement.
func TestFlushBestEffortBatch_NonDeadlockUsesSingleFallbackImmediately(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		// Release the mock database after the flush assertion or an early test exit;
		// this cleanup ignores Close errors and leaves SQL expectations to the test.
		_ = db.Close()
	}()

	mock.ExpectExec(usageLogBestEffortBatchSQL).WillReturnError(errors.New("batch unavailable"))
	mock.ExpectExec(usageLogBestEffortSingleSQL).WillReturnResult(sqlmock.NewResult(0, 1))

	req := newUsageLogBestEffortRequestForTest()
	repo := &Store{}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req})

	require.NoError(t, <-req.resultCh)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFlushBestEffortBatch_DeadlockRetryExhaustedUsesSingleFallback injects
// failure on all three permitted deadlock attempts. It checks the subsequent
// individual insert succeeds and the request acknowledgement is successful.
func TestFlushBestEffortBatch_DeadlockRetryExhaustedUsesSingleFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		// Release the mock database after the flush assertion or an early test exit;
		// this cleanup ignores Close errors and leaves SQL expectations to the test.
		_ = db.Close()
	}()

	for attempt := 1; attempt <= 3; attempt++ {
		mock.ExpectExec(usageLogBestEffortBatchSQL).WillReturnError(&pq.Error{Code: "40P01"})
	}
	mock.ExpectExec(usageLogBestEffortSingleSQL).WillReturnResult(sqlmock.NewResult(0, 1))

	req := newUsageLogBestEffortRequestForTest()
	repo := &Store{}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req})

	require.NoError(t, <-req.resultCh)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUsageLogStaticInsertShape_PlaceholdersMatchArgTypes captures the two
// single-row insert shapes with populated diagnostic input. It checks bound
// arguments and matching column/placeholder counts against the shared type
// table.
func TestUsageLogStaticInsertShape_PlaceholdersMatchArgTypes(t *testing.T) {
	upstreamRequestID := "20260902080329-oneapi"
	log := &usage.UsageLog{
		UserID:            1,
		APIKeyID:          2,
		ProviderID:        3,
		RequestID:         "client:insert-shape",
		UpstreamRequestID: &upstreamRequestID,
		Model:             "claude-3",
		InputTokens:       10,
		CreatedAt:         time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
	}
	prepared := prepareUsageLogInsert(log)
	args := anySliceToDriverValues(prepared.args)

	t.Run("createSingle", func(t *testing.T) {
		var captured []string
		db, mock := newSQLCapturingMock(t, &captured)
		repo := &Store{sql: db}

		mock.ExpectQuery("INSERT INTO usage_logs").
			WithArgs(args...).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(1), log.CreatedAt))

		inserted, err := repo.Create(context.Background(), log)
		require.NoError(t, err)
		require.True(t, inserted)
		require.NoError(t, mock.ExpectationsWereMet())
		require.Len(t, captured, 1)
		requireStaticInsertMatchesArgTypes(t, captured[0])
	})

	t.Run("execUsageLogInsertNoResult", func(t *testing.T) {
		var captured []string
		db, mock := newSQLCapturingMock(t, &captured)

		mock.ExpectExec("INSERT INTO usage_logs").
			WithArgs(args...).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, execUsageLogInsertNoResult(context.Background(), db, prepared))
		require.NoError(t, mock.ExpectationsWereMet())
		require.Len(t, captured, 1)
		requireStaticInsertMatchesArgTypes(t, captured[0])
	})
}

// TestPrepareUsageLogInsert_UpstreamRequestIDArgWiring prepares rows with and
// without upstream identity. It checks the positional nullable argument, text
// type metadata, and upstream column presence in the read shape.
func TestPrepareUsageLogInsert_UpstreamRequestIDArgWiring(t *testing.T) {
	upstreamRequestID := "req_upstream_123"
	prepared := prepareUsageLogInsert(&usage.UsageLog{
		UserID:            1,
		APIKeyID:          2,
		RequestID:         "client:wiring",
		Model:             "gpt-5",
		UpstreamRequestID: &upstreamRequestID,
		CreatedAt:         time.Now().UTC(),
	})
	require.Len(t, prepared.args, len(usageLogInsert.argTypes))

	idx := len(prepared.args) - 11
	arg, ok := prepared.args[idx].(sql.NullString)
	require.True(t, ok, "upstream_request_id arg should be sql.NullString, got %T", prepared.args[idx])
	require.True(t, arg.Valid)
	require.Equal(t, upstreamRequestID, arg.String)
	require.Equal(t, "text", usageLogInsert.argTypes[idx])

	absent := prepareUsageLogInsert(&usage.UsageLog{UserID: 1, APIKeyID: 2, RequestID: "client:absent", Model: "gpt-5", CreatedAt: time.Now().UTC()})
	nullArg, ok := absent.args[idx].(sql.NullString)
	require.True(t, ok)
	require.False(t, nullArg.Valid, "absent upstream request id must be NULL")

	require.Contains(t, usageLogSelectColumns, "upstream_request_id")
}

// TestUsageLogRepositoryCreateSyncRequestTypeAndLegacyFields supplies an
// explicit WebSocket type with false legacy flags. It checks normalized SQL
// arguments, caller row flags, assigned ID, and absent service tier.
func TestUsageLogRepositoryCreateSyncRequestTypeAndLegacyFields(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &Store{sql: db}

	createdAt := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	log := &usage.UsageLog{
		UserID:         1,
		APIKeyID:       2,
		ProviderID:     3,
		RequestID:      "req-1",
		Model:          "gpt-5",
		RequestedModel: "gpt-5",
		InputTokens:    10,
		OutputTokens:   20,
		TotalCost:      1,
		ActualCost:     1,
		BillingType:    usage.BillingTypeBalance,
		RequestType:    usage.RequestTypeWSV2,
		Stream:         false,
		OpenAIWSMode:   false,
		CreatedAt:      createdAt,
	}

	mock.ExpectQuery("INSERT INTO usage_logs").
		WithArgs(
			log.UserID,
			log.UserID,       // billing_user_id defaults to the calling user
			sqlmock.AnyArg(), // team_id
			log.APIKeyID,
			log.ProviderID,
			log.RequestID,
			log.Model,
			log.RequestedModel,
			sqlmock.AnyArg(), // upstream_model
			sqlmock.AnyArg(), // group_id
			sqlmock.AnyArg(), // subscription_id
			log.InputTokens,
			log.OutputTokens,
			log.CacheCreationTokens,
			log.CacheReadTokens,
			log.CacheCreation5mTokens,
			log.CacheCreation1hTokens,
			log.ImageOutputTokens,
			log.ImageOutputCost,
			log.ImageInputTokens,
			log.ImageInputCost,
			log.InputCost,
			log.OutputCost,
			log.CacheCreationCost,
			log.CacheReadCost,
			log.TotalCost,
			log.ActualCost,
			log.SubscriptionAmountUSD,
			log.BalanceAmountUSD,
			sqlmock.AnyArg(), // billing_allocations
			log.RateMultiplier,
			log.ProviderRateMultiplier,
			log.BillingType,
			int16(usage.RequestTypeWSV2),
			true,
			true,
			sqlmock.AnyArg(), // duration_ms
			sqlmock.AnyArg(), // timings JSON
			sqlmock.AnyArg(), // user_agent
			sqlmock.AnyArg(), // ip_address
			log.ImageCount,
			log.ImageFrameCount,
			log.ImageCacheReadTokens,
			sqlmock.AnyArg(), // image_size
			sqlmock.AnyArg(), // image_input_size
			sqlmock.AnyArg(), // image_output_size
			sqlmock.AnyArg(), // image_size_source
			sqlmock.AnyArg(), // image_size_breakdown
			sqlmock.AnyArg(), // video_count
			sqlmock.AnyArg(), // video_resolution
			sqlmock.AnyArg(), // video_duration_seconds
			sqlmock.AnyArg(), // service_tier
			sqlmock.AnyArg(), // reasoning_effort
			sqlmock.AnyArg(), // inbound_endpoint
			sqlmock.AnyArg(), // upstream_endpoint
			log.CacheTTLOverridden,
			log.LongContextBillingApplied,
			sqlmock.AnyArg(), // pricing_config_id
			sqlmock.AnyArg(), // model_mapping_chain
			sqlmock.AnyArg(), // billing_tier
			sqlmock.AnyArg(), // billing_mode
			sqlmock.AnyArg(), // provider_stats_cost
			sqlmock.AnyArg(), // upstream_request_id
			sqlmock.AnyArg(), // session_id
			createdAt,
			sqlmock.AnyArg(), // requested_reasoning_effort
			false,            // native_compaction_v2
			"unknown",        // platform snapshot
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			nil, // billing_key
			"user",
			nil, // no system execution evidence
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(99), createdAt))

	inserted, err := repo.Create(context.Background(), log)
	require.NoError(t, err)
	require.True(t, inserted)
	require.Equal(t, int64(99), log.ID)
	require.Nil(t, log.ServiceTier)
	require.Equal(t, usage.RequestTypeWSV2, log.RequestType)
	require.True(t, log.Stream)
	require.True(t, log.OpenAIWSMode)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUsageLogRepositoryCreate_PersistsServiceTier supplies a priority service
// tier to a direct mocked write. It checks the bound tier appears in the full
// argument order and insertion succeeds.
func TestUsageLogRepositoryCreate_PersistsServiceTier(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &Store{sql: db}

	createdAt := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	serviceTier := "priority"
	log := &usage.UsageLog{
		UserID:         1,
		APIKeyID:       2,
		ProviderID:     3,
		RequestID:      "req-service-tier",
		Model:          "gpt-5.4",
		RequestedModel: "gpt-5.4",
		ServiceTier:    &serviceTier,
		CreatedAt:      createdAt,
	}

	mock.ExpectQuery("INSERT INTO usage_logs").
		WithArgs(
			log.UserID,
			log.UserID,       // billing_user_id defaults to the calling user
			sqlmock.AnyArg(), // team_id
			log.APIKeyID,
			log.ProviderID,
			log.RequestID,
			log.Model,
			log.RequestedModel,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			log.InputTokens,
			log.OutputTokens,
			log.CacheCreationTokens,
			log.CacheReadTokens,
			log.CacheCreation5mTokens,
			log.CacheCreation1hTokens,
			log.ImageOutputTokens,
			log.ImageOutputCost,
			log.ImageInputTokens,
			log.ImageInputCost,
			log.InputCost,
			log.OutputCost,
			log.CacheCreationCost,
			log.CacheReadCost,
			log.TotalCost,
			log.ActualCost,
			log.SubscriptionAmountUSD,
			log.BalanceAmountUSD,
			sqlmock.AnyArg(), // billing_allocations
			log.RateMultiplier,
			log.ProviderRateMultiplier,
			log.BillingType,
			int16(usage.RequestTypeSync),
			false,
			false,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			log.ImageCount,
			log.ImageFrameCount,
			log.ImageCacheReadTokens,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(), // image_input_size
			sqlmock.AnyArg(), // image_output_size
			sqlmock.AnyArg(), // image_size_source
			sqlmock.AnyArg(), // image_size_breakdown
			sqlmock.AnyArg(), // video_count
			sqlmock.AnyArg(), // video_resolution
			sqlmock.AnyArg(), // video_duration_seconds
			serviceTier,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			log.CacheTTLOverridden,
			log.LongContextBillingApplied,
			sqlmock.AnyArg(), // pricing_config_id
			sqlmock.AnyArg(), // model_mapping_chain
			sqlmock.AnyArg(), // billing_tier
			sqlmock.AnyArg(), // billing_mode
			sqlmock.AnyArg(), // provider_stats_cost
			sqlmock.AnyArg(), // upstream_request_id
			sqlmock.AnyArg(), // session_id
			createdAt,
			sqlmock.AnyArg(), // requested_reasoning_effort
			false,            // native_compaction_v2
			"unknown",        // platform snapshot
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			nil, // billing_key
			"user",
			nil, // no system execution evidence
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(100), createdAt))

	inserted, err := repo.Create(context.Background(), log)
	require.NoError(t, err)
	require.True(t, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestBuildUsageLogBestEffortInsertQuery_IncludesRequestedModelColumn builds one
// best-effort payload with a requested display model. It checks model-column
// ordering, argument count, and the retained request-ID parameter.
func TestBuildUsageLogBestEffortInsertQuery_IncludesRequestedModelColumn(t *testing.T) {
	prepared := prepareUsageLogInsert(&usage.UsageLog{
		UserID:         1,
		APIKeyID:       2,
		ProviderID:     3,
		RequestID:      "req-best-effort-query",
		Model:          "gpt-5",
		RequestedModel: "gpt-5",
		CreatedAt:      time.Date(2025, 1, 3, 12, 0, 0, 0, time.UTC),
	})

	query, args := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})

	require.Contains(t, query, "INSERT INTO usage_logs (")
	require.Contains(t, query, "\n\t\t\tmodel,\n\t\t\trequested_model,\n\t\t\tupstream_model,")
	require.Contains(t, query, "\n\t\t\trequest_id,\n\t\t\tmodel,\n\t\t\trequested_model,\n\t\t\tupstream_model,")
	require.Len(t, args, len(prepared.args))
	require.Equal(t, prepared.args[5], args[5])
}

// TestUsageLogBatchInsertShape_ColumnsAndArguments builds two-row result-aware
// and best-effort SQL. It checks column alignment, ordered arguments, synthetic
// indexes, placeholder numbering, and each positional cast.
func TestUsageLogBatchInsertShape_ColumnsAndArguments(t *testing.T) {
	prepared := make([]usageLogInsertPrepared, 2)
	keys := []string{"first", "second"}
	preparedByKey := make(map[string]usageLogInsertPrepared, len(keys))
	for i, key := range keys {
		prepared[i] = prepareUsageLogInsert(&usage.UsageLog{
			UserID:    int64(i + 1),
			APIKeyID:  int64(i + 2),
			RequestID: key,
			Model:     "gpt-5",
			CreatedAt: time.Date(2026, 10, 11, 0, i, 0, 0, time.UTC),
		})
		preparedByKey[key] = prepared[i]
	}
	batchQuery, batchArgs := buildUsageLogBatchInsertQuery(keys, preparedByKey)
	bestEffortQuery, bestEffortArgs := buildUsageLogBestEffortInsertQuery(prepared)
	inputShape := regexp.MustCompile(`(?s)WITH input \((.*?)\) AS \(VALUES (.*?)\n\s*\)`)
	insertShape := regexp.MustCompile(`(?s)INSERT INTO usage_logs \((.*?)\)\s*SELECT(.*?)FROM input`)
	placeholderShape := regexp.MustCompile(`\$(\d+)(?:::(\w+))?`)
	normalizeColumns := func(columns string) string {
		return strings.Join(strings.Fields(columns), "")
	}
	wantColumns := normalizeColumns(usageLogInsert.columns)

	for _, tt := range []struct {
		name       string // insert shape case description
		query      string // generated insert SQL text
		args       []any  // captured ordered bound arguments
		inputIndex bool   // synthetic input index presence
	}{
		{name: "batch", query: batchQuery, args: batchArgs, inputIndex: true},
		{name: "best-effort", query: bestEffortQuery, args: bestEffortArgs},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := inputShape.FindStringSubmatch(tt.query)
			require.Len(t, input, 3)
			inputColumns := wantColumns
			if tt.inputIndex {
				inputColumns = "input_idx," + inputColumns
			}
			require.Equal(t, inputColumns, normalizeColumns(input[1]))
			insert := insertShape.FindStringSubmatch(tt.query)
			require.Len(t, insert, 3)
			require.Equal(t, wantColumns, normalizeColumns(insert[1]))
			require.Equal(t, wantColumns, normalizeColumns(insert[2]))

			var wantArgs []any
			var wantTypes []string
			for i, row := range prepared {
				if tt.inputIndex {
					wantArgs = append(wantArgs, i)
					wantTypes = append(wantTypes, "")
				}
				wantArgs = append(wantArgs, row.args...)
				wantTypes = append(wantTypes, usageLogInsert.argTypes...)
			}
			require.Equal(t, wantArgs, tt.args)
			placeholders := placeholderShape.FindAllStringSubmatch(input[2], -1)
			require.Len(t, placeholders, len(wantArgs))
			for i, placeholder := range placeholders {
				require.Equal(t, strconv.Itoa(i+1), placeholder[1])
				require.Equal(t, wantTypes[i], placeholder[2])
			}
		})
	}
}

// TestExecUsageLogInsertNoResult_PersistsRequestedModel supplies a prepared
// requested-model row to acknowledgement-only execution. It checks all prepared
// arguments reach the mocked insert and execution succeeds.
func TestExecUsageLogInsertNoResult_PersistsRequestedModel(t *testing.T) {
	db, mock := newSQLMock(t)
	prepared := prepareUsageLogInsert(&usage.UsageLog{
		UserID:         1,
		APIKeyID:       2,
		ProviderID:     3,
		RequestID:      "req-best-effort-exec",
		Model:          "gpt-5",
		RequestedModel: "gpt-5",
		CreatedAt:      time.Date(2025, 1, 4, 12, 0, 0, 0, time.UTC),
	})

	mock.ExpectExec("INSERT INTO usage_logs").
		WithArgs(anySliceToDriverValues(prepared.args)...).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := execUsageLogInsertNoResult(context.Background(), db, prepared)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPrepareUsageLogInsert_ArgCountMatchesTypes prepares a populated
// request/model fixture for insertion. It checks the positional argument count
// equals the shared insert type-table length.
func TestPrepareUsageLogInsert_ArgCountMatchesTypes(t *testing.T) {
	prepared := prepareUsageLogInsert(&usage.UsageLog{
		UserID:         1,
		APIKeyID:       2,
		ProviderID:     3,
		RequestID:      "req-arg-count",
		Model:          "gpt-5",
		RequestedModel: "gpt-5",
		CreatedAt:      time.Date(2025, 1, 5, 12, 0, 0, 0, time.UTC),
	})

	require.Len(t, prepared.args, len(usageLogInsert.argTypes))
}

// TestPrepareUsageLogInsert_PersistsImageSizeMetadata supplies image class,
// input/output size, source, and count breakdown. It checks each nullable
// argument position and the encoded breakdown JSON.
func TestPrepareUsageLogInsert_PersistsImageSizeMetadata(t *testing.T) {
	imageSize := "4K"
	inputSize := "1024x1024"
	outputSize := "3840x2160"
	source := "output"
	prepared := prepareUsageLogInsert(&usage.UsageLog{
		UserID:               1,
		APIKeyID:             2,
		ProviderID:           3,
		RequestID:            "req-image-metadata",
		Model:                "gpt-image-2",
		RequestedModel:       "gpt-image-2",
		ImageCount:           2,
		ImageFrameCount:      18,
		ImageCacheReadTokens: 3,
		ImageSize:            &imageSize,
		ImageInputSize:       &inputSize,
		ImageOutputSize:      &outputSize,
		ImageSizeSource:      &source,
		ImageSizeBreakdown:   map[string]int{"1K": 1, "4K": 1},
		CreatedAt:            time.Date(2025, 1, 6, 12, 0, 0, 0, time.UTC),
	})

	require.Equal(t, sql.NullString{String: imageSize, Valid: true}, prepared.args[43])
	require.Equal(t, sql.NullString{String: inputSize, Valid: true}, prepared.args[44])
	require.Equal(t, sql.NullString{String: outputSize, Valid: true}, prepared.args[45])
	require.Equal(t, sql.NullString{String: source, Valid: true}, prepared.args[46])
	breakdownJSON, ok := prepared.args[47].(string)
	require.True(t, ok)
	require.JSONEq(t, `{"1K":1,"4K":1}`, breakdownJSON)
	require.Equal(t, 18, prepared.args[41])
	require.Equal(t, 3, prepared.args[42])
}

// TestBuildUsageLogBatchInsertQuery_UsesConflictDoNothing builds a charged
// result-aware payload for replay handling. It checks the SQL contains conflict
// suppression and no DO UPDATE clause.
func TestBuildUsageLogBatchInsertQuery_UsesConflictDoNothing(t *testing.T) {
	log := &usage.UsageLog{
		UserID:       1,
		APIKeyID:     2,
		ProviderID:   3,
		RequestID:    "req-batch-no-update",
		Model:        "gpt-5",
		InputTokens:  10,
		OutputTokens: 5,
		TotalCost:    1.2,
		ActualCost:   1.2,
		CreatedAt:    time.Now().UTC(),
	}
	prepared := prepareUsageLogInsert(log)

	query, _ := buildUsageLogBatchInsertQuery([]string{usageLogBatchKey(log.RequestID, log.APIKeyID)}, map[string]usageLogInsertPrepared{
		usageLogBatchKey(log.RequestID, log.APIKeyID): prepared,
	})

	require.Contains(t, query, "ON CONFLICT DO NOTHING")
	require.NotContains(t, strings.ToUpper(query), "DO UPDATE")
}

// TestPrepareUsageLogInsert_SessionIDArgWiring prepares a populated session
// correlation ID. It checks the nullable session argument and the following
// timestamp, reasoning, and compaction type positions.
func TestPrepareUsageLogInsert_SessionIDArgWiring(t *testing.T) {
	require.Len(t, usageLogInsert.argTypes, 73, "arg-type table must include billing key")

	sessionID := "sess-persisted-123"
	prepared := prepareUsageLogInsert(newSessionIDUsageLog(&sessionID))

	require.Len(t, prepared.args, len(usageLogInsert.argTypes),
		"prepared args must match the arg-type table length")

	// Assert the trailing diagnostic positions from the schema table's end, so
	// adding a later field exposes a shift in session/timestamp wiring.
	sessionArg := prepared.args[len(prepared.args)-10]
	ns, ok := sessionArg.(sql.NullString)
	require.True(t, ok, "session_id arg should be a sql.NullString, got %T", sessionArg)
	require.True(t, ns.Valid)
	require.Equal(t, sessionID, ns.String)

	require.Equal(t, "text", usageLogInsert.argTypes[len(usageLogInsert.argTypes)-10],
		"session_id arg type must be text")
	require.Equal(t, "timestamptz", usageLogInsert.argTypes[len(usageLogInsert.argTypes)-9],
		"created_at arg type must remain timestamptz")
	require.Equal(t, "text", usageLogInsert.argTypes[len(usageLogInsert.argTypes)-8],
		"requested reasoning effort arg type must be text")
	require.Equal(t, "boolean", usageLogInsert.argTypes[len(usageLogInsert.argTypes)-7],
		"native compaction arg type must be boolean")
}

// TestPrepareUsageLogInsert_SessionIDNullWhenAbsent prepares missing and
// explicitly empty session IDs. It checks both values become invalid nullable
// strings rather than populated empty persistence values.
func TestPrepareUsageLogInsert_SessionIDNullWhenAbsent(t *testing.T) {
	prepared := prepareUsageLogInsert(newSessionIDUsageLog(nil))
	sessionArg := prepared.args[len(prepared.args)-10]
	ns, ok := sessionArg.(sql.NullString)
	require.True(t, ok, "session_id arg should be a sql.NullString, got %T", sessionArg)
	require.False(t, ns.Valid, "absent session id must be NULL, not empty string")

	empty := ""
	preparedEmpty := prepareUsageLogInsert(newSessionIDUsageLog(&empty))
	nsEmpty := testassert.MustType[sql.NullString](preparedEmpty.args[len(preparedEmpty.args)-10])
	require.False(t, nsEmpty.Valid, "empty session id must also be NULL")
}

// TestUsageLogInsertQueries_IncludeSessionID builds both batch shapes from a
// session-bearing row. It checks read-shape ownership columns, session presence
// in SQL, and each shape's expected argument count.
func TestUsageLogInsertQueries_IncludeSessionID(t *testing.T) {
	require.Contains(t, usageLogSelectColumns, "session_id",
		"SELECT column list must include session_id")
	require.Contains(t, usageLogSelectColumns, "billing_user_id",
		"SELECT column list must include billing attribution")
	require.Contains(t, usageLogSelectColumns, "team_id",
		"SELECT column list must include team attribution")

	sessionID := "sess-in-query"
	log := newSessionIDUsageLog(&sessionID)
	prepared := prepareUsageLogInsert(log)
	key := usageLogBatchKey(log.RequestID, log.APIKeyID)

	batchQuery, batchArgs := buildUsageLogBatchInsertQuery([]string{key},
		map[string]usageLogInsertPrepared{key: prepared})
	require.Contains(t, batchQuery, "session_id")
	// Session identity participates in the CTE, insert columns, and select columns;
	// omitting any representation can lose it on one persistence path.
	require.GreaterOrEqual(t, strings.Count(batchQuery, "session_id"), 3)
	require.Len(t, batchArgs, len(prepared.args)+1,
		"batch args include the synthetic input_index before usage-log values")

	bestEffortQuery, bestEffortArgs := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
	require.Contains(t, bestEffortQuery, "session_id")
	require.Len(t, bestEffortArgs, len(prepared.args))
}

// TestPrepareUsageLogInsert_RequestedReasoningEffortArgWiring supplies the
// client's requested max effort. It checks that the correct positional nullable
// argument is valid and retains the supplied value.
func TestPrepareUsageLogInsert_RequestedReasoningEffortArgWiring(t *testing.T) {
	requested := "max"
	prepared := prepareUsageLogInsert(&usage.UsageLog{
		UserID: 1, APIKeyID: 2, ProviderID: 3, RequestID: "req-effort", Model: "gpt-5",
		RequestedReasoningEffort: &requested,
	})
	value, ok := prepared.args[len(prepared.args)-8].(sql.NullString)
	require.True(t, ok)
	require.True(t, value.Valid)
	require.Equal(t, requested, value.String)
}

// newUsageLogBestEffortRequestForTest constructs one prepared charged
// acknowledgement fixture. It uses a fixed request identity and a buffered
// result channel so direct flush tests can receive the worker's final SQL
// result.
func newUsageLogBestEffortRequestForTest() usageLogBestEffortRequest {
	log := &usage.UsageLog{
		UserID:        1,
		BillingUserID: 1,
		APIKeyID:      2,
		ProviderID:    3,
		RequestID:     "req-best-effort-deadlock",
		Model:         "gpt-5",
		InputTokens:   10,
		OutputTokens:  5,
		TotalCost:     1,
		ActualCost:    1,
		CreatedAt:     time.Now().UTC(),
	}
	return usageLogBestEffortRequest{
		prepared: prepareUsageLogInsert(log),
		apiKeyID: log.APIKeyID,
		resultCh: make(chan error, 1),
	}
}

// newSQLCapturingMock returns a mock whose matcher appends submitted SQL to
// captured. The matcher accepts any SQL text and leaves argument checks to
// expectations; callers serialize access to the shared capture slice.
func newSQLCapturingMock(t *testing.T, captured *[]string) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	matcher := sqlmock.QueryMatcherFunc(func(_, actualSQL string) error {
		// Capture storage belongs to the calling test. Record submitted SQL before
		// expectation argument checks, so shape inspection can include a statement
		// whose arguments the mock later rejects.
		*captured = append(*captured, actualSQL)
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	require.NoError(t, err)
	// Register database cleanup before exposing the mock, so a later failed
	// placeholder assertion does not retain the fixture's SQL resource.
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// requireStaticInsertMatchesArgTypes checks one static insert's columns and
// unique contiguous placeholder positions. It fails the test on mismatched shape
// or count and uses the shared type-table length as the expected schema size.
func requireStaticInsertMatchesArgTypes(t *testing.T, query string) {
	t.Helper()
	m := usageLogStaticInsertShapeRe.FindStringSubmatch(query)
	require.Len(t, m, 3, "unrecognised INSERT shape:\n%s", query)

	want := len(usageLogInsert.argTypes)
	columns := 0
	for col := range strings.SplitSeq(m[1], ",") {
		if strings.TrimSpace(col) != "" {
			columns++
		}
	}
	require.Equal(t, want, columns, "INSERT column list must match usageLogInsert.argTypes")

	seen := make(map[int]struct{}, want)
	for _, ph := range usageLogPlaceholderRe.FindAllStringSubmatch(m[2], -1) {
		n, err := strconv.Atoi(ph[1])
		require.NoError(t, err)
		_, dup := seen[n]
		require.False(t, dup, "duplicate placeholder $%d", n)
		seen[n] = struct{}{}
	}
	require.Len(t, seen, want, "VALUES placeholder count must match usageLogInsert.argTypes")
	for i := 1; i <= want; i++ {
		_, ok := seen[i]
		require.True(t, ok, "missing placeholder $%d", i)
	}
}

// anySliceToDriverValues copies the outer argument sequence into driver.Value
// storage. Elements retain their supplied values and referenced data, so callers
// keep those values stable while sqlmock checks the resulting arguments.
func anySliceToDriverValues(values []any) []driver.Value {
	out := make([]driver.Value, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

// newSessionIDUsageLog returns a charged fixture carrying the supplied session
// pointer. Nil is accepted for missing-session tests; the pointer is retained,
// so the caller keeps its text stable during preparation.
func newSessionIDUsageLog(sessionID *string) *usage.UsageLog {
	return &usage.UsageLog{
		UserID:       1,
		APIKeyID:     2,
		ProviderID:   3,
		RequestID:    "req-session-id",
		Model:        "claude-3",
		InputTokens:  10,
		OutputTokens: 5,
		TotalCost:    1.0,
		ActualCost:   1.0,
		SessionID:    sessionID,
		CreatedAt:    time.Now().UTC(),
	}
}

// TestFlushCreateBatchUnresolvedDuplicatesStayUnknown supplies an executed batch with
// missing row identity. Every waiter must receive uncertainty, including duplicates,
// so completion cannot report a successful usage write from an absent acknowledgement.
func TestFlushCreateBatchUnresolvedDuplicatesStayUnknown(t *testing.T) {
	for _, payload := range []string{`[]`, `[{"request_id":"request","api_key_id":2,"id":null,"created_at":null}]`} {
		t.Run(payload, func(t *testing.T) {
			db, mock := newSQLMock(t)
			mock.ExpectQuery(`(?s)WITH input`).WillReturnRows(sqlmock.NewRows([]string{"rows"}).AddRow(payload))
			row := &usage.UsageLog{RequestID: "request", APIKeyID: 2}
			first := usageLogCreateRequest{log: row, prepared: prepareUsageLogInsert(row), resultCh: make(chan usageLogCreateResult, 1)}
			secondRow := *row
			second := usageLogCreateRequest{log: &secondRow, prepared: prepareUsageLogInsert(&secondRow), resultCh: make(chan usageLogCreateResult, 1)}
			(&Store{}).flushCreateBatch(db, []usageLogCreateRequest{first, second})
			for _, request := range []usageLogCreateRequest{first, second} {
				result := <-request.resultCh
				require.Error(t, result.err)
				require.False(t, result.inserted)
				require.False(t, usage.IsUsageLogCreateNotPersisted(result.err))
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
