package openai

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ReplaceModelInBody 保留空输入、同名模型和无效 JSON 的原报文。
func ReplaceModelInBody(body []byte, newModel string) []byte {
	if len(body) == 0 {
		return body
	}
	if current := gjson.GetBytes(body, "model"); current.Exists() && current.String() == newModel {
		return body
	}
	newBody, err := sjson.SetBytes(body, "model", newModel)
	if err != nil {
		return body
	}
	return newBody
}
