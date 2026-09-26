// 搜索工具识别和合成内容的唯一字节规则，不读取账号、设置或存储。
package searchtools

import (
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/search/contract"
	"github.com/tidwall/gjson"
)

const (
	toolTypeWebSearchPrefix = "web_search"
	toolTypeGoogleSearch    = "google_search"
	toolNameWebSearch       = "web_search"
	toolNameGoogleSearch    = "google_search"
	toolNameWebSearch2025   = "web_search_20250305"

	webSearchDefaultMaxResults = 5
	defaultWebSearchModel      = "claude-sonnet-4-6"
	webSearchMsgIDPrefix       = "msg_ws_"
	ToolUseIDPrefix            = "srvtoolu_ws_"
	tokenEstimateDivisor       = 4

	// FeatureKey is the key used in Account.Extra and PricingConfig.FeaturesConfig.
	FeatureKey = "web_search_emulation"
)

func IsOnlyWebSearchToolInBody(body []byte) bool {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return false
	}
	arr := tools.Array()
	if len(arr) != 1 {
		return false
	}
	return IsWebSearchToolJSON(arr[0])
}

func IsWebSearchToolJSON(tool gjson.Result) bool {
	toolType := tool.Get("type").String()
	if strings.HasPrefix(toolType, toolTypeWebSearchPrefix) || toolType == toolTypeGoogleSearch {
		return true
	}
	switch tool.Get("name").String() {
	case toolNameWebSearch, toolNameGoogleSearch, toolNameWebSearch2025:
		return true
	}
	return false
}

func ExtractSearchQueryFromBody(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return ""
	}
	arr := messages.Array()
	if len(arr) == 0 {
		return ""
	}
	lastMsg := arr[len(arr)-1]
	if lastMsg.Get("role").String() != "user" {
		return ""
	}
	return ExtractWebSearchTextFromContent(lastMsg.Get("content"))
}

func ExtractWebSearchTextFromContent(content gjson.Result) string {
	if content.Type == gjson.String {
		return content.String()
	}
	if content.IsArray() {
		for _, block := range content.Array() {
			if block.Get("type").String() == "text" {
				if text := block.Get("text").String(); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func BuildSearchResultBlocks(results []contract.SearchResult) []map[string]string {
	blocks := make([]map[string]string, 0, len(results))
	for _, r := range results {
		block := map[string]string{
			"type":  "web_search_result",
			"url":   r.URL,
			"title": r.Title,
		}
		if r.Snippet != "" {
			block["page_content"] = r.Snippet
		}
		if r.PageAge != "" {
			block["page_age"] = r.PageAge
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func BuildTextSummary(query string, results []contract.SearchResult) string {
	if len(results) == 0 {
		return "No search results found for: " + query
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Here are the search results for \"%s\":\n\n", query)
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. **%s**\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return sb.String()
}
