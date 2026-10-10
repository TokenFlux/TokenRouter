package systemone

import (
	"encoding/json"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

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
