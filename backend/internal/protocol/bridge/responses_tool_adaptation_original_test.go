package bridge_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAdaptOpenAIResponsesClientToolsLeavesNamespaceOnlyBodyUnchanged(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.5",
		"tools": [{"type": "namespace", "name": "code_tools", "tools": [{"type": "function", "name": "run"}]}],
		"tool_choice": "auto"
	}`)

	adapted, mapping, err := bridge.AdaptOpenAIResponsesClientTools(body)

	require.NoError(t, err)
	require.Equal(t, body, adapted)
	require.Empty(t, mapping.CustomTools)
	require.Empty(t, mapping.NamespaceTools)
	require.False(t, mapping.ToolSearch)
}

func TestAdaptOpenAIResponsesClientToolsRejectsTrailingData(t *testing.T) {
	tests := map[string][]byte{
		"trailing garbage":     append(openAIClientToolsRequest(false), []byte(` garbage`)...),
		"second JSON document": append(openAIClientToolsRequest(false), []byte(` {"model":"other"}`)...),
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			adapted, mapping, err := bridge.AdaptOpenAIResponsesClientTools(body)

			require.ErrorContains(t, err, "decode OpenAI Responses client tools trailing data")
			require.Equal(t, body, adapted)
			require.Empty(t, mapping)
		})
	}
}

func TestResponsesFunctionUpstreamsLowerToolSearchDiscoveryOutput(t *testing.T) {
	body := []byte(`{"tools":[{"type":"tool_search"}],"input":[{"type":"tool_search_output","call_id":"search_1","tools":[{"type":"namespace","name":"github"}],"status":"completed","execution":"client"}]}`)
	adapters := map[string]func([]byte) ([]byte, bridge.ResponsesClientToolMapping, error){
		"OpenAI API-key": bridge.AdaptOpenAIResponsesClientTools,
		"Grok": func(body []byte) ([]byte, bridge.ResponsesClientToolMapping, error) {
			return bridge.AdaptResponsesClientToolsJSON(body, "Grok")
		},
	}
	for name, adapt := range adapters {
		t.Run(name, func(t *testing.T) {
			adapted, mapping, err := adapt(body)
			require.NoError(t, err)
			require.True(t, mapping.ToolSearch)
			require.Equal(t, "function_call_output", gjson.GetBytes(adapted, "input.0.type").String())
			require.JSONEq(t, `[{"name":"github","type":"namespace"}]`, gjson.GetBytes(adapted, "input.0.output").String())
			require.False(t, gjson.GetBytes(adapted, "input.0.tools").Exists())
			require.False(t, gjson.GetBytes(adapted, "input.0.status").Exists())
			require.False(t, gjson.GetBytes(adapted, "input.0.execution").Exists())
		})
	}
}

func openAIClientToolsRequest(stream bool) []byte {
	streamValue := "false"
	if stream {
		streamValue = "true"
	}
	return []byte(`{"model":"gpt-5.4","input":"fix it","stream":` + streamValue + `,"tools":[{"type":"custom","name":"exec"},{"type":"custom","name":"apply_patch"}]}`)
}
