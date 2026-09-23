package openai

import "github.com/tidwall/gjson"

// GenericFailedEventPayload 返回客户端可识别的通用失败事件。
func GenericFailedEventPayload(_ ...[]byte) []byte {
	return []byte(`{"type":"response.failed","response":{"error":{"message":"Upstream gateway error"}}}`)
}

// RequestPayloadView 解包 Responses WS 事件，返回实际请求对象视图。
func RequestPayloadView(body []byte) gjson.Result {
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return root
	}
	if root.Get("type").String() == "response.create" && root.Get("response").IsObject() {
		return root.Get("response")
	}
	return root
}
