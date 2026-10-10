package systemone

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// Question 保存请求的问题类型及结构化描述，其他字段在原报文中透传。
type Question struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria"`
}

// Request 保存选路和响应检查需要的字段。
type Request struct {
	Model     string              `json:"model"`
	State     json.RawMessage     `json:"state"`
	Questions map[string]Question `json:"questions"`
	Stream    bool                `json:"stream"`
}

// Response 记录答案及可选的有效用量，原响应字节由执行器交付。
type Response struct {
	Model    string
	Usage    protocol.TokenUsage
	HasUsage bool
}

// ParseRequest 校验同步 SystemOne 请求；问题描述允许 JSON 字符串、对象和数组。
// @project-doc docs/interfaces/jev_upstream.md#systemone_execution
func ParseRequest(body []byte) (Request, error) {
	var request Request
	if err := json.Unmarshal(body, &request); err != nil {
		return request, errors.New("invalid SystemOne JSON request")
	}
	if strings.TrimSpace(request.Model) == "" {
		return request, errors.New("model is required")
	}
	if request.Stream {
		return request, errors.New("SystemOne does not support streaming")
	}
	if !description(request.State, false) {
		return request, errors.New("state must be a string, object, or array")
	}
	if len(request.Questions) == 0 {
		return request, errors.New("questions must be a non-empty object")
	}
	for id, question := range request.Questions {
		if !description(question.Instructions, false) {
			return request, fmt.Errorf("questions[%q].instructions must be a string, object, or array", id)
		}
		switch question.Type {
		case "noul", "choice":
			if question.Type == "noul" && len(question.Criteria) == 0 {
				continue
			}
			var criteria map[string]json.RawMessage
			if json.Unmarshal(question.Criteria, &criteria) != nil || criteria == nil {
				return request, fmt.Errorf("questions[%q].criteria must be an object", id)
			}
			if question.Type == "choice" && (len(criteria) == 0 || len(criteria) > 255) {
				return request, fmt.Errorf("questions[%q].criteria requires 1 to 255 options", id)
			}
			for name, value := range criteria {
				if question.Type == "noul" && name != "true" && name != "false" {
					return request, fmt.Errorf("questions[%q].criteria keys must be true or false", id)
				}
				if !description(value, question.Type == "choice") {
					return request, fmt.Errorf("questions[%q].criteria contains an invalid description", id)
				}
			}
		case "score":
			var levels []json.RawMessage
			if json.Unmarshal(question.Criteria, &levels) != nil || len(levels) < 2 || len(levels) > 10 {
				return request, fmt.Errorf("questions[%q].criteria requires 2 to 10 levels", id)
			}
			for _, value := range levels {
				if !description(value, false) {
					return request, fmt.Errorf("questions[%q].criteria contains an invalid description", id)
				}
			}
		default:
			return request, fmt.Errorf("questions[%q].type must be noul, choice, or score", id)
		}
	}
	return request, nil
}

// description 检查 JSON 描述支持的顶层形态。
func description(raw json.RawMessage, nullable bool) bool {
	raw = bytes.TrimSpace(raw)
	if nullable && bytes.Equal(raw, []byte("null")) {
		return true
	}
	return len(raw) > 0 && (raw[0] == '"' || raw[0] == '{' || raw[0] == '[')
}

// ReplaceModel 修改顶层模型字段，保留问题、答案和扩展字段的字节内容。
func ReplaceModel(body []byte, model string) ([]byte, error) {
	return sjson.SetBytes(body, "model", model)
}

// ModerationBody 解码 state、问题描述和条件中的文本，交给现有审核解析器。
func (r Request) ModerationBody() []byte {
	var content strings.Builder
	appendModerationText(&content, r.State)
	questions, _ := json.Marshal(r.Questions)
	appendModerationText(&content, questions)
	body, _ := json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": content.String()}}})
	return body
}

// appendModerationText 读取已校验 JSON 的键和值，字符串解码一次，数字保持原精度。
func appendModerationText(content *strings.Builder, raw json.RawMessage) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	for {
		value, err := decoder.Token()
		if err != nil {
			return
		}
		switch value.(type) {
		case json.Delim, nil:
			continue
		default:
			_, _ = fmt.Fprintln(content, value)
		}
	}
}

// ParseResponse 先检查答案，再独立解析用量。无效用量通过 HasUsage 表示。
func ParseResponse(body []byte, questions map[string]Question) (Response, error) {
	var wire struct {
		Model   string                     `json:"model"`
		Answers map[string]json.RawMessage `json:"answers"`
		Usage   json.RawMessage            `json:"usage"`
	}
	var result Response
	if json.Unmarshal(body, &wire) != nil || strings.TrimSpace(wire.Model) == "" || len(wire.Answers) != len(questions) {
		return result, errors.New("invalid SystemOne response")
	}
	for id, question := range questions {
		var answer struct {
			Type          string             `json:"type"`
			Noul          *float64           `json:"noul"`
			Choice        *string            `json:"choice"`
			Score         *float64           `json:"score"`
			Confidence    *float64           `json:"confidence"`
			Probabilities map[string]float64 `json:"probabilities"`
			Legend        json.RawMessage    `json:"legend"`
		}
		if json.Unmarshal(wire.Answers[id], &answer) != nil || answer.Type != question.Type {
			return result, errors.New("invalid SystemOne answer")
		}
		valid := false
		switch question.Type {
		case "noul":
			valid = probability(answer.Noul)
		case "choice":
			valid = answer.Choice != nil && probability(answer.Confidence) && len(answer.Probabilities) > 0
		case "score":
			valid = answer.Score != nil && probability(answer.Confidence) && len(answer.Probabilities) > 0 && description(answer.Legend, false)
		}
		if !valid {
			return result, errors.New("invalid SystemOne answer values")
		}
	}
	result.Model = wire.Model
	var usage struct {
		Input  *int `json:"input_tokens"`
		Output *int `json:"output_tokens"`
	}
	if json.Unmarshal(wire.Usage, &usage) == nil && usage.Input != nil && usage.Output != nil && *usage.Input >= 0 && *usage.Output >= 0 && *usage.Input <= int(^uint(0)>>1)-*usage.Output {
		result.HasUsage = true
		result.Usage = protocol.TokenUsage{InputTokens: *usage.Input, OutputTokens: *usage.Output}
	}
	return result, nil
}

// probability 检查概率是否已提供且处于零到一之间。
func probability(value *float64) bool { return value != nil && *value >= 0 && *value <= 1 }

// ProbeBody 构造提供商手动测试和定时探测共用的最小请求。
func ProbeBody(model, state string) []byte {
	if state == "" {
		state = "The service is available."
	}
	body, _ := json.Marshal(map[string]any{"model": model, "state": state, "questions": map[string]any{"available": map[string]string{"type": "noul", "instructions": "Does the state say the service is available?"}}})
	return body
}
