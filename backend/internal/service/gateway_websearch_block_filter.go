package service

import "github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"

// FilterWebSearchHistoryBlocks 只将原平台协议裁决投影给唯一历史块过滤器。
func FilterWebSearchHistoryBlocks(body []byte, mappedModel string) []byte {
	return searchtools.FilterWebSearchHistoryBlocks(body, ResolveThinkingProtocol(mappedModel) == ThinkingProtocolPassbackRequired)
}
