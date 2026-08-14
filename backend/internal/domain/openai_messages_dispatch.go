package domain

// OpenAIMessagesDispatchModelConfig 控制 Anthropic /v1/messages 请求如何映射到 OpenAI/Codex 模型。
// 每个空字段都表示不执行对应的系列或精确模型映射。
type OpenAIMessagesDispatchModelConfig struct {
	OpusMappedModel    string            `json:"opus_mapped_model,omitempty"`
	SonnetMappedModel  string            `json:"sonnet_mapped_model,omitempty"`
	HaikuMappedModel   string            `json:"haiku_mapped_model,omitempty"`
	ExactModelMappings map[string]string `json:"exact_model_mappings,omitempty"`
}
