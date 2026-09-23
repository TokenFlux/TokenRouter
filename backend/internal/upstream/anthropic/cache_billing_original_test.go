//go:build unit

package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

func TestForceCacheBilling_TokenConversion(t *testing.T) {
	tests := []struct {
		name                    string
		forceCacheBilling       bool
		inputTokens             int
		cacheReadInputTokens    int
		expectedInputTokens     int
		expectedCacheReadTokens int
	}{
		{
			name:                    "force cache billing converts input to cache_read",
			forceCacheBilling:       true,
			inputTokens:             1000,
			cacheReadInputTokens:    500,
			expectedInputTokens:     0,
			expectedCacheReadTokens: 1500, // 500 + 1000
		},
		{
			name:                    "no force cache billing keeps tokens unchanged",
			forceCacheBilling:       false,
			inputTokens:             1000,
			cacheReadInputTokens:    500,
			expectedInputTokens:     1000,
			expectedCacheReadTokens: 500,
		},
		{
			name:                    "force cache billing with zero input tokens does nothing",
			forceCacheBilling:       true,
			inputTokens:             0,
			cacheReadInputTokens:    500,
			expectedInputTokens:     0,
			expectedCacheReadTokens: 500,
		},
		{
			name:                    "force cache billing with zero cache_read tokens",
			forceCacheBilling:       true,
			inputTokens:             1000,
			cacheReadInputTokens:    0,
			expectedInputTokens:     0,
			expectedCacheReadTokens: 1000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 原输入矩阵保留，实际响应报文是转换结果。
			usage := upstream.TokenUsage{
				InputTokens:          tt.inputTokens,
				CacheReadInputTokens: tt.cacheReadInputTokens,
			}

			// 同一组原断言调用实际转换函数，不在测试内复制加法实现。
			body, err := json.Marshal(struct {
				Usage upstream.TokenUsage `json:"usage"`
			}{Usage: usage})
			if err != nil {
				t.Fatal(err)
			}
			if tt.forceCacheBilling && usage.InputTokens > 0 {
				body, err = ClassifyResponseInputAsCacheRead(body, &usage)
				if err != nil {
					t.Fatal(err)
				}
			}
			usage = *anthropic.ParseClaudeUsageFromResponseBody(body)

			if usage.InputTokens != tt.expectedInputTokens {
				t.Errorf("InputTokens = %d, want %d", usage.InputTokens, tt.expectedInputTokens)
			}
			if usage.CacheReadInputTokens != tt.expectedCacheReadTokens {
				t.Errorf("CacheReadInputTokens = %d, want %d", usage.CacheReadInputTokens, tt.expectedCacheReadTokens)
			}
		})
	}
}
