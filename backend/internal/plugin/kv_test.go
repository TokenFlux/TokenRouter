package plugin

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// kvListFixture supplies ordered valid records through the storage page contract.
type kvListFixture struct {
	KVStore // unused persistence capability methods
	// entries is initialized before reads; returned pages share only immutable
	// JSON payloads with these ordered records for the duration of each test.
	entries []KVEntry
}

// KVList selects records after the exclusive cursor within the requested count.
// The fixture returns a new slice while sharing immutable JSON payloads. Tests
// configure sorted records before calling the capability and never mutate values.
func (f *kvListFixture) KVList(_ context.Context, _ string, input KVListInput) ([]KVEntry, error) {
	result := make([]KVEntry, 0, input.Limit)
	for _, entry := range f.entries {
		if entry.Key <= input.Cursor || !strings.HasPrefix(entry.Key, input.Prefix) {
			continue
		}
		result = append(result, entry)
		if len(result) == input.Limit {
			break
		}
	}
	return result, nil
}

// TestKVRejectsInvalidInputBeforeStorage supplies malformed namespace names, keys, JSON, conditions, and TTL values without configured persistence.
// Every invalid request fails at capability admission, so none can create database work or use a different plugin namespace accidentally.
func TestKVRejectsInvalidInputBeforeStorage(t *testing.T) {
	service := NewKV(nil, nil)
	binding := Binding{PluginID: "test.kv"}
	for _, key := range []string{"", strings.Repeat("x", 257), "control\n", "invalid\xff", "nul\x00"} {
		_, err := service.Get(t.Context(), binding, "data", key)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, collection := range []string{"", "UPPER", "slash/name", strings.Repeat("x", 65)} {
		_, err := service.Get(t.Context(), binding, collection, "key")
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, input := range []KVPutInput{
		{Collection: "data", Key: "key", Value: []byte("{")},
		{Collection: "data", Key: "key", Value: []byte(`"` + strings.Repeat("x", KVMaxValueBytes) + `"`)},
		{Collection: "data", Key: "key", Value: []byte("1"), IfVersion: -2},
		{Collection: "data", Key: "key", Value: []byte("1"), TTLSeconds: -1},
		{Collection: "data", Key: "key", Value: []byte("1"), TTLSeconds: math.MaxInt64},
	} {
		_, err := service.Put(t.Context(), binding, input)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	_, err := service.Delete(t.Context(), binding, "data", "key", -2)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = service.Incr(t.Context(), binding, "data", "key", 1, -1)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = service.List(t.Context(), binding, KVListInput{Collection: "data", Prefix: "bad\n"})
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = service.List(t.Context(), binding, KVListInput{Collection: "data", Cursor: strings.Repeat("x", 257)})
	require.ErrorIs(t, err, ErrInvalidArgument)
}

// TestKVListBoundsPayloadWithoutLosingRecords reads maximum-size JSON records
// across pages shorter than the requested item count. Every page fits the
// combined key/value budget, continuation uses its final returned key, and
// traversal returns every record exactly once.
func TestKVListBoundsPayloadWithoutLosingRecords(t *testing.T) {
	value := []byte(`"` + strings.Repeat("x", KVMaxValueBytes-2) + `"`)
	store := &kvListFixture{}
	// Three hundred maximum-size values require multiple byte-bounded pages.
	want := make([]string, 300)
	for index := range want {
		want[index] = fmt.Sprintf("%04d", index)
		store.entries = append(store.entries, KVEntry{Key: want[index], Value: value, Version: 1})
	}
	service := NewKV(store, nil)
	input := KVListInput{Collection: "data", Limit: 500}
	var observed []string
	for range want {
		page, err := service.List(t.Context(), Binding{PluginID: "test.kv"}, input)
		require.NoError(t, err)
		require.NotEmpty(t, page.Items)
		payloadBytes := 0
		for _, entry := range page.Items {
			payloadBytes += len(entry.Key) + len(entry.Value)
			require.Equal(t, value, []byte(entry.Value))
			observed = append(observed, entry.Key)
		}
		require.LessOrEqual(t, payloadBytes, KVMaxPageBytes)
		if page.NextCursor == "" {
			break
		}
		require.Less(t, len(page.Items), input.Limit)
		require.Equal(t, page.Items[len(page.Items)-1].Key, page.NextCursor)
		input.Cursor = page.NextCursor
	}
	require.Equal(t, want, observed)
}

// TestKVListCountLimitsAndExactFinalPage requests default, capped, and exact-size
// pages whose payloads fit the byte budget. Item limits remain compatible, and
// a full page containing the collection's last row has no misleading cursor.
func TestKVListCountLimitsAndExactFinalPage(t *testing.T) {
	store := &kvListFixture{}
	for index := range 500 {
		store.entries = append(store.entries, KVEntry{Key: fmt.Sprintf("%04d", index), Value: []byte("1"), Version: 1})
	}
	service := NewKV(store, nil)
	for _, limit := range []int{-1, 0, 2, 100, 500, 1000} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			page, err := service.List(t.Context(), Binding{PluginID: "test.kv"}, KVListInput{Collection: "data", Limit: limit})
			require.NoError(t, err)
			want := min(limit, 500)
			if want <= 0 {
				want = 100
			}
			require.Len(t, page.Items, want)
			if want == len(store.entries) {
				require.Empty(t, page.NextCursor)
			} else {
				require.Equal(t, page.Items[want-1].Key, page.NextCursor)
			}
		})
	}
}

// TestKVListExactPayloadBoundary fills the byte allowance exactly before adding
// one further record. An exact final page has no cursor, while the added record
// creates continuation without excluding the boundary record from its first page.
func TestKVListExactPayloadBoundary(t *testing.T) {
	// Four key bytes plus each JSON payload fill one sixty-four-KiB unit.
	value := []byte(`"` + strings.Repeat("x", KVMaxValueBytes-6) + `"`)
	store := &kvListFixture{}
	for index := range KVMaxPageBytes / KVMaxValueBytes {
		store.entries = append(store.entries, KVEntry{Key: fmt.Sprintf("%04d", index), Value: value, Version: 1})
	}
	service := NewKV(store, nil)
	input := KVListInput{Collection: "data", Limit: 500}
	page, err := service.List(t.Context(), Binding{PluginID: "test.kv"}, input)
	require.NoError(t, err)
	require.Len(t, page.Items, len(store.entries))
	require.Empty(t, page.NextCursor)
	last := store.entries[len(store.entries)-1].Key
	store.entries = append(store.entries, KVEntry{Key: "next", Value: []byte("1"), Version: 1})
	page, err = service.List(t.Context(), Binding{PluginID: "test.kv"}, input)
	require.NoError(t, err)
	require.Len(t, page.Items, len(store.entries)-1)
	require.Equal(t, last, page.NextCursor)
	input.Cursor = page.NextCursor
	page, err = service.List(t.Context(), Binding{PluginID: "test.kv"}, input)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "next", page.Items[0].Key)
	require.Empty(t, page.NextCursor)
}
