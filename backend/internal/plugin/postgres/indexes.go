package postgres

import (
	"context"
	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// indexes.go persists catalog registrations and their last accepted document.
// Refresh failures can update diagnostics while retaining the previous cache.
// Installation foreign keys become null when a custom source is removed, leaving
// accepted artifact identities available for recovery of already installed code.

const (
	// indexColumns supplies the shared ordered projection used by list, lookup, and write-returning scans of catalog registrations.
	indexColumns = `id,name,url,COALESCE(public_key,''),builtin,enabled,cached_document,cached_at,signature_status,COALESCE(last_error,''),created_at,updated_at`
)

// ListIndexes returns catalog registrations ordered by their stable database identifiers.
func (s *Store) ListIndexes(ctx context.Context) ([]plugin.IndexSource, error) {
	rows, err := s.selectedClient(ctx).QueryContext(ctx, "SELECT "+indexColumns+" FROM plugin_indexes ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]plugin.IndexSource, 0)
	for rows.Next() {
		item, err := scanIndex(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *item)
	}
	return result, rows.Err()
}

// GetIndex reads one registered source or returns ErrIndexNotFound when absent.
func (s *Store) GetIndex(ctx context.Context, id int64) (*plugin.IndexSource, error) {
	rows, err := s.selectedClient(ctx).QueryContext(ctx, "SELECT "+indexColumns+" FROM plugin_indexes WHERE id=$1", id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, plugin.ErrIndexNotFound
	}
	return scanIndex(rows)
}

// SaveIndex persists a source and replaces its caller-owned record with returned metadata.
// A zero ID creates a registration. Existing IDs must still exist; callers
// serialize changes and preserve the accepted cache when recording refresh errors.
func (s *Store) SaveIndex(ctx context.Context, item *plugin.IndexSource) error {
	var document any
	if len(item.CachedDocument) > 0 {
		document = string(item.CachedDocument)
	}
	args := []any{item.Name, item.URL, item.PublicKey, item.Builtin, item.Enabled, document, item.CachedAt, item.SignatureStatus, item.LastError}
	query := `INSERT INTO plugin_indexes (name,url,public_key,builtin,enabled,cached_document,cached_at,signature_status,last_error)
VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9) RETURNING ` + indexColumns
	if item.ID != 0 {
		query = `UPDATE plugin_indexes SET name=$1,url=$2,public_key=$3,builtin=$4,enabled=$5,cached_document=$6::jsonb,
cached_at=$7,signature_status=$8,last_error=$9,updated_at=NOW() WHERE id=$10 RETURNING ` + indexColumns
		args = append(args, item.ID)
	}
	rows, err := s.selectedClient(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return plugin.ErrIndexNotFound
	}
	saved, err := scanIndex(rows)
	if err == nil {
		*item = *saved
	}
	return err
}

// DeleteIndex removes a custom catalog while preserving installed artifact references.
// Builtin sources cannot be removed through this method. A missing or builtin
// ID returns ErrIndexNotFound without changing any registration or installation.
func (s *Store) DeleteIndex(ctx context.Context, id int64) error {
	result, err := s.selectedClient(ctx).ExecContext(ctx, "DELETE FROM plugin_indexes WHERE id=$1 AND NOT builtin", id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return plugin.ErrIndexNotFound
	}
	return err
}

// scanIndex decodes one catalog projection with its optional accepted-cache timestamp.
func scanIndex(row rowScanner) (*plugin.IndexSource, error) {
	item := &plugin.IndexSource{}
	var cachedAt sql.NullTime
	var document []byte
	err := row.Scan(
		&item.ID,
		&item.Name,
		&item.URL,
		&item.PublicKey,
		&item.Builtin,
		&item.Enabled,
		&document,
		&cachedAt,
		&item.SignatureStatus,
		&item.LastError,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if cachedAt.Valid {
		item.CachedAt = &cachedAt.Time
	}
	item.CachedDocument = document
	return item, nil
}
