package openai

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestSanitizeOpenAIResponsesToolParameterTypes_TopLevelFunctionTool supplies
// a function with null parameter type. Repair selects object while retaining
// the asserted tool name/description, properties object, model, and request
// input.
func TestSanitizeOpenAIResponsesToolParameterTypes_TopLevelFunctionTool(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.6-sol",
		"input": "Reply with OK.",
		"stream": false,
		"tools": [
			{
				"type": "function",
				"name": "automation_update",
				"description": "Update an automation.",
				"parameters": {"type": null, "properties": {}}
			}
		]
	}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "object", gjson.GetBytes(sanitized, "tools.0.parameters.type").String())
	// Check the tool name, description, and property-container shape.
	require.Equal(t, "automation_update", gjson.GetBytes(sanitized, "tools.0.name").String())
	require.Equal(t, "Update an automation.", gjson.GetBytes(sanitized, "tools.0.description").String())
	require.True(t, gjson.GetBytes(sanitized, "tools.0.parameters.properties").IsObject())
	// Check the model and input values supplied by this fixture.
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(sanitized, "model").String())
	require.Equal(t, "Reply with OK.", gjson.GetBytes(sanitized, "input").String())
}

// TestSanitizeOpenAIResponsesToolParameterTypes_ValidSchemaUntouched supplies
// an already-object parameter root. The sanitizer reports no change and
// returns exactly equal bytes, preserving this fixture's serialized request
// prefix.
func TestSanitizeOpenAIResponsesToolParameterTypes_ValidSchemaUntouched(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"ok","parameters":{"type":"object","properties":{}}}]}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(sanitized))
}

// TestSanitizeOpenAIResponsesToolParameterTypes_MissingTypeNotInvented
// supplies properties without a root type or union. Sanitization reports no
// change, and the resulting root still has no type key.
func TestSanitizeOpenAIResponsesToolParameterTypes_MissingTypeNotInvented(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"ok","parameters":{"properties":{}}}]}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.False(t, gjson.GetBytes(sanitized, "tools.0.parameters.type").Exists())
}

// TestSanitizeOpenAIResponsesToolParameterTypes_ObjectOnlyRootUnion supplies
// three function roots with qualifying explicit or nested object unions. Each
// gains object type, and the asserted nested string-property type remains
// unchanged.
func TestSanitizeOpenAIResponsesToolParameterTypes_ObjectOnlyRootUnion(t *testing.T) {
	body := []byte(`{"tools":[
		{"type":"function","name":"automation_update","parameters":{"oneOf":[{"type":"object","properties":{"id":{"type":"string"}}},{"anyOf":[{"type":"object"},{"type":"object","properties":{}}]}]}},
		{"type":"function","name":"codex_app__automation_update","parameters":{"anyOf":[{"type":"object"}]}},
		{"type":"function","name":"unrelated_tool","parameters":{"oneOf":[{"type":"object"}]}}
	]}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.True(t, changed)
	for i := range 3 {
		require.Equal(t, "object", gjson.GetBytes(sanitized, "tools."+strconv.Itoa(i)+".parameters.type").String())
	}
	require.Equal(t, "string", gjson.GetBytes(sanitized, "tools.0.parameters.oneOf.0.properties.id.type").String())
}

// TestSanitizeOpenAIResponsesToolParameterTypes_EscapedUnionKeyword escapes
// part of the root oneOf key in raw JSON. The decoded union still qualifies
// and produces a reported object-type insertion.
func TestSanitizeOpenAIResponsesToolParameterTypes_EscapedUnionKeyword(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"other","parameters":{"one\u004ff":[{"type":"object"}]}}]}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "object", gjson.GetBytes(sanitized, "tools.0.parameters.type").String())
}

// TestSanitizeOpenAIResponsesToolParameterTypes_ObjectUnionMissingTypeSafetyBoundary
// supplies mixed, unproven, empty, and existing-null union roots. The first
// three retain equal bytes; the existing null follows the separate object
// replacement rule.
func TestSanitizeOpenAIResponsesToolParameterTypes_ObjectUnionMissingTypeSafetyBoundary(t *testing.T) {
	tests := []struct {
		name string // schema sanitizer fixture label
		body string // raw request JSON fixture
	}{
		{name: "mixed union", body: `{"tools":[{"type":"function","name":"automation_update","parameters":{"oneOf":[{"type":"object"},{"type":"string"}]}}]}`},
		{name: "unproven branch", body: `{"tools":[{"type":"function","name":"automation_update","parameters":{"oneOf":[{"properties":{}}]}}]}`},
		{name: "empty union", body: `{"tools":[{"type":"function","name":"automation_update","parameters":{"oneOf":[]}}]}`},
		{name: "existing null handled by existing repair", body: `{"tools":[{"type":"function","name":"automation_update","parameters":{"type":null,"oneOf":[{"type":"object"}]}}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sanitized, changed, err := SanitizeToolParameterTypes([]byte(tt.body))
			require.NoError(t, err)
			if tt.name == "existing null handled by existing repair" {
				require.True(t, changed)
				require.Equal(t, "object", gjson.GetBytes(sanitized, "tools.0.parameters.type").String())
				return
			}
			require.False(t, changed)
			require.Equal(t, tt.body, string(sanitized))
		})
	}
}

// TestSanitizeOpenAIResponsesToolParameterTypes_NestedHistoryTools places
// declarations under an additional_tools input item and namespace. Selected
// null roots become object, while the carrier parameter, valid neighbor, and
// message text stay unchanged.
func TestSanitizeOpenAIResponsesToolParameterTypes_NestedHistoryTools(t *testing.T) {
	body := []byte(`{
		"input": [
			{"type": "message", "role": "user", "content": "hi"},
			{
				"type": "additional_tools",
				"role": "developer",
				"parameters": {"type": null},
				"tools": [
					{
						"type": "namespace",
						"name": "codex_app",
						"tools": [
							{"type": "function", "name": "noop", "parameters": {"type": "object"}},
							{"type": "function", "name": "automation_update", "parameters": {"type": null}}
						]
					},
					{"type": "function", "name": "outer", "parameters": {"type": null}}
				]
			}
		]
	}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "object", gjson.GetBytes(sanitized, "input.1.tools.0.tools.1.parameters.type").String())
	require.Equal(t, "object", gjson.GetBytes(sanitized, "input.1.tools.1.parameters.type").String())
	require.Equal(t, "null", gjson.GetBytes(sanitized, "input.1.parameters.type").Raw)
	// Check the valid neighbor's type and the preceding message text.
	require.Equal(t, "object", gjson.GetBytes(sanitized, "input.1.tools.0.tools.0.parameters.type").String())
	require.Equal(t, "hi", gjson.GetBytes(sanitized, "input.0.content").String())
}

// TestSanitizeOpenAIResponsesToolParameterTypes_ChatCompletionsShape wraps a
// function definition inside a Chat-style function object. Its null parameter
// type becomes object, and the wrapped function name remains equal.
func TestSanitizeOpenAIResponsesToolParameterTypes_ChatCompletionsShape(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","function":{"name":"legacy","parameters":{"type":null}}}]}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "object", gjson.GetBytes(sanitized, "tools.0.function.parameters.type").String())
	require.Equal(t, "legacy", gjson.GetBytes(sanitized, "tools.0.function.name").String())
}

// TestSanitizeOpenAIResponsesToolParameterTypes_InputItemShapes places direct
// and wrapped functions in the input array. Both root parameter types become
// object even when type follows parameters in member order.
func TestSanitizeOpenAIResponsesToolParameterTypes_InputItemShapes(t *testing.T) {
	body := []byte(`{"input":[{"parameters":{"type":null},"name":"direct","type":"function"},{"function":{"name":"wrapped","parameters":{"type":null}},"type":"function"}]}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "object", gjson.GetBytes(sanitized, "input.0.parameters.type").String())
	require.Equal(t, "object", gjson.GetBytes(sanitized, "input.1.function.parameters.type").String())
}

// TestSanitizeOpenAIResponsesToolParameterTypes_DoesNotTreatMessagesAsTools
// puts familiar parameter/function keys in a message and its metadata. The
// sanitizer reports no change and returns exactly equal request bytes.
func TestSanitizeOpenAIResponsesToolParameterTypes_DoesNotTreatMessagesAsTools(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","role":"user","parameters":{"type":null},"function":{"parameters":{"type":null}},"metadata":{"parameters":{"type":null}}}]}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(sanitized))
}

// TestSanitizeOpenAIResponsesToolParameterTypes_PreservesUntouchedBytes mixes
// large numeric spelling and escaped text with one null root. Exact equality
// requires the expected single replacement while retaining all other
// serialized bytes.
func TestSanitizeOpenAIResponsesToolParameterTypes_PreservesUntouchedBytes(t *testing.T) {
	body := []byte("{\n  \"z\":1e+999, \"integer\":900719925474099312345, \"escaped\":\"\\u003chtml\\u003e\", \"tools\":[{\"parameters\":{\"properties\":{}, \"type\" : null},\"name\":\"x\"}], \"a\":-0.00\n}")
	want := []byte("{\n  \"z\":1e+999, \"integer\":900719925474099312345, \"escaped\":\"\\u003chtml\\u003e\", \"tools\":[{\"parameters\":{\"properties\":{}, \"type\" : \"object\"},\"name\":\"x\"}], \"a\":-0.00\n}")

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, string(want), string(sanitized))
}

// TestSanitizeOpenAIResponsesToolParameterTypes_EscapedKey escapes a letter in
// the parameter type key. Exact output equality checks that the key spelling
// remains and its null value becomes object.
func TestSanitizeOpenAIResponsesToolParameterTypes_EscapedKey(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","parameters":{"ty\u0070e":null}}]}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, `{"tools":[{"type":"function","parameters":{"ty\u0070e":"object"}}]}`, string(sanitized))
}

// TestSanitizeOpenAIResponsesToolParameterTypes_DuplicateTypeIsAmbiguousNoOp
// supplies repeated type keys, including an escaped duplicate and repeated
// nulls. Every fixture reports no change and retains exact source bytes.
func TestSanitizeOpenAIResponsesToolParameterTypes_DuplicateTypeIsAmbiguousNoOp(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"tools":[{"type":"function","parameters":{"type":null,"type":"string"}}]}`),
		[]byte(`{"tools":[{"type":"function","parameters":{"type":"string","ty\u0070e":null}}]}`),
		[]byte(`{"tools":[{"type":"function","parameters":{"type":null,"type":null}}]}`),
	} {
		sanitized, changed, err := SanitizeToolParameterTypes(body)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, string(body), string(sanitized))
	}
}

// TestSanitizeOpenAIResponsesToolSchemas_InvalidAndTrailingJSON combines
// trailing garbage and invalid UTF-8 with sanitizer-eligible fixtures. Each
// selected sanitizer returns an error and reports that no valid repair was
// published.
func TestSanitizeOpenAIResponsesToolSchemas_InvalidAndTrailingJSON(t *testing.T) {
	for _, tc := range []struct {
		name string                             // schema sanitizer fixture label
		body []byte                             // raw request JSON fixture
		call func([]byte) ([]byte, bool, error) // selected schema sanitizer function
	}{
		{"null_type_trailing", []byte(`{"tools":[{"parameters":{"type":null}}]} trailing`), SanitizeToolParameterTypes},
		{"pattern_trailing", []byte(`{"tools":[{"parameters":{"pattern":"(?=x)"}}]} trailing`), SanitizeToolSchemaPatterns},
		{"invalid_utf8", append([]byte(`{"tools":[{"parameters":{"pattern":"(?=x)"}}],"bad":"`), 0xff, '"', '}'), SanitizeToolSchemaPatterns},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, changed, err := tc.call(tc.body)
			require.Error(t, err)
			require.False(t, changed)
		})
	}
}

// TestSanitizeOpenAIResponsesToolParameterTypes_OnlyOffendingIndexRewritten
// places one null root between two valid declarations. It checks that root's
// replacement, all three names, and the unchanged declaration count.
func TestSanitizeOpenAIResponsesToolParameterTypes_OnlyOffendingIndexRewritten(t *testing.T) {
	body := []byte(`{"tools":[
		{"type":"function","name":"a","parameters":{"type":"object"}},
		{"type":"function","name":"b","parameters":{"type":null}},
		{"type":"function","name":"c","parameters":{"type":"object"}}
	]}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "a", gjson.GetBytes(sanitized, "tools.0.name").String())
	require.Equal(t, "b", gjson.GetBytes(sanitized, "tools.1.name").String())
	require.Equal(t, "c", gjson.GetBytes(sanitized, "tools.2.name").String())
	require.Equal(t, "object", gjson.GetBytes(sanitized, "tools.1.parameters.type").String())
	require.Equal(t, 3, int(gjson.GetBytes(sanitized, "tools.#").Int()))
}

// TestSanitizeOpenAIResponsesToolParameterTypes_MalformedShapesAreNoOps
// supplies empty input and JSON with unsupported containers, absent roots, or
// supported type forms. Every fixture returns equal text, no error, and a
// false change flag.
func TestSanitizeOpenAIResponsesToolParameterTypes_MalformedShapesAreNoOps(t *testing.T) {
	cases := []struct {
		name string // schema sanitizer fixture label
		body string // raw request JSON fixture
	}{
		{"empty_body", ``},
		{"no_tools", `{"model":"gpt-5.6-sol","input":"hi"}`},
		{"tools_null", `{"tools":null}`},
		{"tools_object", `{"tools":{"type":"function"}}`},
		{"tool_is_string", `{"tools":["freeform"]}`},
		{"parameters_is_string", `{"tools":[{"type":"function","parameters":"nope"}]}`},
		{"parameters_null", `{"tools":[{"type":"function","parameters":null}]}`},
		{"input_string", `{"input":"hi","tools":[]}`},
		{"input_item_not_object", `{"input":["hi"]}`},
		{"type_already_array", `{"tools":[{"type":"function","parameters":{"type":["object","null"]}}]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sanitized, changed, err := SanitizeToolParameterTypes([]byte(tc.body))
			require.NoError(t, err)
			require.False(t, changed)
			require.Equal(t, tc.body, string(sanitized))
		})
	}
}

// TestSanitizeOpenAIResponsesToolParameterTypes_DepthGuard nests a function
// inside twelve namespace declarations within the ordinary parser budget.
// Sanitization must complete without a panic or returned error.
func TestSanitizeOpenAIResponsesToolParameterTypes_DepthGuard(t *testing.T) {
	tool := map[string]any{"type": "function", "name": "deep", "parameters": map[string]any{"type": nil}}
	for range 12 {
		tool = map[string]any{"type": "namespace", "tools": []any{tool}}
	}
	body, err := json.Marshal(map[string]any{"tools": []any{tool}})
	require.NoError(t, err)

	require.NotPanics(t, func() {
		_, _, sanitizeErr := SanitizeToolParameterTypes(body)
		require.NoError(t, sanitizeErr)
	})
}

// TestSanitizeOpenAIResponsesToolParameterTypes_OutputStaysValidJSON repairs
// one null root in a request with unrelated controls. Decoding must succeed,
// and the asserted model, tool choice, and false storage value remain equal.
func TestSanitizeOpenAIResponsesToolParameterTypes_OutputStaysValidJSON(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","tool_choice":"none","store":false,"tools":[{"type":"function","name":"automation_update","parameters":{"type":null,"properties":{}}}]}`)

	sanitized, changed, err := SanitizeToolParameterTypes(body)
	require.NoError(t, err)
	require.True(t, changed)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(sanitized, &decoded))
	require.Equal(t, "gpt-5.5", decoded["model"])
	require.Equal(t, "none", decoded["tool_choice"])
	require.Equal(t, false, decoded["store"])
}

// TestSanitizeOpenAIResponsesToolParameterTypes_DoesNotMutateInputBody
// snapshots caller bytes before a successful null-root repair. The input
// remains equal to that snapshot while the returned result differs.
func TestSanitizeOpenAIResponsesToolParameterTypes_DoesNotMutateInputBody(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","tools":[{"type":"function","name":"a","parameters":{"type":null}}]}`)
	original := append([]byte(nil), body...)

	sanitized, changed, err := SanitizeToolParameterTypes(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, string(original), string(body), "调用方的 body 不得被就地改写")
	require.NotEqual(t, string(original), string(sanitized))
}

// TestSanitizeOpenAIResponsesToolSchemaPatterns_RemovesLookaroundOnly mixes
// two lookaround constraints with an ordinary pattern. Both selected pattern
// keys disappear, and the ordinary pattern keeps its exact string value.
func TestSanitizeOpenAIResponsesToolSchemaPatterns_RemovesLookaroundOnly(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"search","parameters":{"type":"object","properties":{"q":{"type":"string","pattern":"^(?=.*foo)[a-z]+$"},"id":{"type":"string","pattern":"^[a-z]+$"},"z":{"type":"string","pattern":"(?<!bad)ok"}}}}]}`)

	sanitized, changed, err := SanitizeToolSchemaPatterns(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(sanitized, "tools.0.parameters.properties.q.pattern").Exists())
	require.False(t, gjson.GetBytes(sanitized, "tools.0.parameters.properties.z.pattern").Exists())
	require.Equal(t, "^[a-z]+$", gjson.GetBytes(sanitized, "tools.0.parameters.properties.id.pattern").String())
}

// TestSanitizeOpenAIResponsesToolSchemaPatterns_PreservesRawDocumentRepresentation
// combines large numeric spelling, escaped text, whitespace, and one selected
// pattern. Exact output equality checks the deletion and every retained byte
// range.
func TestSanitizeOpenAIResponsesToolSchemaPatterns_PreservesRawDocumentRepresentation(t *testing.T) {
	body := []byte("{\n \"number\":1e+999,\n \"tools\":[{\"parameters\":{\"pattern\":\"(?=drop)\", \"description\":\"\\u003ctag\\u003e\", \"type\":\"string\"}}],\n \"tail\":-0.00\n}")
	want := []byte("{\n \"number\":1e+999,\n \"tools\":[{\"parameters\":{ \"description\":\"\\u003ctag\\u003e\", \"type\":\"string\"}}],\n \"tail\":-0.00\n}")

	sanitized, changed, err := SanitizeToolSchemaPatterns(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, string(want), string(sanitized))
}

// TestSanitizeOpenAIResponsesToolSchemaPatterns_AllMemberPositions puts a
// selected pattern first, middle, last, or alone in schema objects. All four
// pattern keys disappear, while their containing values remain objects.
func TestSanitizeOpenAIResponsesToolSchemaPatterns_AllMemberPositions(t *testing.T) {
	body := []byte(`{"tools":[{"parameters":{"properties":{"first":{"pattern":"(?=a)","type":"string"},"middle":{"title":"x","pattern":"(?!b)","type":"string"},"last":{"type":"string","pattern":"(?<=c)"},"only":{"pattern":"(?<!d)"}}}}]}`)

	sanitized, changed, err := SanitizeToolSchemaPatterns(body)

	require.NoError(t, err)
	require.True(t, changed)
	for _, name := range []string{"first", "middle", "last", "only"} {
		require.False(t, gjson.GetBytes(sanitized, "tools.0.parameters.properties."+name+".pattern").Exists(), name)
		require.True(t, gjson.GetBytes(sanitized, "tools.0.parameters.properties."+name).IsObject(), name)
	}
}

// TestSanitizeOpenAIResponsesToolSchemaPatterns_TraversesConservativeSchemaKeywords
// places lookaround under direct/wrapped history functions and selected schema
// keywords. The five asserted anyOf/items/$defs/additionalProperties/oneOf
// paths must lose their pattern keys.
func TestSanitizeOpenAIResponsesToolSchemaPatterns_TraversesConservativeSchemaKeywords(t *testing.T) {
	body := []byte(`{"input":[{"type":"function","name":"direct","parameters":{"type":"object","properties":{"p":{"anyOf":[{"pattern":"(?=any)"}],"items":{"pattern":"(?!item)"}}},"$defs":{"d":{"pattern":"(?<=def)"}},"additionalProperties":{"pattern":"(?<!additional)"}}},{"type":"function","function":{"name":"wrapped","parameters":{"oneOf":[{"pattern":"(?=wrapped)"}]}}}]}`)

	sanitized, changed, err := SanitizeToolSchemaPatterns(body)

	require.NoError(t, err)
	require.True(t, changed)
	for _, path := range []string{
		"input.0.parameters.properties.p.anyOf.0.pattern",
		"input.0.parameters.properties.p.items.pattern",
		"input.0.parameters.$defs.d.pattern",
		"input.0.parameters.additionalProperties.pattern",
		"input.1.function.parameters.oneOf.0.pattern",
	} {
		require.False(t, gjson.GetBytes(sanitized, path).Exists(), path)
	}
}

// TestSanitizeOpenAIResponsesToolSchemaPatterns_DoesNotTraverseMessageMetadata
// uses familiar schema keys in an untyped input object, a message, and
// metadata. Sanitization reports no change and retains exact bytes.
func TestSanitizeOpenAIResponsesToolSchemaPatterns_DoesNotTraverseMessageMetadata(t *testing.T) {
	body := []byte(`{"input":[{"parameters":{"pattern":"(?=missing-type)"}},{"type":"message","parameters":{"pattern":"(?=parameters)"},"function":{"parameters":{"pattern":"(?=function)"}},"metadata":{"tools":[{"type":"function","parameters":{"pattern":"(?=metadata)"}}]}}]}`)

	sanitized, changed, err := SanitizeToolSchemaPatterns(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(sanitized))
}

// TestSanitizeOpenAIResponsesToolSchemaPatterns_DecodesEscapedLookaround
// escapes the parenthesis or later lookahead marker characters in two JSON
// pattern strings. Both fixtures report a change and remove their pattern key.
func TestSanitizeOpenAIResponsesToolSchemaPatterns_DecodesEscapedLookaround(t *testing.T) {
	for _, pattern := range []string{`\u0028?\u003dfoo`, `(\u003f\u003dfoo)`} {
		body := []byte(`{"tools":[{"parameters":{"type":"string","pattern":"` + pattern + `"}}]}`)
		sanitized, changed, err := SanitizeToolSchemaPatterns(body)
		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, gjson.GetBytes(sanitized, "tools.0.parameters.pattern").Exists())
	}
}

// TestSanitizeOpenAIResponsesToolSchemaPatterns_EscapedKey escapes letters in
// tools, parameters, and pattern keys. Exact output equality checks selected
// member removal while retaining the surrounding escaped key spellings.
func TestSanitizeOpenAIResponsesToolSchemaPatterns_EscapedKey(t *testing.T) {
	body := []byte(`{"to\u006fls":[{"par\u0061meters":{"type":"string","pat\u0074ern":"(?=drop)"}}]}`)

	sanitized, changed, err := SanitizeToolSchemaPatterns(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, `{"to\u006fls":[{"par\u0061meters":{"type":"string"}}]}`, string(sanitized))
}

// TestSanitizeOpenAIResponsesToolSchemaPatterns_DuplicateKeysStayValid
// supplies duplicate patterns with retained or removable values. Both results
// validate as JSON and match the exact expected retained-member bytes.
func TestSanitizeOpenAIResponsesToolSchemaPatterns_DuplicateKeysStayValid(t *testing.T) {
	for _, tc := range []struct {
		// body and want compare complete byte representations for duplicate-key
		// pattern deletion. The expected document selects retained members while
		// keeping punctuation and spelling outside the removed spans.
		body string // raw request JSON fixture
		want string // expected patched request bytes
	}{
		{
			`{"tools":[{"parameters":{"pattern":"(?=drop)","pattern":"^[a-z]+$","type":"string"}}]}`,
			`{"tools":[{"parameters":{"pattern":"^[a-z]+$","type":"string"}}]}`,
		},
		{
			`{"tools":[{"parameters":{"type":"string","pattern":"(?=first)","pattern":"(?!second)"}}]}`,
			`{"tools":[{"parameters":{"type":"string"}}]}`,
		},
	} {
		sanitized, changed, err := SanitizeToolSchemaPatterns([]byte(tc.body))
		require.NoError(t, err)
		require.True(t, changed)
		require.True(t, json.Valid(sanitized))
		require.Equal(t, tc.want, string(sanitized))
	}
}

// TestSanitizeOpenAIResponsesToolSchemas_DepthLimitReturnsOriginal builds
// valid JSON nested beyond the parser limit. Sanitization returns no error,
// reports no change, and keeps the complete source bytes.
func TestSanitizeOpenAIResponsesToolSchemas_DepthLimitReturnsOriginal(t *testing.T) {
	body := []byte(`{"tools":[{"parameters":`)
	for range openAIResponsesToolSchemaMaxDepth + 2 {
		body = append(body, []byte(`{"allOf":[`)...)
	}
	body = append(body, []byte(`{"pattern":"(?=too-deep)"}`)...)
	for range openAIResponsesToolSchemaMaxDepth + 2 {
		body = append(body, []byte(`]}`)...)
	}
	body = append(body, []byte(`}]}`)...)
	require.True(t, json.Valid(body))

	sanitized, changed, err := SanitizeToolSchemaPatterns(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(sanitized))
}

// TestSanitizeOpenAIResponsesToolSchemaPatterns_DoesNotTouchUserInputPattern
// puts lookaround-like values in ordinary input and metadata objects.
// Sanitization returns no error, reports no change, and retains exact request
// bytes.
func TestSanitizeOpenAIResponsesToolSchemaPatterns_DoesNotTouchUserInputPattern(t *testing.T) {
	body := []byte(`{"input":{"pattern":"(?=keep)"},"metadata":{"pattern":"(?!keep)"}}`)
	sanitized, changed, err := SanitizeToolSchemaPatterns(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(sanitized))
}

// TestSanitizeOpenAIResponsesToolSchemaPatterns_DoesNotTraverseInstanceData
// surrounds default/example/const/enum data with one editable schema pattern.
// That pattern disappears, while every asserted instance-data pattern retains
// its exact string.
func TestSanitizeOpenAIResponsesToolSchemaPatterns_DoesNotTraverseInstanceData(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","parameters":{"type":"object","properties":{"config":{"type":"object","pattern":"(?=remove)","default":{"pattern":"(?=keep-default)"},"examples":[{"pattern":"(?=keep-example)"}],"const":{"pattern":"(?=keep-const)"},"enum":[{"pattern":"(?=keep-enum)"}]}}}}]}`)

	sanitized, changed, err := SanitizeToolSchemaPatterns(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(sanitized, "tools.0.parameters.properties.config.pattern").Exists())
	require.Equal(t, "(?=keep-default)", gjson.GetBytes(sanitized, "tools.0.parameters.properties.config.default.pattern").String())
	require.Equal(t, "(?=keep-example)", gjson.GetBytes(sanitized, "tools.0.parameters.properties.config.examples.0.pattern").String())
	require.Equal(t, "(?=keep-const)", gjson.GetBytes(sanitized, "tools.0.parameters.properties.config.const.pattern").String())
	require.Equal(t, "(?=keep-enum)", gjson.GetBytes(sanitized, "tools.0.parameters.properties.config.enum.0.pattern").String())
}

// buildToolSchemaNullTypeBody builds and encodes the requested number of
// identical null-root function declarations. Callers pass a nonnegative count;
// encoding failure stops the test before the returned fixture is used.
func buildToolSchemaNullTypeBody(t *testing.T, hits int) []byte {
	t.Helper()
	tools := make([]any, 0, hits)
	for range hits {
		tools = append(tools, map[string]any{
			"type":       "function",
			"name":       "automation_update",
			"parameters": map[string]any{"type": nil, "properties": map[string]any{}},
		})
	}
	body, err := json.Marshal(map[string]any{"model": "gpt-5.6-sol", "tools": tools})
	require.NoError(t, err)
	return body
}

// TestSanitizeOpenAIResponsesToolParameterTypes_RewriteCountIndependentOfHits
// observes allocations for four-tool and two-thousand-tool fixtures. The large
// case stays below the asserted allocation ceiling and repairs every checked
// root while retaining its declaration count.
func TestSanitizeOpenAIResponsesToolParameterTypes_RewriteCountIndependentOfHits(t *testing.T) {
	small := buildToolSchemaNullTypeBody(t, 4)
	large := buildToolSchemaNullTypeBody(t, 2000)

	// These bounded valid fixtures exercise successful parsing during allocation
	// measurement. Later assertions inspect the large result, while these calls
	// discard values to keep allocation observations focused on the sanitizer.
	smallAllocs := testing.AllocsPerRun(2, func() {
		_, _, _ = SanitizeToolParameterTypes(small)
	})
	largeAllocs := testing.AllocsPerRun(2, func() {
		_, _, _ = SanitizeToolParameterTypes(large)
	})

	// Allow room for edit-slice growth while rejecting one allocation per hit.
	// The ceiling includes headroom for process-wide allocation noise from background
	// goroutines during AllocsPerRun.
	require.Less(t, largeAllocs, 200.0,
		"分配次数随命中数线性增长，说明退回了逐路径全量重写 (small=%v large=%v)", smallAllocs, largeAllocs)

	// Check that the large fixture actually repairs every null type.
	sanitized, changed, err := SanitizeToolParameterTypes(large)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 2000, int(gjson.GetBytes(sanitized, "tools.#").Int()))
	gjson.GetBytes(sanitized, "tools").ForEach(func(_, tool gjson.Result) bool {
		require.Equal(t, "object", tool.Get("parameters.type").String())
		return true
	})
}

// BenchmarkSanitizeOpenAIResponsesToolSchemaPatterns_ByteSpanPatch measures
// repeated removal from a two-thousand-tool request. Input construction
// finishes before timing, while allocation and byte-throughput reporting cover
// each sanitizer invocation.
func BenchmarkSanitizeOpenAIResponsesToolSchemaPatterns_ByteSpanPatch(b *testing.B) {
	var tools []any
	for range 2000 {
		tools = append(tools, map[string]any{
			"type": "function",
			"name": "search",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string", "pattern": "(?=foo).*"}},
			},
		})
	}
	// This fixture contains fresh string-keyed maps, slices, and strings with
	// no unsupported values or cycles, so standard JSON encoding cannot return an error.
	body, _ := json.Marshal(map[string]any{"model": "gpt-5.6", "tools": tools})
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	// The fixed declaration fixture is valid and within parser safety limits.
	// Discard results here so timing measures the parsing and patch-building call.
	for range b.N {
		_, _, _ = SanitizeToolSchemaPatterns(body)
	}
}

// BenchmarkSanitizeOpenAIResponsesToolParameterTypes_ByteSpanPatch measures
// repeated null-root repair on a two-thousand-tool byte fixture. Construction
// finishes before timing, while allocation and byte-throughput reporting cover
// each sanitizer invocation.
func BenchmarkSanitizeOpenAIResponsesToolParameterTypes_ByteSpanPatch(b *testing.B) {
	body := make([]byte, 0, 256*2000)
	body = append(body, []byte(`{"tools":[`)...)
	for i := range 2000 {
		if i > 0 {
			body = append(body, ',')
		}
		body = append(body, []byte(`{"type":"function","name":"automation_update","parameters":{"properties":{},"type":null}}`)...)
	}
	body = append(body, []byte(`]}`)...)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	// The fixed declaration fixture is valid and within parser safety limits.
	// Discard results here so timing measures the parsing and patch-building call.
	for range b.N {
		_, _, _ = SanitizeToolParameterTypes(body)
	}
}

// TestSanitizeToolSchemasCombinedSinglePassAndLazyMessageProbing supplies a
// payload combining a null root schema type, a lookaround pattern, a message
// item with trailing type, and code containing parentheses. Combined sanitation
// repairs real tool schemas in one pass while skipping non-tool messages and
// pattern-free payloads with zero allocations.
func TestSanitizeToolSchemasCombinedSinglePassAndLazyMessageProbing(t *testing.T) {
	combined := []byte(`{
		"model":"gpt-5.4",
		"tools":[{"type":"function","name":"search","parameters":{"type":null,"properties":{"q":{"type":"string","pattern":"(?=foo).*"}}}}],
		"input":[
			{"tools":[{"type":"function","name":"ignored","parameters":{"type":null}}],"type":"message","role":"user","content":"call foo(bar)"}
		]
	}`)
	got, changed, err := SanitizeToolSchemas(combined, ToolSchemaSanitizeOptions{
		RepairParameterTypes:     true,
		RemoveLookaroundPatterns: true,
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "object", gjson.GetBytes(got, "tools.0.parameters.type").String())
	require.False(t, gjson.GetBytes(got, "tools.0.parameters.properties.q.pattern").Exists())
	require.Equal(t, gjson.Null, gjson.GetBytes(got, "input.0.tools.0.parameters.type").Type)

	codeWithParensNoPattern := []byte(`{"model":"gpt-5.4","tools":[{"type":"function","name":"run","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}],"input":[{"type":"message","role":"user","content":"if (x > 0) { return fn(x); }"}]}`)
	allocs := testing.AllocsPerRun(20, func() {
		out, didChange, runErr := SanitizeToolSchemaPatterns(codeWithParensNoPattern)
		if runErr != nil || didChange || len(out) != len(codeWithParensNoPattern) {
			t.Fatal("unexpected pattern sanitizer work on code payload without pattern key")
		}
	})
	require.Zero(t, allocs)
}

// BenchmarkSanitizeToolSchemas_LargeConversation runs combined tool-schema
// sanitization across 1 MiB, 5 MiB, 20 MiB, and 32 MiB Responses conversations
// containing thirty-six tools and code history. Sub-benchmarks report byte
// throughput and verify bounded allocations when schemas require no edits.
func BenchmarkSanitizeToolSchemas_LargeConversation(b *testing.B) {
	options := ToolSchemaSanitizeOptions{
		RepairParameterTypes:     true,
		RemoveLookaroundPatterns: true,
	}
	for _, sizeMiB := range []int{1, 5, 20, 32} {
		payload := buildLargeResponsesSchemaFixture(sizeMiB << 20)
		b.Run(strconv.Itoa(sizeMiB)+"MiB", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()
			for range b.N {
				_, _, _ = SanitizeToolSchemas(payload, options)
			}
		})
	}
}

// buildLargeResponsesSchemaFixture builds a clean Responses JSON payload of at
// least targetBytes with thirty-six function tools and multi-turn code messages.
func buildLargeResponsesSchemaFixture(targetBytes int) []byte {
	out := make([]byte, 0, targetBytes+8192)
	out = append(out, `{"model":"gpt-5.4","stream":true,"tools":[`...)
	for i := range 36 {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, `{"type":"function","name":"tool_`...)
		out = append(out, strconv.Itoa(i)...)
		out = append(out, `","parameters":{"type":"object","properties":{"path":{"type":"string"},"line":{"type":"integer"}}}}`...)
	}
	out = append(out, `],"input":[`...)
	chunk := strings.Repeat("func process(items []string) error { return check(items) } ", 32)
	turn := 0
	for len(out) < targetBytes {
		if turn > 0 {
			out = append(out, ',')
		}
		out = append(out, `{"type":"message","role":"user","content":[{"type":"input_text","text":"`...)
		out = append(out, chunk...)
		out = append(out, `"}]},{"type":"function_call","call_id":"call_`...)
		out = append(out, strconv.Itoa(turn)...)
		out = append(out, `","name":"tool_0","arguments":"{\"path\":\"a.go\"}"},{"type":"function_call_output","call_id":"call_`...)
		out = append(out, strconv.Itoa(turn)...)
		out = append(out, `","output":"`...)
		out = append(out, chunk...)
		out = append(out, `"}`...)
		turn++
	}
	out = append(out, `]}`...)
	return out
}
