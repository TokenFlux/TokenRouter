package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeConvertGeminiToClaudeMessagePreservesCachedPrompt supplies a cached
// Gemini prompt and checks the serialized Messages buckets. Cache-only input
// must remain distinguishable from an absent prompt observation.
func TestNativeConvertGeminiToClaudeMessagePreservesCachedPrompt(t *testing.T) {
	for _, cached := range []int{0, 60, 100} {
		payload := map[string]any{"usageMetadata": map[string]any{
			"promptTokenCount": 100, "cachedContentTokenCount": cached,
			"candidatesTokenCount": 10,
		}}
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		response, usage := NativeConvertGeminiToClaudeMessage(
			NativeGeminiRuntime{MessageID: func() string { return "msg_cache" }},
			payload, "gemini", raw, false,
		)
		encoded, err := json.Marshal(response)
		require.NoError(t, err)
		var decoded AnthropicResponse
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		require.Equal(t, 100-cached, decoded.Usage.InputTokens)
		require.Equal(t, cached, decoded.Usage.CacheReadInputTokens)
		require.Equal(t, usage.CacheReadInputTokens, decoded.Usage.CacheReadInputTokens)
		require.Equal(t, 10, decoded.Usage.OutputTokens)
	}
}

// Native tool conversion groups accepted client functions and retains a
// separate typed search declaration. Schema fixtures check recursive key
// removal, type normalization, scalar enum spelling and representable integer
// minimum conversion, keeping their asserted cases distinct from general
// vendor validation.

// TestConvertClaudeToolsToGeminiTools_CustomType supplies ordinary, nested
// custom, mixed, and malformed custom declarations. It checks nil output for
// the malformed fixture and counts the grouped functions for accepted
// fixtures.
func TestConvertClaudeToolsToGeminiTools_CustomType(t *testing.T) {
	tests := []struct {
		name  string // fixture subtest display name
		tools any    // JSON-shaped client tool declarations
		// expectedLen selects nil-output validation at zero; positive fixtures then
		// check one grouped declaration and the accepted function count.
		expectedLen int
		description string // fixture failure context text
	}{
		{
			name: "Standard tools",

			tools: []any{
				map[string]any{
					"name": "get_weather",

					"description": "Get weather info",

					"input_schema": map[string]any{"type": "object"},
				},
			},

			expectedLen: 1,

			description: "标准工具格式应该正常转换",
		},

		{
			name: "Custom type tool (MCP format)",

			tools: []any{
				map[string]any{
					"type": "custom",

					"name": "mcp_tool",

					"custom": map[string]any{
						"description":  "MCP tool description",
						"input_schema": map[string]any{"type": "object"},
					},
				},
			},

			expectedLen: 1,

			description: "Custom类型工具应该从custom字段读取",
		},

		{
			name: "Mixed standard and custom tools",

			tools: []any{
				map[string]any{
					"name": "standard_tool",

					"description": "Standard",

					"input_schema": map[string]any{"type": "object"},
				},

				map[string]any{
					"type": "custom",

					"name": "custom_tool",

					"custom": map[string]any{
						"description":  "Custom",
						"input_schema": map[string]any{"type": "object"},
					},
				},
			},

			expectedLen: 1,

			description: "混合工具应该都能正确转换",
		},

		{
			name: "Custom tool without custom field",

			tools: []any{
				map[string]any{
					"type": "custom",
					"name": "invalid_custom",
					// The nested custom specification is deliberately absent.
				},
			},

			expectedLen: 0, // Invalid custom tools are skipped.

			description: "缺少custom字段的custom工具应该被跳过",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NativeConvertClaudeToolsToGeminiTools(tt.tools)

			if tt.expectedLen == 0 {
				if result != nil {
					t.Errorf("%s: expected nil result, got %v", tt.description, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("%s: expected non-nil result", tt.description)
			}

			if len(result) != 1 {
				t.Errorf("%s: expected 1 tool declaration, got %d", tt.description, len(result))
				return
			}

			toolDecl, ok := result[0].(map[string]any)
			if !ok {
				t.Fatalf("%s: result[0] is not map[string]any", tt.description)
			}

			funcDecls, ok := toolDecl["functionDeclarations"].([]any)
			if !ok {
				t.Fatalf("%s: functionDeclarations is not []any", tt.description)
			}

			toolsArr, _ := tt.tools.([]any)
			expectedFuncCount := 0
			for _, tool := range toolsArr {
				toolMap, _ := tool.(map[string]any)
				if toolMap["name"] != "" {
					if toolMap["type"] == "custom" {
						if toolMap["custom"] != nil {
							expectedFuncCount++
						}
					} else {
						expectedFuncCount++
					}
				}
			}

			if len(funcDecls) != expectedFuncCount {
				t.Errorf("%s: expected %d function declarations, got %d",
					tt.description, expectedFuncCount, len(funcDecls))
			}
		})
	}
}

// TestCleanToolSchema_NormalizesGeminiUnsupportedSchemaFields supplies nested
// type arrays and both definition-key spellings. It checks definition removal,
// uppercase first non-null types, and omission of a null-only type field.
func TestCleanToolSchema_NormalizesGeminiUnsupportedSchemaFields(t *testing.T) {
	schema := map[string]any{
		"type": "object",

		"$defs": map[string]any{
			"unused": map[string]any{"type": "string"},
		},

		"definitions": map[string]any{
			"legacy": map[string]any{"type": "number"},
		},

		"properties": map[string]any{
			"path": map[string]any{
				"type": []any{"string", "null"},
			},

			"count": map[string]any{
				"type": []any{"null", "integer"},
			},

			"empty": map[string]any{
				"type": []any{"null"},
			},
		},
	}

	cleaned, ok := NativeCleanToolSchema(schema).(map[string]any)
	require.True(t, ok)
	require.Equal(t, "OBJECT", cleaned["type"])
	require.NotContains(t, cleaned, "$defs")
	require.NotContains(t, cleaned, "definitions")

	properties, ok := cleaned["properties"].(map[string]any)
	require.True(t, ok)

	pathSchema, ok := properties["path"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "STRING", pathSchema["type"])

	countSchema, ok := properties["count"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "INTEGER", countSchema["type"])

	emptySchema, ok := properties["empty"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, emptySchema, "type")
}

// TestCleanToolSchema_ConvertsNestedIntegerExclusiveMinimum places integer
// exclusive bounds in array items and sibling properties. It checks
// incremented inclusive minima and retention of an existing stricter minimum
// in these fixtures.
func TestCleanToolSchema_ConvertsNestedIntegerExclusiveMinimum(t *testing.T) {
	schema := map[string]any{
		"type": "object",

		"properties": map[string]any{
			"counts": map[string]any{
				"type": "array",

				"items": map[string]any{
					"type":             "integer",
					"exclusiveMinimum": float64(0),
				},
			},

			"strict": map[string]any{
				"type":             "integer",
				"exclusiveMinimum": 0,
				"minimum":          5,
			},

			"weak": map[string]any{
				"type":             "integer",
				"exclusiveMinimum": 2,
				"minimum":          1,
			},
		},
	}

	cleaned, ok := NativeCleanToolSchema(schema).(map[string]any)
	require.True(t, ok)
	properties, ok := cleaned["properties"].(map[string]any)
	require.True(t, ok)
	counts, ok := properties["counts"].(map[string]any)
	require.True(t, ok)
	items, ok := counts["items"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, items, "exclusiveMinimum")
	require.Equal(t, float64(1), items["minimum"])

	strict, ok := properties["strict"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, strict, "exclusiveMinimum")
	require.Equal(t, 5, strict["minimum"])

	weak, ok := properties["weak"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, weak, "exclusiveMinimum")
	require.Equal(t, 3, weak["minimum"])
}

// TestCleanToolSchema_DropsAmbiguousExclusiveMinimumWithoutConversion supplies
// a number schema and an integer schema with a fractional exclusive bound.
// Both fixtures must lose exclusiveMinimum without gaining an invented
// minimum.
func TestCleanToolSchema_DropsAmbiguousExclusiveMinimumWithoutConversion(t *testing.T) {
	for name, schema := range map[string]map[string]any{
		"number schema": {
			"type":             "number",
			"exclusiveMinimum": 0,
		},

		"fractional integer bound": {
			"type":             "integer",
			"exclusiveMinimum": 0.5,
		},
	} {
		t.Run(name, func(t *testing.T) {
			cleaned, ok := NativeCleanToolSchema(schema).(map[string]any)
			require.True(t, ok)
			require.NotContains(t, cleaned, "exclusiveMinimum")
			require.NotContains(t, cleaned, "minimum")
		})
	}
}

// TestCleanToolSchema_RemovesNestedDeprecatedAndNormalizesMixedScalarEnum
// supplies two anyOf branches with deprecated metadata and mixed scalar enum
// values. It checks nested removal and JSON-spelled string enum entries while
// retaining both branches.
func TestCleanToolSchema_RemovesNestedDeprecatedAndNormalizesMixedScalarEnum(t *testing.T) {
	schema := map[string]any{
		"anyOf": []any{
			map[string]any{
				"type":       "string",
				"deprecated": true,
			},

			map[string]any{
				"enum": []any{"enabled", false, float64(1), nil},
			},
		},
	}

	cleaned, ok := NativeCleanToolSchema(schema).(map[string]any)
	require.True(t, ok)
	anyOf, ok := cleaned["anyOf"].([]any)
	require.True(t, ok)
	require.Len(t, anyOf, 2)

	deprecatedSchema, ok := anyOf[0].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, deprecatedSchema, "deprecated")

	enumSchema, ok := anyOf[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{"enabled", "false", "1", "null"}, enumSchema["enum"])
}

// TestCleanToolSchema_DropsEnumWithNonScalarValue combines a string and a map
// in one enum. It checks removal of the entire enum rather than partial
// retention of its scalar entry.
func TestCleanToolSchema_DropsEnumWithNonScalarValue(t *testing.T) {
	schema := map[string]any{
		"enum": []any{"valid", map[string]any{"invalid": true}},
	}

	cleaned, ok := NativeCleanToolSchema(schema).(map[string]any)
	require.True(t, ok)
	require.NotContains(t, cleaned, "enum")
}

// TestConvertClaudeToolsToGeminiTools_PreservesWebSearchAlongsideFunctions
// combines a client function with a typed server search tool. It checks one
// grouped function declaration and a separate empty built-in Google Search
// declaration.
func TestConvertClaudeToolsToGeminiTools_PreservesWebSearchAlongsideFunctions(t *testing.T) {
	tools := []any{
		map[string]any{
			"name": "get_weather",

			"description": "Get weather info",

			"input_schema": map[string]any{"type": "object"},
		},

		map[string]any{
			"type": "web_search_20250305",
			"name": "web_search",
		},
	}

	result := NativeConvertClaudeToolsToGeminiTools(tools)
	require.Len(t, result, 2)

	functionDecl, ok := result[0].(map[string]any)
	require.True(t, ok)
	funcDecls, ok := functionDecl["functionDeclarations"].([]any)
	require.True(t, ok)
	require.Len(t, funcDecls, 1)

	searchDecl, ok := result[1].(map[string]any)
	require.True(t, ok)
	googleSearch, ok := searchDecl["googleSearch"].(map[string]any)
	require.True(t, ok)
	require.Empty(t, googleSearch)
}
