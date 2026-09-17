// 任务平台 Adapter 保持原协议载荷与传输顺序；输入只包含本次绑定的技术能力。
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/upstream" // ExecuteOpenAI 执行 OpenAI 平台任务：generate 走 /v1/images/generations（JSON），
	// edit/inpaint 走 /v1/images/edits（multipart，多源图 + mask）。
	"github.com/tidwall/gjson"
)

func (e *Target) ExecuteOpenAI(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload, upstreamModel string) ([]creative.CreativeOutput, error) {
	if e.OpenAI == nil {
		return nil, errors.New("creative openai gateway is not configured")
	}
	endpoint := upstream.OpenAIImagesGenerationsEndpoint
	if run.Operation != creative.CreativeOperationGenerate {
		endpoint = upstream.OpenAIImagesEditsEndpoint
	}
	body, contentType, err := BuildCreativeOpenAIRequestBody(run, payload, upstreamModel)
	if err != nil {
		return nil, err
	}
	token, err := e.OpenAI.Token(ctx)
	if err != nil {
		return nil, creative.CreativeHTTPStatusError(0, err.Error())
	}
	targetURL, err := e.CreativeOpenAIURL(endpoint)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = e.OpenAI.Prepare(req)
	authHeaders, err := e.OpenAI.AuthHeaders(ctx, token)
	if err != nil {
		return nil, creative.CreativeHTTPStatusError(0, err.Error())
	}
	for key, values := range authHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if strings.TrimSpace(contentType) != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// 账号级请求头覆写最后应用，配置值优先于内置默认头。
	e.OpenAI.ApplyHeaders(req.Header)

	resp, err := e.OpenAI.Do(req)
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
func (e *Target) CreativeOpenAIURL(endpoint string) (string, error) { return e.OpenAI.URL(endpoint) }

// BuildCreativeOpenAIRequestBody 构造 OpenAI images 请求体：
// generate 为 JSON；edit/inpaint 为 multipart（image 多文件、mask、model、prompt）。
// 输出格式固定为 PNG；其余可选参数已经过模型能力校验，生成与编辑请求保持同一语义。
func BuildCreativeOpenAIRequestBody(run creative.CreativeRun, payload creative.CreativeRunPayload, upstreamModel string) ([]byte, string, error) {
	if run.Operation == creative.CreativeOperationGenerate {
		bodyMap := map[string]any{
			"model":         upstreamModel,
			"prompt":        payload.Prompt,
			"n":             1,
			"output_format": "png",
			"size":          CreativeOpenAIImageSize(run.ImageSize, run.AspectRatio),
		}
		if CreativeOpenAIUsesResponseFormat(upstreamModel) {
			bodyMap["response_format"] = "b64_json"
		}
		if quality := strings.TrimSpace(payload.Quality); quality != "" {
			bodyMap["quality"] = quality
		}
		if background := strings.TrimSpace(payload.Background); background != "" {
			bodyMap["background"] = background
		}
		body, err := json.Marshal(bodyMap)
		if err != nil {
			return nil, "", err
		}
		return body, "application/json", nil
	}

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for i, image := range payload.Sources {
		part, err := writer.CreateFormFile("image", fmt.Sprintf("source_%d.%s", i, CreativeFileExtension(image.Mime)))
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(image.Bytes); err != nil {
			return nil, "", err
		}
	}
	if payload.Mask != nil {
		part, err := writer.CreateFormFile("mask", "mask."+CreativeFileExtension(payload.Mask.Mime))
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(payload.Mask.Bytes); err != nil {
			return nil, "", err
		}
	}
	if err := writer.WriteField("model", upstreamModel); err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("prompt", payload.Prompt); err != nil {
		return nil, "", err
	}
	if CreativeOpenAIUsesResponseFormat(upstreamModel) {
		if err := writer.WriteField("response_format", "b64_json"); err != nil {
			return nil, "", err
		}
	}
	if err := writer.WriteField("output_format", "png"); err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("size", CreativeOpenAIImageSize(run.ImageSize, run.AspectRatio)); err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("n", "1"); err != nil {
		return nil, "", err
	}
	if quality := strings.TrimSpace(payload.Quality); quality != "" {
		if err := writer.WriteField("quality", quality); err != nil {
			return nil, "", err
		}
	}
	if background := strings.TrimSpace(payload.Background); background != "" {
		if err := writer.WriteField("background", background); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

// CreativeOpenAIUsesResponseFormat 仅对 DALL-E 保留旧版 response_format 参数；GPT Image 固定返回 base64。
func CreativeOpenAIUsesResponseFormat(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "dall-e")
}

// ParseCreativeOpenAIImageOutputs 解析 OpenAI images 响应（grok 同结构）的 data[].b64_json。
func ParseCreativeOpenAIImageOutputs(body []byte) ([]creative.CreativeOutput, error) {
	data := gjson.GetBytes(body, "data")
	if !data.IsArray() || len(data.Array()) == 0 {
		return nil, creative.CreativeHTTPStatusError(http.StatusBadGateway, "upstream returned no image output")
	}
	outputs := make([]creative.CreativeOutput, 0, len(data.Array()))
	for _, item := range data.Array() {
		b64 := strings.TrimSpace(item.Get("b64_json").String())
		if b64 == "" {
			continue
		}
		decoded, err := DecodeBase64Image(b64)
		if err != nil || len(decoded.Bytes) == 0 {
			continue
		}
		outputs = append(outputs, creative.CreativeOutput{Index: len(outputs), Bytes: decoded.Bytes, Mime: decoded.Mime})
		break
	}
	if len(outputs) == 0 {
		return nil, creative.CreativeHTTPStatusError(http.StatusBadGateway, "upstream returned no decodable image output")
	}
	return outputs, nil
}
