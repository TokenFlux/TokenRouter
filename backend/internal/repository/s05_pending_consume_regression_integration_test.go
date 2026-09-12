//go:build integration

package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestS05PendingTransactionConsumesOnce 两个独立 PostgreSQL 事务读到未消费会话后竞争提交。
func TestS05PendingTransactionConsumesOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// 独立 Ent 包装隔离测试 hook；底层连接池仍由集成测试总拥有者关闭。
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, integrationDB)))
	session, err := client.PendingAuthSession.Create().SetSessionToken(uuid.NewString()).SetIntent("login").SetProviderType("oidc").SetProviderKey("s05-test").SetProviderSubject(uuid.NewString()).SetBrowserSessionKey("s05-browser").SetExpiresAt(time.Now().Add(time.Minute)).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, client.PendingAuthSession.DeleteOneID(session.ID).Exec(context.Background()))
	})
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	client.PendingAuthSession.Use(func(next dbent.Mutator) dbent.Mutator {
		return dbent.MutateFunc(func(ctx context.Context, m dbent.Mutation) (dbent.Value, error) {
			select {
			case arrived <- struct{}{}:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			select {
			case <-release:
				return next.Mutate(ctx, m)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
	})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			tx, e := client.Tx(ctx)
			if e != nil {
				results <- e
				return
			}
			defer func() { _ = tx.Rollback() }()
			e = identitypostgres.ConsumePendingOAuthBrowserSessionTx(dbent.NewTxContext(ctx, tx), tx, session)
			if e == nil {
				e = tx.Commit()
			}
			results <- e
		}()
	}
	for range 2 {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal("两个事务未到达消费写入屏障", ctx.Err())
		}
	}
	unblock()
	workers.Wait()
	close(results)
	success, consumed := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, service.ErrPendingAuthSessionConsumed) {
			consumed++
		} else {
			t.Errorf("非预期事务错误: %v", e)
		}
	}
	require.Equal(t, 1, success, "同一 pending session 只能由一个事务完成")
	require.Equal(t, 1, consumed, "另一个事务必须报告已消费")
}
