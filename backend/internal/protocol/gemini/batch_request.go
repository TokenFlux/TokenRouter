// Gemini 批量/创作的既有 wire 变体保留无 role 的原字段形状。
package gemini

type BatchJSONLLine struct {
	Key     string               `json:"key"`
	Request BatchGenerateRequest `json:"request"`
}
type BatchGenerateRequest struct {
	Contents         []BatchContent        `json:"contents"`
	GenerationConfig BatchGenerationConfig `json:"generationConfig"`
}
type BatchContent struct {
	Parts []BatchPart `json:"parts"`
}
type BatchPart struct {
	Text       string           `json:"text,omitempty"`
	InlineData *BatchInlineData `json:"inlineData,omitempty"`
	FileData   *BatchFileData   `json:"fileData,omitempty"`
}
type BatchInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}
type BatchFileData struct {
	MimeType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}
type BatchGenerationConfig struct {
	ResponseModalities []string `json:"responseModalities"`
}
