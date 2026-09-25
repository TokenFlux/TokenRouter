//go:build unit

package rediscache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 历史快照编码作为固定测试数据；不能使用被测编码器生成期望值。
func historicalSchedulerPayload(t *testing.T, kind string) []byte {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_") + "-" + kind + ".json"
	value, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return value
}
