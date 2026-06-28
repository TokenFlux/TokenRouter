package service

import "net/http"

// setOpenAIChatGPTAccountHeaders 统一补齐 ChatGPT internal API 需要的账号级请求头。
func setOpenAIChatGPTAccountHeaders(headers http.Header, account *Account) {
	if headers == nil || account == nil || !account.IsOpenAIOAuth() {
		return
	}
	if chatgptAccountID := account.GetChatGPTAccountID(); chatgptAccountID != "" {
		headers.Set("chatgpt-account-id", chatgptAccountID)
	}
	if account.IsChatGPTAccountFedRAMP() {
		headers.Set("x-openai-fedramp", "true")
	} else {
		headers.Del("x-openai-fedramp")
	}
}
