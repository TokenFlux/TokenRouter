// Package modeldisplay 只拥有展示值与稳定目录合并，不读取平台或存储。
package modeldisplay

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

type Catalog interface {
	OpenAIModels() []OpenAIModel
	OpenAIModelIDs() []string
	GrokModels() []GrokModel
	GrokModelIDs() []string
	GrokSupportsXHigh(string) bool
	ClaudeModels(string) []ClaudeModel
	QoderModelIDs() []string
	GeminiList(bool) GeminiModelsList
	GeminiModel(string, bool) GeminiModel
	HasGeminiFallback(string) bool
}

type ClaudeModel struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

type OpenAIModel struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
}

type GrokModel struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Type        string `json:"type,omitempty"`
	Created     int64  `json:"created,omitempty"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name,omitempty"`
}

type GeminiModel struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName,omitempty"`
	Description                string   `json:"description,omitempty"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`
}

type GeminiModelsList struct {
	Models []GeminiModel `json:"models"`
}

func DefaultModelIDs(catalog Catalog, platform string) []string {
	switch platform {
	case capability.PlatformOpenAI:
		return catalog.OpenAIModelIDs()
	case capability.PlatformGemini:
		ids := make([]string, 0, len(catalog.ClaudeModels(capability.PlatformGemini)))
		for _, model := range catalog.ClaudeModels(capability.PlatformGemini) {
			ids = append(ids, model.ID)
		}
		return ids
	case capability.PlatformAntigravity:
		models := catalog.ClaudeModels(capability.PlatformAntigravity)
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids
	case capability.PlatformQoder:
		return catalog.QoderModelIDs()
	case capability.PlatformAnthropic:
		ids := make([]string, 0, len(catalog.ClaudeModels(capability.PlatformAnthropic))+len(catalog.ClaudeModels(capability.PlatformAntigravity)))
		for _, model := range catalog.ClaudeModels(capability.PlatformAnthropic) {
			ids = append(ids, model.ID)
		}
		for _, model := range catalog.ClaudeModels(capability.PlatformAntigravity) {
			ids = append(ids, model.ID)
		}
		return MergeModelIDs(ids, nil)
	case capability.PlatformGrok:
		return catalog.GrokModelIDs()
	default:
		ids := make([]string, 0, len(catalog.ClaudeModels(capability.PlatformAnthropic)))
		for _, model := range catalog.ClaudeModels(capability.PlatformAnthropic) {
			ids = append(ids, model.ID)
		}
		return ids
	}
}

func MergeModelIDs(primary, secondary []string) []string {
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	merged := make([]string, 0, len(primary)+len(secondary))
	for _, models := range [][]string{primary, secondary} {
		for _, model := range models {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			if _, ok := seen[model]; ok {
				continue
			}
			seen[model] = struct{}{}
			merged = append(merged, model)
		}
	}
	return merged
}
