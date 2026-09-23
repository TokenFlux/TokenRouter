package provider

import "github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"

// CompletionModels 复用按需型号候选，不持有第二份别名缓存。
type CompletionModels struct{}

func (CompletionModels) Candidates(model string, alternates ...string) []string {
	return modelidentity.UsageCandidates(model, alternates...)
}
