package account

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 显式有效映射不读取平台默认表，默认返回值也不能暴露外层目录的可变 map。
func TestModelMappingDefaultsAreLazyAndIsolated(t *testing.T) {
	calls := 0
	defaults := map[string]string{"alias": "default-model"}
	options := ModelMappingDefaults{Antigravity: func() map[string]string { calls++; return defaults }}
	r := &Record{Platform: PlatformAntigravity, Credentials: map[string]any{"model_mapping": map[string]any{"explicit": "target"}}}
	value := ResolveModelMapping(r, options)
	require.Zero(t, calls)
	require.Equal(t, "target", value["explicit"])
	r.Credentials = nil
	value = ResolveModelMapping(r, options)
	require.Equal(t, 1, calls)
	value["alias"] = "changed"
	require.Equal(t, "default-model", defaults["alias"])
	require.Equal(t, "default-model", ResolveModelMapping(r, options)["alias"])
}
