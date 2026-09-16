// Package modeltrace 拥有单次请求的模型链与响应元数据恢复。
package modeltrace

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/tidwall/gjson"
)

// APIKeyModelRedirectTrace 保存一次请求中的客户端模型与内部模型阶段。
type APIKeyModelRedirectTrace struct {
	ClientModel string
	SourceModel string
	TargetModel string

	mu             sync.RWMutex
	responseModels map[string]struct{}
}

// NewAPIKeyModelRedirectTrace 创建模型重定向追踪，并登记首个内部目标模型。
func NewAPIKeyModelRedirectTrace(clientModel, sourceModel, targetModel string) *APIKeyModelRedirectTrace {
	trace := &APIKeyModelRedirectTrace{
		ClientModel:    strings.TrimSpace(clientModel),
		SourceModel:    strings.TrimSpace(sourceModel),
		TargetModel:    strings.TrimSpace(targetModel),
		responseModels: make(map[string]struct{}),
	}
	trace.RegisterModel(targetModel)
	return trace
}
func (t *APIKeyModelRedirectTrace) RegisterModel(model string) {
	if t == nil {
		return
	}
	model = strings.TrimSpace(model)
	if model == "" || model == t.SourceModel || model == t.ClientModel {
		return
	}
	t.mu.Lock()
	t.responseModels[model] = struct{}{}
	t.mu.Unlock()
}

// ResponseModels 返回按字典序排列的内部模型快照，避免并发写响应时遍历可变 map。
func (t *APIKeyModelRedirectTrace) ResponseModels() []string {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	models := make([]string, 0, len(t.responseModels))
	for model := range t.responseModels {
		models = append(models, model)
	}
	t.mu.RUnlock()
	sort.Strings(models)
	return models
}

// ReplaceModelMetadata 只替换常见协议中的模型元数据字段，不触碰正文内容。
func ReplaceModelMetadata(data []byte, fromModel, toModel string) []byte {
	fromModel = strings.TrimSpace(fromModel)
	toModel = strings.TrimSpace(toModel)
	if fromModel == "" || toModel == "" || fromModel == toModel {
		return data
	}
	fromValue, _ := json.Marshal(fromModel)
	toValue, _ := json.Marshal(toModel)
	fromGeminiName, _ := json.Marshal("models/" + fromModel)
	toGeminiName, _ := json.Marshal("models/" + toModel)
	patterns := [][2][]byte{
		{append([]byte(`"model":`), fromValue...), append([]byte(`"model":`), toValue...)},
		{append([]byte(`"model": `), fromValue...), append([]byte(`"model": `), toValue...)},
		{append([]byte(`"modelVersion":`), fromValue...), append([]byte(`"modelVersion":`), toValue...)},
		{append([]byte(`"modelVersion": `), fromValue...), append([]byte(`"modelVersion": `), toValue...)},
		{append([]byte(`"model_version":`), fromValue...), append([]byte(`"model_version":`), toValue...)},
		{append([]byte(`"model_version": `), fromValue...), append([]byte(`"model_version": `), toValue...)},
		{append([]byte(`"id":`), fromValue...), append([]byte(`"id":`), toValue...)},
		{append([]byte(`"id": `), fromValue...), append([]byte(`"id": `), toValue...)},
		{append([]byte(`"name":`), fromGeminiName...), append([]byte(`"name":`), toGeminiName...)},
		{append([]byte(`"name": `), fromGeminiName...), append([]byte(`"name": `), toGeminiName...)},
	}
	rewritten := data
	for _, pattern := range patterns {
		rewritten = bytes.ReplaceAll(rewritten, pattern[0], pattern[1])
	}
	return rewritten
}

// RegisterResponsePayload 从完整 JSON 或 SSE data 事件中登记实际上游模型元数据。
func (t *APIKeyModelRedirectTrace) RegisterResponsePayload(data []byte) {
	if t == nil || len(data) == 0 {
		return
	}
	for _, model := range responseMetadataModels(data) {
		t.RegisterModel(model)
	}
}

// responseMetadataModels 只读取协议模型字段，不扫描正文或工具参数中的同名文本。
func responseMetadataModels(data []byte) []string {
	payloads := make([][]byte, 0, 4)
	trimmed := bytes.TrimSpace(data)
	if json.Valid(trimmed) {
		payloads = append(payloads, trimmed)
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) || !json.Valid(payload) {
			continue
		}
		payloads = append(payloads, payload)
	}

	paths := []string{"model", "modelVersion", "model_version", "response.model", "session.model"}
	models := make([]string, 0, len(payloads))
	seen := make(map[string]struct{})
	appendModel := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		if _, exists := seen[model]; exists {
			return
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	for _, payload := range payloads {
		for _, path := range paths {
			value := gjson.GetBytes(payload, path)
			if value.Type == gjson.String {
				appendModel(value.String())
			}
		}
		for _, path := range []string{"name", "response.name"} {
			name := strings.TrimSpace(gjson.GetBytes(payload, path).String())
			if strings.HasPrefix(name, "models/") {
				appendModel(strings.TrimPrefix(name, "models/"))
			}
		}
	}
	return models
}

// Restore 只恢复原协议元数据，调用方显式传递本次请求追踪。
func (trace *APIKeyModelRedirectTrace) Restore(data []byte) []byte {
	if trace == nil || strings.TrimSpace(trace.ClientModel) == "" {
		return data
	}
	trace.RegisterResponsePayload(data)
	for _, model := range trace.ResponseModels() {
		data = ReplaceModelMetadata(data, model, trace.ClientModel)
	}
	return data
}

// ContextKey 供旧上下文边界保留唯一 Trace；新用例直接接收 Trace。
type ContextKey struct{}

func WithContext(ctx context.Context, trace *APIKeyModelRedirectTrace) context.Context {
	if trace == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ContextKey{}, trace)
}
func FromContext(ctx context.Context) (*APIKeyModelRedirectTrace, bool) {
	if ctx == nil {
		return nil, false
	}
	trace, ok := ctx.Value(ContextKey{}).(*APIKeyModelRedirectTrace)
	return trace, ok && trace != nil
}

// Snapshot 在异步交接时复制当前模型链，之后的 attempt/turn 不会污染已提交任务。
func (t *APIKeyModelRedirectTrace) Snapshot() *APIKeyModelRedirectTrace {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := &APIKeyModelRedirectTrace{ClientModel: t.ClientModel, SourceModel: t.SourceModel, TargetModel: t.TargetModel, responseModels: make(map[string]struct{}, len(t.responseModels))}
	for model := range t.responseModels {
		out.responseModels[model] = struct{}{}
	}
	return out
}
