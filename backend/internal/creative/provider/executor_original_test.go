//go:build unit

package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/stretchr/testify/require"
)

func TestParseCreativeOpenAIImageOutputs(t *testing.T) {
	img1 := base64.StdEncoding.EncodeToString([]byte("png-bytes-1"))
	img2 := base64.StdEncoding.EncodeToString([]byte("png-bytes-2"))
	body, err := json.Marshal(map[string]any{
		"created": 123,
		"data": []map[string]any{
			{"b64_json": img1},
			{"b64_json": img2},
		},
	})
	require.NoError(t, err)

	outputs, err := ParseCreativeOpenAIImageOutputs(body)
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, 0, outputs[0].Index)
	require.Equal(t, []byte("png-bytes-1"), outputs[0].Bytes)
	require.Equal(t, "image/png", outputs[0].Mime)
	// 空 data 报 502 可重试上游错误。
	_, err = ParseCreativeOpenAIImageOutputs([]byte(`{"data":[]}`))
	require.Error(t, err)
	var upstreamErr *creative.CreativeUpstreamError
	require.True(t, errors.As(err, &upstreamErr))
	require.Equal(t, 502, upstreamErr.StatusCode)
	require.True(t, upstreamErr.Retryable)
}

// TestParseCreativeGeminiImageOutputs 解析 Gemini generateContent 响应的 inlineData。
func TestParseCreativeGeminiImageOutputs(t *testing.T) {
	img := base64.StdEncoding.EncodeToString([]byte("gemini-image-bytes"))
	body := []byte(fmt.Sprintf(`{
		"candidates": [{"content": {"parts": [
			{"text": "说明"},
			{"inlineData": {"mimeType": "image/png", "data": "%s"}},
			{"inlineData": {"mime_type": "image/webp", "data": "%s"}}
		]}}]
	}`, img, base64.StdEncoding.EncodeToString([]byte("webp-bytes"))))

	outputs, err := parseCreativeGeminiImageOutputs(body)
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, []byte("webp-bytes"), outputs[0].Bytes)
	require.Equal(t, "image/webp", outputs[0].Mime)

	// 无候选内容时报可重试错误。
	_, err = parseCreativeGeminiImageOutputs([]byte(`{"candidates":[]}`))
	require.Error(t, err)
	var upstreamErr *creative.CreativeUpstreamError
	require.True(t, errors.As(err, &upstreamErr))
	require.True(t, upstreamErr.Retryable)
}

// TestNormalizeCreativeOutputs 校验大小上限、去重并固定只保留一张。
func TestNormalizeCreativeOutputs(t *testing.T) {
	// sha256 去重：相同字节只保留一张。
	outputs, err := creative.NormalizeCreativeOutputs([]creative.CreativeOutput{
		{Index: 0, Bytes: []byte("same"), Mime: "image/png"},
		{Index: 1, Bytes: []byte("same"), Mime: "image/png"},
		{Index: 2, Bytes: []byte("other"), Mime: "image/png"},
	})
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, []byte("same"), outputs[0].Bytes)

	// 上游返回多张时固定截断为一张并重排行号。
	outputs, err = creative.NormalizeCreativeOutputs([]creative.CreativeOutput{
		{Index: 0, Bytes: []byte("a"), Mime: "image/png"},
		{Index: 1, Bytes: []byte("b"), Mime: "image/png"},
		{Index: 2, Bytes: []byte("c"), Mime: "image/png"},
	})
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, 0, outputs[0].Index)

	// 单张超限（>32MiB）视为失败。
	big := make([]byte, creative.CreativeMaxOutputBytes+1)
	_, err = creative.NormalizeCreativeOutputs([]creative.CreativeOutput{{Index: 0, Bytes: big, Mime: "image/png"}})
	require.Error(t, err)
	require.False(t, creative.IsRetryableCreativeError(err))

	// 空输出视为失败。
	_, err = creative.NormalizeCreativeOutputs(nil)
	require.Error(t, err)
}

// TestCreativeOpenAIImageSize 校验 OpenAI size 映射。
func TestCreativeOpenAIImageSize(t *testing.T) {
	require.Equal(t, "1024x1024", CreativeOpenAIImageSize("1K", "1:1"))
	require.Equal(t, "1536x1024", CreativeOpenAIImageSize("1K", "16:9"))
	require.Equal(t, "1024x1536", CreativeOpenAIImageSize("1K", "9:16"))
	require.Equal(t, "1536x1536", CreativeOpenAIImageSize("2K", ""))
	require.Equal(t, "2880x2880", CreativeOpenAIImageSize("4K", "1:1"))
	require.Equal(t, "3840x2160", CreativeOpenAIImageSize("4K", "16:9"))
	require.Equal(t, "2160x3840", CreativeOpenAIImageSize("4K", "9:16"))
	require.Equal(t, "3264x2448", CreativeOpenAIImageSize("4K", "4:3"))
}

// TestCreativeGrokOperationMatrix grok 平台支持 generate 与 edit，但不支持 inpaint。
func TestCreativeGrokOperationMatrix(t *testing.T) {
	executor := &Target{}
	for _, operation := range []string{creative.CreativeOperationInpaint} {
		run := creative.CreativeRun{RunID: "crun_x", Operation: operation, RequestedOutputCount: 1}
		payload := creative.CreativeRunPayload{Prompt: "p"}
		_, err := executor.ExecuteGrok(context.Background(), run, payload, "grok-imagine")
		require.Error(t, err)
		require.False(t, creative.IsRetryableCreativeError(err), "grok %s 应当不可重试", operation)
	}
}

// TestCreativeErrorRetryableMatrix 校验状态码到可重试性的映射。
func TestCreativeErrorRetryableMatrix(t *testing.T) {
	retryable := []int{0, 429, 500, 502, 503}
	for _, status := range retryable {
		err := creative.CreativeHTTPStatusError(status, "boom")
		require.True(t, creative.IsRetryableCreativeError(err), "status %d 应当可重试", status)
	}
	nonRetryable := []int{400, 401, 403, 404, 422}
	for _, status := range nonRetryable {
		err := creative.CreativeHTTPStatusError(status, "bad request")
		require.False(t, creative.IsRetryableCreativeError(err), "status %d 应当不可重试", status)
	}
	require.False(t, creative.IsRetryableCreativeError(nil))
	require.True(t, creative.IsRetryableCreativeError(errors.New("network down")))
	require.True(t, creative.IsRetryableCreativeError(creative.CreativeNonRetryableError("x")) == false)
}

// TestBuildCreativeGrokRequest 校验 grok 请求体构造。
func TestBuildCreativeGrokRequest(t *testing.T) {
	run := creative.CreativeRun{ImageSize: "2K", AspectRatio: "16:9", RequestedOutputCount: 2}
	payload := creative.CreativeRunPayload{Prompt: "画猫", Quality: "low"}
	request := BuildCreativeGrokRequest(run, payload, "grok-imagine")
	require.Equal(t, "grok-imagine", request["model"])
	require.Equal(t, "画猫", request["prompt"])
	require.Equal(t, 1, request["n"])
	require.Equal(t, "b64_json", request["response_format"])
	require.Equal(t, "2k", request["resolution"])
	require.Equal(t, "16:9", request["aspect_ratio"])
	require.Equal(t, "low", request["quality"])

	// 不支持的 aspect_ratio 不落字段；1K 映射 1k。
	run = creative.CreativeRun{ImageSize: "1K", AspectRatio: "21:99"}
	request = BuildCreativeGrokRequest(run, payload, "grok-imagine")
	require.Equal(t, "1k", request["resolution"])
	_, ok := request["aspect_ratio"]
	require.False(t, ok)
}

// TestBuildCreativeGrokEditRequest 校验 Grok 单图与多图编辑 JSON 结构。
func TestBuildCreativeGrokEditRequest(t *testing.T) {
	run := creative.CreativeRun{ImageSize: "2K", AspectRatio: "16:9", RequestedOutputCount: 2}
	payload := creative.CreativeRunPayload{
		Prompt:  "edit image",
		Quality: "medium",
		Sources: []creative.CreativeInputImage{
			{Bytes: []byte("first"), Mime: "image/png"},
			{Bytes: []byte("second"), Mime: "image/jpeg"},
		},
	}
	request := BuildCreativeGrokEditRequest(run, payload, "grok-imagine-image-2.0")
	require.Equal(t, "grok-imagine-image-2.0", request["model"])
	require.Equal(t, "edit image", request["prompt"])
	require.Equal(t, "b64_json", request["response_format"])
	require.Equal(t, "2k", request["resolution"])
	require.Equal(t, "16:9", request["aspect_ratio"])
	require.Equal(t, 1, request["n"])
	require.Equal(t, "medium", request["quality"])
	require.NotContains(t, request, "image")
	images, ok := request["images"].([]map[string]string)
	require.True(t, ok)
	require.Len(t, images, 2)
	require.Equal(t, "image_url", images[0]["type"])
	require.Equal(t, "data:image/png;base64,Zmlyc3Q=", images[0]["url"])
	require.Equal(t, "data:image/jpeg;base64,c2Vjb25k", images[1]["url"])

	one := BuildCreativeGrokEditRequest(run, creative.CreativeRunPayload{
		Prompt:  "edit image",
		Sources: []creative.CreativeInputImage{{Bytes: []byte("one"), Mime: "image/png"}},
	}, "grok-imagine-image-2.0")
	require.Contains(t, one, "image")
	require.NotContains(t, one, "images")
}

// TestExecuteCreativeGrokEditUsesJSONEditEndpoint 校验编辑端点、鉴权请求和 b64 输出解析。
func TestBuildCreativeOpenAIRequestBody(t *testing.T) {
	// generate：JSON。
	run := creative.CreativeRun{Operation: creative.CreativeOperationGenerate, ImageSize: "1K", RequestedOutputCount: 2}
	payload := creative.CreativeRunPayload{
		Prompt:     "hello",
		Quality:    "high",
		Background: "opaque",
	}
	body, contentType, err := BuildCreativeOpenAIRequestBody(run, payload, "gpt-image-2")
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	var generateBody map[string]any
	require.NoError(t, json.Unmarshal(body, &generateBody))
	require.Equal(t, "gpt-image-2", generateBody["model"])
	require.NotContains(t, generateBody, "response_format")
	require.Equal(t, float64(1), generateBody["n"])
	require.Equal(t, "high", generateBody["quality"])
	require.Equal(t, "png", generateBody["output_format"])
	require.NotContains(t, generateBody, "output_compression")
	require.Equal(t, "opaque", generateBody["background"])
	// DALL-E 仍使用旧版 response_format 契约。
	dalleBody, _, err := BuildCreativeOpenAIRequestBody(run, payload, "dall-e-3")
	require.NoError(t, err)
	var dalleJSON map[string]any
	require.NoError(t, json.Unmarshal(dalleBody, &dalleJSON))
	require.Equal(t, "b64_json", dalleJSON["response_format"])

	// GPT Image 2 的 4K 横向尺寸使用真实的 3840x2160 像素值。
	run = creative.CreativeRun{Operation: creative.CreativeOperationGenerate, ImageSize: "4K", AspectRatio: "16:9", RequestedOutputCount: 1}
	body, contentType, err = BuildCreativeOpenAIRequestBody(run, payload, "gpt-image-2")
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Contains(t, string(body), `"size":"3840x2160"`)

	// inpaint：multipart，含 image/mask/model/prompt 字段。
	run = creative.CreativeRun{Operation: creative.CreativeOperationInpaint, ImageSize: "1K", AspectRatio: "1:1", RequestedOutputCount: 2}
	payload = creative.CreativeRunPayload{
		Prompt:     "inpaint me",
		Sources:    []creative.CreativeInputImage{{Bytes: []byte("img"), Mime: "image/png"}},
		Mask:       &creative.CreativeInputImage{Bytes: []byte("mask"), Mime: "image/png"},
		Quality:    "high",
		Background: "opaque",
	}
	body, contentType, err = BuildCreativeOpenAIRequestBody(run, payload, "gpt-image-2")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(contentType, "multipart/form-data"))
	require.Contains(t, string(body), `name="image"`)
	require.Contains(t, string(body), `name="mask"`)
	require.Contains(t, string(body), `name="model"`)
	require.Contains(t, string(body), `name="prompt"`)
	require.Contains(t, string(body), `name="size"`)
	require.Contains(t, string(body), `name="n"`)
	require.Contains(t, string(body), `name="quality"`)
	require.Contains(t, string(body), `name="output_format"`)
	require.NotContains(t, string(body), `name="output_compression"`)
	require.Contains(t, string(body), `name="background"`)
}

// TestBuildCreativeGeminiRequest 校验 Gemini edit 请求体构造，且不附加独立 mask。
func TestBuildCreativeGeminiRequest(t *testing.T) {
	run := creative.CreativeRun{Operation: creative.CreativeOperationEdit, ImageSize: "2K", AspectRatio: "16:9"}
	payload := creative.CreativeRunPayload{
		Prompt:        "重绘",
		ThinkingLevel: "high",
		Sources:       []creative.CreativeInputImage{{Bytes: []byte("src"), Mime: "image/jpeg"}},
	}
	request := BuildCreativeGeminiRequest(run, payload, "gemini-3.1-flash-image")
	require.Len(t, request.Contents, 1)
	parts := request.Contents[0].Parts
	require.Len(t, parts, 2)
	require.Equal(t, "重绘", parts[0].Text)
	require.Equal(t, "image/jpeg", parts[1].InlineData.MimeType)
	require.Equal(t, []string{"TEXT", "IMAGE"}, request.GenerationConfig.ResponseModalities)
	require.NotNil(t, request.GenerationConfig.ImageConfig)
	require.Equal(t, "2K", request.GenerationConfig.ImageConfig.ImageSize)
	require.Equal(t, "16:9", request.GenerationConfig.ImageConfig.AspectRatio)
	require.NotNil(t, request.GenerationConfig.ThinkingConfig)
	require.Equal(t, "high", request.GenerationConfig.ThinkingConfig.ThinkingLevel)
	require.False(t, request.GenerationConfig.ThinkingConfig.IncludeThoughts)

	// 必须校验序列化后的层级，避免只检查内存结构而漏掉真实上游请求格式。
	body, err := json.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, `{"contents":[{"parts":[{"text":"重绘"},{"inlineData":{"mimeType":"image/jpeg","data":"c3Jj"}}]}],"generationConfig":{"responseModalities":["TEXT","IMAGE"],"imageConfig":{"imageSize":"2K","aspectRatio":"16:9"},"thinkingConfig":{"thinkingLevel":"high","includeThoughts":false}}}`, string(body))
}

// TestCreativeGeminiInpaintIsRejectedBeforeUpstream 校验历史 Gemini inpaint 任务不会触发上游请求。
func TestParseCreativeGeminiImageOutputsUsesFinalImagePart(t *testing.T) {
	thought := base64.StdEncoding.EncodeToString([]byte("thought-image"))
	final := base64.StdEncoding.EncodeToString([]byte("final-image"))
	body := fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":%q}},{"inlineData":{"mimeType":"image/png","data":%q}}]}}]}`, thought, final)

	outputs, err := parseCreativeGeminiImageOutputs([]byte(body))
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, []byte("final-image"), outputs[0].Bytes)
}

// parseCreativeGeminiImageOutputs 只绑定任务错误类型，解析算法仍唯一位于 upstream。
func parseCreativeGeminiImageOutputs(body []byte) ([]creative.CreativeOutput, error) {
	return gemininative.ParseImageOutputs(body, func(status int, message string) error { return creative.CreativeHTTPStatusError(status, message) })
}
