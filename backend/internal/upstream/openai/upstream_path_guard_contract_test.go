package openai_test

import (
	"testing"

	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/stretchr/testify/require"
)

// 以下合同直接验证所属模块，保留原输入与断言。
func TestAppendOpenAIResponsesRequestPathSuffixRefusesUnsafeSuffix(t *testing.T) {
	// 调用方漏了校验时，拼接函数本身也不得把不合规片段带进上游 URL。
	require.Equal(t, "https://chatgpt.com/backend-api/codex/responses", upstreamopenai.AppendResponsesPathSuffix("https://chatgpt.com/backend-api/codex/responses", "/../../x"))
	require.Equal(t, "https://chatgpt.com/backend-api/codex/responses", upstreamopenai.AppendResponsesPathSuffix("https://chatgpt.com/backend-api/codex/responses", "/?a=b"))
	require.Equal(t, "https://chatgpt.com/backend-api/codex/responses"+"/compact", upstreamopenai.AppendResponsesPathSuffix("https://chatgpt.com/backend-api/codex/responses", "/compact"))
}
