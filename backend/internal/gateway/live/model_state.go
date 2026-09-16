package live

import (
	"strings"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func firstModel(first, fallback string) string {
	if strings.TrimSpace(first) != "" {
		return first
	}
	return fallback
}

type liveSidebandModelState struct {
	mu             sync.RWMutex
	clientModel    string
	internalModels map[string]struct{}
}

// newLiveSidebandModelState 使用创建会话时的模型链初始化双向恢复状态。
func newLiveSidebandModelState(record *session.LiveCallRecord) *liveSidebandModelState {
	state := &liveSidebandModelState{internalModels: make(map[string]struct{})}
	if record == nil {
		return state
	}
	state.update(firstModel(record.RequestedModel, record.Model), record.Model, record.UpstreamModel)
	return state
}

func (s *liveSidebandModelState) update(clientModel string, internalModels ...string) {
	if s == nil {
		return
	}
	clientModel = strings.TrimSpace(clientModel)
	s.mu.Lock()
	if clientModel != "" {
		s.clientModel = clientModel
	}
	s.internalModels = make(map[string]struct{}, len(internalModels))
	for _, model := range internalModels {
		model = strings.TrimSpace(model)
		if model == "" || model == s.clientModel {
			continue
		}
		s.internalModels[model] = struct{}{}
	}
	s.mu.Unlock()
}

func (s *liveSidebandModelState) snapshot() (string, []string) {
	if s == nil {
		return "", nil
	}
	s.mu.RLock()
	clientModel := s.clientModel
	models := make([]string, 0, len(s.internalModels))
	for model := range s.internalModels {
		models = append(models, model)
	}
	s.mu.RUnlock()
	return clientModel, models
}

// RestoreServerPayload 只恢复 Live 响应中的协议模型字段。
func RestoreServerPayload(payload []byte, clientModel string, internalModels []string) []byte {
	clientModel = strings.TrimSpace(clientModel)
	if clientModel == "" || len(internalModels) == 0 || !gjson.ValidBytes(payload) {
		return payload
	}
	internal := make(map[string]struct{}, len(internalModels))
	for _, model := range internalModels {
		if model = strings.TrimSpace(model); model != "" && model != clientModel {
			internal[model] = struct{}{}
		}
	}
	rewritten := payload
	for _, modelPath := range []string{"model", "modelVersion", "model_version", "response.model", "session.model"} {
		value := gjson.GetBytes(rewritten, modelPath)
		if value.Type != gjson.String {
			continue
		}
		if _, ok := internal[strings.TrimSpace(value.String())]; !ok {
			continue
		}
		if next, err := sjson.SetBytes(rewritten, modelPath, clientModel); err == nil {
			rewritten = next
		}
	}
	return rewritten
}
