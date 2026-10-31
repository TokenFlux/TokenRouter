//go:build integration

package postgres

import (
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// kv_scenario_integration_test.go exercises plugin/kv.go together with the
// PostgreSQL conditional writes and maintenance queries. Real transactions
// establish namespace isolation, expiration semantics, atomic increments, quota
// admission, and rollback when JSONB normalization or integer bounds fail.

// TestKVVersionsExpirationAndNamespaces replaces and expires one row while checking all supported version conditions.
// Expired physical versions advance until deletion, and other collections or plugin identities never observe or modify the selected record.
func TestKVVersionsExpirationAndNamespaces(t *testing.T) {
	store, _, db := persistenceFixture(t)
	service := plugin.NewKV(store, nil)
	binding := plugin.Binding{PluginID: "test.kv"}
	input := plugin.KVPutInput{Collection: "data", Key: "key", Value: []byte(`{"value":1}`), IfVersion: -1, TTLSeconds: 60}
	version, err := service.Put(t.Context(), binding, input)
	require.NoError(t, err)
	require.Equal(t, int64(1), version)
	_, err = service.Put(t.Context(), binding, input)
	require.ErrorIs(t, err, plugin.ErrVersionConflict)
	input.IfVersion = 2
	_, err = service.Put(t.Context(), binding, input)
	require.ErrorIs(t, err, plugin.ErrVersionConflict)
	input.IfVersion = 1
	version, err = service.Put(t.Context(), binding, input)
	require.NoError(t, err)
	require.Equal(t, int64(2), version)
	entry, err := service.Get(t.Context(), binding, "data", "key")
	require.NoError(t, err)
	require.JSONEq(t, `{"value":1}`, string(entry.Value))
	other, err := service.Get(t.Context(), plugin.Binding{PluginID: "other.kv"}, "data", "key")
	require.NoError(t, err)
	require.Nil(t, other)
	other, err = service.Get(t.Context(), binding, "other", "key")
	require.NoError(t, err)
	require.Nil(t, other)
	_, err = db.ExecContext(t.Context(), "UPDATE plugin_kv SET expires_at=statement_timestamp()-INTERVAL '1 second'")
	require.NoError(t, err)
	entry, err = service.Get(t.Context(), binding, "data", "key")
	require.NoError(t, err)
	require.Nil(t, entry)
	input.IfVersion = 2
	_, err = service.Put(t.Context(), binding, input)
	require.ErrorIs(t, err, plugin.ErrVersionConflict)
	input.IfVersion = -1
	input.TTLSeconds = 0
	version, err = service.Put(t.Context(), binding, input)
	require.NoError(t, err)
	require.Equal(t, int64(3), version)
	var persistent bool
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT expires_at IS NULL FROM plugin_kv").Scan(&persistent))
	require.True(t, persistent)
	deleted, err := service.Delete(t.Context(), binding, "data", "key", -1)
	require.ErrorIs(t, err, plugin.ErrVersionConflict)
	require.False(t, deleted)
	_, err = db.ExecContext(t.Context(), "UPDATE plugin_kv SET expires_at=statement_timestamp()-INTERVAL '1 second'")
	require.NoError(t, err)
	counter, err := service.Incr(t.Context(), binding, "data", "key", 5, 0)
	require.NoError(t, err)
	require.Equal(t, plugin.KVCounter{Value: 5, Version: 4}, counter)
	deleted, err = service.Delete(t.Context(), binding, "data", "key", 4)
	require.NoError(t, err)
	require.True(t, deleted)
	deleted, err = service.Delete(t.Context(), binding, "data", "key", -1)
	require.NoError(t, err)
	require.False(t, deleted)
	_, err = service.Delete(t.Context(), binding, "data", "key", 4)
	require.ErrorIs(t, err, plugin.ErrVersionConflict)
}

// TestKVConcurrentIncrementsAndCompareAndSet sends one hundred increments and then races forty writes against one observed version.
// Every increment contributes once, while exactly one matching-version replacement succeeds and all other callers receive a conflict.
func TestKVConcurrentIncrementsAndCompareAndSet(t *testing.T) {
	store, _, db := persistenceFixture(t)
	// Keep all one hundred callers concurrent while queueing database admission inside a pool smaller than the fixture server's connection ceiling.
	db.SetMaxOpenConns(16)
	service := plugin.NewKV(store, nil)
	binding := plugin.Binding{PluginID: "test.kv"}
	failures := make(chan error, 100)
	var callers sync.WaitGroup
	for range 100 {
		callers.Go(func() {
			_, err := service.Incr(t.Context(), binding, "counts", "visits", 1, 0)
			failures <- err
		})
	}
	callers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	entry, err := service.Get(t.Context(), binding, "counts", "visits")
	require.NoError(t, err)
	require.Equal(t, "100", string(entry.Value))
	require.Equal(t, int64(100), entry.Version)
	var changed, conflicts atomic.Int64
	failures = make(chan error, 40)
	for range 40 {
		callers.Go(func() {
			_, err := service.Put(t.Context(), binding, plugin.KVPutInput{Collection: "counts", Key: "visits", Value: []byte("200"), IfVersion: 100})
			switch err {
			case nil:
				changed.Add(1)
			case plugin.ErrVersionConflict:
				conflicts.Add(1)
			default:
				failures <- err
			}
		})
	}
	callers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.Equal(t, int64(1), changed.Load())
	require.Equal(t, int64(39), conflicts.Load())
}

// TestKVIntegerValidationAndNormalizationRollback increments integral decimal notation while rejecting fractions, other JSON types, and overflow.
// Failed writes leave both data and version unchanged, including scientific notation that expands beyond the protocol's payload limit inside JSONB.
func TestKVIntegerValidationAndNormalizationRollback(t *testing.T) {
	store, _, _ := persistenceFixture(t)
	service := plugin.NewKV(store, nil)
	binding := plugin.Binding{PluginID: "test.kv"}
	for _, value := range []string{"1.0", "1e2", "-2", "9223372036854775807", "-9223372036854775808", "1.5", "1e30", `"1"`, "true", "null", "{}", "[]"} {
		t.Run(value, func(t *testing.T) {
			version, err := service.Put(t.Context(), binding, plugin.KVPutInput{Collection: "integers", Key: value, Value: []byte(value)})
			require.NoError(t, err)
			delta := int64(1)
			if value == "-9223372036854775808" {
				delta = -1
			}
			counter, err := service.Incr(t.Context(), binding, "integers", value, delta, 0)
			if value == "1.0" || value == "1e2" || value == "-2" {
				require.NoError(t, err)
				require.Equal(t, version+1, counter.Version)
				return
			}
			require.ErrorIs(t, err, plugin.ErrInvalidArgument)
			entry, err := service.Get(t.Context(), binding, "integers", value)
			require.NoError(t, err)
			require.Equal(t, version, entry.Version)
		})
	}
	for _, value := range []string{"1e100000", `"\u0000"`} {
		_, err := service.Put(t.Context(), binding, plugin.KVPutInput{Collection: "invalid", Key: "value", Value: []byte(value)})
		require.ErrorIs(t, err, plugin.ErrInvalidArgument)
		entry, err := service.Get(t.Context(), binding, "invalid", "value")
		require.NoError(t, err)
		require.Nil(t, entry)
	}
}

// TestKVLiteralPrefixPaginationAndCleanup lists wildcard-shaped keys through multiple exclusive pages and then expires selected rows.
// Prefix matching remains literal, ordering is stable across pages, and bounded cleanup deletes only expired rows from all retained namespaces.
func TestKVLiteralPrefixPaginationAndCleanup(t *testing.T) {
	store, _, db := persistenceFixture(t)
	service := plugin.NewKV(store, nil)
	binding := plugin.Binding{PluginID: "test.kv"}
	for _, key := range []string{"%a", "%b", "%é", "_literal", "z", "中"} {
		_, err := service.Put(t.Context(), binding, plugin.KVPutInput{Collection: "list", Key: key, Value: []byte("1")})
		require.NoError(t, err)
	}
	page, err := service.List(t.Context(), binding, plugin.KVListInput{Collection: "list", Prefix: "%", Limit: 2})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.Equal(t, "%a", page.Items[0].Key)
	require.Equal(t, "%b", page.NextCursor)
	page, err = service.List(t.Context(), binding, plugin.KVListInput{Collection: "list", Prefix: "%", Cursor: page.NextCursor, Limit: 2})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "%é", page.Items[0].Key)
	require.Empty(t, page.NextCursor)
	_, err = db.ExecContext(t.Context(), "UPDATE plugin_kv SET expires_at=statement_timestamp()-INTERVAL '1 second' WHERE starts_with(key,'%')")
	require.NoError(t, err)
	page, err = service.List(t.Context(), binding, plugin.KVListInput{Collection: "list", Prefix: "%"})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	count, err := store.KVExpire(t.Context(), 2)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	count, err = store.KVExpire(t.Context(), 2)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	usage, err := store.KVMeasure(t.Context())
	require.NoError(t, err)
	require.Equal(t, int64(3), usage[binding.PluginID].Keys)
	require.Equal(t, int64(len("_literal")+len("z")+len("中")+3), usage[binding.PluginID].Bytes)
}

// TestKVSampledQuotaBlocksOnlyNamespaceGrowth fills a namespace to its key ceiling and publishes the database measurement.
// Existing records remain editable and deletable, new keys fail, and a fresh sample after deletion allows growth again without affecting another plugin.
func TestKVSampledQuotaBlocksOnlyNamespaceGrowth(t *testing.T) {
	store, _, db := persistenceFixture(t)
	_, err := db.ExecContext(t.Context(), `INSERT INTO plugin_kv(plugin_id,collection,key,value)
SELECT 'test.kv','quota',number::text,'1'::jsonb FROM generate_series(1,$1) AS number`, plugin.KVMaxKeys)
	require.NoError(t, err)
	// Individually valid values exceed the byte ceiling with far fewer rows than the separate key-count allowance permits.
	_, err = db.ExecContext(t.Context(), `INSERT INTO plugin_kv(plugin_id,collection,key,value)
SELECT 'bytes.kv','quota',number::text,to_jsonb(repeat('x',65000)) FROM generate_series(1,$1) AS number`, plugin.KVMaxBytes/65000+1)
	require.NoError(t, err)
	service := plugin.NewKV(store, nil)
	require.NoError(t, service.RefreshUsage(t.Context()))
	binding := plugin.Binding{PluginID: "test.kv"}
	input := plugin.KVPutInput{Collection: "quota", Key: "new", Value: []byte("2")}
	_, err = service.Put(t.Context(), plugin.Binding{PluginID: "bytes.kv"}, input)
	require.ErrorIs(t, err, plugin.ErrKVQuotaExceeded)
	_, err = service.Put(t.Context(), binding, input)
	require.ErrorIs(t, err, plugin.ErrKVQuotaExceeded)
	_, err = service.Incr(t.Context(), binding, "quota", "new-counter", 1, 0)
	require.ErrorIs(t, err, plugin.ErrKVQuotaExceeded)
	input.Key = "1"
	_, err = service.Put(t.Context(), binding, input)
	require.NoError(t, err)
	_, err = service.Incr(t.Context(), binding, "quota", "1", 1, 0)
	require.NoError(t, err)
	_, err = service.Put(t.Context(), plugin.Binding{PluginID: "other.kv"}, input)
	require.NoError(t, err)
	for number := 1; number <= 2; number++ {
		deleted, err := service.Delete(t.Context(), binding, "quota", strconv.Itoa(number), 0)
		require.NoError(t, err)
		require.True(t, deleted)
	}
	require.NoError(t, service.RefreshUsage(t.Context()))
	input.Key = "new"
	_, err = service.Put(t.Context(), binding, input)
	require.NoError(t, err)
	usage, err := store.KVMeasure(t.Context())
	require.NoError(t, err)
	require.Equal(t, int64(plugin.KVMaxKeys-1), usage[binding.PluginID].Keys, fmt.Sprint(usage))
}
