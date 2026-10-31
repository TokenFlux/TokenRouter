package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// kv.go persists isolated JSON records with expiration-aware compare-and-set
// and integer increments. Mutations hold a transaction-scoped advisory lock for
// their full plugin/collection/key identity, including absent keys. Existing rows
// are also locked so expiration cleanup cannot remove a row between its version
// check and replacement. The replacement itself uses one conditional UPSERT.
//
// Expiration uses the database statement clock after lock acquisition. Physical
// expired rows retain their version until cleanup deletes them, while reads and
// conditional admission treat their content as absent. JSONB normalizes values;
// returned JSON is compacted and checked against the protocol size ceiling before
// a replacement commits. A normalization expansion or arithmetic overflow rolls
// back the mutation and returns a stable argument error without SQL diagnostics.

// kvState records the locked physical version and logical expiration observation.
type kvState struct {
	version int64 // retained physical row version
	live    bool  // row unexpired at observation
}

// KVGet reads one live record and returns independently owned compact JSON.
// Missing and expired keys return nil. The database clock determines visibility,
// and the returned version belongs only to a currently visible physical row.
func (s *Store) KVGet(ctx context.Context, id, collection, key string) (*plugin.KVEntry, error) {
	rows, err := s.selectedClient(ctx).QueryContext(ctx, `SELECT key,value,version FROM plugin_kv
WHERE plugin_id=$1 AND collection=$2 AND key=$3 AND (expires_at IS NULL OR expires_at>statement_timestamp())`, id, collection, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	return scanKVEntry(rows)
}

// KVPut atomically checks a logical version and replaces JSON, expiry, and version.
// The caller has validated syntax. A blocked namespace may change only live
// keys, and an expired physical row satisfies an absent-only condition.
//
// The key lock covers absent-key admission, while a row lock prevents cleanup
// from deleting an observed version before the UPSERT. Its SQL condition checks
// expiration again at replacement time. Returned JSONB is compacted and bounded
// before commit, preventing numeric normalization from producing an unreadable
// oversized value after a small scientific-notation input was accepted.
func (s *Store) KVPut(ctx context.Context, id string, input plugin.KVPutInput, blocked bool) (int64, error) {
	var version int64
	err := s.withKVKey(ctx, id, input.Collection, input.Key, func(bound context.Context, client *ent.Client, state kvState) error {
		if err := state.condition(input.IfVersion); err != nil {
			return err
		}
		if blocked && !state.live {
			return plugin.ErrKVQuotaExceeded
		}
		rows, err := client.QueryContext(bound, `INSERT INTO plugin_kv(plugin_id,collection,key,value,expires_at)
VALUES($1,$2,$3,$4::jsonb,CASE WHEN $6::bigint=0 THEN NULL ELSE statement_timestamp()+$6::bigint*INTERVAL '1 second' END)
ON CONFLICT(plugin_id,collection,key) DO UPDATE SET value=EXCLUDED.value,expires_at=EXCLUDED.expires_at,
version=plugin_kv.version+1,updated_at=statement_timestamp()
WHERE ($5::bigint=0 OR ($5=-1 AND plugin_kv.expires_at<=statement_timestamp()) OR
($5>0 AND plugin_kv.version=$5 AND (plugin_kv.expires_at IS NULL OR plugin_kv.expires_at>statement_timestamp())))
AND (NOT $7::boolean OR plugin_kv.expires_at IS NULL OR plugin_kv.expires_at>statement_timestamp())
RETURNING key,value,version`, id, input.Collection, input.Key, string(input.Value), input.IfVersion, input.TTLSeconds, blocked)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return err
			}
			if blocked && input.IfVersion <= 0 {
				return plugin.ErrKVQuotaExceeded
			}
			return plugin.ErrVersionConflict
		}
		entry, err := scanKVEntry(rows)
		if err != nil {
			return err
		}
		version = entry.Version
		return nil
	})
	return version, kvError(err)
}

// KVDelete removes an eligible physical row and reports its logical visibility.
// Expired deletion returns false. The same key and row locks used by writes
// prevent a concurrent replacement from slipping between condition checking and removal.
func (s *Store) KVDelete(ctx context.Context, id, collection, key string, wanted int64) (bool, error) {
	var deleted bool
	err := s.withKVKey(ctx, id, collection, key, func(bound context.Context, client *ent.Client, state kvState) error {
		if err := state.condition(wanted); err != nil {
			return err
		}
		rows, err := client.QueryContext(bound, `DELETE FROM plugin_kv WHERE plugin_id=$1 AND collection=$2 AND key=$3
AND ($4::bigint=0 OR ($4=-1 AND expires_at<=statement_timestamp()) OR
($4>0 AND version=$4 AND (expires_at IS NULL OR expires_at>statement_timestamp())))
RETURNING expires_at IS NULL OR expires_at>statement_timestamp()`, id, collection, key, wanted)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return err
			}
			if wanted > 0 {
				return plugin.ErrVersionConflict
			}
			return nil
		}
		return rows.Scan(&deleted)
	})
	return deleted, kvError(err)
}

// KVList reads a literal-prefix page using byte ordering for both cursor and sort.
// The caller requests an extra row when it needs continuation detection. Values
// are compacted into independent slices before the shared database cursor closes.
func (s *Store) KVList(ctx context.Context, id string, input plugin.KVListInput) ([]plugin.KVEntry, error) {
	rows, err := s.selectedClient(ctx).QueryContext(ctx, `SELECT key,value,version FROM plugin_kv
WHERE plugin_id=$1 AND collection=$2 AND starts_with(key,$3::text) AND key COLLATE "C">$4
AND (expires_at IS NULL OR expires_at>statement_timestamp()) ORDER BY key COLLATE "C" LIMIT $5`,
		id, input.Collection, input.Prefix, input.Cursor, input.Limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]plugin.KVEntry, 0)
	for rows.Next() {
		entry, err := scanKVEntry(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *entry)
	}
	return result, rows.Err()
}

// KVIncr commits an integer addition with its new expiry and physical version.
// Expired values start at zero. JSON strings, booleans, fractions, and values
// outside signed integer bounds cannot participate in arithmetic.
//
// PostgreSQL numeric comparisons establish integrality before bigint conversion,
// since a direct cast would round fractional JSON numbers. The same UPSERT
// applies addition and advances version, so concurrent increments cannot lose
// updates. Integer or version overflow aborts the statement and transaction;
// the caller receives INVALID_ARGUMENT and the original record remains intact.
func (s *Store) KVIncr(ctx context.Context, id, collection, key string, delta, ttl int64, blocked bool) (plugin.KVCounter, error) {
	var result plugin.KVCounter
	err := s.withKVKey(ctx, id, collection, key, func(bound context.Context, client *ent.Client, state kvState) error {
		if blocked && !state.live {
			return plugin.ErrKVQuotaExceeded
		}
		rows, err := client.QueryContext(bound, `INSERT INTO plugin_kv(plugin_id,collection,key,value,expires_at)
VALUES($1,$2,$3,to_jsonb($4::bigint),CASE WHEN $5::bigint=0 THEN NULL ELSE statement_timestamp()+$5::bigint*INTERVAL '1 second' END)
ON CONFLICT(plugin_id,collection,key) DO UPDATE SET
value=to_jsonb((CASE WHEN plugin_kv.expires_at<=statement_timestamp() THEN 0 ELSE (plugin_kv.value::text)::numeric::bigint END)+$4::bigint),
version=plugin_kv.version+1,expires_at=EXCLUDED.expires_at,updated_at=statement_timestamp()
WHERE (NOT $6::boolean OR plugin_kv.expires_at IS NULL OR plugin_kv.expires_at>statement_timestamp())
AND CASE WHEN plugin_kv.expires_at<=statement_timestamp() THEN TRUE
WHEN jsonb_typeof(plugin_kv.value)='number' THEN trunc((plugin_kv.value::text)::numeric)=(plugin_kv.value::text)::numeric ELSE FALSE END
RETURNING (value::text)::bigint,version`, id, collection, key, delta, ttl, blocked)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return err
			}
			if blocked {
				// Expiration can pass after admission while the row remains locked; distinguish that lost growth permission from a noninteger counter value.
				if err := rows.Close(); err != nil {
					return err
				}
				current, err := lockKVState(bound, client, id, collection, key)
				if err != nil {
					return err
				}
				if !current.live {
					return plugin.ErrKVQuotaExceeded
				}
			}
			return plugin.ErrInvalidArgument
		}
		return rows.Scan(&result.Value, &result.Version)
	})
	return result, kvError(err)
}

// withKVKey commits one mutation under its namespace key and existing-row locks.
// Nested transactions are rejected because callback results must describe a
// completed commit. The callback receives a separately observed expiration state.
//
// Advisory locking also covers absence, which ordinary row locks cannot protect.
// The following SELECT FOR UPDATE keeps cleanup from removing an observed row;
// cleanup skips those locked rows and retries on its next scheduled run. The
// callback's failed condition or SQL error rolls back before releasing either
// lock, and successful completion commits all value, version, and expiry changes.
func (s *Store) withKVKey(ctx context.Context, id, collection, key string, operation func(context.Context, *ent.Client, kvState) error) error {
	if ent.TxFromContext(ctx) != nil {
		return fmt.Errorf("plugin KV transactions cannot be nested")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	bound := ent.NewTxContext(ctx, tx)
	client := tx.Client()
	// The advisory identity covers missing rows; the row lock then excludes expiration cleanup until this mutation commits or rolls back.
	if _, err := client.ExecContext(bound, `SELECT pg_advisory_xact_lock(hashtextextended('plugin-kv:'||$1||':'||$2||':'||$3,0))`, id, collection, key); err != nil {
		return err
	}
	state, err := lockKVState(bound, client, id, collection, key)
	if err != nil {
		return err
	}
	if err := operation(bound, client, state); err != nil {
		return err
	}
	return tx.Commit()
}

// lockKVState observes one physical version while preventing concurrent cleanup deletion.
// Missing rows return zero state. Advisory ownership must already cover the key,
// because this row lock alone cannot serialize creation or conditions on absence.
func lockKVState(ctx context.Context, client *ent.Client, id, collection, key string) (kvState, error) {
	rows, err := client.QueryContext(ctx, `SELECT version,expires_at IS NULL OR expires_at>statement_timestamp()
FROM plugin_kv WHERE plugin_id=$1 AND collection=$2 AND key=$3 FOR UPDATE`, id, collection, key)
	if err != nil {
		return kvState{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return kvState{}, rows.Err()
	}
	var state kvState
	if err := rows.Scan(&state.version, &state.live); err != nil {
		return kvState{}, err
	}
	return state, nil
}

// condition evaluates unconditional, absent-only, and current-version mutation requirements.
// Expired physical rows cannot satisfy a positive version. The caller retains
// the row lock through its mutation, while SQL rechecks expiration before replacement.
func (s kvState) condition(wanted int64) error {
	if (wanted == -1 && s.live) || (wanted > 0 && (!s.live || wanted != s.version)) {
		return plugin.ErrVersionConflict
	}
	return nil
}

// scanKVEntry compacts a JSONB value into an independently owned protocol record.
// PostgreSQL can expand scientific notation, so the normalized payload is checked
// again before a mutation may commit or a read may return its JSON bytes.
func scanKVEntry(row rowScanner) (*plugin.KVEntry, error) {
	entry := &plugin.KVEntry{}
	var raw []byte
	if err := row.Scan(&entry.Key, &raw, &entry.Version); err != nil {
		return nil, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	if compact.Len() > plugin.KVMaxValueBytes {
		return nil, plugin.ErrInvalidArgument
	}
	entry.Value = compact.Bytes()
	return entry, nil
}

// kvError hides malformed JSONB, unsupported text, and numeric overflow behind argument errors.
// Unrecognized database and context errors pass through unchanged. The outer host
// mapper retains cancellation semantics and suppresses private database cause text.
func kvError(err error) error {
	if database, ok := errors.AsType[*pq.Error](err); ok {
		switch string(database.Code) {
		case "22P02", "22P05", "22003", "22008":
			return plugin.ErrInvalidArgument
		}
	}
	return err
}
