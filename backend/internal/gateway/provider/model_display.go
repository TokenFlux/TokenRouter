package provider

import (
	"strings"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeldisplay"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// ModelDisplayCatalogue 将统一目录条目转换为客户端展示字段。
type ModelDisplayCatalogue struct{ Source modelcatalog.Reader }

// Model 保留请求 ID，名称和厂商按完整身份查询统一目录。
func (c ModelDisplayCatalogue) Model(id string) modeldisplay.OpenAIModel {
	model := modeldisplay.OpenAIModel{ID: id, Object: "model", Type: "model", OwnedBy: "tokenrouter", DisplayName: id}
	if c.Source != nil {
		entry := c.Source.ModelEntry(id)
		if entry.Provider != "" {
			model.OwnedBy = entry.Provider
		}
		if entry.Attributes.DisplayName != nil && strings.TrimSpace(*entry.Attributes.DisplayName) != "" {
			model.DisplayName = *entry.Attributes.DisplayName
		}
	}
	return model
}

// GrokSupportsXHigh 读取请求适配器的推理档位规则。
func (ModelDisplayCatalogue) GrokSupportsXHigh(model string) bool {
	return (grok.BodyCodec{NewID: uuid.NewString}).GrokSupportsXHighReasoningEffort(model)
}

// GeminiModel 生成已通过协议检查的 Gemini 模型资源。
func (c ModelDisplayCatalogue) GeminiModel(id string, _ bool) modeldisplay.GeminiModel {
	id = strings.TrimPrefix(id, "models/")
	return modeldisplay.GeminiModel{Name: "models/" + id, DisplayName: c.Model(id).DisplayName, SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"}}
}
