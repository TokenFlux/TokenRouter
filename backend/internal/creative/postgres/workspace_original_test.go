//go:build unit

package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	creativepostgres "github.com/TokenFlux/TokenRouter/internal/creative/postgres"

	"entgo.io/ent/dialect"
	"github.com/TokenFlux/TokenRouter/internal/creative"

	entsql "entgo.io/ent/dialect/sql"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/enttest"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

const (
	creativeRepoWorkspaceA = "11111111-1111-4111-8111-111111111111"
	creativeRepoWorkspaceB = "22222222-2222-4222-8222-222222222222"
)

// openCreativeRunRepositoryForTest 创建带完整 Ent schema 的内存仓储。
func openCreativeRunRepositoryForTest(t *testing.T) (creative.CreativeRunRepository, func()) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:creative_run_scope_%s?mode=memory&cache=shared&_fk=1", t.Name()))
	require.NoError(t, err)
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	return creativepostgres.NewCreativeRunRepository(client), func() {
		require.NoError(t, client.Close())
		_ = db.Close()
	}
}

// creativeRepoWorkspaceRunParams 构造仓储测试所需的最小任务参数。
func creativeRepoWorkspaceRunParams(runID, workspaceID string, key *string) creative.CreateCreativeRunParams {
	return creative.CreateCreativeRunParams{
		RunID:                runID,
		UserID:               7,
		WorkspaceID:          workspaceID,
		GroupID:              12,
		APIKeyID:             100,
		Model:                "gpt-image-1",
		RequestedModel:       "gpt-image-1",
		Operation:            creative.CreativeOperationGenerate,
		RequestedOutputCount: 0,
		ImageSize:            "1K",
		ResponseMIMEType:     "image/png",
		PromptHash:           "prompt-hash",
		RequestFingerprint:   "fingerprint-" + runID,
		IdempotencyKey:       key,
	}
}

// TestCreativeRunRepositoryWorkspaceScope 校验真实 Ent 查询按用户和工作区双重隔离。
func TestCreativeRunRepositoryWorkspaceScope(t *testing.T) {
	repo, cleanup := openCreativeRunRepositoryForTest(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	key := "same-key"

	first, err := repo.CreateCreativeRun(ctx, creativeRepoWorkspaceRunParams("crun_scope_a", creativeRepoWorkspaceA, &key))
	require.NoError(t, err)
	second, err := repo.CreateCreativeRun(ctx, creativeRepoWorkspaceRunParams("crun_scope_b", creativeRepoWorkspaceB, &key))
	require.NoError(t, err)

	firstList, err := repo.ListCreativeRunsForOwner(ctx, creative.CreativeRunScope{UserID: 7, WorkspaceID: creativeRepoWorkspaceA}, creative.CreativeRunFilter{Limit: 20})
	require.NoError(t, err)
	require.Len(t, firstList, 1)
	require.Equal(t, first.RunID, firstList[0].RunID)
	secondList, err := repo.ListCreativeRunsForOwner(ctx, creative.CreativeRunScope{UserID: 7, WorkspaceID: creativeRepoWorkspaceB}, creative.CreativeRunFilter{Limit: 20})
	require.NoError(t, err)
	require.Len(t, secondList, 1)
	require.Equal(t, second.RunID, secondList[0].RunID)

	got, err := repo.GetCreativeRunByIdempotencyKey(ctx, creative.CreativeRunScope{UserID: 7, WorkspaceID: creativeRepoWorkspaceA}, key)
	require.NoError(t, err)
	require.Equal(t, first.RunID, got.RunID)
	got, err = repo.GetCreativeRunByIdempotencyKey(ctx, creative.CreativeRunScope{UserID: 7, WorkspaceID: creativeRepoWorkspaceB}, key)
	require.NoError(t, err)
	require.Equal(t, second.RunID, got.RunID)

	_, err = repo.GetCreativeRunByRunIDForOwner(ctx, creative.CreativeRunScope{UserID: 7, WorkspaceID: creativeRepoWorkspaceB}, first.RunID)
	require.ErrorIs(t, err, creative.ErrCreativeRunNotFound)

	legacy, err := repo.CreateCreativeRun(ctx, creativeRepoWorkspaceRunParams("crun_scope_legacy", "", nil))
	require.NoError(t, err)
	require.Nil(t, legacy.WorkspaceID)
	firstList, err = repo.ListCreativeRunsForOwner(ctx, creative.CreativeRunScope{UserID: 7, WorkspaceID: creativeRepoWorkspaceA}, creative.CreativeRunFilter{Limit: 20})
	require.NoError(t, err)
	require.Len(t, firstList, 1)
}
