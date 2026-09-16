// Gemini Batch 资源报文保留原字段、省略规则与 Raw 承载。
package gemini

type GeminiUploadedFile struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	URI         string `json:"uri"`
	MimeType    string `json:"mimeType"`
}
type GeminiBatchJob struct {
	Name     string               `json:"name"`
	State    string               `json:"state"`
	Dest     *GeminiBatchDest     `json:"dest"`
	Response *GeminiBatchResponse `json:"response"`
	Error    *GeminiBatchError    `json:"error"`
	Raw      map[string]any       `json:"-"`
}
type GeminiBatchDest struct {
	FileName      string `json:"fileName"`
	FileNameSnake string `json:"file_name"`
}
type GeminiBatchResponse struct {
	ResponsesFile       string `json:"responsesFile"`
	ResponsesFileSnake  string `json:"responses_file"`
	InlinedResponses    []any  `json:"inlinedResponses"`
	InlinedResponsesAlt []any  `json:"inlined_responses"`
}
type GeminiBatchError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}
