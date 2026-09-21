package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExtractOpenAIUsageFromJSONBytes_MergesHostedImageGenToolUsage(t *testing.T) {
	// 流式终态把主 usage 和生图工具 usage 分别放在 response 下。
	body := []byte(`{
		"type": "response.completed",
		"response": {
			"usage": {
				"input_tokens": 43792,
				"output_tokens": 1005,
				"total_tokens": 44797
			},
			"tool_usage": {
				"image_gen": {
					"input_tokens": 7918,
					"input_tokens_details": {"image_tokens": 7620, "text_tokens": 298},
					"output_tokens": 186,
					"output_tokens_details": {"image_tokens": 186, "text_tokens": 0},
					"total_tokens": 8104
				}
			}
		}
	}`)

	usage, ok := openai.ExtractOpenAIUsageFromJSONBytes(body)
	require.True(t, ok)
	require.Equal(t, 43792, usage.InputTokens)
	require.Equal(t, 1005, usage.OutputTokens)
	require.Equal(t, 186, usage.ImageOutputTokens)
	require.Equal(t, 7620, usage.ImageInputTokens)
}

func TestExtractOpenAIUsageFromJSONBytes_NonStreamingMergesImageGen(t *testing.T) {
	// 非流式响应在顶层返回主 usage 和生图工具 usage。
	body := []byte(`{
		"id": "resp_abc123",
		"object": "response",
		"usage": {
			"input_tokens": 5000,
			"output_tokens": 200
		},
		"tool_usage": {
			"image_gen": {
				"input_tokens": 3000,
				"input_tokens_details": {"image_tokens": 2800, "text_tokens": 200},
				"output_tokens": 150,
				"output_tokens_details": {"image_tokens": 150, "text_tokens": 0},
				"total_tokens": 3150
			}
		}
	}`)

	usage, ok := openai.ExtractOpenAIUsageFromJSONBytes(body)
	require.True(t, ok)
	require.Equal(t, 5000, usage.InputTokens)
	require.Equal(t, 200, usage.OutputTokens)
	require.Equal(t, 150, usage.ImageOutputTokens)
	require.Equal(t, 2800, usage.ImageInputTokens)
}

func TestExtractOpenAIUsageFromJSONBytes_HostedImageGenFallbackRules(t *testing.T) {
	tests := []struct {
		name            string
		body            string
		wantImageInput  int
		wantImageOutput int
	}{
		{
			name:            "without tool usage",
			body:            `{"usage":{"input_tokens":100,"output_tokens":50}}`,
			wantImageInput:  0,
			wantImageOutput: 0,
		},
		{
			name:            "base usage takes precedence",
			body:            `{"usage":{"input_tokens":100,"output_tokens":50,"input_tokens_details":{"image_tokens":20},"output_tokens_details":{"image_tokens":30}},"tool_usage":{"image_gen":{"input_tokens_details":{"image_tokens":90},"output_tokens_details":{"image_tokens":100}}}}`,
			wantImageInput:  20,
			wantImageOutput: 30,
		},
		{
			name:            "malformed tool details ignored",
			body:            `{"usage":{"input_tokens":100,"output_tokens":50},"tool_usage":{"image_gen":{"input_tokens_details":{"image_tokens":1.5},"output_tokens_details":{"image_tokens":-1}}}}`,
			wantImageInput:  0,
			wantImageOutput: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage, ok := openai.ExtractOpenAIUsageFromJSONBytes([]byte(tt.body))
			require.True(t, ok)
			require.Equal(t, tt.wantImageInput, usage.ImageInputTokens)
			require.Equal(t, tt.wantImageOutput, usage.ImageOutputTokens)
		})
	}
}

func TestMergeHostedImageGenToolUsage_EmptyImageGen(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{name: "missing", json: `{}`},
		{name: "null", json: `{"image_gen":null}`},
		{name: "not object", json: `{"image_gen":42}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := openai.ForwardUsage{InputTokens: 100, OutputTokens: 50}
			original := usage
			openai.MergeHostedImageGenToolUsage(gjson.Get(tt.json, "image_gen"), &usage)
			require.Equal(t, original, usage)
		})
	}
}

func TestParseSSEUsageBytes_ResponseCompletedWithImageGen(t *testing.T) {
	svc := &OpenAIGatewayService{}
	data := []byte(`{
		"type": "response.completed",
		"response": {
			"usage": {"input_tokens": 10000, "output_tokens": 500},
			"tool_usage": {
				"image_gen": {
					"input_tokens_details": {"image_tokens": 3800, "text_tokens": 200},
					"output_tokens_details": {"image_tokens": 186, "text_tokens": 0}
				}
			}
		}
	}`)

	usage := &openai.ForwardUsage{}
	svc.parseSSEUsageBytes(data, usage)

	require.Equal(t, 10000, usage.InputTokens)
	require.Equal(t, 500, usage.OutputTokens)
	require.Equal(t, 186, usage.ImageOutputTokens)
	require.Equal(t, 3800, usage.ImageInputTokens)
}
