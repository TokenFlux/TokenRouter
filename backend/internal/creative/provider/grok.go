// 任务平台 Adapter 保持原协议载荷与传输顺序；输入只包含本次绑定的技术能力。
package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok" // ExecuteGrok 执行 Grok 平台任务：generate 与 edit 分别使用 xAI 图片端点。
)

func (e *Target) ExecuteGrok(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload, upstreamModel string) ([]creative.CreativeOutput, error) {
	if run.Operation != creative.CreativeOperationGenerate && run.Operation != creative.CreativeOperationEdit {
		return nil, creative.CreativeNonRetryableError("grok platform does not support creative operation %s", run.Operation)
	}
	if run.Operation == creative.CreativeOperationEdit {
		if len(payload.Sources) == 0 {
			return nil, creative.CreativeNonRetryableError("grok image edit requires at least one source image")
		}
		if len(payload.Sources) > 3 {
			return nil, creative.CreativeNonRetryableError("grok image edit supports at most %d source images", 3)
		}
	}
	if e.Grok == nil {
		return nil, errors.New("creative grok gateway is not configured")
	}
	endpoint := grok.GrokMediaEndpointImagesGenerations
	if run.Operation == creative.CreativeOperationEdit {
		endpoint = grok.GrokMediaEndpointImagesEdits
	}
	targetURL, err := e.Grok.URL(endpoint)
	if err != nil {
		return nil, creative.CreativeHTTPStatusError(0, err.Error())
	}
	token, err := e.Grok.Token(ctx)
	if err != nil {
		return nil, creative.CreativeHTTPStatusError(0, err.Error())
	}
	request := BuildCreativeGrokRequest(run, payload, upstreamModel)
	if run.Operation == creative.CreativeOperationEdit {
		request = BuildCreativeGrokEditRequest(run, payload, upstreamModel)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = e.Grok.Prepare(req)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if e.Grok.OAuth && (grok.MediaCodec{}).IsGrokCLIProxyTarget(targetURL) {
		grok.ApplyCLIHeaders(req.Header)
	}
	// 账号级请求头覆写最后应用，配置值优先于内置默认头。
	e.Grok.ApplyHeaders(req.Header)

	resp, err := e.Grok.Do(req)
	if err != nil {
		return nil, creative.CreativeHTTPStatusError(0, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := ReadCreativeUpstreamBody(resp.Body, 64<<20)
	if err != nil {
		return nil, creative.CreativeHTTPStatusError(0, err.Error())
	}
	if resp.StatusCode >= 400 {
		return nil, creative.CreativeHTTPStatusError(resp.StatusCode, upstream.ExtractErrorMessage(respBody))
	}
	return ParseCreativeOpenAIImageOutputs(respBody)
}

// BuildCreativeGrokRequest 构造 xAI images/generations 请求体。
func BuildCreativeGrokRequest(run creative.CreativeRun, payload creative.CreativeRunPayload, upstreamModel string) map[string]any {
	request := map[string]any{
		"model":           upstreamModel,
		"prompt":          payload.Prompt,
		"n":               1,
		"response_format": "b64_json",
		"resolution":      CreativeGrokImageResolution(run.ImageSize),
	}
	if aspectRatio := CreativeGrokAspectRatio(run.AspectRatio); aspectRatio != "" {
		request["aspect_ratio"] = aspectRatio
	}
	if quality := strings.TrimSpace(payload.Quality); quality != "" {
		request["quality"] = quality
	}
	return request
}

// BuildCreativeGrokEditRequest 构造 xAI images/edits 所需的 JSON 请求体。
// xAI 编辑端点不接受 OpenAI SDK 的 multipart 格式，图片必须作为 data URI 引用。
func BuildCreativeGrokEditRequest(run creative.CreativeRun, payload creative.CreativeRunPayload, upstreamModel string) map[string]any {
	request := map[string]any{
		"model":           upstreamModel,
		"prompt":          payload.Prompt,
		"n":               1,
		"response_format": "b64_json",
		"resolution":      CreativeGrokImageResolution(run.ImageSize),
	}
	if aspectRatio := CreativeGrokAspectRatio(run.AspectRatio); aspectRatio != "" {
		request["aspect_ratio"] = aspectRatio
	}
	if quality := strings.TrimSpace(payload.Quality); quality != "" {
		request["quality"] = quality
	}

	images := make([]map[string]string, 0, len(payload.Sources))
	for _, source := range payload.Sources {
		mime := source.Mime
		if mime == "" {
			mime = "image/png"
		}
		images = append(images, map[string]string{
			"type": "image_url",
			"url":  "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(source.Bytes),
		})
	}
	if len(images) == 1 {
		request["image"] = images[0]
	} else {
		request["images"] = images
	}
	return request
}
