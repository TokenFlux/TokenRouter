//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// 核对状态比较及同连接参与，外层回滚和 outbox 写入失败都不能留下轮换凭据。
func TestS06RefreshCredentialsCASAndOuterRollback(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	suffix := time.Now().UnixNano()
	row, err := client.Account.Create().SetName(fmt.Sprintf("s06-credential-%d", suffix)).SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeOAuth).SetCredentials(map[string]any{"refresh_token": "first"}).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
	repo := &accountRepository{client: client, sql: integrationDB}
	readVersion := func() accountcore.CredentialVersion {
		current, e := repo.GetByID(ctx, row.ID)
		require.NoError(t, e)
		return accountcore.CredentialVersion{ID: current.ID, Platform: current.Platform, Type: current.Type, Status: current.Status, ProxyID: current.ProxyID, Credentials: current.Credentials}
	}
	initial := readVersion()
	require.NoError(t, repo.UpdateCredentials(ctx, row.ID, map[string]any{"refresh_token": "administrator"}))
	applied, err := repo.UpdateOAuthCredentialsIfUnchanged(ctx, initial, map[string]any{"refresh_token": "stale"})
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, "administrator", readVersion().Credentials["refresh_token"])

	version := readVersion()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	applied, err = repo.UpdateOAuthCredentialsIfUnchanged(txCtx, version, map[string]any{"refresh_token": "pending"})
	require.NoError(t, err)
	require.True(t, applied)
	pending, err := tx.Client().Account.Get(txCtx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Credentials["refresh_token"])
	// 独立连接提交前仍看到旧值，证明参与方法没有自行提交。
	require.Equal(t, "administrator", readVersion().Credentials["refresh_token"])
	require.NoError(t, tx.Rollback())
	require.Equal(t, "administrator", readVersion().Credentials["refresh_token"])

	applied, err = repo.UpdateOAuthCredentialsIfUnchanged(ctx, version, map[string]any{"refresh_token": "committed"})
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, "committed", readVersion().Credentials["refresh_token"])
	stale := readVersion()
	require.NoError(t, client.Account.UpdateOneID(row.ID).SetStatus(service.StatusDisabled).Exec(ctx))
	applied, err = repo.UpdateOAuthCredentialsIfUnchanged(ctx, stale, map[string]any{"refresh_token": "late"})
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, "committed", readVersion().Credentials["refresh_token"])
}

func TestS06RefreshCredentialsOutboxFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	suffix := time.Now().UnixNano()
	row, err := client.Account.Create().SetName(fmt.Sprintf("s06-credential-outbox-%d", suffix)).SetPlatform(service.PlatformGemini).SetType(service.AccountTypeOAuth).SetCredentials(map[string]any{"refresh_token": "first"}).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
	functionName := fmt.Sprintf("s06_credential_outbox_fail_%d", suffix)
	triggerName := fmt.Sprintf("s06_credential_outbox_trigger_%d", suffix)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.account_id = %d THEN RAISE EXCEPTION 's06 forced outbox failure'; END IF; RETURN NEW; END $$`, functionName, row.ID))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON scheduler_outbox", triggerName))
		require.NoError(t, e)
		_, e = integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
		require.NoError(t, e)
	})
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION %s()", triggerName, functionName))
	require.NoError(t, err)
	repo := &accountRepository{client: client, sql: integrationDB}
	version := accountcore.CredentialVersion{ID: row.ID, Platform: row.Platform, Type: row.Type, Status: row.Status, Credentials: row.Credentials}
	applied, err := repo.UpdateOAuthCredentialsIfUnchanged(ctx, version, map[string]any{"refresh_token": "uncommitted"})
	require.ErrorContains(t, err, "s06 forced outbox failure")
	require.False(t, applied)
	current, err := repo.GetByID(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "first", current.Credentials["refresh_token"])
}

// 交换器只使用本地可控闸门，真实数据库在交换暂停期间完成管理员改凭据。
type s06RefreshExchange struct {
	started chan struct{}
	release chan struct{}
	calls   int
}

func (e *s06RefreshExchange) CanRefresh(a *service.Account) bool {
	return a.Platform == service.PlatformOpenAI && a.Type == service.AccountTypeOAuth
}
func (e *s06RefreshExchange) NeedsRefresh(*service.Account, time.Duration) bool { return true }
func (e *s06RefreshExchange) CacheKey(a *service.Account) string {
	return fmt.Sprintf("s06-refresh:%d", a.ID)
}
func (e *s06RefreshExchange) Refresh(ctx context.Context, _ *service.Account) (map[string]any, error) {
	e.calls++
	close(e.started)
	select {
	case <-e.release:
		return map[string]any{"refresh_token": "late-provider-result"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestS06RefreshPublicEntryPreservesConcurrentAdminCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := testEntClient(t)
	row, err := client.Account.Create().SetName(fmt.Sprintf("s06-refresh-public-%d", time.Now().UnixNano())).SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeOAuth).SetCredentials(map[string]any{"refresh_token": "original"}).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
	repo := &accountRepository{client: client, sql: integrationDB}
	original, err := repo.GetByID(ctx, row.ID)
	require.NoError(t, err)
	executor := &s06RefreshExchange{started: make(chan struct{}), release: make(chan struct{})}
	type result struct {
		value *service.OAuthRefreshResult
		err   error
	}
	done := make(chan result, 1)
	go func() {
		v, e := service.NewOAuthRefreshAPI(repo, nil).RefreshIfNeeded(ctx, original, executor, time.Minute)
		done <- result{v, e}
	}()
	select {
	case <-executor.started:
	case <-ctx.Done():
		t.Fatal("刷新未进入交换阶段")
	}
	require.NoError(t, repo.UpdateCredentials(ctx, row.ID, map[string]any{"refresh_token": "administrator"}))
	close(executor.release)
	var r result
	select {
	case r = <-done:
	case <-ctx.Done():
		t.Fatal("刷新未结束")
	}
	require.NoError(t, r.err)
	require.NotNil(t, r.value)
	require.False(t, r.value.Refreshed)
	require.Nil(t, r.value.NewCredentials)
	require.Equal(t, "administrator", r.value.Account.Credentials["refresh_token"])
	persisted, err := repo.GetByID(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "administrator", persisted.Credentials["refresh_token"])
	require.Equal(t, 1, executor.calls)
}
