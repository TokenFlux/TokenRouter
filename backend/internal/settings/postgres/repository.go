package postgres

import (
	"context"
	"sort"
	"time"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/predicate"
	"github.com/TokenFlux/TokenRouter/ent/setting"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// repository.go persists raw settings and conditionally replaces batches after
// checking prior values.
// Settings keys store raw strings and distinguish a saved empty value from an
// absent record on ordinary reads. Single and bulk upserts use the key conflict
// target to replace values, with one captured update time for a bulk write.
// Modules decide how those stored strings become effective configuration.
//
// Conditional writes prepare missing expected rows as empty placeholders, then
// lock expected keys in sorted order before comparing values. A nil expectation
// therefore accepts absence or a stored empty string. Comparisons and subsequent
// upserts share one transaction, so a conflict or write failure follows rollback
// cleanup and cannot commit a partial settings batch. A successful expected-only
// placeholder can remain as an empty saved record.

// settingRepository implements raw-value settings storage through its bound Ent client.
type settingRepository struct {
	// client is retained from construction for every read and write. Its owner
	// manages the connection lifecycle; conditional updates start a transaction on
	// this client, while ordinary methods can use an already transactional client.
	client *ent.Client
}

// NewSettingRepository binds raw setting operations to the supplied Ent client.
// The caller owns that client and its database lifecycle; the returned implementation
// also supports conditional updates through the ConditionalRepository interface.
func NewSettingRepository(client *ent.Client) settings.Repository {
	return &settingRepository{client: client}
}

// Get reads one record by its setting key and returns a new core value.
// Ent absence becomes ErrSettingNotFound, while other query failures remain errors
// for the module deciding its setting default.
func (r *settingRepository) Get(ctx context.Context, key string) (*settings.Setting, error) {
	m, err := r.client.Setting.Query().Where(setting.KeyEQ(key)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// Keep storage absence distinct from a saved empty value so the domain
			// reader can choose its missing-setting default while retaining other query failures.
			return nil, settings.ErrSettingNotFound
		}
		return nil, err
	}
	return &settings.Setting{
		ID:        m.ID,
		Key:       m.Key,
		Value:     m.Value,
		UpdatedAt: m.UpdatedAt,
	}, nil
}

// GetValue returns the raw string from Get for the supplied key. A saved empty
// string succeeds, while an absent record returns the same settings not-found
// error used by the record reader.
func (r *settingRepository) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

// Set upserts a raw value by key and records the current update time. The
// call does not validate module policy or publish notifications; its caller performs
// those phases around persistence completion.
func (r *settingRepository) Set(ctx context.Context, key, value string) error {
	now := time.Now()
	return r.client.Setting.
		Create().
		SetKey(key).
		SetValue(value).
		SetUpdatedAt(now).
		OnConflictColumns(setting.FieldKey).
		UpdateNewValues().
		Exec(ctx)
}

// GetMultiple returns a new map containing existing records among the supplied
// keys. Missing keys are omitted, and an empty key list returns an empty map before
// any database query is constructed.
func (r *settingRepository) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}
	settings, err := r.client.Setting.Query().Where(setting.KeyIn(keys...)).All(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

// SetMultiple upserts all supplied settings through one bulk operation using a
// shared update timestamp. An empty map succeeds before database work; a rejected
// row aborts the bulk statement, leaving the completed batch atomic.
func (r *settingRepository) SetMultiple(ctx context.Context, settings map[string]string) error {
	if len(settings) == 0 {
		return nil
	}

	now := time.Now()
	builders := make([]*ent.SettingCreate, 0, len(settings))
	for key, value := range settings {
		builders = append(builders, r.client.Setting.Create().SetKey(key).SetValue(value).SetUpdatedAt(now))
	}
	return r.client.Setting.
		CreateBulk(builders...).
		OnConflictColumns(setting.FieldKey).
		UpdateNewValues().
		Exec(ctx)
}

// GetAll reads every saved setting into a newly allocated key/value map.
// Callers can change that returned map independently of saved records, while
// the owning modules remain responsible for interpreting its raw string values.
func (r *settingRepository) GetAll(ctx context.Context) (map[string]string, error) {
	settings, err := r.client.Setting.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

// GetConfigurationValues reads administrator configuration rows while excluding
// operational notification delivery, locale, and preference prefixes in SQL.
// Callers receive an independent map without loading unbounded mail-tracking rows.
func (r *settingRepository) GetConfigurationValues(ctx context.Context) (map[string]string, error) {
	query := r.client.Setting.Query()
	if len(settings.NonConfigurationKeyPrefixes) > 0 {
		// Push operational-prefix exclusion into the SQL predicate so composite
		// settings reads do not transfer historical email delivery and locale rows.
		excluded := make([]predicate.Setting, 0, len(settings.NonConfigurationKeyPrefixes))
		for _, prefix := range settings.NonConfigurationKeyPrefixes {
			excluded = append(excluded, setting.KeyHasPrefix(prefix))
		}
		query = query.Where(setting.Not(setting.Or(excluded...)))
	}
	rows, err := query.All(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(rows))
	for _, row := range rows {
		result[row.Key] = row.Value
	}
	return result, nil
}

// Delete removes the supplied setting key through the bound client. An already
// absent record succeeds because the affected-row count is discarded; callers
// handle runtime defaults and publication separately after this storage operation.
func (r *settingRepository) Delete(ctx context.Context, key string) error {
	_, err := r.client.Setting.Delete().Where(setting.KeyEQ(key)).Exec(ctx)
	return err
}

// CompareAndSetMultiple compares expected raw values and commits the supplied
// write batch within one newly started transaction. Expected keys are sorted
// before placeholder insertion and row locking, giving overlapping expectation
// sets a consistent lock acquisition order.
//
// A missing expected row is inserted with an empty value before comparison.
// Nil expectations accept that placeholder or an existing empty value; nonnil
// expectations require the same text, and mismatch returns locale.ErrConflict.
// After all comparisons, values are upserted and committed together. Early returns
// attempt rollback through cleanup; successful expected-only placeholders can
// remain even when values contains no write for those keys.
func (r *settingRepository) CompareAndSetMultiple(ctx context.Context, values map[string]string, expected map[string]*string) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	// Attempt rollback on every early return before commit; cleanup after a
	// completed commit can report an already-finished transaction and its error is discarded.
	defer func() { _ = tx.Rollback() }()
	keys := make([]string, 0, len(expected))
	for key := range expected {
		keys = append(keys, key)
	}
	// Lock expected keys in stable order so requests comparing overlapping
	// records do not acquire those expectation locks in opposite map-iteration orders.
	sort.Strings(keys)
	// Insert empty placeholders before row locking so absent expected keys have
	// a record to lock and compare within this same conditional-write transaction.
	for _, key := range keys {
		if _, err := tx.Client().ExecContext(ctx, "INSERT INTO settings (key,value,updated_at) VALUES ($1,'',now()) ON CONFLICT (key) DO NOTHING", key); err != nil {
			return err
		}
		current, err := tx.Setting.Query().Where(setting.KeyEQ(key)).ForUpdate().Only(ctx)
		if err != nil {
			return err
		}
		prior := expected[key]
		if prior == nil && current.Value != "" || prior != nil && current.Value != *prior {
			return locale.ErrConflict
		}
	}
	// Keep every supplied value in the transaction after all expected values
	// match, so a later upsert failure cannot commit sibling configuration changes.
	now := time.Now()
	for key, value := range values {
		if err := tx.Setting.Create().SetKey(key).SetValue(value).SetUpdatedAt(now).OnConflictColumns(setting.FieldKey).UpdateNewValues().Exec(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
}
