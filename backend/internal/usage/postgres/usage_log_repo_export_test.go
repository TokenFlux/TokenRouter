package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/enttest"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// TestStoreExportBatchReaderKeysetCursorAndCachedOwnedTeamLookup runs
// CountExportLogs and multi-page NextBatch reads against sqlmock. Queries apply
// half-open [StartTime, EndTime) bounds, advance descending (created_at, id)
// keyset cursors without SQL OFFSET, and query owned teams only once across batches.
func TestStoreExportBatchReaderKeysetCursorAndCachedOwnedTeamLookup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	store := &Store{sql: db}
	uid := int64(19)
	start := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	filters := usage.UsageExportFilters{
		StartTime:        start,
		EndTime:          end,
		Timezone:         "Asia/Shanghai",
		UserID:           &uid,
		IncludeOwnedTeam: true,
	}

	// CountExportLogs resolves owned team once for the initial count query.
	mock.ExpectQuery(`FROM team_memberships tm`).
		WithArgs(uid).
		WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(int64(88)))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM \(`).
		WithArgs(uid, int64(88), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(2)))

	count, err := store.CountExportLogs(context.Background(), filters)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)

	// NewExportBatchReader resolves owned team on batch 1 and reuses it on batch 2.
	reader := store.NewExportBatchReader(usage.UsageExportScopeUser)
	cols := strings.Split(usageLogSelectColumns, ", ")

	rowTime := start.Add(30 * time.Minute)
	mock.ExpectQuery(`FROM team_memberships tm`).
		WithArgs(uid).
		WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(int64(88)))
	mock.ExpectQuery(`ORDER BY created_at DESC, id DESC LIMIT \$5`).
		WithArgs(uid, int64(88), start, end, 1).
		WillReturnRows(sqlmock.NewRows(cols).AddRow(makeMockUsageLogRow(501, uid, rowTime)...))

	batch1, err := reader.NextBatch(context.Background(), filters, nil, 1)
	require.NoError(t, err)
	require.Len(t, batch1, 1)
	require.Equal(t, int64(501), batch1[0].ID)

	cursor := &usage.UsageExportCursor{CreatedAt: batch1[0].CreatedAt, ID: batch1[0].ID}
	mock.ExpectQuery(`\(created_at < \$5 OR \(created_at = \$5 AND id < \$6\)\) ORDER BY created_at DESC, id DESC LIMIT \$7`).
		WithArgs(uid, int64(88), start, end, cursor.CreatedAt, cursor.ID, 1).
		WillReturnRows(sqlmock.NewRows(cols))

	batch2, err := reader.NextBatch(context.Background(), filters, cursor, 1)
	require.NoError(t, err)
	require.Empty(t, batch2)
}

// makeMockUsageLogRow builds a 76-column row matching usageLogSelectColumns for sqlmock tests.
func makeMockUsageLogRow(id, userID int64, createdAt time.Time) []driver.Value {
	return []driver.Value{
		id, userID, userID, nil, int64(3), int64(4), "req_test", "gpt-5.4", "gpt-5.4", nil,
		nil, nil, 10, 20, 0, 0, 0, 0, nil, nil,
		0, 0.0, 0, 0.0, 0.001, 0.002, 0.0, 0.0, 0.003, 0.003, 0.0, 0.003,
		nil, 1.0, nil, int16(0), int16(usage.RequestTypeStream), true, false, nil, nil, nil,
		nil, 0, 0, 0, nil, nil, nil, nil, nil, 0,
		nil, nil, nil, nil, nil, nil, false, false, nil, nil,
		nil, nil, nil, nil, nil, createdAt, nil, false, "openai", nil,
		nil, usage.SourceUser, nil, nil,
	}
}

// TestExportBatchReaderCacheEvictionPreservesCurrentBatchAssociations hydrates
// two consecutive batches whose combined unique IDs exceed maxAssocCacheEntries.
// Evicting stale entries when loading new IDs preserves cached APIKey, User,
// Provider, and Group display views referenced in the current batch and keeps
// those active IDs warm in the per-task cache afterward.
func TestExportBatchReaderCacheEvictionPreservesCurrentBatchAssociations(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	var (
		userIDs     = make([]int64, 4)
		keyIDs      = make([]int64, 4)
		providerIDs = make([]int64, 4)
		groupIDs    = make([]int64, 4)
	)
	for i := range 4 {
		u, createErr := client.User.Create().
			SetEmail(fmt.Sprintf("user-%d@example.com", i+1)).
			SetPasswordHash("hash").
			Save(ctx)
		require.NoError(t, createErr)
		userIDs[i] = u.ID

		k, createErr := client.APIKey.Create().
			SetUserID(u.ID).
			SetKey(fmt.Sprintf("sk-export-cache-%d", i+1)).
			SetName(fmt.Sprintf("Key-%d", i+1)).
			Save(ctx)
		require.NoError(t, createErr)
		keyIDs[i] = k.ID

		p, createErr := client.Provider.Create().
			SetName(fmt.Sprintf("Provider-%d", i+1)).
			SetPlatform("openai").
			SetType("apikey").
			SetCredentials(map[string]any{}).
			Save(ctx)
		require.NoError(t, createErr)
		providerIDs[i] = p.ID

		g, createErr := client.Group.Create().
			SetName(fmt.Sprintf("Group-%d", i+1)).
			SetStatus(billing.StatusActive).
			SetRateMultiplier(1.0).
			SetIsExclusive(false).
			SetForceOpenaiFast(false).
			Save(ctx)
		require.NoError(t, createErr)
		groupIDs[i] = g.ID
	}

	store := &Store{client: client, sql: db}
	reader, ok := store.NewExportBatchReader(usage.UsageExportScopeAdmin).(*exportBatchReader)
	require.True(t, ok)
	reader.maxAssocCacheEntries = 2

	// Batch 1 fills all four caches to capacity (2 entries: index 0 and index 1).
	g0, g1 := groupIDs[0], groupIDs[1]
	batch1 := []usage.UsageLog{
		{ID: 10, UserID: userIDs[0], APIKeyID: keyIDs[0], ProviderID: providerIDs[0], GroupID: &g0},
		{ID: 9, UserID: userIDs[1], APIKeyID: keyIDs[1], ProviderID: providerIDs[1], GroupID: &g1},
	}
	require.NoError(t, reader.hydrateExportBatch(ctx, batch1))
	require.NotNil(t, batch1[0].APIKey)
	require.Equal(t, "Key-1", batch1[0].APIKey.Name)

	// Batch 2 reuses index 0 (already cached) alongside two uncached entries (index 2 and 3),
	// pushing total entries past maxAssocCacheEntries=2 and triggering eviction.
	g2, g3 := groupIDs[2], groupIDs[3]
	batch2 := []usage.UsageLog{
		{ID: 8, UserID: userIDs[0], APIKeyID: keyIDs[0], ProviderID: providerIDs[0], GroupID: &g0},
		{ID: 7, UserID: userIDs[2], APIKeyID: keyIDs[2], ProviderID: providerIDs[2], GroupID: &g2},
		{ID: 6, UserID: userIDs[3], APIKeyID: keyIDs[3], ProviderID: providerIDs[3], GroupID: &g3},
	}
	require.NoError(t, reader.hydrateExportBatch(ctx, batch2))

	// Row 0 (referencing pre-cached index 0) must still retain all four display names!
	require.NotNil(t, batch2[0].APIKey)
	require.Equal(t, "Key-1", batch2[0].APIKey.Name)
	require.NotNil(t, batch2[0].User)
	require.Equal(t, "user-1@example.com", batch2[0].User.Email)
	require.NotNil(t, batch2[0].Provider)
	require.Equal(t, "Provider-1", batch2[0].Provider.Name)
	require.NotNil(t, batch2[0].Group)
	require.Equal(t, "Group-1", batch2[0].Group.Name)

	// Newly loaded rows (index 2 and index 3) must also be populated even though batch2
	// referenced 3 distinct entities while maxAssocCacheEntries was capped at 2.
	require.NotNil(t, batch2[1].APIKey)
	require.Equal(t, "Key-3", batch2[1].APIKey.Name)
	require.NotNil(t, batch2[2].APIKey)
	require.Equal(t, "Key-4", batch2[2].APIKey.Name)
	require.NotNil(t, batch2[2].Group)
	require.Equal(t, "Group-4", batch2[2].Group.Name)
	require.LessOrEqual(t, len(reader.apiKeyCache), 2)
	require.Contains(t, reader.apiKeyCache, keyIDs[0])
}
