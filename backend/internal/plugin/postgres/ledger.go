package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// ledger.go serializes each plugin's write-capability decisions with a PostgreSQL
// transaction-scoped advisory lock. Replay lookup, daily quota totals, business
// effects, and ledger insertion share the same Ent connection through the context
// supplied to the callback. A failed business operation or ledger insert rolls
// back every write before another caller can inspect that plugin's quota usage.
//
// The transaction adapter exposes only its bound plugin namespace. Ledger rows
// survive installation deletion and retain exact numeric amounts and JSON intent.
// Email claims use the same quota lock while external delivery occurs later;
// pending and uncertain claims therefore remain part of quota totals. Callers
// provide the accounting timestamp after acquiring the lock so a wait across
// midnight cannot assign quota checks and ledger insertion to different days.

// ledgerTransaction binds reads and writes to one locked plugin database transaction.
type ledgerTransaction struct {
	client   *ent.Client // transaction-owned shared Ent connection
	pluginID string      // exclusively locked plugin namespace
}

// Transact commits one callback's business writes and ledger changes under a plugin lock.
// The callback must propagate errors and pass its supplied context to business
// stores. Existing outer transactions are rejected because this method owns the
// commit boundary used to decide when post-commit effects may run.
//
// Advisory locking precedes replay and quota reads, serializing identical keys
// and independent spending requests for this plugin. The callback's Ent context
// makes existing billing adapters join that same connection. Deferred rollback
// releases all partial effects on callback failure; a successful commit ends both
// the database transaction and its advisory lock before the method returns.
func (s *Store) Transact(ctx context.Context, id string, operation func(context.Context, plugin.LedgerTransaction) error) error {
	if ent.TxFromContext(ctx) != nil {
		return fmt.Errorf("plugin ledger transactions cannot be nested")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	bound := ent.NewTxContext(ctx, tx)
	client := tx.Client()
	if _, err := client.ExecContext(bound, "SELECT pg_advisory_xact_lock(hashtext('plugin-ledger:' || $1)::bigint)", id); err != nil {
		return err
	}
	if err := operation(bound, &ledgerTransaction{client: client, pluginID: id}); err != nil {
		return err
	}
	// Publish business effects and replay evidence together; callers may invalidate caches or record auxiliary history only after this commit succeeds.
	return tx.Commit()
}

// Find reads a prior operation from the transaction's locked plugin namespace.
// Missing keys return nil without an error. JSON bytes and nullable user identity
// are copied into the result before the database cursor is closed.
func (t *ledgerTransaction) Find(ctx context.Context, key string) (*plugin.LedgerEntry, error) {
	rows, err := t.client.QueryContext(ctx, `SELECT plugin_id,idempotency_key,kind,user_id,amount::text,request,result,state,reason,created_at
FROM plugin_ledger WHERE plugin_id=$1 AND idempotency_key=$2`, t.pluginID, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	entry := &plugin.LedgerEntry{}
	var userID sql.NullInt64
	var request, result []byte
	if err := rows.Scan(&entry.PluginID, &entry.Key, &entry.Kind, &userID, &entry.Amount, &request, &result, &entry.State, &entry.Reason, &entry.CreatedAt); err != nil {
		return nil, err
	}
	entry.UserID = nullableID(userID)
	entry.Request = request
	entry.Result = result
	return entry, nil
}

// Totals returns exact accepted amounts and counts inside the requested calendar window.
// Optional user and redeem-type filters narrow the same bound plugin namespace.
// All recorded states count, preserving quota consumption for uncertain email claims.
func (t *ledgerTransaction) Totals(ctx context.Context, query plugin.LedgerQuery) (plugin.LedgerTotals, error) {
	rows, err := t.client.QueryContext(ctx, `SELECT COALESCE(SUM(amount),0)::text,COUNT(*)
FROM plugin_ledger WHERE plugin_id=$1 AND kind=$2 AND created_at >= $3 AND created_at < $4
AND ($5::bigint=0 OR user_id=$5) AND ($6::text='' OR request->>'type'=$6)`,
		t.pluginID, string(query.Kind), query.Since, query.Until, query.UserID, query.Subtype)
	if err != nil {
		return plugin.LedgerTotals{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return plugin.LedgerTotals{}, err
		}
		return plugin.LedgerTotals{}, fmt.Errorf("plugin ledger aggregate returned no row")
	}
	var result plugin.LedgerTotals
	if err := rows.Scan(&result.Amount, &result.Count); err != nil {
		return plugin.LedgerTotals{}, err
	}
	return result, nil
}

// Insert stores a complete operation under the transaction's already acquired plugin lock.
// A mismatched namespace or missing accounting time fails before SQL execution.
// The unique replay key also protects against writers that bypass this adapter.
func (t *ledgerTransaction) Insert(ctx context.Context, entry *plugin.LedgerEntry) error {
	if entry.PluginID != t.pluginID || entry.CreatedAt.IsZero() {
		return fmt.Errorf("plugin ledger entry does not match its transaction")
	}
	_, err := t.client.ExecContext(ctx, `INSERT INTO plugin_ledger
(plugin_id,idempotency_key,kind,user_id,amount,request,result,state,reason,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5::numeric,$6::jsonb,$7::jsonb,$8,$9,$10,$10)`,
		entry.PluginID, entry.Key, string(entry.Kind), entry.UserID, entry.Amount, string(entry.Request), string(entry.Result), string(entry.State), entry.Reason, entry.CreatedAt)
	return err
}
