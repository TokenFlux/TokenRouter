package service

import (
	"bytes"
	"encoding/json"

	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	"github.com/gin-gonic/gin"
)

const grokResponsesClientToolMappingContextKey = "grok_responses_client_tool_mapping"

// adaptGrokResponsesClientTools 保留 Grok 专用调用方的兼容入口。
func adaptGrokResponsesClientTools(body []byte) ([]byte, protocolbridge.ResponsesClientToolMapping, error) {
	return protocolbridge.AdaptResponsesClientToolsJSON(body, "Grok")
}

func hasGrokResponsesClientToolMapping(mapping protocolbridge.ResponsesClientToolMapping) bool {
	return len(mapping.CustomTools) > 0 || mapping.ToolSearch || len(mapping.NamespaceTools) > 0
}

func setGrokResponsesClientToolMapping(c *gin.Context, mapping protocolbridge.ResponsesClientToolMapping) {
	if c == nil {
		return
	}
	if !hasGrokResponsesClientToolMapping(mapping) {
		clearGrokResponsesClientToolMapping(c)
		return
	}
	c.Set(grokResponsesClientToolMappingContextKey, mapping)
}

func clearGrokResponsesClientToolMapping(c *gin.Context) {
	if c == nil {
		return
	}
	if _, exists := c.Get(grokResponsesClientToolMappingContextKey); !exists {
		return
	}
	c.Set(grokResponsesClientToolMappingContextKey, protocolbridge.ResponsesClientToolMapping{})
}

func grokResponsesClientToolMapping(c *gin.Context) (protocolbridge.ResponsesClientToolMapping, bool) {
	if c == nil {
		return protocolbridge.ResponsesClientToolMapping{}, false
	}
	value, ok := c.Get(grokResponsesClientToolMappingContextKey)
	if !ok {
		return protocolbridge.ResponsesClientToolMapping{}, false
	}
	mapping, ok := value.(protocolbridge.ResponsesClientToolMapping)
	return mapping, ok && hasGrokResponsesClientToolMapping(mapping)
}

func restoreGrokResponsesClientToolPayload(c *gin.Context, payload []byte) ([]byte, error) {
	mapping, ok := grokResponsesClientToolMapping(c)
	if !ok || !bytes.Contains(payload, []byte(`"function_call"`)) || !json.Valid(payload) {
		return payload, nil
	}
	restored, _, err := protocolbridge.RestoreResponsesClientToolPayload(payload, mapping)
	return restored, err
}
