// Gemini 创作 generateContent 的 wire 变体保留原 JSON 字段与省略语义。
package gemini

// ImageGenerationConfig 是创作台 Gemini generateContent 的生成配置。
type ImageGenerationConfig struct {
	ResponseModalities []string             `json:"responseModalities"`
	ImageConfig        *ImageConfig         `json:"imageConfig,omitempty"`
	ThinkingConfig     *ImageThinkingConfig `json:"thinkingConfig,omitempty"`
}

// ImageConfig 是 Gemini 图片生成专用配置；imageSize 必须位于 imageConfig 内。
type ImageConfig struct {
	ImageSize   string `json:"imageSize,omitempty"`
	AspectRatio string `json:"aspectRatio,omitempty"`
}

// ImageThinkingConfig 控制 Gemini 图片模型的思考强度；中间思考内容固定不返回。
type ImageThinkingConfig struct {
	ThinkingLevel   string `json:"thinkingLevel"`
	IncludeThoughts bool   `json:"includeThoughts"`
}

// ImageGenerateRequest 是创作台 Gemini generateContent 请求体。
type ImageGenerateRequest struct {
	Contents         []BatchContent        `json:"contents"`
	GenerationConfig ImageGenerationConfig `json:"generationConfig"`
}
