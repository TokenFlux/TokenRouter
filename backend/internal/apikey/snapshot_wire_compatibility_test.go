package apikey

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestV40OriginalHEADWire 使用旧 HEAD 类型实际序列化的完整/空集合报文，检测迁包造成的字段丢失或形状漂移。
func TestV40OriginalHEADWire(t *testing.T) {
	for _, kind := range []string{"full", "empty"} {
		t.Run(kind, func(t *testing.T) {
			original, e := os.ReadFile("testdata/v40-" + kind + ".json")
			require.NoError(t, e)
			var entry APIKeyAuthCacheEntry
			require.NoError(t, json.Unmarshal(original, &entry))
			require.Equal(t, 40, entry.Snapshot.Version)
			actual, e := json.Marshal(entry)
			require.NoError(t, e)
			require.JSONEq(t, string(original), string(actual))
		})
	}
}
