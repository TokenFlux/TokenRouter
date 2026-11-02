package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// installations.go stores installation intent and complete previous-version
// snapshots with handwritten SQL on the shared Ent connection. Context-carried
// transactions remain caller-owned; standalone uninstall creates a transaction
// that deletes the installation and optional KV together while retaining ledger
// history. The caller drains executable and host callback work before deletion.
//
// Manifest digests are independent of JSONB serialization, preserving the original
// package identity even though PostgreSQL normalizes stored JSON. Nonsecret and
// encrypted secret configuration travel with each version snapshot, so a failed
// update can restore removed required fields without reconstructing them from
// a new schema. Reads decode fresh containers for the manager to publish immutably.
// Saving resolves current and previous source references under database row locks;
// deleting a source removes only that relationship, including from older snapshots
// still held by an administrative operation that is preparing failure recovery.

const (
	// installationColumns keeps read projection and scanner positions identical across singular and ordered installation queries.
	installationColumns = `plugin_id, version, source, index_id, manifest, manifest_sha256, artifacts,
enabled, granted_permissions, config, COALESCE(secret_config,''), limits,
previous_version, previous_source, previous_index_id, previous_manifest,
previous_manifest_sha256, previous_artifacts, previous_granted_permissions,
previous_config, previous_secret_config, installed_at, updated_at, updated_by, ignore_host_version, previous_ignore_host_version`
)

// Store persists plugin records through a shared application Ent client.
type Store struct {
	client *ent.Client // shared application database client
}

// rowScanner abstracts the scanned projection shared by installation queries.
type rowScanner interface {
	// Scan copies the current projected row into caller-supplied destination values.
	Scan(...any) error
}

// encodedSnapshot holds SQL-ready values while preserving an absent rollback as NULL.
type encodedSnapshot struct {
	version     any // optional rollback release version
	source      any // optional rollback distribution source
	indexID     any // optional rollback index identifier
	manifest    any // serialized release declaration JSON
	digest      any // original release manifest digest
	artifacts   any // serialized platform artifact JSON
	permissions any // serialized granted permission JSON
	config      any // serialized nonsecret configuration JSON
	secret      any // encrypted secret configuration text
}

// New constructs a plugin store without opening a transaction or reading records.
func New(client *ent.Client) *Store { return &Store{client: client} }

// selectedClient joins an existing Ent transaction when the caller supplies one.
// The returned client shares that transaction's connection. Without a transaction,
// ordinary reads and writes use the store's application database client.
func (s *Store) selectedClient(ctx context.Context) *ent.Client {
	if tx := ent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return s.client
}

// List returns newly decoded installations in deterministic plugin identifier order.
func (s *Store) List(ctx context.Context) ([]plugin.Installation, error) {
	rows, err := s.selectedClient(ctx).QueryContext(ctx, "SELECT "+installationColumns+" FROM plugin_installations ORDER BY plugin_id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]plugin.Installation, 0)
	for rows.Next() {
		item, err := scanInstallation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *item)
	}
	return result, rows.Err()
}

// Get reads one installation using a context transaction when present.
// Missing rows return ErrPluginNotFound. Decoded maps belong to the returned
// record and are not shared with a previously loaded manager snapshot.
func (s *Store) Get(ctx context.Context, id string) (*plugin.Installation, error) {
	rows, err := s.selectedClient(ctx).QueryContext(ctx, "SELECT "+installationColumns+" FROM plugin_installations WHERE plugin_id=$1", id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, plugin.ErrPluginNotFound
	}
	return scanInstallation(rows)
}

// Save upserts a complete version and rollback snapshot as one database statement.
// Callers hold the manager's per-plugin mutation lock. Existing installed_at
// remains unchanged, while updated_at and updated_by describe this persisted change.
//
// Source identifiers come from accepted releases or previously stored snapshots.
// A source removed after either snapshot was read becomes a NULL relationship,
// retaining its accepted artifacts and configuration. Key-share locks protect
// surviving current and previous references until the statement or caller-owned
// transaction completes, so a concurrent deletion cannot invalidate them between
// lookup and foreign-key validation or prevent failure recovery from saving.
func (s *Store) Save(ctx context.Context, item *plugin.Installation) error {
	current, err := encodeSnapshot(&item.VersionSnapshot)
	if err != nil {
		return err
	}
	previous, err := encodeSnapshot(item.Previous)
	if err != nil {
		return err
	}
	limits := item.Limits
	if len(limits) == 0 {
		limits = []byte(`{}`)
	}
	// Resolve both references inside this write and retain their row locks through
	// persistence, allowing an already deleted source to remain detached during recovery.
	query := `INSERT INTO plugin_installations
(plugin_id,version,source,index_id,manifest,manifest_sha256,artifacts,enabled,granted_permissions,config,secret_config,limits,
previous_version,previous_source,previous_index_id,previous_manifest,previous_manifest_sha256,previous_artifacts,previous_granted_permissions,previous_config,previous_secret_config,updated_by,ignore_host_version,previous_ignore_host_version)
VALUES ($1,$2,$3,(SELECT id FROM plugin_indexes WHERE id=$4 FOR KEY SHARE),$5::jsonb,$6,$7::jsonb,$8,$9::jsonb,$10::jsonb,$11,$12::jsonb,
$13,$14,(SELECT id FROM plugin_indexes WHERE id=$15 FOR KEY SHARE),$16::jsonb,$17,$18::jsonb,$19::jsonb,$20::jsonb,$21,$22,$23,$24)
ON CONFLICT (plugin_id) DO UPDATE SET version=EXCLUDED.version,source=EXCLUDED.source,index_id=EXCLUDED.index_id,
manifest=EXCLUDED.manifest,manifest_sha256=EXCLUDED.manifest_sha256,artifacts=EXCLUDED.artifacts,enabled=EXCLUDED.enabled,
granted_permissions=EXCLUDED.granted_permissions,config=EXCLUDED.config,secret_config=EXCLUDED.secret_config,limits=EXCLUDED.limits,
previous_version=EXCLUDED.previous_version,previous_source=EXCLUDED.previous_source,previous_index_id=EXCLUDED.previous_index_id,
previous_manifest=EXCLUDED.previous_manifest,previous_manifest_sha256=EXCLUDED.previous_manifest_sha256,previous_artifacts=EXCLUDED.previous_artifacts,
previous_granted_permissions=EXCLUDED.previous_granted_permissions,previous_config=EXCLUDED.previous_config,previous_secret_config=EXCLUDED.previous_secret_config,
updated_at=NOW(),updated_by=EXCLUDED.updated_by,
ignore_host_version=EXCLUDED.ignore_host_version,previous_ignore_host_version=EXCLUDED.previous_ignore_host_version`
	_, err = s.selectedClient(ctx).ExecContext(
		ctx,
		query,
		item.PluginID,
		item.Version,
		string(item.Source),
		item.IndexID,
		current.manifest,
		item.ManifestSHA256,
		current.artifacts,
		item.Enabled,
		current.permissions,
		current.config,
		item.SecretConfig,
		string(limits),
		previous.version,
		previous.source,
		previous.indexID,
		previous.manifest,
		previous.digest,
		previous.artifacts,
		previous.permissions,
		previous.config,
		previous.secret,
		item.UpdatedBy,
		item.IgnoreHostVersion,
		item.Previous != nil && item.Previous.IgnoreHostVersion,
	)
	return err
}

// Delete removes installation intent and optional KV after the caller has drained work.
// Existing context transactions remain caller-owned. Standalone calls commit
// both deletions together, and ledger rows are deliberately outside this cleanup.
// The installation delete runs first, but it becomes visible only when optional
// KV deletion also succeeds. A failure rolls back the owned transaction, leaving
// both records available for retry. When a transaction already exists, its caller
// decides commit or rollback after this method returns and receives every error.
func (s *Store) Delete(ctx context.Context, id string, purge bool) error {
	if ent.TxFromContext(ctx) != nil {
		return s.deleteInTransaction(ctx, id, purge)
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.deleteInTransaction(ent.NewTxContext(ctx, tx), id, purge); err != nil {
		return err
	}
	// Commit installation removal and optional business-data purge together only after both statements succeed on the same owned connection.
	return tx.Commit()
}

// deleteInTransaction performs removal on its caller-owned transaction connection.
// Callers establish that transaction before entering this helper. It neither commits
// nor suppresses a deletion failure, leaving atomic completion with the transaction owner.
func (s *Store) deleteInTransaction(ctx context.Context, id string, purge bool) error {
	client := s.selectedClient(ctx)
	if _, err := client.ExecContext(ctx, "DELETE FROM plugin_installations WHERE plugin_id=$1", id); err != nil {
		return err
	}
	if purge {
		_, err := client.ExecContext(ctx, "DELETE FROM plugin_kv WHERE plugin_id=$1", id)
		return err
	}
	return nil
}

// encodeSnapshot serializes one immutable release snapshot for SQL parameters.
// A nil snapshot supplies NULL values for every previous-version column. Empty
// config and permission collections use objects and arrays instead of JSON null.
// Complete stored manifest JSON takes precedence over the decoded metadata so
// unknown future fields survive a read and save cycle. Its original release
// digest remains a separate value because PostgreSQL normalizes JSONB bytes.
// No ciphertext is decrypted here; rollback retains the same encrypted secret object.
func encodeSnapshot(snapshot *plugin.VersionSnapshot) (encodedSnapshot, error) {
	if snapshot == nil {
		return encodedSnapshot{}, nil
	}
	values := encodedSnapshot{
		version: snapshot.Version,
		source:  string(snapshot.Source),
		indexID: snapshot.IndexID,
		digest:  snapshot.ManifestSHA256,
		secret:  snapshot.SecretConfig,
	}
	var declaration any = snapshot.Manifest
	if len(snapshot.ManifestJSON) > 0 {
		declaration = snapshot.ManifestJSON
	}
	for _, field := range []struct {
		value       any  // snapshot field to serialize
		destination *any // SQL parameter value destination
	}{
		{declaration, &values.manifest}, {snapshot.Artifacts, &values.artifacts}, {snapshot.GrantedPermissions, &values.permissions}, {snapshot.Config, &values.config},
	} {
		data, err := json.Marshal(field.value)
		if err != nil {
			return encodedSnapshot{}, err
		}
		*field.destination = string(data)
	}
	if snapshot.Config == nil {
		values.config = "{}"
	}
	if snapshot.GrantedPermissions == nil {
		values.permissions = "[]"
	}
	if snapshot.Artifacts == nil {
		values.artifacts = "{}"
	}
	return values, nil
}

// scanInstallation reconstructs current and previous release state from one projection.
// PostgreSQL may normalize manifest JSON, so the original digest is read from
// its dedicated column. Configuration decoding retains PostgreSQL's decimal text.
// Previous-version presence is determined by previous_version, preserving an absent
// rollback separately from empty settings in an existing rollback snapshot. Optional
// foreign keys are reconstructed as independent pointers after index removal.
// Every decoded map belongs to this result, so later manager publication cannot
// share mutable containers with another read of the same installation.
func scanInstallation(row rowScanner) (*plugin.Installation, error) {
	item := &plugin.Installation{}
	var currentManifest, artifacts, permissions, config, limits []byte
	var previousIgnoreHostVersion bool
	var indexID, previousIndexID, updatedBy sql.NullInt64
	var previousVersion, previousSource, previousDigest, previousSecret sql.NullString
	var previousManifest, previousArtifacts, previousPermissions, previousConfig []byte
	err := row.Scan(
		&item.PluginID,
		&item.Version,
		&item.Source,
		&indexID,
		&currentManifest,
		&item.ManifestSHA256,
		&artifacts,
		&item.Enabled,
		&permissions,
		&config,
		&item.SecretConfig,
		&limits,
		&previousVersion,
		&previousSource,
		&previousIndexID,
		&previousManifest,
		&previousDigest,
		&previousArtifacts,
		&previousPermissions,
		&previousConfig,
		&previousSecret,
		&item.InstalledAt,
		&item.UpdatedAt,
		&updatedBy,
		&item.IgnoreHostVersion,
		&previousIgnoreHostVersion,
	)
	if err != nil {
		return nil, err
	}
	item.IndexID = nullableID(indexID)
	item.UpdatedBy = nullableID(updatedBy)
	item.Limits = append(json.RawMessage(nil), limits...)
	if err := decodeSnapshot(&item.VersionSnapshot, currentManifest, artifacts, permissions, config); err != nil {
		return nil, err
	}
	if previousVersion.Valid {
		item.Previous = &plugin.VersionSnapshot{
			IgnoreHostVersion: previousIgnoreHostVersion,
			Version:           previousVersion.String,
			Source:            plugin.Source(previousSource.String),
			IndexID:           nullableID(previousIndexID),
			ManifestSHA256:    previousDigest.String,
			SecretConfig:      previousSecret.String,
		}
		if err := decodeSnapshot(item.Previous, previousManifest, previousArtifacts, previousPermissions, previousConfig); err != nil {
			return nil, err
		}
	}
	return item, nil
}

// decodeSnapshot preserves stored declarations and reconstructs independently owned containers.
// The manager checks compatibility before strictly validating the complete raw
// declaration, so a future plugin contract cannot hide other installed records.
// Raw JSON is copied alongside the metadata required by compatibility checks.
// Artifacts, grants, and ordinary settings decode into separately owned containers,
// while the separately stored digest continues to identify the original release
// bytes. This decoder intentionally does not authorize execution or reinterpret
// unknown declaration fields as supported contributions.
func decodeSnapshot(snapshot *plugin.VersionSnapshot, declaration, artifacts, permissions, config []byte) error {
	var m manifest.Manifest
	if err := json.Unmarshal(declaration, &m); err != nil {
		return fmt.Errorf("decode stored plugin manifest: %w", err)
	}
	snapshot.Manifest = &m
	snapshot.ManifestJSON = bytes.Clone(declaration)
	if err := json.Unmarshal(artifacts, &snapshot.Artifacts); err != nil {
		return err
	}
	if err := json.Unmarshal(permissions, &snapshot.GrantedPermissions); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(config))
	decoder.UseNumber()
	return decoder.Decode(&snapshot.Config)
}

// nullableID returns a separately owned identifier pointer for valid SQL values.
func nullableID(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	id := value.Int64
	return &id
}
