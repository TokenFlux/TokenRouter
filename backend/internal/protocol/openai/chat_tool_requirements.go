package openai

import (
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// chat_tool_requirements.go detects ordinary function-tool use in Chat
// requests. Selection reads the result before choosing a provider, so the
// parser reports only the required tool kind and leaves the request body
// available for forwarding.

// ChatUsesFunctionTools reports whether an admitted JSON body needs ordinary
// function tools. It accepts modern declarations and historical calls as well
// as legacy functions, named selectors, and legacy function results. Custom
// tool use alone leaves the result false. Callers validate the request shape.
func ChatUsesFunctionTools(body []byte) bool {
	var messages gjson.Result
	required := false
	// Read the top-level object once and defer history checks until declaration
	// fields have had a chance to identify the required tool kind.
	wirejson.ParseView(body).ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "tools":
			required = value.Get(`#(type=="function")`).Exists()
		case "functions":
			required = value.Get("#").Int() > 0
		case "tool_choice":
			required = value.Get("type").String() == "function"
		case "function_call":
			required = value.Get("name").String() != ""
		case "messages":
			messages = value
		}
		return !required
	})
	if required || !messages.IsArray() {
		return required
	}
	messages.ForEach(func(_, message gjson.Result) bool {
		required = message.Get("role").String() == "function" ||
			message.Get("function_call.name").String() != "" ||
			message.Get(`tool_calls.#(type=="function")`).Exists()
		return !required
	})
	return required
}
