// 通知 HTTP DTO 保留原字段与省略语义。
package dto

// EmailTemplatePreviewResponse 是模板渲染后的预览响应。
type EmailTemplatePreviewResponse struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

// PreviewEmailTemplateRequest 预览未保存的模板内容。
type PreviewEmailTemplateRequest struct {
	Event     string            `json:"event"`
	Locale    string            `json:"locale"`
	Subject   string            `json:"subject"`
	HTML      string            `json:"html"`
	Variables map[string]string `json:"variables,omitempty"`
}

// UpdateEmailTemplateRequest 更新模板覆盖内容。
type UpdateEmailTemplateRequest struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

// EmailTemplateDetail 是指定事件和语言的模板详情。
type EmailTemplateDetail struct {
	Event        string   `json:"event"`
	Locale       string   `json:"locale"`
	Subject      string   `json:"subject"`
	HTML         string   `json:"html"`
	IsCustom     bool     `json:"is_custom,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
	Placeholders []string `json:"placeholders,omitempty"`
}

// EmailTemplateListResponse 是邮件模板列表接口的响应。
type EmailTemplateListResponse struct {
	Events       []EmailTemplateEventOption `json:"events"`
	Locales      []string                   `json:"locales"`
	Templates    []EmailTemplateSummary     `json:"templates,omitempty"`
	Placeholders []string                   `json:"placeholders,omitempty"`
}

// EmailTemplateSummary 是后台邮件模板列表展示的摘要。
type EmailTemplateSummary struct {
	Event     string `json:"event"`
	Locale    string `json:"locale"`
	Subject   string `json:"subject"`
	IsCustom  bool   `json:"is_custom,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// EmailTemplateEventOption 描述可编辑的通知邮件事件。
type EmailTemplateEventOption struct {
	Value       string `json:"value"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Optional    bool   `json:"optional,omitempty"`
}
