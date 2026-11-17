package provider

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// TestPatchGrokResponsesBodySetsMappedModelAndDropsUnsupportedFields patches a
// Responses body with a supplied Grok model and unsupported cache/safety
// fields. The final model and reasoning effort remain while those unsupported
// fields disappear.
func TestPatchGrokResponsesBodySetsMappedModelAndDropsUnsupportedFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok",
		"input": "hello",
		"prompt_cache_retention": "24h",
		"safety_identifier": "user-1",
		"reasoning": {"effort": "high"}
	}`)

	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.3", gjson.GetBytes(patched, "model").String())
	require.False(t, gjson.GetBytes(patched, "prompt_cache_retention").Exists())
	require.False(t, gjson.GetBytes(patched, "safety_identifier").Exists())
	require.Equal(t, "high", gjson.GetBytes(patched, "reasoning.effort").String())
}

// TestPatchGrokResponsesBodyDropsRedundantViewImageForCurrentInlineImage
// supplies current inline image content with view_image and shell declarations
// in regular/Lite tool forms. The patched tools retain shell_command and
// remove redundant view_image.
func TestPatchGrokResponsesBodyDropsRedundantViewImageForCurrentInlineImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string // Grok normalization scenario label
		body string // incoming Grok request JSON
	}{
		{
			name: "top-level tools",
			body: `{
				"model":"grok-4.6",
				"input":[{"type":"message","role":"user","content":[
					{"type":"input_text","text":"What text is in this image?"},
					{"type":"input_image","image_url":"data:image/png;base64,AA=="}
				]}],
				"tools":[
					{"type":"function","name":"view_image","parameters":{"type":"object"}},
					{"type":"function","name":"shell_command","parameters":{"type":"object"}}
				]
			}`,
		},
		{
			name: "Responses Lite additional tools",
			body: `{
				"model":"grok-4.6",
				"input":[
					{"type":"additional_tools","role":"developer","tools":[
						{"type":"function","name":"view_image","parameters":{"type":"object"}},
						{"type":"function","name":"shell_command","parameters":{"type":"object"}}
					]},
					{"type":"message","role":"user","content":[
						{"type":"input_text","text":"What text is in this image?"},
						{"type":"input_image","image_url":"data:image/png;base64,AA=="}
					]}
				]
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			patched, err := grokAttributeCodec().PatchGrokResponsesBody([]byte(tt.body), "grok-4.6")
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(patched, `tools.#(name=="view_image")`).Exists())
			require.Equal(t, "shell_command", gjson.GetBytes(patched, "tools.0.name").String())
		})
	}
}

// TestPatchGrokResponsesBodyKeepsNonRedundantViewImage varies absent current
// image, historical image, explicit view_image selection, and required
// single-tool choice. Patching retains view_image in each case where the
// test's intent needs it.
func TestPatchGrokResponsesBodyKeepsNonRedundantViewImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string // Grok normalization scenario label
		body string // incoming Grok request JSON
	}{
		{
			name: "current turn has no inline image",
			body: `{"input":[{"role":"user","content":[{"type":"input_text","text":"Inspect a local image"}]}],"tools":[{"type":"function","name":"view_image"}]}`,
		},
		{
			name: "inline image is only historical",
			body: `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]},{"role":"assistant","content":[{"type":"output_text","text":"Done"}]},{"role":"user","content":[{"type":"input_text","text":"Inspect another local image"}]}],"tools":[{"type":"function","name":"view_image"}]}`,
		},
		{
			name: "view image is explicitly selected",
			body: `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],"tools":[{"type":"function","name":"view_image"}],"tool_choice":{"type":"function","name":"view_image"}}`,
		},
		{
			name: "required with view image as the only tool",
			body: `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],"tools":[{"type":"function","name":"view_image"}],"tool_choice":"required"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			patched, err := grokAttributeCodec().PatchGrokResponsesBody([]byte(tt.body), "grok-4.6")
			require.NoError(t, err)
			require.Equal(t, "view_image", gjson.GetBytes(patched, "tools.0.name").String())
		})
	}
}

// TestPatchGrokResponsesBodyDropsViewImageOnlyToolMetadata supplies an inline
// image and a sole passive view_image tool with auto/parallel controls.
// Patching removes the redundant tool list and both orphan controls.
func TestPatchGrokResponsesBodyDropsViewImageOnlyToolMetadata(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],
		"tools":[{"type":"function","name":"view_image"}],
		"tool_choice":"auto",
		"parallel_tool_calls":true
	}`)

	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.6")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(patched, "parallel_tool_calls").Exists())
}

// TestPatchGrokResponsesBodySanitizesComposerReasoningParameters compares
// Composer aliases and prefixed identities with Grok reasoning models.
// Composer requests lose reasoning fields, while tested Grok models retain
// supported medium values and remove camel-case effort.
func TestPatchGrokResponsesBodySanitizesComposerReasoningParameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string // mapped model normalization case
		upstreamModel string // final upstream model identity
		wantReasoning bool   // expected reasoning field retention
	}{
		{name: "composer fast", upstreamModel: "grok-composer-2.5-fast"},
		{name: "composer shorthand", upstreamModel: "grok-composer"},
		{name: "composer legacy alias", upstreamModel: "composer-2.5"},
		{name: "provider-prefixed composer", upstreamModel: "xai/grok-composer-2.5-fast"},
		{name: "grok 4.5", upstreamModel: "grok-4.5", wantReasoning: true},
		{name: "grok 4.6", upstreamModel: "grok-4.6", wantReasoning: true},
		{name: "grok 4.6 latest", upstreamModel: "grok-4.6-latest", wantReasoning: true},
	}

	bodyTemplate := []byte(`{
		"model": "grok",
		"input": "hello",
		"reasoning": {"effort": "medium", "summary": "auto"},
		"reasoning_effort": "medium",
		"reasoningEffort": "medium"
	}`)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patched, err := grokAttributeCodec().PatchGrokResponsesBody(append([]byte(nil), bodyTemplate...), tt.upstreamModel)
			require.NoError(t, err)
			require.True(t, json.Valid(patched))
			require.Equal(t, tt.upstreamModel, gjson.GetBytes(patched, "model").String())

			if tt.wantReasoning {
				require.Equal(t, "medium", gjson.GetBytes(patched, "reasoning.effort").String())
				require.Equal(t, "medium", gjson.GetBytes(patched, "reasoning_effort").String())
				require.False(t, gjson.GetBytes(patched, "reasoningEffort").Exists())
				return
			}

			require.False(t, gjson.GetBytes(patched, "reasoning").Exists())
			require.False(t, gjson.GetBytes(patched, "reasoning_effort").Exists())
			require.False(t, gjson.GetBytes(patched, "reasoningEffort").Exists())
		})
	}
}

// TestPatchGrokResponsesBodyDropsGrok45ReasoningUnsupportedFields supplies
// penalty aliases and stop fields for Grok 4.5. Patching keeps the selected
// model but removes every listed unsupported sampling/stop field.
func TestPatchGrokResponsesBodyDropsGrok45ReasoningUnsupportedFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-latest",
		"input": "hello",
		"presence_penalty": 0.1,
		"presencePenalty": 0.2,
		"frequency_penalty": 0.3,
		"frequencyPenalty": 0.4,
		"stop": ["done"]
	}`)

	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.5", gjson.GetBytes(patched, "model").String())
	require.False(t, gjson.GetBytes(patched, "presence_penalty").Exists())
	require.False(t, gjson.GetBytes(patched, "presencePenalty").Exists())
	require.False(t, gjson.GetBytes(patched, "frequency_penalty").Exists())
	require.False(t, gjson.GetBytes(patched, "frequencyPenalty").Exists())
	require.False(t, gjson.GetBytes(patched, "stop").Exists())
}

// TestPatchGrokResponsesBodyKeepsPenaltyAndStopFieldsForNon45Models uses a
// Grok 4.3 body carrying penalties and stop. The patched model and listed
// sampling values survive as valid JSON.
func TestPatchGrokResponsesBodyKeepsPenaltyAndStopFieldsForNon45Models(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-4.3",
		"input": "hello",
		"presence_penalty": 0.1,
		"frequency_penalty": 0.2,
		"stop": ["done"]
	}`)

	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.3", gjson.GetBytes(patched, "model").String())
	require.Equal(t, 0.1, gjson.GetBytes(patched, "presence_penalty").Float())
	require.Equal(t, 0.2, gjson.GetBytes(patched, "frequency_penalty").Float())
	require.Len(t, gjson.GetBytes(patched, "stop").Array(), 1)
}

// TestPatchGrokResponsesBodyDropsLogprobsForGrok420Family supplies both
// logprobs and top_logprobs on an explicit Grok 4.20 reasoning model request.
// Patching removes the two fields from the final response-generation body.
func TestPatchGrokResponsesBodyDropsLogprobsForGrok420Family(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"grok-4.20-0309-reasoning","input":"hello","logprobs":true,"top_logprobs":5}`)
	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.20-0309-reasoning")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "logprobs").Exists())
	require.False(t, gjson.GetBytes(patched, "top_logprobs").Exists())
}

// TestPatchGrokResponsesBodyNormalizesReasoningEffortAliases supplies
// minimal/xhigh spellings through nested and flat fields for two Grok
// families. Resolved attributes choose the expected family-specific effort and
// remove camel-case encoding.
func TestPatchGrokResponsesBodyNormalizesReasoningEffortAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string // mapped model normalization case
		body          string // incoming reasoning request JSON
		upstreamModel string // final upstream model identity
		path          string // checked outgoing effort path
		want          string // expected outgoing field value
	}{
		{name: "minimal nested", body: `{"input":"hi","reasoning":{"effort":"minimal"}}`, upstreamModel: "grok-4.5", path: "reasoning.effort", want: "low"},
		{name: "xhigh snake for 4.5", body: `{"input":"hi","reasoning_effort":"xhigh"}`, upstreamModel: "grok-4.5", path: "reasoning_effort", want: "xhigh"},
		{name: "xhigh nested for 4.6", body: `{"input":"hi","reasoning":{"effort":"xhigh"}}`, upstreamModel: "grok-4.6", path: "reasoning.effort", want: "xhigh"},
		{name: "xhigh snake for 4.6 latest", body: `{"input":"hi","reasoning_effort":"xhigh"}`, upstreamModel: "grok-4.6-latest", path: "reasoning_effort", want: "xhigh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patched, err := grokAttributeCodec().PatchGrokResponsesBody([]byte(tt.body), tt.upstreamModel)
			require.NoError(t, err)
			require.Equal(t, tt.want, gjson.GetBytes(patched, tt.path).String(), string(patched))
			require.False(t, gjson.GetBytes(patched, "reasoningEffort").Exists())
		})
	}
}

// TestPatchGrokResponsesBodyAddsDefaultFunctionParameters supplies absent/null
// function parameters beside a large numeric tool value. Patching creates
// object/properties defaults while retaining the exact large number encoding.
func TestPatchGrokResponsesBodyAddsDefaultFunctionParameters(t *testing.T) {
	patched, err := grokAttributeCodec().PatchGrokResponsesBody(
		[]byte(`{"input":"hi","tools":[{"type":"function","name":"lookup","large_id":9007199254740993},{"type":"function","name":"wait","parameters":null}]}`),
		"grok-4.5",
	)
	require.NoError(t, err)
	for _, tool := range gjson.GetBytes(patched, "tools").Array() {
		require.Equal(t, "object", tool.Get("parameters.type").String(), string(patched))
		require.True(t, tool.Get("parameters.properties").IsObject(), string(patched))
	}
	require.Equal(t, "9007199254740993", gjson.GetBytes(patched, "tools.0.large_id").Raw)
}

// TestNormalizeGrokChatReasoningEffort compares camel ultra, supported xhigh,
// and Composer high in Chat. Normalization uses the expected snake-case value
// or removes unsupported Composer effort.
func TestNormalizeGrokChatReasoningEffort(t *testing.T) {
	patched, err := grokAttributeCodec().NormalizeGrokChatReasoningEffort([]byte(`{"reasoningEffort":"ultra"}`), "grok-4.3")
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(patched, "reasoning_effort").String())
	require.False(t, gjson.GetBytes(patched, "reasoningEffort").Exists())

	patched, err = grokAttributeCodec().NormalizeGrokChatReasoningEffort([]byte(`{"reasoning_effort":"xhigh"}`), "grok-4.6")
	require.NoError(t, err)
	require.Equal(t, "xhigh", gjson.GetBytes(patched, "reasoning_effort").String())

	patched, err = grokAttributeCodec().NormalizeGrokChatReasoningEffort([]byte(`{"reasoning_effort":"high"}`), "grok-composer-2.5-fast")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "reasoning_effort").Exists())
}

// TestPatchGrokResponsesBodyDropsNestedUnsupportedFields places
// external_web_access throughout tools, schema, metadata, and the body.
// Patching removes that unsupported text, retains the named function, and
// drops metadata.
func TestPatchGrokResponsesBodyDropsNestedUnsupportedFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok",
		"input": "hello",
		"external_web_access": true,
		"tools": [
			{"type": "function", "name": "kept_fn", "external_web_access": true, "parameters": {"type": "object", "properties": {"q": {"type": "string", "external_web_access": true}}}}
		],
		"metadata": {"external_web_access": false, "large_id": 9007199254740993}
	}`)

	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.False(t, strings.Contains(string(patched), "external_web_access"))
	require.Equal(t, "kept_fn", gjson.GetBytes(patched, "tools.0.name").String())
	require.False(t, gjson.GetBytes(patched, "metadata").Exists())
}

// TestPatchGrokResponsesBodyDropsToolChoiceWhenNoSupportedToolsRemain supplies
// namespace and foreign image tools with namespace choice. Patching removes
// the empty supported tool list and its remaining tool choice.
func TestPatchGrokResponsesBodyDropsToolChoiceWhenNoSupportedToolsRemain(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok",
		"input": "hello",
		"tools": [
			{"type": "namespace", "namespace": "functions"},
			{"type": "image_generation", "model": "gpt-image-2"}
		],
		"tool_choice": {"type": "namespace", "namespace": "functions"}
	}`)

	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
}

// TestPatchGrokResponsesBodyRestoresCompactInput supplies a compaction item
// with encrypted state and visible summary before user input. Patching
// converts encrypted history to reasoning and inserts the summary before the
// continuing user message.
func TestPatchGrokResponsesBodyRestoresCompactInput(t *testing.T) {
	body := []byte(`{
		"model":"grok-4.5",
		"input":[
			{"id":"cmp_1","type":"compaction","status":"completed","encrypted_content":"grok-encrypted-state","summary":[{"type":"summary_text","text":"summary text"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}
		]
	}`)

	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.Equal(t, "reasoning", gjson.GetBytes(patched, "input.0.type").String())
	require.Equal(t, "grok-encrypted-state", gjson.GetBytes(patched, "input.0.encrypted_content").String())
	require.Equal(t, "message", gjson.GetBytes(patched, "input.1.type").String())
	require.Contains(t, gjson.GetBytes(patched, "input.1.content.0.text").String(), "summary text")
	require.Equal(t, "continue", gjson.GetBytes(patched, "input.2.content.0.text").String())
}

// TestParseGrokMediaRequestBuildsMultipartModerationBody creates a multipart
// image edit with prompt/model and PNG bytes. Parsing retains prompt/model,
// and moderation encoding exposes the image as a data URL.
func TestParseGrokMediaRequestBuildsMultipartModerationBody(t *testing.T) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	require.NoError(t, writer.WriteField("prompt", "edit this private image"))
	require.NoError(t, writer.WriteField("model", "grok-imagine-edit"))
	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Disposition", `form-data; name="image"; filename="input.png"`)
	partHeader.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(partHeader)
	require.NoError(t, err)
	_, err = part.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	require.NoError(t, err)
	// Close the multipart writer before parsing to append its terminating boundary;
	// the fixture then represents a complete media upload for moderation-body extraction.
	require.NoError(t, writer.Close())

	info := GrokMediaCodec().ParseGrokMediaRequest(writer.FormDataContentType(), buf.Bytes())
	require.Equal(t, "grok-imagine-edit", info.Model)
	require.Equal(t, "edit this private image", info.Prompt)

	moderationBody := info.ModerationBody()
	require.NotEmpty(t, moderationBody)
	require.Equal(t, "edit this private image", gjson.GetBytes(moderationBody, "prompt").String())
	require.True(t, strings.HasPrefix(gjson.GetBytes(moderationBody, "images.0.image_url").String(), "data:image/"))
}

// TestParseGrokMediaVideoRequestResolution parses a JSON video request with
// explicit 720p resolution. The media request observation retains both the
// video model and supplied resolution.
func TestParseGrokMediaVideoRequestResolution(t *testing.T) {
	info := GrokMediaCodec().ParseGrokMediaRequest("application/json", []byte(`{"model":"grok-imagine-video","prompt":"waves","resolution":"720p"}`))

	require.Equal(t, "grok-imagine-video", info.Model)
	require.Equal(t, "720p", info.Resolution)
}

// TestParseGrokMediaRequestAcceptsOfficialImageURLFields supplies official url
// objects in the direct image and reference_images fields of a video request.
// Media parsing collects both source URLs in encounter order.
func TestParseGrokMediaRequestAcceptsOfficialImageURLFields(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-video-1.5",
		"image":{"url":"https://example.com/source.png"},
		"reference_images":[{"url":"https://example.com/reference.png"}]
	}`)

	info := GrokMediaCodec().ParseGrokMediaRequest("application/json", body)

	require.Equal(t, []string{
		"https://example.com/source.png",
		"https://example.com/reference.png",
	}, info.InputImageURLs)
}

// TestNormalizeGrokMediaForwardBodyCanonicalizesImageURLAlias uses image_url
// on a video input image object. Forwarding moves it to image.url, retains
// model/content type, and removes the alias field.
func TestNormalizeGrokMediaForwardBodyCanonicalizesImageURLAlias(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-video-1.5",
		"prompt":"animate",
		"image":{"image_url":"https://example.com/source.png"},
		"duration":8
	}`)

	out, contentType, err := GrokMediaCodec().NormalizeGrokMediaForwardBody(grok.GrokMediaEndpointVideosGenerations, body, "application/json")

	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, "grok-imagine-video-1.5", gjson.GetBytes(out, "model").String())
	require.Equal(t, "https://example.com/source.png", gjson.GetBytes(out, "image.url").String())
	require.False(t, gjson.GetBytes(out, "image.image_url").Exists())
}

// TestNormalizeGrokMediaForwardBodyPreservesImageToVideoModelForOfficialURL
// supplies a Grok video 1.5 request with prompt and an official image.url.
// Forward normalization retains the complete image-to-video model and its
// input source URL.
func TestNormalizeGrokMediaForwardBodyPreservesImageToVideoModelForOfficialURL(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-video-1.5",
		"prompt":"animate",
		"image":{"url":"https://example.com/source.png"}
	}`)

	out, _, err := GrokMediaCodec().NormalizeGrokMediaForwardBody(grok.GrokMediaEndpointVideosGenerations, body, "application/json")

	require.NoError(t, err)
	require.Equal(t, "grok-imagine-video-1.5", gjson.GetBytes(out, "model").String())
	require.Equal(t, "https://example.com/source.png", gjson.GetBytes(out, "image.url").String())
}

// TestCanonicalizeGrokMediaImageURLFieldsPreservesOfficialURL mixes official
// and legacy URLs across image, images, references, and mask. Canonicalization
// prefers nonblank official URL and transfers aliases before removing their
// fields.
func TestCanonicalizeGrokMediaImageURLFieldsPreservesOfficialURL(t *testing.T) {
	body := []byte(`{
		"image":{"url":"https://example.com/official.png","image_url":"https://example.com/legacy.png"},
		"images":[
			{"image_url":"https://example.com/first.png"},
			{"url":"https://example.com/second.png"}
		],
		"reference_images":[{"image_url":"https://example.com/reference.png"}],
		"mask":{"image_url":"https://example.com/mask.png"}
	}`)

	out, err := GrokMediaCodec().CanonicalizeGrokMediaImageURLFields(body, "image", "images", "reference_images", "mask")

	require.NoError(t, err)
	require.Equal(t, "https://example.com/official.png", gjson.GetBytes(out, "image.url").String())
	require.False(t, gjson.GetBytes(out, "image.image_url").Exists())
	require.Equal(t, "https://example.com/first.png", gjson.GetBytes(out, "images.0.url").String())
	require.False(t, gjson.GetBytes(out, "images.0.image_url").Exists())
	require.Equal(t, "https://example.com/second.png", gjson.GetBytes(out, "images.1.url").String())
	require.Equal(t, "https://example.com/reference.png", gjson.GetBytes(out, "reference_images.0.url").String())
	require.False(t, gjson.GetBytes(out, "reference_images.0.image_url").Exists())
	require.Equal(t, "https://example.com/mask.png", gjson.GetBytes(out, "mask.url").String())
	require.False(t, gjson.GetBytes(out, "mask.image_url").Exists())
}

// TestCanonicalizeGrokMediaImageURLFieldsReplacesEmptyOfficialURL supplies a
// whitespace-only official image URL beside a nonblank legacy image_url alias.
// Canonicalization selects the alias value under url and removes the legacy
// field.
func TestCanonicalizeGrokMediaImageURLFieldsReplacesEmptyOfficialURL(t *testing.T) {
	body := []byte(`{"image":{"url":" ","image_url":"https://example.com/legacy.png"}}`)

	out, err := GrokMediaCodec().CanonicalizeGrokMediaImageURLFields(body, "image")

	require.NoError(t, err)
	require.Equal(t, "https://example.com/legacy.png", gjson.GetBytes(out, "image.url").String())
	require.False(t, gjson.GetBytes(out, "image.image_url").Exists())
}

// TestPrepareGrokImageEditNormalizesOfficialImageObjects mixes nested
// image_url, string array source, and mask aliases in an image edit.
// Preparation produces typed image_url objects with nonblank url and removes
// nested aliases.
func TestPrepareGrokImageEditNormalizesOfficialImageObjects(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-image-quality",
		"image":{"image_url":{"url":"https://example.com/first.png"}},
		"images":["https://example.com/second.png"],
		"mask":{"image_url":"https://example.com/mask.png"}
	}`)

	out, contentType, err := GrokMediaCodec().PrepareGrokMediaForwardBody(grok.GrokMediaEndpointImagesEdits, body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	for _, path := range []string{"image", "images.0", "mask"} {
		require.Equal(t, "image_url", gjson.GetBytes(out, path+".type").String())
		require.NotEmpty(t, gjson.GetBytes(out, path+".url").String())
		require.False(t, gjson.GetBytes(out, path+".image_url").Exists())
	}
}

// TestPrepareGrokImageEditRejectsMoreThanThreeSources supplies four source
// images to Grok edit preparation. It returns a source-count error with nil
// output and the maximum-of-three explanation.
func TestPrepareGrokImageEditRejectsMoreThanThreeSources(t *testing.T) {
	body := []byte(`{"images":["https://example.com/1.png","https://example.com/2.png","https://example.com/3.png","https://example.com/4.png"]}`)

	out, _, err := GrokMediaCodec().PrepareGrokMediaForwardBody(grok.GrokMediaEndpointImagesEdits, body, "application/json")
	require.Error(t, err)
	require.Nil(t, out)
	require.Contains(t, err.Error(), "maximum of 3 source images")
}

// TestGrokMediaForwardBodyPreservesModel varies image/video endpoint aliases
// and optional input image across explicit media models. Forward normalization
// retains each complete model identity and canonicalizes the tested image URL.
func TestGrokMediaForwardBodyPreservesModel(t *testing.T) {
	tests := []struct {
		name string // mapped model normalization case
		// selected media generation endpoint
		endpoint      grok.GrokMediaEndpoint
		model         string // complete requested media model
		hasInputImage bool   // source image fixture inclusion
		want          string // expected outgoing field value
	}{
		{name: "image generation alias", endpoint: grok.GrokMediaEndpointImagesGenerations, model: "grok-imagine", want: "grok-imagine"},
		{name: "image edit alias", endpoint: grok.GrokMediaEndpointImagesEdits, model: "grok-imagine", want: "grok-imagine"},
		{name: "image quality passthrough", endpoint: grok.GrokMediaEndpointImagesGenerations, model: "grok-imagine-image-quality", want: "grok-imagine-image-quality"},
		{name: "image fast passthrough", endpoint: grok.GrokMediaEndpointImagesGenerations, model: "grok-imagine-image", want: "grok-imagine-image"},
		{name: "video passthrough", endpoint: grok.GrokMediaEndpointVideosGenerations, model: "grok-imagine-video", want: "grok-imagine-video"},
		{name: "video 1.5 text-only remains explicit", endpoint: grok.GrokMediaEndpointVideosGenerations, model: "grok-imagine-video-1.5", want: "grok-imagine-video-1.5"},
		{name: "video 1.5 image-to-video passthrough", endpoint: grok.GrokMediaEndpointVideosGenerations, model: "grok-imagine-video-1.5", hasInputImage: true, want: "grok-imagine-video-1.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := map[string]any{"model": tt.model}
			if tt.hasInputImage {
				request["image"] = map[string]any{"image_url": "https://example.com/input.png"}
			}
			body, err := json.Marshal(request)
			require.NoError(t, err)
			body, _, err = GrokMediaCodec().NormalizeGrokMediaForwardBody(tt.endpoint, body, "application/json")
			require.NoError(t, err)
			require.Equal(t, tt.want, gjson.GetBytes(body, "model").String())
			if tt.hasInputImage {
				require.Equal(t, "https://example.com/input.png", gjson.GetBytes(body, "image.url").String())
			}
		})
	}
}

// TestExtractGrokMediaVideoRequestIDPreservesExistingPrecedence supplies
// direct and nested request, task, and generic IDs together. Extraction
// selects the top-level request_id over the other candidates.
func TestExtractGrokMediaVideoRequestIDPreservesExistingPrecedence(t *testing.T) {
	body := []byte(`{
		"request_id":"request-id",
		"id":"id",
		"task_id":"task-id",
		"data":{"request_id":"data-request-id","id":"data-id","task_id":"data-task-id"},
		"video":{"request_id":"video-request-id","id":"video-id","task_id":"video-task-id"}
	}`)

	require.Equal(t, "request-id", GrokMediaCodec().ExtractGrokMediaVideoRequestID(body))
}

// TestGrokCompactionBlobRecoveryStripsCompactionItem detects a compaction
// decoding rejection and constructs its retry body. The compaction item
// disappears while the following message becomes the first input item.
func TestGrokCompactionBlobRecoveryStripsCompactionItem(t *testing.T) {
	body := []byte(`{"model":"grok","input":[{"type":"compaction","id":"cmp_1","encrypted_content":"blob"},{"type":"message","role":"user","content":"hi"}]}`)
	require.True(t, grokAttributeCodec().IsGrokInvalidEncryptedContentResponse(http.StatusUnprocessableEntity, []byte(`{"code":"invalid_compaction","error":"could not decode the compaction blob"}`)))
	retry, changed, err := grokAttributeCodec().TrimGrokInvalidEncryptedContentRetryBody(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(retry, "input.0.type").String())
	require.False(t, gjson.GetBytes(retry, "input.#(type==\"compaction\")").Exists())
}

// TestPatchGrokResponsesBody_StripsReasoningContentNull supplies reasoning
// summary with null content between ordinary messages. Patching keeps all
// three input items and reasoning summary while removing the null content
// field.
func TestPatchGrokResponsesBody_StripsReasoningContentNull(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-latest",
		"input": [
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking..."}],"content":null,"encrypted_content":null},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello!"}]}
		]
	}`)

	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))

	input := gjson.GetBytes(patched, "input")
	require.True(t, input.IsArray())

	items := input.Array()
	require.Len(t, items, 3)

	reasoning := items[1]
	require.Equal(t, "reasoning", reasoning.Get("type").String())
	require.True(t, reasoning.Get("summary").Exists(), "summary should be preserved")
	require.False(t, reasoning.Get("content").Exists(), "content: null should be stripped")
}

// TestPatchGrokResponsesBody_KeepsReasoningContentNonNull supplies a reasoning
// item whose content is a nonnull string. Patching retains that exact content
// value in the resulting history.
func TestPatchGrokResponsesBody_KeepsReasoningContentNonNull(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-latest",
		"input": [
			{"type":"reasoning","summary":[{"type":"summary_text","text":"ok"}],"content":"real content"}
		]
	}`)

	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)

	reasoning := gjson.GetBytes(patched, "input.0")
	require.Equal(t, "real content", reasoning.Get("content").String(), "non-null content must not be stripped")
}

// TestPatchGrokResponsesBody_MultipleReasoningContentNull places null content
// on two reasoning items around a user message. Patching retains all three
// items and removes both null fields.
func TestPatchGrokResponsesBody_MultipleReasoningContentNull(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-latest",
		"input": [
			{"type":"reasoning","summary":[{"type":"summary_text","text":"r1"}],"content":null},
			{"type":"message","role":"user","content":"hi"},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"r2"}],"content":null}
		]
	}`)

	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)

	items := gjson.GetBytes(patched, "input").Array()
	require.Len(t, items, 3)

	require.False(t, items[0].Get("content").Exists())
	require.False(t, items[2].Get("content").Exists())
}

// TestPatchGrokResponsesBodyCombinesAdditionalToolsAndDropsOrphanControls
// duplicates a function across regular and Lite tool lists, then tests
// absent/malformed tools. Patching deduplicates the function and removes
// controls when no usable tools remain.
func TestPatchGrokResponsesBodyCombinesAdditionalToolsAndDropsOrphanControls(t *testing.T) {
	body := []byte(`{
		"input":[{"type":"additional_tools","tools":[{"type":"function","name":"lookup"}]},{"role":"user","content":"hi"}],
		"tools":[{"type":"function","name":"lookup"}],
		"tool_choice":"auto","parallel_tool_calls":true
	}`)
	patched, err := grokAttributeCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.Equal(t, int64(1), gjson.GetBytes(patched, "tools.#").Int())
	require.Equal(t, "lookup", gjson.GetBytes(patched, "tools.0.name").String())
	require.Equal(t, "message", gjson.GetBytes(patched, "input.0.type").String())

	withoutTools, err := grokAttributeCodec().PatchGrokResponsesBody([]byte(`{"input":"hi","tool_choice":"auto","parallel_tool_calls":true}`), "grok-4.5")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(withoutTools, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(withoutTools, "parallel_tool_calls").Exists())

	for _, malformedTools := range []string{"null", `{}`, `"invalid"`} {
		malformedBody := []byte(`{"input":"hi","tools":` + malformedTools + `,"tool_choice":"auto","parallel_tool_calls":true}`)
		patched, err := grokAttributeCodec().PatchGrokResponsesBody(malformedBody, "grok-4.5")
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(patched, "tool_choice").Exists(), string(patched))
		require.False(t, gjson.GetBytes(patched, "parallel_tool_calls").Exists(), string(patched))
	}
}

// TestSanitizeGrokCompactionReplayBodyPreservesVisibleSummary supplies opaque
// compaction plus visible summary and previous-response ID. Sanitation removes
// the continuation/reasoning state, retains summary and user history, and
// recognizes the tested decoder errors.
func TestSanitizeGrokCompactionReplayBodyPreservesVisibleSummary(t *testing.T) {
	body := []byte(`{
		"previous_response_id":"resp_stale",
		"input":[{"type":"compaction","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"visible history"}]},{"type":"message","role":"user","content":"continue"}]
	}`)
	patched, changed, err := grokAttributeCodec().SanitizeGrokCompactionReplayBody(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(patched, "previous_response_id").Exists())
	require.False(t, gjson.GetBytes(patched, `input.#(type=="reasoning")`).Exists())
	require.Contains(t, gjson.GetBytes(patched, "input.0.content.0.text").String(), "visible history")
	require.Equal(t, "continue", gjson.GetBytes(patched, "input.1.content").String())
	require.True(t, grokAttributeCodec().IsGrokCompactionReplayDecodeError(http.StatusBadRequest, []byte(`{"error":{"message":"failed to deserialize compaction summary history"}}`)))
	require.True(t, grokAttributeCodec().IsGrokCompactionReplayDecodeError(http.StatusBadRequest, []byte(`{"error":{"type":"invalid_request_error"},"detail":"could not decode compaction blob"}`)))
}

// grokAttributeCodec binds offline resolved Grok attributes to the gateway
// body codec for tests. Each lookup uses the supplied final model identity and
// an empty attribute override.
func grokAttributeCodec() grok.BodyCodec {
	codec := GrokBodyCodec()
	codec.Attributes = func(model string) modelcatalog.Attributes {
		return testkit.OfflineView().Resolve(modelrules.Target{Platform: "grok", Model: model}, modelcatalog.Attributes{}).Attributes
	}
	return codec
}

// TestGrokVideoCustomResolutionAndLongDuration parses JSON and multipart task
// submissions. Both formats retain the custom video price label and all requested
// seconds while still exposing recognized image geometry to image endpoints.
func TestGrokVideoCustomResolutionAndLongDuration(t *testing.T) {
	codec := GrokMediaCodec()
	info := codec.ParseGrokMediaRequest("application/json", []byte(`{"model":"custom-video","resolution":"2K","duration":60}`))
	require.Equal(t, "2k", info.Resolution)
	require.Equal(t, "2k", info.ImageResolution)
	require.Equal(t, 60, info.DurationSeconds)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "custom-video"))
	require.NoError(t, writer.WriteField("resolution", "768P"))
	require.NoError(t, writer.WriteField("duration", "999"))
	require.NoError(t, writer.Close())
	info = codec.ParseGrokMediaRequest(writer.FormDataContentType(), body.Bytes())
	require.Equal(t, "768p", info.Resolution)
	require.Equal(t, 999, info.DurationSeconds)
}
