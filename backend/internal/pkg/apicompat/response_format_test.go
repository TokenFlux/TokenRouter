package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseFormatJsonSchemaRoundTrip(t *testing.T) {
	chatFormat := json.RawMessage(`{
		"type":"json_schema",
		"json_schema":{
			"name":"answer",
			"description":"structured answer",
			"schema":{"type":"object","properties":{"value":{"type":"string"}}},
			"strict":true
		}
	}`)

	responsesFormat := chatResponseFormatToResponsesTextFormat(chatFormat)
	require.NotEmpty(t, responsesFormat)
	roundTripped := responsesTextFormatToChatResponseFormat(responsesFormat)
	assert.JSONEq(t, string(chatFormat), string(roundTripped))
}

func TestResponseFormatPassesThroughCompatibleShapes(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "json_object", raw: json.RawMessage(`{"type":"json_object"}`)},
		{name: "text", raw: json.RawMessage(`{"type":"text"}`)},
		{name: "unknown", raw: json.RawMessage(`{"type":"custom","option":true}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 只有 json_schema 的层级差异需要转换，其余格式必须原样保留。
			assert.JSONEq(t, string(tt.raw), string(chatResponseFormatToResponsesTextFormat(tt.raw)))
			assert.JSONEq(t, string(tt.raw), string(responsesTextFormatToChatResponseFormat(tt.raw)))
		})
	}
}

func TestResponseFormatTreatsNullAsUnset(t *testing.T) {
	require.Nil(t, chatResponseFormatToResponsesTextFormat(json.RawMessage(" null ")))
	require.Nil(t, responsesTextFormatToChatResponseFormat(json.RawMessage(" null ")))
}
