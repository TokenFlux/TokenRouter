package service

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
)

type apiKeyModelRedirectContextKey struct{}

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
	trace.addResponseModel(targetModel)
	return trace
}

// WithAPIKeyModelRedirectTrace 把模型重定向追踪附加到请求上下文。
func WithAPIKeyModelRedirectTrace(ctx context.Context, trace *APIKeyModelRedirectTrace) context.Context {
	if trace == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, apiKeyModelRedirectContextKey{}, trace)
}

// APIKeyModelRedirectTraceFromContext 读取当前请求的模型重定向追踪。
func APIKeyModelRedirectTraceFromContext(ctx context.Context) (*APIKeyModelRedirectTrace, bool) {
	if ctx == nil {
		return nil, false
	}
	trace, ok := ctx.Value(apiKeyModelRedirectContextKey{}).(*APIKeyModelRedirectTrace)
	return trace, ok && trace != nil
}

// PropagateAPIKeyModelRedirectTrace 把来源上下文中的追踪附加到异步任务上下文。
func PropagateAPIKeyModelRedirectTrace(dst, src context.Context) context.Context {
	trace, ok := APIKeyModelRedirectTraceFromContext(src)
	if !ok {
		return dst
	}
	if dst == nil {
		dst = context.Background()
	}
	dst = WithAPIKeyModelRedirectTrace(dst, trace)
	if strings.TrimSpace(trace.ClientModel) != "" {
		dst = context.WithValue(dst, ctxkey.ClientModel, trace.ClientModel)
	}
	return dst
}

// RegisterAPIKeyModelRedirectStage 登记可能出现在上游响应中的内部模型名。
func RegisterAPIKeyModelRedirectStage(ctx context.Context, model string) {
	trace, ok := APIKeyModelRedirectTraceFromContext(ctx)
	if !ok {
		return
	}
	trace.addResponseModel(model)
}

func (t *APIKeyModelRedirectTrace) addResponseModel(model string) {
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

// buildModelMappingChain 按首次出现顺序生成去重后的模型映射链。
func buildModelMappingChain(models ...string) string {
	stages := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		stages = append(stages, model)
	}
	if len(stages) < 2 {
		return ""
	}
	return strings.Join(stages, "→")
}
