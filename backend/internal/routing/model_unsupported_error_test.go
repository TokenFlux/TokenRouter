package routing

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 原错误值的空值、截断与展示字段在移除旧类型后保持一致。
func TestGroupModelUnsupportedErrorPresentation(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var err *GroupModelUnsupportedError
		require.Empty(t, err.Error())
	})
	t.Run("empty models", func(t *testing.T) {
		err := &GroupModelUnsupportedError{RequestedModel: "  requested  "}
		require.Equal(t, `The current group does not support the requested model "requested"`, err.Error())
	})
	t.Run("bounded list does not mutate source", func(t *testing.T) {
		models := make([]string, 21)
		for i := range models {
			models[i] = fmt.Sprintf("model-%02d", i)
		}
		err := &GroupModelUnsupportedError{RequestedModel: "requested", AvailableModels: models}
		want := `The current group does not support the requested model "requested". Available models: ` + strings.Join(models[:20], ", ") + ", and 1 more"
		require.Equal(t, want, err.Error())
		require.Len(t, models, 21)
		require.Equal(t, "model-20", models[20])
	})
}
