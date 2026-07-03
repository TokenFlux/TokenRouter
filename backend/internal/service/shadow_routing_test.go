package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultSparkShadowModelMapping(t *testing.T) {
	mapping := defaultSparkShadowModelMapping()

	require.Len(t, mapping, 5, "spark 默认包含 base 与推理强度别名")
	require.Equal(t, "gpt-5.3-codex-spark", mapping["gpt-5.3-codex-spark"], "恒等映射：base 映射到自身")
	require.Equal(t, "gpt-5.3-codex-spark-low", mapping["gpt-5.3-codex-spark-low"], "恒等映射：low 别名映射到自身")
	require.Equal(t, "gpt-5.3-codex-spark-medium", mapping["gpt-5.3-codex-spark-medium"], "恒等映射：medium 别名映射到自身")
	require.Equal(t, "gpt-5.3-codex-spark-high", mapping["gpt-5.3-codex-spark-high"], "恒等映射：high 别名映射到自身")
	require.Equal(t, "gpt-5.3-codex-spark-xhigh", mapping["gpt-5.3-codex-spark-xhigh"], "恒等映射：xhigh 别名映射到自身")
}

func TestSparkModelVariantsDerivedFromAliases(t *testing.T) {
	got := sparkModelVariants()
	require.ElementsMatch(t, []string{
		"gpt-5.3-codex-spark",
		"gpt-5.3-codex-spark-low",
		"gpt-5.3-codex-spark-medium",
		"gpt-5.3-codex-spark-high",
		"gpt-5.3-codex-spark-xhigh",
	}, got, "spark 变体应从 codexModelMap 派生，避免默认影子映射漂移")
}
