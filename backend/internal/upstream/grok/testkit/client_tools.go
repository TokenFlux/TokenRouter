package testkit

import "fmt"

// ClientToolsRequest 提供跨平台解析与HTTP转发共用的原始工具报文。
func ClientToolsRequest(stream bool) []byte {
	return []byte(fmt.Sprintf(`{
		"model":"grok","stream":%t,
		"tools":[
			{"type":"custom","name":"apply_patch","description":"apply a patch","format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"}},
			{"type":"tool_search"},
			{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"send_message","description":"send a message","parameters":{"type":"object","properties":{"target":{"type":"string"}}}}]}
		],
		"tool_choice":{"type":"custom","name":"apply_patch"},
		"input":[
			{"type":"custom_tool_call","id":"old_custom","call_id":"old_custom_call","name":"apply_patch","input":"*** Begin Patch"},
			{"type":"custom_tool_call_output","call_id":"old_custom_call","output":"Done!"},
			{"type":"tool_search_call","id":"old_search","call_id":"old_search_call","arguments":{"query":"github"},"execution":"client"},
			{"type":"tool_search_output","call_id":"old_search_call","output":{"groups":["github"]}},
			{"type":"function_call","id":"old_namespace","call_id":"old_namespace_call","namespace":"collaboration","name":"send_message","arguments":"{\"target\":\"root\"}"},
			{"type":"function_call_output","call_id":"old_namespace_call","output":"ok"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}
		]
	}`, stream))
}
