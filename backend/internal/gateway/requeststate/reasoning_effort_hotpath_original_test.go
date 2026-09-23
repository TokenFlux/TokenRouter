package requeststate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractOpenAIReasoningEffortFromBody(t *testing.T) {
	tests := []struct {
		name      string
		body      []byte
		model     string
		wantNil   bool
		wantValue string
	}{
		{
			name:      "优先读取 reasoning.effort",
			body:      []byte(`{"reasoning":{"effort":"medium"}}`),
			model:     "gpt-5-high",
			wantNil:   false,
			wantValue: "medium",
		},
		{
			name:      "兼容 reasoning_effort",
			body:      []byte(`{"reasoning_effort":"x-high"}`),
			model:     "",
			wantNil:   false,
			wantValue: "xhigh",
		},
		{
			name:      "保留 max 档位",
			body:      []byte(`{"reasoning":{"effort":"max"}}`),
			model:     "gpt-5.6-sol",
			wantNil:   false,
			wantValue: "max",
		},
		{
			name:      "DeepSeek V4 保留 max 档位",
			body:      []byte(`{"reasoning":{"effort":"max"}}`),
			model:     "deepseek-v4-pro",
			wantNil:   false,
			wantValue: "max",
		},
		{
			name:    "不提取 ultra 档位",
			body:    []byte(`{"reasoning":{"effort":"ultra"}}`),
			model:   "gpt-5.6-terra",
			wantNil: true,
		},
		{
			name:      "旧模型显式 max 仍按请求值记录",
			body:      []byte(`{"reasoning":{"effort":"max"}}`),
			model:     "gpt-5.5",
			wantNil:   false,
			wantValue: "max",
		},
		{
			name:    "Luna 拒绝 ultra 档位",
			body:    []byte(`{"reasoning":{"effort":"ultra"}}`),
			model:   "gpt-5.6-luna",
			wantNil: true,
		},
		{
			name:    "minimal 归一化为空",
			body:    []byte(`{"reasoning":{"effort":"minimal"}}`),
			model:   "gpt-5-high",
			wantNil: true,
		},
		{
			name:      "缺失字段时从模型后缀推导",
			body:      []byte(`{"input":"hi"}`),
			model:     "gpt-5-high",
			wantNil:   false,
			wantValue: "high",
		},
		{
			name:    "不从 GPT-5.6 后缀推导 ultra",
			body:    []byte(`{"input":"hi"}`),
			model:   "gpt-5.6-sol-ultra",
			wantNil: true,
		},
		{
			name:    "旧模型后缀拒绝 ultra",
			body:    []byte(`{"input":"hi"}`),
			model:   "gpt-5.4-ultra",
			wantNil: true,
		},
		{
			name:    "Luna 后缀拒绝 ultra",
			body:    []byte(`{"input":"hi"}`),
			model:   "gpt-5.6-luna-ultra",
			wantNil: true,
		},
		{
			name:    "未知后缀不返回",
			body:    []byte(`{"input":"hi"}`),
			model:   "gpt-5-unknown",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractOpenAIReasoningEffortFromBody(tt.body, tt.model)
			if tt.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.wantValue, *got)
		})
	}
}

func TestValidateOpenAIReasoningEffort(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		model   string
		wantErr bool
	}{
		{name: "max 档位通过", body: []byte(`{"reasoning":{"effort":"max"}}`), model: "gpt-5.6-sol"},
		{name: "拒绝 Responses ultra", body: []byte(`{"reasoning":{"effort":"ultra"}}`), model: "gpt-5.6-sol", wantErr: true},
		{name: "拒绝 Chat Completions ultra", body: []byte(`{"reasoning_effort":"ULTRA"}`), model: "gpt-5.6-terra", wantErr: true},
		{name: "拒绝 Anthropic ultra", body: []byte(`{"output_config":{"effort":" ultra "}}`), model: "gpt-5.6-luna", wantErr: true},
		{name: "拒绝请求模型 ultra 后缀", body: []byte(`{"input":"hi"}`), model: "openai/gpt-5.6-sol-ultra", wantErr: true},
		{name: "拒绝请求体模型 ultra 后缀", body: []byte(`{"model":"gpt-5.6-terra_ultra"}`), wantErr: true},
		{name: "拒绝 WS 会话模型 ultra 后缀", body: []byte(`{"type":"session.update","session":{"model":"gpt-5.6-luna-ultra"}}`), wantErr: true},
		{name: "拒绝 WS 会话 ultra 档位", body: []byte(`{"type":"session.update","session":{"reasoning":{"effort":"ultra"}}}`), wantErr: true},
		{name: "拒绝 Realtime 响应 ultra 档位", body: []byte(`{"type":"response.create","response":{"reasoning":{"effort":"ultra"}}}`), wantErr: true},
		{name: "不误伤非 OpenAI 模型", body: []byte(`{"model":"spark-ultra"}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOpenAIReasoningEffort(tt.body, tt.model)
			if tt.wantErr {
				require.ErrorContains(t, err, "not supported")
				return
			}
			require.NoError(t, err)
		})
	}
}
