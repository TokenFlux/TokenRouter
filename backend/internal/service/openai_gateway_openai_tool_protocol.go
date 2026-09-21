package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIResponsesClientToolMappingContextKey = "openai_responses_client_tool_mapping"

func hasOpenAIResponsesClientToolMapping(mapping bridge.ResponsesClientToolMapping) bool {
	return len(mapping.CustomTools) > 0 || mapping.ToolSearch || len(mapping.NamespaceTools) > 0
}

// adaptOpenAIResponsesClientTools 将 Codex 专用工具降级为上游可接受的 function 工具。
func adaptOpenAIResponsesClientTools(body []byte) ([]byte, bridge.ResponsesClientToolMapping, error) {
	if !needsOpenAIResponsesClientToolAdaptation(body) {
		return body, bridge.ResponsesClientToolMapping{}, nil
	}
	return adaptOpenAIResponsesClientToolsWithMapping(body, bridge.ResponsesClientToolMapping{})
}

// adaptOpenAIResponsesClientToolsWithMapping 支持在 bridge 多轮请求中继承工具映射。
// body 已由上游请求解析过，但仍严格拒绝尾随 JSON，避免静默丢弃请求内容。
func adaptOpenAIResponsesClientToolsWithMapping(
	body []byte,
	inherited bridge.ResponsesClientToolMapping,
) ([]byte, bridge.ResponsesClientToolMapping, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var requestBody map[string]any
	if err := decoder.Decode(&requestBody); err != nil {
		return body, bridge.ResponsesClientToolMapping{}, fmt.Errorf("decode OpenAI Responses client tools: %w", err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return body, bridge.ResponsesClientToolMapping{}, fmt.Errorf("decode OpenAI Responses client tools trailing data: %w", err)
	}
	mapping, changed, err := bridge.AdaptResponsesClientToolsWithInheritedMapping(requestBody, inherited)
	if err != nil || !changed {
		return body, mapping, err
	}
	rebuilt, err := wirejson.Marshal(requestBody)
	if err != nil {
		return body, bridge.ResponsesClientToolMapping{}, fmt.Errorf("encode OpenAI Responses client tools: %w", err)
	}
	return rebuilt, mapping, nil
}

func needsOpenAIResponsesClientToolAdaptation(body []byte) bool {
	needsAdaptation := false
	var visit func(gjson.Result) bool
	visit = func(value gjson.Result) bool {
		if value.IsObject() {
			switch strings.TrimSpace(value.Get("type").String()) {
			case "custom", "custom_tool_call", "custom_tool_call_output",
				"tool_search", "tool_search_call", "tool_search_output":
				needsAdaptation = true
				return false
			}
		}
		if value.IsObject() || value.IsArray() {
			value.ForEach(func(_, child gjson.Result) bool {
				return visit(child)
			})
		}
		return !needsAdaptation
	}
	visit(gjson.ParseBytes(body))
	return needsAdaptation
}

func openAIResponsesClientToolMapping(c *gin.Context) (bridge.ResponsesClientToolMapping, bool) {
	if c == nil {
		return bridge.ResponsesClientToolMapping{}, false
	}
	value, ok := c.Get(openAIResponsesClientToolMappingContextKey)
	mapping, typed := value.(bridge.ResponsesClientToolMapping)
	return mapping, ok && typed && hasOpenAIResponsesClientToolMapping(mapping)
}

// setOpenAIResponsesClientToolMapping 写入本次账号尝试的工具映射；空映射会清理旧账号状态。
func setOpenAIResponsesClientToolMapping(c *gin.Context, mapping bridge.ResponsesClientToolMapping) {
	if c == nil {
		return
	}
	if !hasOpenAIResponsesClientToolMapping(mapping) {
		clearOpenAIResponsesClientToolMapping(c)
		return
	}
	c.Set(openAIResponsesClientToolMappingContextKey, mapping)
}

// clearOpenAIResponsesClientToolMapping 清理同一 Gin context 上一次账号尝试留下的映射。
func clearOpenAIResponsesClientToolMapping(c *gin.Context) {
	if c == nil {
		return
	}
	if _, exists := c.Get(openAIResponsesClientToolMappingContextKey); exists {
		c.Set(openAIResponsesClientToolMappingContextKey, bridge.ResponsesClientToolMapping{})
	}
}

func restoreOpenAIResponsesClientToolPayload(c *gin.Context, payload []byte) ([]byte, error) {
	mapping, ok := openAIResponsesClientToolMapping(c)
	if !ok || !json.Valid(payload) {
		return payload, nil
	}
	restored, _, err := bridge.RestoreResponsesClientToolPayload(payload, mapping)
	return restored, err
}
