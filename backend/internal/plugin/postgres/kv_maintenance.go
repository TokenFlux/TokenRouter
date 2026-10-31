package postgres

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// kv_maintenance.go measures retained namespace storage and reclaims expired
// records in bounded batches. Measurements include disabled and uninstalled
// plugins because their retained data still occupies the database. Cleanup skips
// rows locked by active mutations, preserving those operations' version checks.

// KVMeasure returns complete physical usage without requiring an installation record.
// Expired rows count until cleanup removes them. A scan failure discards the
// partial map, allowing the capability to preserve its previous successful sample.
func (s *Store) KVMeasure(ctx context.Context) (map[string]plugin.KVUsage, error) {
	rows, err := s.selectedClient(ctx).QueryContext(ctx, `SELECT plugin_id,COUNT(*),COALESCE(SUM(octet_length(value::text)+octet_length(key)),0)::bigint
FROM plugin_kv GROUP BY plugin_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	usage := make(map[string]plugin.KVUsage)
	for rows.Next() {
		var id string
		var sample plugin.KVUsage
		if err := rows.Scan(&id, &sample.Keys, &sample.Bytes); err != nil {
			return nil, err
		}
		usage[id] = sample
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return usage, nil
}

// KVExpire commits one bounded batch while skipping rows owned by active writes.
// The caller schedules subsequent batches and supplies the run budget. Locked
// expired rows remain for a later run, so cleanup never waits behind plugin work.
func (s *Store) KVExpire(ctx context.Context, limit int) (int64, error) {
	result, err := s.selectedClient(ctx).ExecContext(ctx, `WITH expired AS (
SELECT ctid FROM plugin_kv WHERE expires_at<=statement_timestamp() LIMIT $1 FOR UPDATE SKIP LOCKED
) DELETE FROM plugin_kv AS records USING expired WHERE records.ctid=expired.ctid`, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
