//go:build unit

package provider_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	grok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPatchGrokResponsesBodySetsMappedModelAndDropsUnsupportedFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok",
		"input": "hello",
		"prompt_cache_retention": "24h",
		"safety_identifier": "user-1",
		"reasoning": {"effort": "high"}
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.3", gjson.GetBytes(patched, "model").String())
	require.False(t, gjson.GetBytes(patched, "prompt_cache_retention").Exists())
	require.False(t, gjson.GetBytes(patched, "safety_identifier").Exists())
	require.Equal(t, "high", gjson.GetBytes(patched, "reasoning.effort").String())
}

func TestPatchGrokResponsesBodyDropsRedundantViewImageForCurrentInlineImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
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
			patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody([]byte(tt.body), "grok-4.6")
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(patched, `tools.#(name=="view_image")`).Exists())
			require.Equal(t, "shell_command", gjson.GetBytes(patched, "tools.0.name").String())
		})
	}
}

func TestPatchGrokResponsesBodyKeepsNonRedundantViewImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
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
			patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody([]byte(tt.body), "grok-4.6")
			require.NoError(t, err)
			require.Equal(t, "view_image", gjson.GetBytes(patched, "tools.0.name").String())
		})
	}
}

func TestPatchGrokResponsesBodyDropsViewImageOnlyToolMetadata(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],
		"tools":[{"type":"function","name":"view_image"}],
		"tool_choice":"auto",
		"parallel_tool_calls":true
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.6")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(patched, "parallel_tool_calls").Exists())
}

func TestPatchGrokResponsesBodySanitizesComposerReasoningParameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		upstreamModel string
		wantReasoning bool
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
			patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(append([]byte(nil), bodyTemplate...), tt.upstreamModel)
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

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.5", gjson.GetBytes(patched, "model").String())
	require.False(t, gjson.GetBytes(patched, "presence_penalty").Exists())
	require.False(t, gjson.GetBytes(patched, "presencePenalty").Exists())
	require.False(t, gjson.GetBytes(patched, "frequency_penalty").Exists())
	require.False(t, gjson.GetBytes(patched, "frequencyPenalty").Exists())
	require.False(t, gjson.GetBytes(patched, "stop").Exists())
}

func TestPatchGrokResponsesBodyKeepsPenaltyAndStopFieldsForNon45Models(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-4.3",
		"input": "hello",
		"presence_penalty": 0.1,
		"frequency_penalty": 0.2,
		"stop": ["done"]
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.3", gjson.GetBytes(patched, "model").String())
	require.Equal(t, 0.1, gjson.GetBytes(patched, "presence_penalty").Float())
	require.Equal(t, 0.2, gjson.GetBytes(patched, "frequency_penalty").Float())
	require.Len(t, gjson.GetBytes(patched, "stop").Array(), 1)
}

func TestPatchGrokResponsesBodyDropsLogprobsForGrok420Family(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"grok-4.20-0309-reasoning","input":"hello","logprobs":true,"top_logprobs":5}`)
	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.20-0309-reasoning")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "logprobs").Exists())
	require.False(t, gjson.GetBytes(patched, "top_logprobs").Exists())
}

func TestPatchGrokResponsesBodyNormalizesReasoningEffortAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		body          string
		upstreamModel string
		path          string
		want          string
	}{
		{name: "minimal nested", body: `{"input":"hi","reasoning":{"effort":"minimal"}}`, upstreamModel: "grok-4.5", path: "reasoning.effort", want: "low"},
		{name: "xhigh stays high for 4.5", body: `{"input":"hi","reasoning_effort":"xhigh"}`, upstreamModel: "grok-4.5", path: "reasoning_effort", want: "high"},
		{name: "xhigh nested for 4.6", body: `{"input":"hi","reasoning":{"effort":"xhigh"}}`, upstreamModel: "grok-4.6", path: "reasoning.effort", want: "xhigh"},
		{name: "xhigh snake for 4.6 latest", body: `{"input":"hi","reasoning_effort":"xhigh"}`, upstreamModel: "grok-4.6-latest", path: "reasoning_effort", want: "xhigh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody([]byte(tt.body), tt.upstreamModel)
			require.NoError(t, err)
			require.Equal(t, tt.want, gjson.GetBytes(patched, tt.path).String(), string(patched))
			require.False(t, gjson.GetBytes(patched, "reasoningEffort").Exists())
		})
	}
}

func TestPatchGrokResponsesBodyAddsDefaultFunctionParameters(t *testing.T) {
	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(
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

func TestNormalizeGrokChatReasoningEffort(t *testing.T) {
	patched, err := gatewayprovider.GrokBodyCodec().NormalizeGrokChatReasoningEffort([]byte(`{"reasoningEffort":"ultra"}`), "grok-4.3")
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(patched, "reasoning_effort").String())
	require.False(t, gjson.GetBytes(patched, "reasoningEffort").Exists())

	patched, err = gatewayprovider.GrokBodyCodec().NormalizeGrokChatReasoningEffort([]byte(`{"reasoning_effort":"xhigh"}`), "grok-4.6")
	require.NoError(t, err)
	require.Equal(t, "xhigh", gjson.GetBytes(patched, "reasoning_effort").String())

	patched, err = gatewayprovider.GrokBodyCodec().NormalizeGrokChatReasoningEffort([]byte(`{"reasoning_effort":"high"}`), "grok-composer-2.5-fast")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "reasoning_effort").Exists())
}

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

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.False(t, strings.Contains(string(patched), "external_web_access"))
	require.Equal(t, "kept_fn", gjson.GetBytes(patched, "tools.0.name").String())
	require.False(t, gjson.GetBytes(patched, "metadata").Exists())
}

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

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
}

func TestPatchGrokResponsesBodyRestoresCompactInput(t *testing.T) {
	body := []byte(`{
		"model":"grok-4.5",
		"input":[
			{"id":"cmp_1","type":"compaction","status":"completed","encrypted_content":"grok-encrypted-state","summary":[{"type":"summary_text","text":"summary text"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}
		]
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.Equal(t, "reasoning", gjson.GetBytes(patched, "input.0.type").String())
	require.Equal(t, "grok-encrypted-state", gjson.GetBytes(patched, "input.0.encrypted_content").String())
	require.Equal(t, "message", gjson.GetBytes(patched, "input.1.type").String())
	require.Contains(t, gjson.GetBytes(patched, "input.1.content.0.text").String(), "summary text")
	require.Equal(t, "continue", gjson.GetBytes(patched, "input.2.content.0.text").String())
}

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
	require.NoError(t, writer.Close())

	info := gatewayprovider.GrokMediaCodec().ParseGrokMediaRequest(writer.FormDataContentType(), buf.Bytes())
	require.Equal(t, "grok-imagine-edit", info.Model)
	require.Equal(t, "edit this private image", info.Prompt)

	moderationBody := info.ModerationBody()
	require.NotEmpty(t, moderationBody)
	require.Equal(t, "edit this private image", gjson.GetBytes(moderationBody, "prompt").String())
	require.True(t, strings.HasPrefix(gjson.GetBytes(moderationBody, "images.0.image_url").String(), "data:image/"))
}

func TestParseGrokMediaVideoRequestResolution(t *testing.T) {
	info := gatewayprovider.GrokMediaCodec().ParseGrokMediaRequest("application/json", []byte(`{"model":"grok-imagine-video","prompt":"waves","resolution":"720p"}`))

	require.Equal(t, "grok-imagine-video", info.Model)
	require.Equal(t, "720p", info.Resolution)
}

func TestParseGrokMediaRequestAcceptsOfficialImageURLFields(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-video-1.5",
		"image":{"url":"https://example.com/source.png"},
		"reference_images":[{"url":"https://example.com/reference.png"}]
	}`)

	info := gatewayprovider.GrokMediaCodec().ParseGrokMediaRequest("application/json", body)

	require.Equal(t, []string{
		"https://example.com/source.png",
		"https://example.com/reference.png",
	}, info.InputImageURLs)
	require.True(t, info.HasInputImage())
}

func TestNormalizeGrokMediaForwardBodyCanonicalizesImageURLAlias(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-video-1.5",
		"prompt":"animate",
		"image":{"image_url":"https://example.com/source.png"},
		"duration":8
	}`)

	out, contentType, err := gatewayprovider.GrokMediaCodec().NormalizeGrokMediaForwardBody(grok.GrokMediaEndpointVideosGenerations, body, "application/json")

	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, "grok-imagine-video-1.5", gjson.GetBytes(out, "model").String())
	require.Equal(t, "https://example.com/source.png", gjson.GetBytes(out, "image.url").String())
	require.False(t, gjson.GetBytes(out, "image.image_url").Exists())
}

func TestNormalizeGrokMediaForwardBodyPreservesImageToVideoModelForOfficialURL(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-video-1.5",
		"prompt":"animate",
		"image":{"url":"https://example.com/source.png"}
	}`)

	out, _, err := gatewayprovider.GrokMediaCodec().NormalizeGrokMediaForwardBody(grok.GrokMediaEndpointVideosGenerations, body, "application/json")

	require.NoError(t, err)
	require.Equal(t, "grok-imagine-video-1.5", gjson.GetBytes(out, "model").String())
	require.Equal(t, "https://example.com/source.png", gjson.GetBytes(out, "image.url").String())
}

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

	out, err := gatewayprovider.GrokMediaCodec().CanonicalizeGrokMediaImageURLFields(body, "image", "images", "reference_images", "mask")

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

func TestCanonicalizeGrokMediaImageURLFieldsReplacesEmptyOfficialURL(t *testing.T) {
	body := []byte(`{"image":{"url":" ","image_url":"https://example.com/legacy.png"}}`)

	out, err := gatewayprovider.GrokMediaCodec().CanonicalizeGrokMediaImageURLFields(body, "image")

	require.NoError(t, err)
	require.Equal(t, "https://example.com/legacy.png", gjson.GetBytes(out, "image.url").String())
	require.False(t, gjson.GetBytes(out, "image.image_url").Exists())
}

func TestPrepareGrokImageEditNormalizesOfficialImageObjects(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-image-quality",
		"image":{"image_url":{"url":"https://example.com/first.png"}},
		"images":["https://example.com/second.png"],
		"mask":{"image_url":"https://example.com/mask.png"}
	}`)

	out, contentType, err := gatewayprovider.GrokMediaCodec().PrepareGrokMediaForwardBody(grok.GrokMediaEndpointImagesEdits, body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	for _, path := range []string{"image", "images.0", "mask"} {
		require.Equal(t, "image_url", gjson.GetBytes(out, path+".type").String())
		require.NotEmpty(t, gjson.GetBytes(out, path+".url").String())
		require.False(t, gjson.GetBytes(out, path+".image_url").Exists())
	}
}

func TestPrepareGrokImageEditRejectsMoreThanThreeSources(t *testing.T) {
	body := []byte(`{"images":["https://example.com/1.png","https://example.com/2.png","https://example.com/3.png","https://example.com/4.png"]}`)

	out, _, err := gatewayprovider.GrokMediaCodec().PrepareGrokMediaForwardBody(grok.GrokMediaEndpointImagesEdits, body, "application/json")
	require.Error(t, err)
	require.Nil(t, out)
	require.Contains(t, err.Error(), "maximum of 3 source images")
}

func TestNormalizeGrokMediaModelForEndpoint(t *testing.T) {
	tests := []struct {
		name          string
		endpoint      grok.GrokMediaEndpoint
		model         string
		hasInputImage bool
		want          string
	}{
		{name: "image generation alias", endpoint: grok.GrokMediaEndpointImagesGenerations, model: "grok-imagine", want: "grok-imagine-image-quality"},
		{name: "image edit alias", endpoint: grok.GrokMediaEndpointImagesEdits, model: "grok-imagine", want: "grok-imagine-image-quality"},
		{name: "image quality passthrough", endpoint: grok.GrokMediaEndpointImagesGenerations, model: "grok-imagine-image-quality", want: "grok-imagine-image-quality"},
		{name: "image fast passthrough", endpoint: grok.GrokMediaEndpointImagesGenerations, model: "grok-imagine-image", want: "grok-imagine-image"},
		{name: "video passthrough", endpoint: grok.GrokMediaEndpointVideosGenerations, model: "grok-imagine-video", want: "grok-imagine-video"},
		{name: "video 1.5 text-only remains explicit", endpoint: grok.GrokMediaEndpointVideosGenerations, model: "grok-imagine-video-1.5", want: "grok-imagine-video-1.5"},
		{name: "video 1.5 image-to-video passthrough", endpoint: grok.GrokMediaEndpointVideosGenerations, model: "grok-imagine-video-1.5", hasInputImage: true, want: "grok-imagine-video-1.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, gatewayprovider.GrokMediaCodec().NormalizeGrokMediaModelForEndpoint(tt.endpoint, tt.model, tt.hasInputImage))
		})
	}
}

func TestExtractGrokMediaVideoRequestIDPreservesExistingPrecedence(t *testing.T) {
	body := []byte(`{
		"request_id":"request-id",
		"id":"id",
		"task_id":"task-id",
		"data":{"request_id":"data-request-id","id":"data-id","task_id":"data-task-id"},
		"video":{"request_id":"video-request-id","id":"video-id","task_id":"video-task-id"}
	}`)

	require.Equal(t, "request-id", gatewayprovider.GrokMediaCodec().ExtractGrokMediaVideoRequestID(body))
}

func TestGrokCompactionBlobRecoveryStripsCompactionItem(t *testing.T) {
	body := []byte(`{"model":"grok","input":[{"type":"compaction","id":"cmp_1","encrypted_content":"blob"},{"type":"message","role":"user","content":"hi"}]}`)
	require.True(t, gatewayprovider.GrokBodyCodec().IsGrokInvalidEncryptedContentResponse(http.StatusUnprocessableEntity, []byte(`{"code":"invalid_compaction","error":"could not decode the compaction blob"}`)))
	retry, changed, err := gatewayprovider.GrokBodyCodec().TrimGrokInvalidEncryptedContentRetryBody(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(retry, "input.0.type").String())
	require.False(t, gjson.GetBytes(retry, "input.#(type==\"compaction\")").Exists())
}

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

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
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

func TestPatchGrokResponsesBody_KeepsReasoningContentNonNull(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-latest",
		"input": [
			{"type":"reasoning","summary":[{"type":"summary_text","text":"ok"}],"content":"real content"}
		]
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)

	reasoning := gjson.GetBytes(patched, "input.0")
	require.Equal(t, "real content", reasoning.Get("content").String(), "non-null content must not be stripped")
}

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

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)

	items := gjson.GetBytes(patched, "input").Array()
	require.Len(t, items, 3)

	require.False(t, items[0].Get("content").Exists())
	require.False(t, items[2].Get("content").Exists())
}
