// 删除续接引用只改该字段，保留原失败返回原报文的语义。
package openai

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// RemovePreviousResponseIDFromBody 删除请求体中的 previous_response_id，用于会话失配时改用完整 input 重建上下文。
func RemovePreviousResponseIDFromBody(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	if !gjson.GetBytes(body, "previous_response_id").Exists() {
		return body
	}
	newBody, err := sjson.DeleteBytes(body, "previous_response_id")
	if err != nil {
		return body
	}
	return newBody
}
