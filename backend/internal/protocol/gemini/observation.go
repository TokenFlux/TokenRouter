// 本文件辨认 Gemini 已到达的协议事实，不推算金额或决定客户端重试。
package gemini

import "github.com/tidwall/gjson"

type Observation struct{ HasUsage, Semantic, Terminal bool }

func ObservePayload(payload []byte) Observation {
	value := gjson.ParseBytes(payload)
	observed := Observation{}
	usage := value.Get("usageMetadata")
	for _, field := range []string{"promptTokenCount", "candidatesTokenCount", "totalTokenCount", "cachedContentTokenCount", "thoughtsTokenCount"} {
		v := usage.Get(field)
		if v.Exists() && v.Type != gjson.Null {
			observed.HasUsage = true
		}
	}
	value.Get("candidates").ForEach(func(_, candidate gjson.Result) bool {
		if candidate.Get("finishReason").String() != "" {
			observed.Terminal = true
		}
		candidate.Get("content.parts").ForEach(func(_, part gjson.Result) bool {
			if part.Get("text").String() != "" || part.Get("functionCall.name").String() != "" || part.Get("inlineData.data").String() != "" || part.Get("inline_data.data").String() != "" || part.Get("fileData.fileUri").String() != "" {
				observed.Semantic = true
			}
			return true
		})
		return true
	})
	return observed
}
