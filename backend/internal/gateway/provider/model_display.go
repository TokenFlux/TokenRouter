// 模型目录固定端口只连接 routing 查询、平台元数据和只读 Gemini 传输。
package provider

import (
	"slices"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeldisplay"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

type ModelDisplayCatalogue struct{}

func (ModelDisplayCatalogue) OpenAIModels() []modeldisplay.OpenAIModel {
	out := make([]modeldisplay.OpenAIModel, len(openai.DefaultModels))
	for i, m := range openai.DefaultModels {
		out[i] = modeldisplay.OpenAIModel(m)
	}
	return out
}
func (ModelDisplayCatalogue) OpenAIModelIDs() []string { return openai.DefaultModelIDs() }
func (ModelDisplayCatalogue) GrokModels() []modeldisplay.GrokModel {
	models := grok.DefaultModels()
	out := make([]modeldisplay.GrokModel, len(models))
	for i, m := range models {
		out[i] = modeldisplay.GrokModel(m)
	}
	return out
}
func (ModelDisplayCatalogue) GrokModelIDs() []string { return grok.DefaultModelIDs() }
func (ModelDisplayCatalogue) GrokSupportsXHigh(model string) bool {
	return (grok.BodyCodec{NewID: uuid.NewString}).GrokSupportsXHighReasoningEffort(model)
}
func (ModelDisplayCatalogue) QoderModelIDs() []string { return qoder.DefaultRequestModelIDs() }
func (ModelDisplayCatalogue) ClaudeModels(platform string) []modeldisplay.ClaudeModel {
	var out []modeldisplay.ClaudeModel
	switch platform {
	case capability.PlatformGemini:
		for _, m := range codeassist.DefaultModels {
			out = append(out, modeldisplay.ClaudeModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
		}
	case capability.PlatformAntigravity:
		for _, m := range antigravity.DefaultModels() {
			out = append(out, modeldisplay.ClaudeModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
		}
	case capability.PlatformQoder:
		for _, m := range qoder.DefaultModels {
			out = append(out, modeldisplay.ClaudeModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
		}
	default:
		for _, m := range anthropic.DefaultModels {
			out = append(out, modeldisplay.ClaudeModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
		}
	}
	return out
}
func (ModelDisplayCatalogue) GeminiList(ag bool) modeldisplay.GeminiModelsList {
	models := gemini.FallbackModelsList().Models
	if ag {
		values := antigravity.FallbackGeminiModelsList().Models
		out := make([]modeldisplay.GeminiModel, len(values))
		for i, m := range values {
			out[i] = modeldisplay.GeminiModel{Name: m.Name, DisplayName: m.DisplayName, SupportedGenerationMethods: slices.Clone(m.SupportedGenerationMethods)}
		}
		return modeldisplay.GeminiModelsList{Models: out}
	}
	out := make([]modeldisplay.GeminiModel, len(models))
	for i, m := range models {
		out[i] = projectGeminiDisplayModel(m)
	}
	return modeldisplay.GeminiModelsList{Models: out}
}
func (ModelDisplayCatalogue) GeminiModel(name string, ag bool) modeldisplay.GeminiModel {
	if ag {
		m := antigravity.FallbackGeminiModel(name)
		return modeldisplay.GeminiModel{Name: m.Name, DisplayName: m.DisplayName, SupportedGenerationMethods: slices.Clone(m.SupportedGenerationMethods)}
	}
	return projectGeminiDisplayModel(gemini.FallbackModel(name))
}
func projectGeminiDisplayModel(m gemini.Model) modeldisplay.GeminiModel {
	return modeldisplay.GeminiModel{Name: m.Name, DisplayName: m.DisplayName, Description: m.Description, SupportedGenerationMethods: slices.Clone(m.SupportedGenerationMethods)}
}
func (ModelDisplayCatalogue) HasGeminiFallback(name string) bool {
	return gemini.HasFallbackModel(name)
}
