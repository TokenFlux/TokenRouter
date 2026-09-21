package capability

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAIReasoningEffortForGPT56(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		model string
		want  string
	}{
		{name: "Sol 保留 max", raw: "max", model: "gpt-5.6-sol", want: "max"},
		{name: "Terra 保留 max", raw: "max", model: "openai/gpt-5.6-terra", want: "max"},
		{name: "Luna 后缀保留 max", raw: "max", model: "gpt-5.6-luna-2026-07-09", want: "max"},
		{name: "DeepSeek V4 保留 max", raw: "max", model: "deepseek-v4-pro", want: "max"},
		{name: "GLM 保留 max", raw: "max", model: "glm-5.2", want: "max"},
		{name: "旧模型拒绝 max 档位", raw: "max", model: "gpt-5.5", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, NormalizeRecordedOpenAIEffortForModel(tt.raw, tt.model))
		})
	}
}
