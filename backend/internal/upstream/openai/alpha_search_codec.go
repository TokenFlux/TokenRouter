// Alpha Search 的不透明请求和 Responses 回退转换保留原结构、截断和引用顺序。
package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

func BuildOpenAIAlphaSearchResponsesWebSearchBody(alphaBody []byte, model string) ([]byte, error) {
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	tool := map[string]any{"type": "web_search"}
	if contextSize := strings.TrimSpace(gjson.GetBytes(alphaBody, "settings.search_context_size").String()); contextSize != "" {
		tool["search_context_size"] = contextSize
	}
	if userLocation := gjson.GetBytes(alphaBody, "settings.user_location"); userLocation.IsObject() {
		var loc map[string]any
		if err := json.Unmarshal([]byte(userLocation.Raw), &loc); err == nil && len(loc) > 0 {
			tool["user_location"] = loc
		}
	}
	payload := map[string]any{
		"model":  model,
		"stream": true,
		"store":  false,
		"input": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": OpenAIAlphaSearchResponsesWebSearchPrompt(alphaBody),
					},
				},
			},
		},
		"tools": []any{tool},
	}
	return json.Marshal(payload)
}

func OpenAIAlphaSearchResponsesWebSearchPrompt(alphaBody []byte) string {
	var b strings.Builder
	_, _ = b.WriteString("Execute this Codex standalone web.run request for another model.\n")
	_, _ = b.WriteString("Use the hosted web_search tool when web/current information is needed.\n")
	_, _ = b.WriteString("Return concise source-backed results. Include titles, URLs, dates, and direct answers when available.\n")
	if commands := strings.TrimSpace(gjson.GetBytes(alphaBody, "commands").Raw); commands != "" {
		_, _ = b.WriteString("\nCommands JSON:\n")
		_, _ = b.WriteString(TruncateOpenAIAlphaSearchPromptJSON(commands, 12000))
	}
	if settings := strings.TrimSpace(gjson.GetBytes(alphaBody, "settings").Raw); settings != "" {
		_, _ = b.WriteString("\n\nSearch settings JSON:\n")
		_, _ = b.WriteString(TruncateOpenAIAlphaSearchPromptJSON(settings, 4000))
	}
	if input := strings.TrimSpace(gjson.GetBytes(alphaBody, "input").Raw); input != "" {
		_, _ = b.WriteString("\n\nRecent conversation/input JSON:\n")
		_, _ = b.WriteString(TruncateOpenAIAlphaSearchPromptJSON(input, 8000))
	}
	if b.Len() == 0 {
		return "Execute the requested web search and return concise source-backed results."
	}
	return b.String()
}

func TruncateOpenAIAlphaSearchPromptJSON(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "\n...<truncated>"
}

var openAIAlphaSearchUnsupportedBodyFields = [...]string{
	// Codex alpha/search 是 SearchRequest 独立协议，不是 /responses 子请求。
	// 新版 Codex/第三方代理可能把 Responses 公共字段误带到搜索请求里；ChatGPT
	// alpha/search 会对这些字段返回 Unknown parameter（例如 prompt_cache_key）。
	"prompt_cache_key",
	"prompt_cache_retention",
	"store",
}

func SanitizeOpenAIAlphaSearchBody(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return body, nil
	}
	changed := false
	for _, field := range openAIAlphaSearchUnsupportedBodyFields {
		if _, ok := obj[field]; ok {
			delete(obj, field)
			changed = true
		}
	}
	if !changed {
		return body, nil
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func OpenAIAlphaSearchResponseFromResponsesSSE(body []byte) ([]byte, error) {
	output, results := ParseOpenAIResponsesSSEForAlphaSearch(body)
	resp := map[string]any{
		"output": output,
	}
	if len(results) > 0 {
		resp["results"] = results
	}
	return json.Marshal(resp)
}

func ParseOpenAIResponsesSSEForAlphaSearch(body []byte) (string, []any) {
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	var output strings.Builder
	var completedResponse any
	results := make([]any, 0)
	seenURLs := make(map[string]struct{})

	for _, block := range strings.Split(text, "\n\n") {
		data := OpenAIAlphaSearchSSEData(block)
		if data == "" || data == "[DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		if delta, _ := event["delta"].(string); delta != "" && event["type"] == "response.output_text.delta" {
			_, _ = output.WriteString(delta)
		}
		if event["type"] == "response.completed" {
			completedResponse = event["response"]
		}
		CollectOpenAIAlphaSearchURLCitations(event, &results, seenURLs)
	}

	out := output.String()
	if strings.TrimSpace(out) == "" && completedResponse != nil {
		out = ExtractOpenAIResponsesCompletedText(completedResponse)
		CollectOpenAIAlphaSearchURLCitations(completedResponse, &results, seenURLs)
	}
	return out, results
}

func OpenAIAlphaSearchSSEData(block string) string {
	var lines []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func ExtractOpenAIResponsesCompletedText(response any) string {
	resp, ok := response.(map[string]any)
	if !ok {
		return ""
	}
	outputItems, _ := resp["output"].([]any)
	var b strings.Builder
	for _, item := range outputItems {
		itemMap, ok := item.(map[string]any)
		if !ok || itemMap["type"] != "message" {
			continue
		}
		contentItems, _ := itemMap["content"].([]any)
		for _, content := range contentItems {
			contentMap, ok := content.(map[string]any)
			if !ok {
				continue
			}
			if contentMap["type"] == "output_text" {
				if text, _ := contentMap["text"].(string); text != "" {
					_, _ = b.WriteString(text)
				}
			}
		}
	}
	return b.String()
}

func CollectOpenAIAlphaSearchURLCitations(value any, results *[]any, seen map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		if typed["type"] == "url_citation" {
			if urlValue, _ := typed["url"].(string); strings.TrimSpace(urlValue) != "" {
				urlValue = strings.TrimSpace(urlValue)
				if _, exists := seen[urlValue]; !exists {
					seen[urlValue] = struct{}{}
					result := map[string]any{
						"type":   "text_result",
						"ref_id": fmt.Sprintf("turn0search%d", len(*results)),
						"url":    urlValue,
					}
					if title, _ := typed["title"].(string); strings.TrimSpace(title) != "" {
						result["title"] = strings.TrimSpace(title)
					}
					*results = append(*results, result)
				}
			}
		}
		for _, child := range typed {
			CollectOpenAIAlphaSearchURLCitations(child, results, seen)
		}
	case []any:
		for _, child := range typed {
			CollectOpenAIAlphaSearchURLCitations(child, results, seen)
		}
	}
}
