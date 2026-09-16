// Vertex JSONL 使用独立 wire 投影，保留显式 user 角色与校验顺序。
package vertex

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
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

func BuildVertexBatchJSONL(input BatchJSONLInput, inputError func(string, ...any) error) ([]byte, error) {
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
		parts, err := vertexBatchImageParts(prompt, item.ReferenceImages, inputError)
		if err != nil {
			return nil, err
		}
		line := map[string]any{
			"key": customID,
			"request": map[string]any{
				"contents": []any{map[string]any{
					"role":  "user",
					"parts": parts,
				}},
				"generationConfig": map[string]any{
					"responseModalities": []string{"TEXT", "IMAGE"},
				},
			},
		}
		if err := enc.Encode(line); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
func vertexBatchImageParts(prompt string, refs []BatchReference, inputError func(string, ...any) error) ([]any, error) {
	parts := []any{map[string]any{"text": prompt}}
	for _, ref := range refs {
		mimeType := ref.MimeType
		if mimeType == "" {
			return nil, inputError("reference image mime_type is required")
		}
		fileURI := strings.TrimSpace(ref.FileURI)
		switch {
		case len(ref.Data) > 0 && fileURI == "":
			parts = append(parts, map[string]any{
				"inlineData": map[string]any{
					"mimeType": mimeType,
					"data":     base64.StdEncoding.EncodeToString(ref.Data),
				},
			})
		case len(ref.Data) == 0 && fileURI != "":
			parts = append(parts, map[string]any{
				"fileData": map[string]any{
					"mimeType": mimeType,
					"fileUri":  fileURI,
				},
			})
		default:
			return nil, inputError("reference image must contain exactly one of data or file_uri")
		}
	}
	return parts, nil
}
