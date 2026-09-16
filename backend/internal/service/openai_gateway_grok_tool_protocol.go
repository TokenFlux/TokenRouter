package service

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/TokenFlux/TokenRouter/internal/upstream"

	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
)

const grokResponsesClientToolMappingContextKey = "grok_responses_client_tool_mapping"

func adaptResponsesClientToolsForFunctionUpstream(body []byte, upstream string) ([]byte, apicompat.ResponsesClientToolMapping, error) {
	return protocolbridge.AdaptResponsesClientToolsJSON(body, upstream)
}

func adaptResponsesClientToolsForFunctionUpstreamWithMapping(
	body []byte,
	upstream string,
	inherited apicompat.ResponsesClientToolMapping,
	inheritedLoweredTools ...[]any,
) ([]byte, apicompat.ResponsesClientToolMapping, error) {
	return protocolbridge.AdaptResponsesClientToolsJSONWithMapping(body, upstream, inherited, inheritedLoweredTools...)
}

// adaptGrokResponsesClientTools 保留 Grok 专用调用方的兼容入口。
func adaptGrokResponsesClientTools(body []byte) ([]byte, apicompat.ResponsesClientToolMapping, error) {
	return adaptResponsesClientToolsForFunctionUpstream(body, "Grok")
}

func hasGrokResponsesClientToolMapping(mapping apicompat.ResponsesClientToolMapping) bool {
	return len(mapping.CustomTools) > 0 || mapping.ToolSearch || len(mapping.NamespaceTools) > 0
}

func setGrokResponsesClientToolMapping(c *gin.Context, mapping apicompat.ResponsesClientToolMapping) {
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
	c.Set(grokResponsesClientToolMappingContextKey, apicompat.ResponsesClientToolMapping{})
}

func grokResponsesClientToolMapping(c *gin.Context) (apicompat.ResponsesClientToolMapping, bool) {
	if c == nil {
		return apicompat.ResponsesClientToolMapping{}, false
	}
	value, ok := c.Get(grokResponsesClientToolMappingContextKey)
	if !ok {
		return apicompat.ResponsesClientToolMapping{}, false
	}
	mapping, ok := value.(apicompat.ResponsesClientToolMapping)
	return mapping, ok && hasGrokResponsesClientToolMapping(mapping)
}

func restoreGrokResponsesClientToolPayload(c *gin.Context, payload []byte) ([]byte, error) {
	mapping, ok := grokResponsesClientToolMapping(c)
	if !ok || !bytes.Contains(payload, []byte(`"function_call"`)) || !json.Valid(payload) {
		return payload, nil
	}
	restored, _, err := apicompat.RestoreResponsesClientToolPayload(payload, mapping)
	return restored, err
}

func newResponsesClientToolStreamBody(
	source io.ReadCloser,
	mapping apicompat.ResponsesClientToolMapping,
	maxLineSize int,
) io.ReadCloser {
	return upstream.NewResponsesClientToolStreamBody(source, mapping, maxLineSize)
}

func newGrokResponsesClientToolStreamBody(
	source io.ReadCloser,
	mapping apicompat.ResponsesClientToolMapping,
	maxLineSize int,
) io.ReadCloser {
	return newResponsesClientToolStreamBody(source, mapping, maxLineSize)
}
