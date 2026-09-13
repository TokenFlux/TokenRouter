//go:build unit

// 本文件维护 httpx 的所属能力；兼容入口复用唯一实现。
package httpx

import (
	sync "sync"
	atomic "sync/atomic"
	testing "testing"
	time "time"

	require "github.com/stretchr/testify/require"
)

func TestSnapshotCache_SetAndGet(t *testing.T) {
	c := NewSnapshotCache(5 * time.Second)

	entry := c.Set("key1", map[string]string{"hello": "world"})
	require.NotEmpty(t, entry.ETag)
	require.NotNil(t, entry.Payload)

	got, ok := c.Get("key1")
	require.True(t, ok)
	require.Equal(t, entry.ETag, got.ETag)
}

func TestSnapshotCache_Expiration(t *testing.T) {
	c := NewSnapshotCache(1 * time.Millisecond)

	c.Set("key1", "value")
	time.Sleep(5 * time.Millisecond)

	_, ok := c.Get("key1")
	require.False(t, ok, "expired entry should not be returned")
}

func TestSnapshotCache_GetEmptyKey(t *testing.T) {
	c := NewSnapshotCache(5 * time.Second)
	_, ok := c.Get("")
	require.False(t, ok)
}

func TestSnapshotCache_GetMiss(t *testing.T) {
	c := NewSnapshotCache(5 * time.Second)
	_, ok := c.Get("nonexistent")
	require.False(t, ok)
}

func TestSnapshotCache_NilReceiver(t *testing.T) {
	var c *SnapshotCache
	_, ok := c.Get("key")
	require.False(t, ok)

	entry := c.Set("key", "value")
	require.Empty(t, entry.ETag)
}

func TestSnapshotCache_SetEmptyKey(t *testing.T) {
	c := NewSnapshotCache(5 * time.Second)

	// Set with empty key should return entry but not store it
	entry := c.Set("", "value")
	require.NotEmpty(t, entry.ETag)

	_, ok := c.Get("")
	require.False(t, ok)
}

func TestSnapshotCache_DefaultTTL(t *testing.T) {
	// 通过实际条目的有效期验证兼容边界，不访问已迁出的缓存内部字段。
	for _, ttl := range []time.Duration{0, -time.Second} {
		c := NewSnapshotCache(ttl)
		before := time.Now()
		entry := c.Set("default", "value")
		after := time.Now()
		require.False(t, entry.ExpiresAt.Before(before.Add(30*time.Second)))
		require.False(t, entry.ExpiresAt.After(after.Add(30*time.Second)))
	}
}

func TestSnapshotCache_ETagDeterministic(t *testing.T) {
	c := NewSnapshotCache(5 * time.Second)
	payload := map[string]int{"a": 1, "b": 2}

	entry1 := c.Set("k1", payload)
	entry2 := c.Set("k2", payload)
	require.Equal(t, entry1.ETag, entry2.ETag, "same payload should produce same ETag")
}

func TestSnapshotCache_ETagFormat(t *testing.T) {
	c := NewSnapshotCache(5 * time.Second)
	entry := c.Set("k", "test")
	// ETag should be quoted hex string: "abcdef..."
	require.True(t, len(entry.ETag) > 2)
	require.Equal(t, byte('"'), entry.ETag[0])
	require.Equal(t, byte('"'), entry.ETag[len(entry.ETag)-1])
}

func TestBuildETagFromAny_UnmarshalablePayload(t *testing.T) {
	// channels are not JSON-serializable
	etag := BuildETagFromAny(make(chan int))
	require.Empty(t, etag)
}

func TestSnapshotCache_GetOrLoad_MissThenHit(t *testing.T) {
	c := NewSnapshotCache(5 * time.Second)
	var loads atomic.Int32

	entry, hit, err := c.GetOrLoad("key1", func() (any, error) {
		loads.Add(1)
		return map[string]string{"hello": "world"}, nil
	})
	require.NoError(t, err)
	require.False(t, hit)
	require.NotEmpty(t, entry.ETag)
	require.Equal(t, int32(1), loads.Load())

	entry2, hit, err := c.GetOrLoad("key1", func() (any, error) {
		loads.Add(1)
		return map[string]string{"unexpected": "value"}, nil
	})
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, entry.ETag, entry2.ETag)
	require.Equal(t, int32(1), loads.Load())
}

func TestSnapshotCache_GetOrLoad_ConcurrentSingleflight(t *testing.T) {
	c := NewSnapshotCache(5 * time.Second)
	var loads atomic.Int32
	start := make(chan struct{})
	const callers = 8
	errCh := make(chan error, callers)

	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			<-start
			_, _, err := c.GetOrLoad("shared", func() (any, error) {
				loads.Add(1)
				time.Sleep(20 * time.Millisecond)
				return "value", nil
			})
			errCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	require.Equal(t, int32(1), loads.Load())
}

func TestParseBoolQueryWithDefault(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		def  bool
		want bool
	}{
		{"empty returns default true", "", true, true},
		{"empty returns default false", "", false, false},
		{"1", "1", false, true},
		{"true", "true", false, true},
		{"TRUE", "TRUE", false, true},
		{"yes", "yes", false, true},
		{"on", "on", false, true},
		{"0", "0", true, false},
		{"false", "false", true, false},
		{"FALSE", "FALSE", true, false},
		{"no", "no", true, false},
		{"off", "off", true, false},
		{"whitespace trimmed", "  true  ", false, true},
		{"unknown returns default true", "maybe", true, true},
		{"unknown returns default false", "maybe", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseBoolQueryWithDefault(tc.raw, tc.def)
			require.Equal(t, tc.want, got)
		})
	}
}
