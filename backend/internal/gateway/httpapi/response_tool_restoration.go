package httpapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	openaicore "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"fmt"

	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

func SetCodexToolNameReverse(c *gin.Context, reverse map[string]string) {
	if c == nil {
		return
	}
	responseTools(c, true).SetCodexNames(false, reverse)
	responseTools(c, true).SetCodexNames(true, nil)
}

func MergeCodexToolNameReverse(c *gin.Context, reverse map[string]string) {
	if c == nil || len(reverse) == 0 {
		return
	}
	merged := make(map[string]string, len(reverse)+len(CodexToolNameReverseFromContext(c)))
	for aliased, original := range CodexToolNameReverseFromContext(c) {
		merged[aliased] = original
	}
	for aliased, original := range reverse {
		merged[aliased] = original
	}
	responseTools(c, true).SetCodexNames(false, merged)
}

func CodexToolNameReverseFromContext(c *gin.Context) map[string]string {
	return responseTools(c, false).CodexNames(false)
}

// UpdateCodexToolNameReverseForWSFrame 将会话更新与仍在输出的当前 turn 分开保存.
func UpdateCodexToolNameReverseForWSFrame(c *gin.Context, frame []byte, reverse map[string]string) {
	if c == nil {
		return
	}
	eventType := strings.TrimSpace(gjson.GetBytes(frame, "type").String())
	switch eventType {
	case "session.update":
		if gjson.GetBytes(frame, "session.tools").Exists() {
			responseTools(c, true).SetCodexNames(true, reverse)
		}
	case "response.create", "":
		active := reverse
		if !OpenAIWSFrameHasExplicitToolDeclarations(frame) {
			active = openai.MergeCodexToolNameReverseMaps(
				responseTools(c, false).CodexNames(true),
				reverse,
			)
		}
		responseTools(c, true).SetCodexNames(false, active)
	}
}

func OpenAIWSFrameHasExplicitToolDeclarations(frame []byte) bool {
	if gjson.GetBytes(frame, "tools").Exists() {
		return true
	}
	for _, item := range gjson.GetBytes(frame, "input").Array() {
		if strings.EqualFold(strings.TrimSpace(item.Get("type").String()), "additional_tools") && item.Get("tools").Exists() {
			return true
		}
	}
	return false
}

func RestoreCodexToolNamesFromContext(c *gin.Context, data []byte) []byte {
	reverse := CodexToolNameReverseFromContext(c)
	switch strings.TrimSpace(gjson.GetBytes(data, "type").String()) {
	case "session.created", "session.updated":
		reverse = responseTools(c, false).CodexNames(true)
	}
	return openai.RestoreCodexToolNamesInJSON(data, reverse)
}

func RestoreCodexToolNamesFromSSEContext(c *gin.Context, data []byte, eventType string) []byte {
	if strings.TrimSpace(gjson.GetBytes(data, "type").String()) != "" || strings.TrimSpace(eventType) == "" {
		return RestoreCodexToolNamesFromContext(c, data)
	}
	compat := []byte(openaicore.OpenAICompatPayloadWithEventType(string(data), eventType))
	restored := RestoreCodexToolNamesFromContext(c, compat)
	if string(restored) == string(compat) {
		return data
	}
	withoutSyntheticType, err := sjson.DeleteBytes(restored, "type")
	if err != nil {
		return restored
	}
	return withoutSyntheticType
}

func HasOpenAIResponsesClientToolMapping(mapping protocolbridge.ResponsesClientToolMapping) bool {
	return len(mapping.CustomTools) > 0 || mapping.ToolSearch || len(mapping.NamespaceTools) > 0
}

func RestoreOpenAIResponsesClientToolPayload(c *gin.Context, payload []byte) ([]byte, error) {
	mapping, ok := OpenAIResponsesClientToolMapping(c)
	if !ok || !json.Valid(payload) {
		return payload, nil
	}
	restored, _, err := protocolbridge.RestoreResponsesClientToolPayload(payload, mapping)
	return restored, err
}

func RestoreGrokResponsesClientToolPayload(c *gin.Context, payload []byte) ([]byte, error) {
	mapping, ok := GrokResponsesClientToolMapping(c)
	if !ok || !bytes.Contains(payload, []byte(`"function_call"`)) || !json.Valid(payload) {
		return payload, nil
	}
	restored, _, err := protocolbridge.RestoreResponsesClientToolPayload(payload, mapping)
	return restored, err
}

func FlattenOpenAIResponsesNamespaces(c *gin.Context, body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"namespace"`)) {
		return body, nil
	}
	var requestBody map[string]any
	if err := json.Unmarshal(body, &requestBody); err != nil {
		return body, fmt.Errorf("decode OpenAI namespace body: %w", err)
	}
	names, changed, err := protocolbridge.FlattenResponsesNamespacesExcept(requestBody, map[string]bool{"image_gen": true})
	if err != nil {
		return body, err
	}
	if !changed {
		return body, nil
	}
	rebuilt, err := wirejson.Marshal(requestBody)
	if err != nil {
		return body, fmt.Errorf("encode OpenAI namespace body: %w", err)
	}
	SetOpenAIResponsesNamespaceNames(c, names)
	return rebuilt, nil
}

func RestoreOpenAIResponsesNamespacePayload(c *gin.Context, payload []byte) ([]byte, error) {
	names := OpenAIResponsesNamespaceNames(c)
	if len(names) == 0 || !json.Valid(payload) {
		return payload, nil
	}
	restored, changed, err := protocolbridge.RestoreResponsesNamespaceCalls(payload, names)
	if err != nil {
		return payload, err
	}
	if changed {
		return restored, nil
	}
	return payload, nil
}

const responseToolsContextKey = "gateway_response_tools"

// 初始化只保护每个 HTTP 请求的状态指针发布，工具内容仍由请求自己持有。
var responseToolsInit sync.Mutex

func responseTools(c *gin.Context, create bool) *requeststate.ResponseTools {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(responseToolsContextKey); ok {
		if state, ok := value.(*requeststate.ResponseTools); ok {
			return state
		}
	}
	if !create {
		return nil
	}
	responseToolsInit.Lock()
	defer responseToolsInit.Unlock()
	if value, ok := c.Get(responseToolsContextKey); ok {
		if state, ok := value.(*requeststate.ResponseTools); ok {
			return state
		}
	}
	state := &requeststate.ResponseTools{}
	c.Set(responseToolsContextKey, state)
	return state
}
func SetOpenAIResponsesClientToolMapping(c *gin.Context, mapping protocolbridge.ResponsesClientToolMapping) {
	if !HasOpenAIResponsesClientToolMapping(mapping) {
		ClearOpenAIResponsesClientToolMapping(c)
		return
	}
	responseTools(c, true).SetClientMapping(false, mapping)
}
func ClearOpenAIResponsesClientToolMapping(c *gin.Context) {
	responseTools(c, false).SetClientMapping(false, protocolbridge.ResponsesClientToolMapping{})
}
func OpenAIResponsesClientToolMapping(c *gin.Context) (protocolbridge.ResponsesClientToolMapping, bool) {
	mapping := responseTools(c, false).ClientMapping(false)
	return mapping, HasOpenAIResponsesClientToolMapping(mapping)
}
func SetGrokResponsesClientToolMapping(c *gin.Context, mapping protocolbridge.ResponsesClientToolMapping) {
	if !HasOpenAIResponsesClientToolMapping(mapping) {
		ClearGrokResponsesClientToolMapping(c)
		return
	}
	responseTools(c, true).SetClientMapping(true, mapping)
}
func ClearGrokResponsesClientToolMapping(c *gin.Context) {
	responseTools(c, false).SetClientMapping(true, protocolbridge.ResponsesClientToolMapping{})
}
func GrokResponsesClientToolMapping(c *gin.Context) (protocolbridge.ResponsesClientToolMapping, bool) {
	mapping := responseTools(c, false).ClientMapping(true)
	return mapping, HasOpenAIResponsesClientToolMapping(mapping)
}
func SetOpenAIResponsesNamespaceNames(c *gin.Context, names map[string]protocolbridge.ResponsesNamespaceName) {
	if len(names) > 0 {
		responseTools(c, true).SetNamespaces(names)
	}
}
func ClearOpenAIResponsesNamespaceNames(c *gin.Context) { responseTools(c, false).SetNamespaces(nil) }
func OpenAIResponsesNamespaceNames(c *gin.Context) map[string]protocolbridge.ResponsesNamespaceName {
	return responseTools(c, false).Namespaces()
}
func OpenAIWSHTTPBridgeToolStateFromContext(c *gin.Context) (requeststate.WSBridgeTools, bool) {
	return responseTools(c, false).Bridge()
}
func SetOpenAIWSHTTPBridgeToolState(c *gin.Context, state requeststate.WSBridgeTools) {
	responseTools(c, true).SetBridge(state)
}
