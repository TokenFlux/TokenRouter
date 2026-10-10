package modelcatalog

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
)

// Attributes 描述模型展示信息，网关准入和参数转换使用各自的规则。
// 指针保留未知、显式 false 和空模态集合的区别。
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_attributes
type Attributes struct {
	DisplayNameLocalization *locale.Update[string] `json:"display_name_localization,omitempty"`
	DisplayName             *string                `json:"display_name,omitempty"`
	Context                 *int                   `json:"context,omitempty"`
	InputLimit              *int                   `json:"input_limit,omitempty"`
	OutputLimit             *int                   `json:"output_limit,omitempty"`
	InputModalities         *[]string              `json:"input_modalities,omitempty"`
	OutputModalities        *[]string              `json:"output_modalities,omitempty"`
	Reasoning               *bool                  `json:"reasoning,omitempty"`
	ToolCall                *bool                  `json:"tool_call,omitempty"`
	StructuredOutput        *bool                  `json:"structured_output,omitempty"`
	Temperature             *bool                  `json:"temperature,omitempty"`
	Attachment              *bool                  `json:"attachment,omitempty"`
}

// Presentation 是模型信息的展示值，不表达网关准入决策。
type Presentation struct {
	SearchTerms            []string           `json:"search_terms,omitempty"`
	LocalizationResolution *locale.Resolution `json:"localization_resolution,omitempty"`
	Attributes
	RouteDifferences bool `json:"route_differences,omitempty"`
}

// fields 通过值编码复制所有可选字段，避免展示消费者改写目录快照。
func (a Attributes) fields() map[string]json.RawMessage {
	body, _ := json.Marshal(a)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	return fields
}

// Merge 按字段覆盖，未填写的字段继续继承，数组整体替换。
func Merge(base, patch Attributes) Attributes {
	fields := base.fields()
	maps.Copy(fields, patch.fields())
	body, _ := json.Marshal(fields)
	var result Attributes
	_ = json.Unmarshal(body, &result)
	return result
}

// ApplyAttributeSupplement 从模型补充条目的 attributes 中填入缺失字段。
// 目录中的 false 和空数组优先，同名中继继续使用各自的属性。
func (c *Catalog) ApplyAttributeSupplement(model string, body json.RawMessage) error {
	var supplement struct {
		Provider   string      `json:"provider"`
		Attributes *Attributes `json:"attributes"`
	}
	if err := json.Unmarshal(body, &supplement); err != nil {
		return fmt.Errorf("invalid model supplement %s: %w", model, err)
	}
	if supplement.Attributes == nil {
		return nil
	}
	if err := supplement.Attributes.Validate(); err != nil {
		return fmt.Errorf("invalid model attributes %s: %w", model, err)
	}
	key := normalize(model)
	entry, exists := c.Entries[key]
	if c.Ambiguous[key] || (exists && supplement.Provider != "" && entry.Provider != supplement.Provider) {
		return nil
	}
	if !exists {
		entry = Entry{Model: model, Provider: supplement.Provider, Source: "local_supplement"}
	}
	entry.Attributes = Merge(*supplement.Attributes, entry.Attributes)
	c.Entries[key] = entry
	return nil
}

// Validate 校验管理员编辑值；目录中的零长度由解析器转换为未知。
func (a Attributes) Validate() error {
	for key, value := range map[string]*int{"context": a.Context, "input_limit": a.InputLimit, "output_limit": a.OutputLimit} {
		if value != nil && *value <= 0 {
			return fmt.Errorf("%s must be positive", key)
		}
	}
	for _, values := range []*[]string{a.InputModalities, a.OutputModalities} {
		if values == nil {
			continue
		}
		seen := map[string]bool{}
		for _, value := range *values {
			if !slices.Contains([]string{"text", "image", "audio", "video", "pdf"}, value) || seen[value] {
				return fmt.Errorf("invalid or duplicate modality: %s", value)
			}
			seen[value] = true
		}
	}
	return nil
}

// Common 取多条可请求路线的共同能力和已知最小上限，只供展示和导出。
func Common(values []Attributes) (Attributes, bool) {
	if len(values) == 0 {
		return Attributes{}, false
	}
	result := Merge(Attributes{}, values[0])
	first, _ := json.Marshal(result)
	different := false
	for _, value := range values[1:] {
		body, _ := json.Marshal(value)
		different = different || string(body) != string(first)
		result.Context = minimum(result.Context, value.Context)
		result.InputLimit = minimum(result.InputLimit, value.InputLimit)
		result.OutputLimit = minimum(result.OutputLimit, value.OutputLimit)
		result.Reasoning = commonBool(result.Reasoning, value.Reasoning)
		result.ToolCall = commonBool(result.ToolCall, value.ToolCall)
		result.StructuredOutput = commonBool(result.StructuredOutput, value.StructuredOutput)
		result.Temperature = commonBool(result.Temperature, value.Temperature)
		result.Attachment = commonBool(result.Attachment, value.Attachment)
		result.InputModalities = intersection(result.InputModalities, value.InputModalities)
		result.OutputModalities = intersection(result.OutputModalities, value.OutputModalities)
		if result.DisplayName == nil || value.DisplayName == nil || *result.DisplayName != *value.DisplayName {
			result.DisplayName = nil
		}
	}
	return result, different
}

func minimum(a, b *int) *int {
	if a == nil {
		return b
	}
	if b != nil && *b < *a {
		return b
	}
	return a
}

func commonBool(a, b *bool) *bool {
	if a != nil && !*a || b != nil && !*b {
		value := false
		return &value
	}
	if a == nil || b == nil {
		return nil
	}
	return a
}

func intersection(a, b *[]string) *[]string {
	if a == nil || b == nil {
		return nil
	}
	result := []string{}
	for _, value := range *a {
		if slices.Contains(*b, value) {
			result = append(result, value)
		}
	}
	return &result
}
