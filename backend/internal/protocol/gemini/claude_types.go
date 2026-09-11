package gemini

// GeminiModel Gemini v1beta 模型格式
type GeminiModel struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName,omitempty"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`
}

// GeminiModelsListResponse Gemini v1beta 模型列表响应
type GeminiModelsListResponse struct {
	Models []GeminiModel `json:"models"`
}
