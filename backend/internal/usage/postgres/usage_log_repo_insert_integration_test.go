//go:build integration

package postgres

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// Diagnostic identity must survive every usage persistence shape, including
// nullable response-model evidence and session correlation text. PostgreSQL
// round trips compare supplied fields with stored values, and financial-key
// replay cases write old and unified request forms in both orders to check
// duplicate recovery.
//
// The activity benchmarks alter a shared insert trigger and vary whether
// sixty-four rows use one actor or distinct actors. Cleanup restores that
// trigger and removes user-dependent fixtures after each variant. Parallel
// invocations share an atomic request sequence so duplicate suppression cannot
// replace the intended batch write work, while serial invocations use the same
// production SQL shape for comparison.

// TestResponseModelBatchRoundTrip inserts absent and present model declarations
// with nullable mismatch flags through batch SQL. It checks diagnostic readback,
// best-effort/direct paths, and successful repeat execution.
func TestResponseModelBatchRoundTrip(t *testing.T) {
	ctx := context.Background()
	// Result-aware batch SQL uses the shared pool, so registered truncation owns
	// removal of these committed fixtures rather than a surrounding rollback.
	client := testEntClient(t)
	suffix := uuid.NewString()
	user := mustCreateUser(t, client, &identity.User{Email: suffix + "@response-model.test"})
	key := mustCreateApiKey(t, client, &apikey.APIKey{UserID: user.ID, Key: suffix, Name: "model"})
	upstream := mustCreateProvider(t, client, &providercore.Record{Name: suffix})
	repo := &Store{}
	keys := []string{}
	prepared := map[string]usageLogInsertPrepared{}
	for _, kind := range []string{"unknown", "same", "different"} {
		log := &usage.UsageLog{UserID: user.ID, APIKeyID: key.ID, ProviderID: upstream.ID, RequestID: suffix + kind, Model: "sent", CreatedAt: time.Now()}
		if kind != "unknown" {
			value := kind
			mismatch := kind == "different"
			log.UpstreamResponseModel = &value
			log.UpstreamModelMismatch = &mismatch
		}
		batchKey := usageLogBatchKey(log.RequestID, log.APIKeyID)
		keys = append(keys, batchKey)
		prepared[batchKey] = prepareUsageLogInsert(log)
	}
	_, states, _, err := repo.batchInsertUsageLogs(integrationDB, keys, prepared)
	require.NoError(t, err)
	for _, key := range keys {
		state := states[key]
		log, err := scanUsageLog(integrationDB.QueryRowContext(ctx, "SELECT "+usageLogSelectColumns+" FROM usage_logs WHERE id=$1", state.ID))
		require.NoError(t, err)
		want := prepared[key].args
		require.Equal(t, want[len(want)-5], nullString(log.UpstreamResponseModel))
		require.Equal(t, want[len(want)-4], log.UpstreamModelMismatch)
	}
	// Execute the acknowledgement-only and direct shapes with populated response
	// evidence, then compare their raw readback to the same supplied diagnostics.
	for _, path := range []string{"best-effort", "fallback"} {
		model, mismatch := "runtime-version", true
		log := &usage.UsageLog{UserID: user.ID, APIKeyID: key.ID, ProviderID: upstream.ID, RequestID: suffix + path, Model: "sent", CreatedAt: time.Now(), UpstreamResponseModel: &model, UpstreamModelMismatch: &mismatch}
		value := prepareUsageLogInsert(log)
		if path == "best-effort" {
			query, args := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{value})
			_, err = integrationDB.ExecContext(ctx, query, args...)
		} else {
			err = execUsageLogInsertNoResult(ctx, integrationDB, value)
		}
		require.NoError(t, err)
		loaded, err := scanUsageLog(integrationDB.QueryRowContext(ctx, "SELECT "+usageLogSelectColumns+" FROM usage_logs WHERE request_id=$1 AND api_key_id=$2", log.RequestID, key.ID))
		require.NoError(t, err)
		require.Equal(t, log.UpstreamResponseModel, loaded.UpstreamResponseModel)
		require.Equal(t, log.UpstreamModelMismatch, loaded.UpstreamModelMismatch)
	}
	// Reexecute the same prepared identities after readback. Conflict suppression
	// must let the result-aware batch resolve these stored rows successfully.
	_, _, _, err = repo.batchInsertUsageLogs(integrationDB, keys, prepared)
	require.NoError(t, err)
}

// TestUsageLog_SessionIDPersistence records one populated session ID and one
// missing value through pool-backed Create. It checks assigned row identity,
// session readback, and nil for the missing field.
func TestUsageLog_SessionIDPersistence(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageLogRepositoryWithSQL(client, integrationDB, timezone.NewCalendar(time.Local))

	user := mustCreateUser(t, client, &identity.User{Email: "session-id-" + uuid.NewString() + "@example.com"})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{UserID: user.ID, Key: "sk-session-" + uuid.NewString(), Name: "k"})
	provider := mustCreateProvider(t, client, &providercore.Record{Name: "acc-session-" + uuid.NewString()})

	sessionID := "sess-" + uuid.NewString()

	withSession := &usage.UsageLog{
		UserID:                 user.ID,
		APIKeyID:               apiKey.ID,
		ProviderID:             provider.ID,
		RequestID:              uuid.NewString(),
		Model:                  "gpt-6.2-sol",
		InputTokens:            10,
		OutputTokens:           5,
		CacheCreationTokens:    7,
		CacheCreation30mTokens: new(0),
		CacheCreation12hTokens: new(7),
		TotalCost:              1.0,
		ActualCost:             1.0,
		SessionID:              &sessionID,
		CreatedAt:              time.Now().UTC(),
	}
	_, err := repo.Create(ctx, withSession)
	require.NoError(t, err)
	require.NotZero(t, withSession.ID)

	withoutSession := &usage.UsageLog{
		UserID:       user.ID,
		APIKeyID:     apiKey.ID,
		ProviderID:   provider.ID,
		RequestID:    uuid.NewString(),
		Model:        "claude-3",
		InputTokens:  7,
		OutputTokens: 3,
		TotalCost:    0.5,
		ActualCost:   0.5,
		CreatedAt:    time.Now().UTC(),
	}
	_, err = repo.Create(ctx, withoutSession)
	require.NoError(t, err)

	// Read the inserted row by database identity so the session assertion exercises
	// both positional persistence and the raw nullable-column scanner.
	got, err := repo.GetByID(ctx, withSession.ID)
	require.NoError(t, err)
	require.NotNil(t, got.SessionID)
	require.Equal(t, sessionID, *got.SessionID)
	require.NotNil(t, got.CacheCreation30mTokens)
	require.Zero(t, *got.CacheCreation30mTokens)
	require.NotNil(t, got.CacheCreation12hTokens)
	require.Equal(t, 7, *got.CacheCreation12hTokens)

	// The second row omits session evidence; nullable scanning must keep that
	// absence distinct from the first row's populated correlation text.
	gotNone, err := repo.GetByID(ctx, withoutSession.ID)
	require.NoError(t, err)
	require.Nil(t, gotNone.SessionID)
	require.Nil(t, gotNone.CacheCreation30mTokens)
	require.Nil(t, gotNone.CacheCreation12hTokens)
}

// BenchmarkUserActivityWrites measures production batch construction and SQL
// execution with activity triggers enabled or disabled. It varies shared versus
// distinct actors through the serial benchmark helper.
func BenchmarkUserActivityWrites(b *testing.B) {
	benchmarkUserActivityWrites(b, false)
}

// BenchmarkUserActivityConcurrentWrites measures parallel production batch
// writes under shared and distinct actor fixtures. It runs the same trigger
// variants so contention and activity-maintenance costs can be compared.
func BenchmarkUserActivityConcurrentWrites(b *testing.B) {
	benchmarkUserActivityWrites(b, true)
}

// benchmarkUserActivityWrites runs serial or parallel activity-trigger variants
// with sixty-four-row batches. Each sub-benchmark changes the shared usage
// trigger, and callers serialize this fixture database while those settings are
// in use.
//
// A registered cleanup re-enables the insert trigger and removes dependent user
// fixtures after every variant. Shared-user mode reuses one actor/key across
// batch positions; spread mode creates separate identities. An atomic sequence
// keeps each batch's request IDs unique during concurrent execution, preventing
// replay suppression from replacing the activity-write work being measured.
func benchmarkUserActivityWrites(b *testing.B, concurrent bool) {
	ctx := context.Background()
	client := integrationEntClient
	for _, spread := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			b.Run(fmt.Sprintf("spread=%t/activity=%t", spread, enabled), func(b *testing.B) {
				mode := "DISABLE"
				if enabled {
					mode = "ENABLE"
				}
				// Change the shared insert trigger before timing this variant so each
				// measured batch uses the selected activity-maintenance condition.
				_, err := integrationDB.Exec("ALTER TABLE usage_logs " + mode + " TRIGGER usage_activity_insert")
				require.NoError(b, err)
				// Restore the shared trigger and remove user-dependent rows after each
				// variant, including one whose measured SQL reports an error.
				b.Cleanup(func() {
					_, e := integrationDB.Exec("ALTER TABLE usage_logs ENABLE TRIGGER usage_activity_insert; TRUNCATE users CASCADE")
					require.NoError(b, e)
				})
				p, err := client.Provider.Create().SetName("activity-bench").SetPlatform("openai").SetType("apikey").SetCredentials(map[string]any{}).Save(ctx)
				require.NoError(b, err)
				// Use the result-aware worker's sixty-four-record maximum for
				// both fixture arrays and each timed batch, keeping
				// actor-spread comparisons equally sized.
				userIDs, keyIDs := make([]int64, 64), make([]int64, 64)
				for i := range userIDs {
					if i > 0 && !spread {
						// Shared-user mode reuses one actor and key across all
						// batch rows, making activity updates contend on the
						// same summarized identity.
						userIDs[i], keyIDs[i] = userIDs[0], keyIDs[0]
						continue
					}
					u, e := client.User.Create().SetEmail(fmt.Sprintf("%d@activity-bench.test", i)).SetPasswordHash("hash").Save(ctx)
					require.NoError(b, e)
					k, e := client.APIKey.Create().SetUserID(u.ID).SetKey(fmt.Sprintf("activity-bench-%d", i)).SetName("bench").Save(ctx)
					require.NoError(b, e)
					userIDs[i], keyIDs[i] = u.ID, k.ID
				}
				repo := &Store{}
				sequence := new(atomic.Int64)
				write := func() {
					// Parallel benchmark invocations share this sequence so
					// every batch has distinct request IDs and performs writes
					// rather than replay suppression.
					current := sequence.Add(1)
					keys := make([]string, 64)
					prepared := make(map[string]usageLogInsertPrepared, 64)
					for i := range keys {
						log := &usage.UsageLog{UserID: userIDs[i], BillingUserID: userIDs[i], APIKeyID: keyIDs[i], ProviderID: p.ID, RequestID: fmt.Sprintf("bench-%d-%d", current, i), Model: "test", CreatedAt: time.Now(), InputTokens: 100, OutputTokens: 100, TotalCost: 0.1, ActualCost: 0.1}
						keys[i] = usageLogBatchKey(log.RequestID, log.APIKeyID)
						prepared[keys[i]] = prepareUsageLogInsert(log)
					}
					_, _, _, writeErr := repo.batchInsertUsageLogs(integrationDB, keys, prepared)
					if writeErr != nil {
						b.Error(writeErr)
					}
				}
				if concurrent {
					b.ResetTimer()
					// Each parallel worker builds and writes its own batch
					// while sharing the fixture identities, exposing contention
					// in the selected actor spread.
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							write()
						}
					})
				} else {
					for b.Loop() {
						write()
					}
				}
			})
		}
	}
}

// TestUsageLogBillingKeyUpgradeReplay writes legacy financial IDs and unified
// request IDs in both orders. It checks one inserted row, duplicate recovery to
// the same ID, and financial-expression uniqueness.
func TestUsageLogBillingKeyUpgradeReplay(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	suffix := uuid.NewString()
	user := mustCreateUser(t, client, &identity.User{Email: suffix + "@billing-key.test"})
	key := mustCreateApiKey(t, client, &apikey.APIKey{UserID: user.ID, Key: suffix, Name: "billing-key"})
	provider := mustCreateProvider(t, client, &providercore.Record{Name: suffix})
	repo := &Store{sql: integrationDB}
	for _, oldFirst := range []bool{true, false} {
		billingKey := "creative_settle:" + uuid.NewString()
		legacy := &usage.UsageLog{UserID: user.ID, APIKeyID: key.ID, ProviderID: provider.ID, RequestID: billingKey, Model: "model", CreatedAt: time.Now().UTC()}
		current := *legacy
		current.RequestID = uuid.NewString()
		current.BillingKey = billingKey
		first, second := legacy, &current
		if !oldFirst {
			first, second = &current, legacy
		}
		inserted, err := repo.createSingle(ctx, integrationDB, first)
		require.NoError(t, err)
		require.True(t, inserted)
		inserted, err = repo.createSingle(ctx, integrationDB, second)
		require.NoError(t, err)
		require.False(t, inserted)
		require.Equal(t, first.ID, second.ID)
		var count int
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_logs WHERE COALESCE(billing_key, request_id)=$1 AND api_key_id=$2", billingKey, key.ID).Scan(&count))
		require.Equal(t, 1, count)
	}
}

// TestUsagePerformanceLateWriteKeepsSnapshotAndExistingRows expires an acknowledgement
// after queue admission, then executes the retained SQL payload. It verifies immutable
// timing ownership, late persistence, and conflict handling that leaves historical JSON absent.
func TestUsagePerformanceLateWriteKeepsSnapshotAndExistingRows(t *testing.T) {
	client := testEntClient(t)
	repo := NewUsageLogRepositoryWithSQL(client, integrationDB, timezone.NewCalendar(time.Local))
	user := mustCreateUser(t, client, &identity.User{Email: "usage-timing-" + uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &apikey.APIKey{UserID: user.ID, Key: "sk-timing-" + uuid.NewString(), Name: "timing"})
	provider := mustCreateProvider(t, client, &providercore.Record{Name: "timing-" + uuid.NewString()})
	p := timing.NewPerformance(timing.FirstOutput, 10)
	p.Values["upstream_first_output_ms"] = 20
	row := &usage.UsageLog{RequestID: uuid.NewString(), UserID: user.ID, APIKeyID: key.ID, ProviderID: provider.ID, Model: "model", Timings: p, CreatedAt: time.Now().UTC()}
	// Hold accepted work outside the worker until the caller abandons acknowledgement;
	// the SQL arguments already own their serialized performance and remain executable.
	repo.bestEffortBatchCh = make(chan usageLogBestEffortRequest, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- repo.CreateBestEffort(ctx, row) }()
	queued := <-repo.bestEffortBatchCh
	err := <-result
	require.True(t, usage.IsUsageLogCreateDropped(err))
	require.False(t, usage.IsUsageLogCreateNotPersisted(err))
	p.Values["upstream_first_output_ms"] = 999
	repo.flushBestEffortBatch(integrationDB, []usageLogBestEffortRequest{queued})
	require.NoError(t, <-queued.resultCh)
	var observed int64
	require.NoError(t, integrationDB.QueryRowContext(t.Context(), `SELECT (timings->'values'->>'upstream_first_output_ms')::bigint FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, row.RequestID, key.ID).Scan(&observed))
	require.Equal(t, int64(20), observed)
	row.RequestID = uuid.NewString()
	row.Timings = nil
	inserted, err := repo.createSingle(t.Context(), integrationDB, row)
	require.NoError(t, err)
	require.True(t, inserted)
	row.Timings = p
	inserted, err = repo.createSingle(t.Context(), integrationDB, row)
	require.NoError(t, err)
	require.False(t, inserted)
	var missing bool
	require.NoError(t, integrationDB.QueryRowContext(t.Context(), `SELECT timings IS NULL FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, row.RequestID, key.ID).Scan(&missing))
	require.True(t, missing)
}
