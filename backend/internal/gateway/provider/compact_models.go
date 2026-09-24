package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/compact"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// CompactModels 保存静态回退配置；账号映射与默认模型仍按原顺序按需求值。
type CompactModels struct{ Default string }

type compactAttemptModels struct {
	defaults CompactModels
	target   *ExecutionAccount
}

func (p compactAttemptModels) AccountModel(model string) (string, bool) {
	if p.target == nil {
		return "", false
	}
	return p.target.View().ResolveCompactMappedModel(model)
}
func (p compactAttemptModels) GlobalModel() string { return p.defaults.Default }
func (p compactAttemptModels) ResolveGlobalModel(model string) string {
	return ExecutionModelPolicy(p.target).OpenAIUpstream(model, false, false)
}
func (p CompactModels) Recovery(target *ExecutionAccount) compact.Recovery {
	return compact.Recovery{Models: compactAttemptModels{defaults: p, target: target}, ContextWindow: openai.IsOpenAIContextWindowError, RewriteModel: protocolopenai.ReplaceModelInBody}
}
