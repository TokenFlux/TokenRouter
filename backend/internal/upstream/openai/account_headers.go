package openai

import "net/http"

// SetChatGPTAccountHeaders 统一输出已解析的账号身份字段，不读取凭据或账号状态。
func SetChatGPTAccountHeaders(headers http.Header, id string, fedRAMP bool) {
	if headers == nil {
		return
	}
	if id != "" {
		headers.Set("chatgpt-account-id", id)
	}
	if fedRAMP {
		headers.Set("x-openai-fedramp", "true")
	} else {
		headers.Del("x-openai-fedramp")
	}
}
