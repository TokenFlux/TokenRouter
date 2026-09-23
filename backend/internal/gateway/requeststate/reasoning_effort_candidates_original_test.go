package requeststate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractOpenAIReasoningEffortFromBodyModelCandidates(t *testing.T) {
	bodyWithoutEffort := []byte(`{"model":"whatever","input":"hello"}`)
	bodyWithMax := []byte(`{"model":"sol","reasoning":{"effort":"max"},"input":"hello"}`)

	tests := []struct {
		name       string
		body       []byte
		candidates []string
		want       string // "" 表示期望 nil
	}{
		{
			name:       "后缀推导回退到原始模型（OAuth 上游模型已剥后缀）",
			body:       bodyWithoutEffort,
			candidates: []string{"gpt-5.4", "gpt-5.4", "gpt-5.4-xhigh"},
			want:       "xhigh",
		},
		{
			name:       "GPT-5.6 后缀 max 经原始模型推导保留",
			body:       bodyWithoutEffort,
			candidates: []string{"gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol-max"},
			want:       "max",
		},
		{
			name:       "显式 max 不受映射后模型能力门槛影响",
			body:       bodyWithMax,
			candidates: []string{"deepseek/deepseek-v4-flash-0731", "deepseek-v4-flash"},
			want:       "max",
		},
		{
			name:       "显式 max 对旧版 GPT 也按请求值记录",
			body:       bodyWithMax,
			candidates: []string{"gpt-5.4", "sol"},
			want:       "max",
		},
		{
			name:       "第三方模型名 max 后缀不作为显式档位推导",
			body:       bodyWithoutEffort,
			candidates: []string{"glm-4.6-max"},
			want:       "",
		},
		{
			name:       "所有候选均无后缀时返回 nil",
			body:       bodyWithoutEffort,
			candidates: []string{"gpt-5.4", "gpt-5.4", "gpt-5.4"},
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractOpenAIReasoningEffortFromBody(tt.body, tt.candidates...)
			if tt.want == "" {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.want, *got)
		})
	}
}

func TestExtractOpenAIReasoningEffortModelCandidates(t *testing.T) {
	reqBody := map[string]any{"model": "gpt-5.3-codex-high", "input": "hello"}

	got := ExtractOpenAIReasoningEffort(reqBody, "gpt-5.3-codex", "gpt-5.3-codex-high")

	require.NotNil(t, got)
	require.Equal(t, "high", *got)
}

func TestExtractOpenAIReasoningEffortMapPreservesExplicitThirdPartyMax(t *testing.T) {
	reqBody := map[string]any{
		"model": "deepseek-v4-flash",
		"reasoning": map[string]any{
			"effort": " MAX ",
		},
	}

	got := ExtractOpenAIReasoningEffort(reqBody, "deepseek/deepseek-v4-flash-0731", "deepseek-v4-flash")

	require.NotNil(t, got)
	require.Equal(t, "max", *got)
}

func TestExtractEffectiveOpenAIReasoningEffortFromBody(t *testing.T) {
	t.Run("记录最终上游改写值", func(t *testing.T) {
		got := ExtractEffectiveOpenAIReasoningEffortFromBody(
			[]byte(`{"model":"glm-5.2","reasoning_effort":"max"}`),
			[]byte(`{"model":"glm-5.2","reasoning_effort":"xhigh"}`),
			"glm-5.2",
		)

		require.NotNil(t, got)
		require.Equal(t, "max", *got)
	})

	t.Run("显式字段被转换丢弃后不从模型后缀补值", func(t *testing.T) {
		got := ExtractEffectiveOpenAIReasoningEffortFromBody(
			[]byte(`{"model":"gpt-5.6"}`),
			[]byte(`{"model":"gpt-5.6-max","reasoning":{"effort":"high"}}`),
			"gpt-5.6-max",
		)

		require.Nil(t, got)
	})

	t.Run("原请求省略字段时保留模型后缀推导", func(t *testing.T) {
		got := ExtractEffectiveOpenAIReasoningEffortFromBody(
			[]byte(`{"model":"gpt-5.6"}`),
			[]byte(`{"model":"gpt-5.6-max"}`),
			"gpt-5.6-max",
		)

		require.NotNil(t, got)
		require.Equal(t, "max", *got)
	})

	t.Run("空值按未提供处理并保留模型后缀推导", func(t *testing.T) {
		tests := []struct {
			name string
			body []byte
		}{
			{name: "空字符串", body: []byte(`{"model":"gpt-5.6-max","reasoning_effort":""}`)},
			{name: "空白字符串", body: []byte(`{"model":"gpt-5.6-max","reasoning_effort":"   "}`)},
			{name: "null", body: []byte(`{"model":"gpt-5.6-max","reasoning_effort":null}`)},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := ExtractEffectiveOpenAIReasoningEffortFromBody(
					[]byte(`{"model":"gpt-5.6"}`),
					tt.body,
					"gpt-5.6-max",
				)

				require.NotNil(t, got)
				require.Equal(t, "max", *got)
			})
		}
	})
}
