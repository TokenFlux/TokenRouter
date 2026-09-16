// JSONL 构造不接收任务实体，保留逐项验证顺序与原 wire 字段。
package gemini

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
)

type BatchJSONLInput struct {
	Model string
	Items []BatchJSONLItem
}
type BatchJSONLItem struct {
	CustomID, Prompt string
	ReferenceImages  []BatchReference
}
type BatchReference struct {
	MimeType string
	Data     []byte
	FileURI  string
}
type geminiJSONLLine = wire.BatchJSONLLine

type geminiGenerateRequest = wire.BatchGenerateRequest

type geminiContent = wire.BatchContent

type geminiPart = wire.BatchPart

type geminiInlineData = wire.BatchInlineData

type geminiFileData = wire.BatchFileData

type geminiGenerationConfig = wire.BatchGenerationConfig

func BuildGeminiBatchJSONL(input BatchJSONLInput, inputError func(string, ...any) error) ([]byte, error) {
	if strings.TrimSpace(input.Model) == "" {
		return nil, inputError("model is required")
	}
	if len(input.Items) == 0 {
		return nil, inputError("at least one item is required")
	}

	seen := make(map[string]struct{}, len(input.Items))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, item := range input.Items {
		customID := strings.TrimSpace(item.CustomID)
		if customID == "" {
			return nil, inputError("custom_id is required")
		}
		if _, ok := seen[customID]; ok {
			return nil, inputError("duplicate custom_id %q", customID)
		}
		seen[customID] = struct{}{}

		prompt := strings.TrimSpace(item.Prompt)
		if prompt == "" {
			return nil, inputError("prompt is required for custom_id %q", customID)
		}
		parts, err := batchImageGeminiParts(prompt, item.ReferenceImages, inputError)
		if err != nil {
			return nil, err
		}

		// TODO(batch-image)：待 Gemini 批量图片 REST 参数格式稳定后，补充
		// response_mime_type、aspect_ratio 和 image_size。
		line := geminiJSONLLine{
			Key: customID,
			Request: geminiGenerateRequest{
				Contents: []geminiContent{{
					Parts: parts,
				}},
				GenerationConfig: geminiGenerationConfig{
					ResponseModalities: []string{"TEXT", "IMAGE"},
				},
			},
		}
		if err := enc.Encode(line); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
func batchImageGeminiParts(prompt string, refs []BatchReference, inputError func(string, ...any) error) ([]geminiPart, error) {
	parts := []geminiPart{{Text: prompt}}
	for _, ref := range refs {
		mimeType := ref.MimeType
		if mimeType == "" {
			return nil, inputError("reference image mime_type is required")
		}
		fileURI := strings.TrimSpace(ref.FileURI)
		switch {
		case len(ref.Data) > 0 && fileURI == "":
			parts = append(parts, geminiPart{InlineData: &geminiInlineData{
				MimeType: mimeType,
				Data:     base64.StdEncoding.EncodeToString(ref.Data),
			}})
		case len(ref.Data) == 0 && fileURI != "":
			parts = append(parts, geminiPart{FileData: &geminiFileData{
				MimeType: mimeType,
				FileURI:  fileURI,
			}})
		default:
			return nil, inputError("reference image must contain exactly one of data or file_uri")
		}
	}
	return parts, nil
}
