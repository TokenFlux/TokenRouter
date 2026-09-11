package openai

// TextProtocol 描述普通文本请求发往上游时使用的协议。
type TextProtocol string

const (
	TextProtocolChatCompletions TextProtocol = "chat_completions"
	TextProtocolResponses       TextProtocol = "responses"
)
