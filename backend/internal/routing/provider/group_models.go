package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	antigravity "github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	openai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	qoder "github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// DefaultGroupModelCandidates 保留平台目录的原始顺序与动态读取时点。
func DefaultGroupModelCandidates(platform string) []string {
	switch platform {
	case capability.PlatformOpenAI:
		return openai.DefaultModelIDs()
	case capability.PlatformGemini:
		ids := make([]string, 0, len(geminicli.DefaultModels))
		for _, model := range geminicli.DefaultModels {
			ids = append(ids, model.ID)
		}
		return ids
	case capability.PlatformAntigravity:
		models := antigravity.DefaultModels()
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids
	case capability.PlatformQoder:
		return qoder.DefaultRequestModelIDs()
	case capability.PlatformGrok:
		return xai.DefaultModelIDs()
	default:
		ids := make([]string, 0, len(claude.DefaultModels))
		for _, model := range claude.DefaultModels {
			ids = append(ids, model.ID)
		}
		return ids
	}
}
