package querycache

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 已发布查询值在写入、命中和 singleflight 返回边界都必须独立。
func TestQueryCacheSnapshotIsolation(t *testing.T) {
	type payload struct {
		Items []map[string]*int
		Empty []string
		At    time.Time
	}
	value := 3
	original := &payload{Items: []map[string]*int{{"value": &value}}, At: time.Now()}
	c := NewCache(time.Minute)
	c.Set("key", original)
	value = 9
	a, ok := c.Get("key")
	require.True(t, ok)
	first, ok := a.Payload.(*payload)
	require.True(t, ok)
	require.Equal(t, 3, *first.Items[0]["value"])
	require.Nil(t, first.Empty)
	require.True(t, first.At.Equal(original.At))
	*first.Items[0]["value"] = 17
	second, _ := c.Get("key")
	secondPayload, ok := second.Payload.(*payload)
	require.True(t, ok)
	require.Equal(t, 3, *secondPayload.Items[0]["value"])
}
func TestQueryCacheConcurrentLoadReturnsIndependentValues(t *testing.T) {
	c := NewCache(time.Minute)
	start := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	results := make([]map[string][]int, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e, _, err := c.GetOrLoad("key", func() (any, error) {
				once.Do(func() { close(start) })
				<-release
				return map[string][]int{"values": {1, 2}}, nil
			})
			require.NoError(t, err)
			payload, ok := e.Payload.(map[string][]int)
			require.True(t, ok)
			results[i] = payload
		}(i)
	}
	<-start
	close(release)
	wg.Wait()
	results[0]["values"][0] = 99
	for _, v := range results[1:] {
		require.Equal(t, 1, v["values"][0])
	}
	e, _ := c.Get("key")
	payload, ok := e.Payload.(map[string][]int)
	require.True(t, ok)
	require.Equal(t, 1, payload["values"][0])
}
