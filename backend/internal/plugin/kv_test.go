package plugin

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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
