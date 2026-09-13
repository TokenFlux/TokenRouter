package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/account"
)

// CredentialRefreshStore 复用原 Ent 事务并锁定比较行，持有锁直到凭据及原 outbox 一起提交。
// Persist 接收相同事务 context；生产路径直接绑定同一 AccountStore 的凭据写入。
type CredentialRefreshStore struct {
	Client  *dbent.Client
	Persist func(context.Context, int64, map[string]any) error
}

// Apply 返回是否写入以及本调用是否已提交；加入外层事务时不发布提交后副作用。
func (s CredentialRefreshStore) Apply(ctx context.Context, version account.CredentialVersion, credentials map[string]any) (applied, committed bool, err error) {
	if s.Client == nil || s.Persist == nil {
		return false, false, errors.New("account credential refresh store is not configured")
	}
	expected := version.Credentials
	if expected == nil {
		expected = map[string]any{}
	}
	payload, err := json.Marshal(expected)
	if err != nil {
		return false, false, err
	}
	client := s.Client
	var owned *dbent.Tx
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	} else {
		owned, err = s.Client.Tx(ctx)
		if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
			return false, false, err
		}
		if owned != nil {
			defer func() { _ = owned.Rollback() }()
			ctx = dbent.NewTxContext(ctx, owned)
			client = owned.Client()
		}
	}
	rows, err := client.QueryContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL
 AND platform=$2 AND type=$3 AND status=$4 AND credentials=$5::jsonb
 AND proxy_id IS NOT DISTINCT FROM $6 FOR UPDATE`, version.ID, version.Platform, version.Type, version.Status, string(payload), version.ProxyID)
	if err != nil {
		return false, false, err
	}
	matched := rows.Next()
	scanErr := rows.Err()
	if matched {
		var id int64
		scanErr = rows.Scan(&id)
	}
	closeErr := rows.Close()
	if scanErr != nil {
		return false, false, scanErr
	}
	if closeErr != nil {
		return false, false, closeErr
	}
	if !matched {
		return false, false, nil
	}
	if err := s.Persist(ctx, version.ID, credentials); err != nil {
		return false, false, err
	}
	if owned != nil {
		if err := owned.Commit(); err != nil {
			return false, false, fmt.Errorf("commit credential refresh: %w", err)
		}
		return true, true, nil
	}
	return true, false, nil
}

// UpdateOAuthCredentialsIfUnchanged 只在本次持有的事务提交后同步快照。
func (r *AccountStore) UpdateOAuthCredentialsIfUnchanged(ctx context.Context, version account.CredentialVersion, credentials map[string]any) (bool, error) {
	applied, committed, err := (CredentialRefreshStore{Client: r.client, Persist: r.UpdateCredentials}).Apply(ctx, version, credentials)
	if err == nil && committed {
		r.afterChange(ctx, version.ID)
	}
	return applied, err
}
