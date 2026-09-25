package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
)

// OpenAIAuxiliary 执行搜索、嵌入和计数单次请求；准入、选择与完成处理由各入口拥有。
type OpenAIAuxiliary struct {
	Requests      *OpenAIRequests
	Output        *OpenAIResponseOutput
	CodexUsage    *accountprovider.CodexUsageObserver
	Authorization *account.OpenAIAuthorization
	Enter         func() (func(), error)
}

const openaiPlatformAPIInputTokensURL = "https://api.openai.com/v1/responses/input_tokens"
