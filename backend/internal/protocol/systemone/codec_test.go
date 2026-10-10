package systemone

import (
	"encoding/json"
	"fmt"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestModerationBodyDecodesDescriptions 检查审核文本与上游读取的 JSON 字符串一致。
func TestModerationBodyDecodesDescriptions(t *testing.T) {
	for _, tc := range []struct {
		name, state, questions string
		want                   []string
	}{
		{name: "string", state: `"\u0062locked"`, want: []string{"blocked"}},
		{name: "object", state: `{"\u006bey":{"text":"\u0062locked","number":9007199254740993,"flag":true}}`, want: []string{"key", "blocked", "9007199254740993", "true"}},
		{name: "array", state: `["\u0062locked",["\u4e2d\u6587"]]`, want: []string{"blocked", "中文"}},
		{name: "escaped punctuation", state: `"\u003cscript\u003e\nline\t\"quoted\""`, want: []string{"<script>\nline\t\"quoted\""}},
		{name: "literal escape", state: `"\\u0062locked"`, want: []string{`\u0062locked`}},
		{name: "instructions", questions: `{"a":{"type":"noul","instructions":{"\u0061sk":["\u0062locked"]}}}`, want: []string{"ask", "blocked"}},
		{name: "noul criteria", questions: `{"a":{"type":"noul","instructions":"ok","criteria":{"true":"\u0062locked","false":["\u4e2d\u6587"]}}}`, want: []string{"blocked", "中文"}},
		{name: "choice criteria", questions: `{"a":{"type":"choice","instructions":"ok","criteria":{"\u0062locked":null,"other":{"text":"\u4e2d\u6587"}}}}`, want: []string{"blocked", "中文"}},
		{name: "score criteria", questions: `{"a":{"type":"score","instructions":"ok","criteria":["\u0062locked",["\u4e2d\u6587"]]}}`, want: []string{"blocked", "中文"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, questions := tc.state, tc.questions
			if state == "" {
				state = `"ok"`
			}
			if questions == "" {
				questions = `{"a":{"type":"noul","instructions":"ok"}}`
			}
			request, err := ParseRequest([]byte(fmt.Sprintf(`{"model":"jev-latest","state":%s,"questions":%s}`, state, questions)))
			require.NoError(t, err)
			var body struct {
				Messages []struct{ Content string } `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(request.ModerationBody(), &body))
			require.Len(t, body.Messages, 1)
			for _, want := range tc.want {
				require.Contains(t, body.Messages[0].Content, want)
			}
			if tc.name == "literal escape" {
				require.NotContains(t, body.Messages[0].Content, "blocked")
			}
		})
	}
}

// TestCodecPreservesMixedQuestions 检查结构化问题和扩展字段在模型改写后仍可解析。
func TestCodecPreservesMixedQuestions(t *testing.T) {
	body := []byte(`{"model":"alias","state":{"message":"hello","number":9007199254740993},"extension":{"keep":true},"questions":{"a":{"type":"noul","instructions":["yes?"]},"b":{"type":"choice","instructions":{"ask":"pick"},"criteria":{"one":null,"two":{"hint":"second"}}},"c":{"type":"score","instructions":"rank","criteria":["low",["high"]]}}}`)
	r, err := ParseRequest(body)
	require.NoError(t, err)
	require.Len(t, r.Questions, 3)
	mapped, err := ReplaceModel(body, "jev-latest")
	require.NoError(t, err)
	require.Contains(t, string(mapped), `9007199254740993`)
	require.Contains(t, string(mapped), `"extension":{"keep":true}`)
	require.Contains(t, string(r.ModerationBody()), "message")
	response := []byte(`{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0.9},"b":{"type":"choice","choice":"one","confidence":0.8,"probabilities":{"one":0.9,"two":0.1}},"c":{"type":"score","score":0.1,"confidence":0.8,"probabilities":{"0":0.9,"1":0.1},"legend":{"0":"low","1":"high"}}},"usage":{"input_tokens":10,"output_tokens":0}}`)
	decoded, err := ParseResponse(response, r.Questions)
	require.NoError(t, err)
	require.True(t, decoded.HasUsage)
	require.Equal(t, 10, decoded.Usage.InputTokens)
}

// TestResponseUsageValidity 区分缺失、非法和合法零用量，答案继续有效。
func TestResponseUsageValidity(t *testing.T) {
	questions := map[string]Question{"a": {Type: "noul"}}
	for _, usage := range []string{`null`, `{}`, `{"input_tokens":-1,"output_tokens":0}`, `{"input_tokens":"10","output_tokens":0}`, `{"input_tokens":1.5,"output_tokens":0}`, `{"input_tokens":9223372036854775808,"output_tokens":0}`, `{"input_tokens":0,"output_tokens":0}`} {
		t.Run(usage, func(t *testing.T) {
			body := []byte(`{"model":"jev","answers":{"a":{"type":"noul","noul":0}},"usage":` + usage + `}`)
			r, err := ParseResponse(body, questions)
			require.NoError(t, err)
			require.Equal(t, usage == `{"input_tokens":0,"output_tokens":0}`, r.HasUsage)
		})
	}
	for _, body := range []string{`null`, `{"model":"jev","answers":{}}`, `{"model":"jev","answers":{"a":{"type":"noul","noul":2}}}`} {
		_, err := ParseResponse([]byte(body), questions)
		require.Error(t, err)
	}
}

func TestParseRequestRejectsUnsupportedShapes(t *testing.T) {
	for _, field := range []map[string]any{{"state": true}, {"state": nil}, {"stream": true}, {"model": ""}, {"questions": map[string]any{}}, {"questions": map[string]any{"a": map[string]any{"type": "chat", "instructions": "test"}}}} {
		var request map[string]any
		require.NoError(t, json.Unmarshal(ProbeBody("jev-latest", ""), &request))
		maps.Copy(request, field)
		body, err := json.Marshal(request)
		require.NoError(t, err)
		_, err = ParseRequest(body)
		require.Error(t, err)
	}
}
