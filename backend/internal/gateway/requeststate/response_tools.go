package requeststate

import (
	"encoding/json"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
)

// ResponseTools 持有请求与 WS 当前 turn 的工具恢复信息；会话更新单独保存。
// 已发布的映射只读，后续 turn 用新映射替换，避免改动正在输出的 turn。
type ResponseTools struct {
	mu                        sync.RWMutex
	openai, grok              bridge.ResponsesClientToolMapping
	namespaces                map[string]bridge.ResponsesNamespaceName
	activeNames, sessionNames map[string]string
	bridgeState               WSBridgeTools
	bridgePresent             bool
}

// WSBridgeTools 保存 HTTP bridge 下一轮继承的工具声明和降级映射。
type WSBridgeTools struct {
	ClientMapping bridge.ResponsesClientToolMapping
	LoweredTools  json.RawMessage
}

func (s *ResponseTools) ClientMapping(grok bool) bridge.ResponsesClientToolMapping {
	if s == nil {
		return bridge.ResponsesClientToolMapping{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if grok {
		return s.grok
	}
	return s.openai
}
func (s *ResponseTools) SetClientMapping(grok bool, mapping bridge.ResponsesClientToolMapping) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if grok {
		s.grok = mapping
	} else {
		s.openai = mapping
	}
}
func (s *ResponseTools) Namespaces() map[string]bridge.ResponsesNamespaceName {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.namespaces
}
func (s *ResponseTools) SetNamespaces(names map[string]bridge.ResponsesNamespaceName) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.namespaces = names
	s.mu.Unlock()
}
func (s *ResponseTools) CodexNames(session bool) map[string]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if session {
		return s.sessionNames
	}
	return s.activeNames
}
func (s *ResponseTools) SetCodexNames(session bool, reverse map[string]string) {
	if s == nil {
		return
	}
	copyMap := make(map[string]string, len(reverse))
	for aliased, original := range reverse {
		copyMap[aliased] = original
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if session {
		s.sessionNames = copyMap
	} else {
		s.activeNames = copyMap
	}
}
func (s *ResponseTools) Bridge() (WSBridgeTools, bool) {
	if s == nil {
		return WSBridgeTools{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bridgeState, s.bridgePresent
}
func (s *ResponseTools) SetBridge(state WSBridgeTools) {
	if s == nil {
		return
	}
	state.LoweredTools = append(json.RawMessage(nil), state.LoweredTools...)
	s.mu.Lock()
	s.bridgeState, s.bridgePresent = state, true
	s.mu.Unlock()
}
