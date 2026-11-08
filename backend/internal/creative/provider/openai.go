package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai.go builds OpenAI creative image requests and obtains image bytes from
// base64 or URL results.
// OpenAI creative tasks need image bytes for transient delivery even when the
// generation response supplies only a signed URL. Buffered JSON closes before
// its first nonblank result URL is downloaded. Native SSE uses the shared reader
// to aggregate completion events, downloading URL-only events through the same
// callback. That callback owns download transport and credential isolation.
//
// Generation uses JSON and editing uses multipart references with an optional
// mask, while both request one output in the task format. GPT Image bodies omit the legacy
// response_format field and DALL-E names include it. URL downloads have
// bounded retries under the execution context, but exhausted downloads and
// unusable successful response bodies produce nonretryable result errors so
// the worker does not submit another generation for those delivery failures.

// ExecuteOpenAI submits one generation or edit request and returns the
// selected image bytes. Callers provide usable OpenAI callbacks and validate
// creative operation and model parameters before execution.
//
// Generation authentication and provider headers belong to the POST request.
// JSON responses use the shared sixty-four-MiB reader, while native streams
// aggregate completed images under the same byte budget. Stream-only providers
// receive stream=true with partial_images=0. Buffered JSON is closed before its
// result URL fetch; streamed URL completions use the protected download callback
// during aggregation. HTTP failures use status-class retry decisions, while
// conversion failures after a successful exchange end the creative attempt.
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
	operation := protocol.ProtocolImagesGenerations
	if endpoint == upstream.OpenAIImagesEditsEndpoint {
		operation = protocol.ProtocolImagesEdits
	}
	mode := protocol.ImageModeBoth
	if e.OpenAI.ImageMode != nil {
		mode, err = e.OpenAI.ImageMode(operation)
		if err != nil {
			return nil, creative.CreativeNonRetryableError("invalid image stream mode")
		}
	}
	body, contentType, err = upstream.RewriteImageRequest(body, contentType, upstreamModel, mode, false)
	if err != nil {
		return nil, creative.CreativeNonRetryableError("invalid image request")
	}
	token, err := e.OpenAI.Token(ctx)
	if err != nil {
		return nil, creative.CreativeHTTPStatusError(0, err.Error())
	}
	var req *http.Request
	if e.OpenAI.OAuth {
		parsed, parseErr := upstream.ParseImageRequest(endpoint, contentType, body)
		if parseErr != nil {
			return nil, creative.CreativeNonRetryableError("invalid image request: %v", parseErr)
		}
		responsesBody, buildErr := openai.BuildOpenAIImagesResponsesRequest(parsed, upstreamModel, e.OpenAI.MainModel)
		if buildErr != nil {
			return nil, buildErr
		}
		req, err = e.OpenAI.BuildOAuth(ctx, responsesBody, token)
		if err != nil {
			return nil, err
		}
	} else {
		targetURL, err := e.CreativeOpenAIURL(endpoint)
		if err != nil {
			return nil, err
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
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
		// Authentication headers and protocol Content-Type are prepared before the
		// provider override callback. Apply it last so configured provider header
		// values take precedence on the generation request sent by this transport.
		e.OpenAI.ApplyHeaders(req.Header)
	}

	resp, err := e.OpenAI.Do(req)
	if err != nil {
		return nil, creative.CreativeHTTPStatusError(0, err.Error())
	}
	var respBody []byte
	if resp.StatusCode >= 400 {
		respBody, err = ReadCreativeUpstreamBody(resp.Body, 64<<20)
	} else if e.OpenAI.OAuth {
		var raw []byte
		raw, err = ReadCreativeUpstreamBody(resp.Body, 64<<20)
		if err == nil {
			var images []openai.OpenAIResponsesImageResult
			var created int64
			var usage []byte
			var metadata openai.OpenAIResponsesImageResult
			images, created, usage, metadata, _, err = openai.CollectOpenAIImagesFromResponsesBody(raw, time.Now)
			if err == nil && len(images) == 0 {
				err = errors.New("upstream returned no image output")
			}
			if err == nil {
				respBody, err = openai.BuildOpenAIImagesAPIResponse(images, created, usage, metadata, "b64_json", time.Now)
			}
			if err == nil {
				respBody, _, err = openai.ValidateGIFImagesResponse(respBody, run.FrameCount > 0, nil)
			}
		}
	} else {
		options := openai.ImageResponseOptions{
			ExpectedGIF:    run.FrameCount > 0,
			ExpectedImages: 1,
			ReadLimit:      func() int64 { return 64 << 20 },
			ReadBody:       func(reader io.Reader) ([]byte, error) { return ReadCreativeUpstreamBody(reader, 64<<20) },
			FetchImage: func(rawURL string) (string, error) {
				if e.OpenAI.FetchImage == nil {
					return "", errors.New("image downloader unavailable")
				}
				return e.OpenAI.FetchImage(ctx, rawURL)
			},
		}
		respBody, _, err = openai.ReadImagesBufferedResponse(ctx, resp, options, "b64_json")
	}
	// Release the generation connection before buffered-result downloads. A successful
	// HTTP exchange followed by a conversion failure must not enqueue generation again.
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, creative.CreativeHTTPStatusError(resp.StatusCode, upstream.ExtractErrorMessage(respBody))
	}
	if err != nil {
		return nil, creative.CreativeImageResultError("INVALID_IMAGE_RESPONSE", "creative platform returned an incomplete image response")
	}
	return e.parseOpenAIImageOutputs(ctx, respBody)
}

// parseOpenAIImageOutputs selects usable base64 bytes or downloads the first
// nonblank result URL. Invalid JSON and responses with no usable base64 or URL
// return INVALID_IMAGE_RESPONSE. Callers supply nonnil OpenAI options for URL
// handling; a missing FetchImage callback returns IMAGE_DOWNLOAD_FAILED.
//
// A URL receives at most three fetch attempts, separated by one-second waits
// and governed by ctx. A successful fetch still needs nonempty decodable
// bytes. Failure of that first URL ends with IMAGE_DOWNLOAD_FAILED rather than
// trying later URL entries. Both result classifications retain fixed public
// messages and stop worker generation retries, while cancellation stops
// download waiting and further attempts.
func (e *Target) parseOpenAIImageOutputs(ctx context.Context, body []byte) ([]creative.CreativeOutput, error) {
	if !gjson.ValidBytes(body) {
		return nil, creative.CreativeImageResultError("INVALID_IMAGE_RESPONSE", "creative platform returned invalid image response")
	}
	// The shared base64 parser uses retryable errors for malformed image content.
	// Consume only its successful result here, then resolve URL delivery or return
	// a terminal image-result error for this already received generation response.
	if outputs, err := ParseCreativeOpenAIImageOutputs(body); err == nil {
		return outputs, nil
	}
	for _, item := range gjson.GetBytes(body, "data").Array() {
		rawURL := strings.TrimSpace(item.Get("url").String())
		if rawURL == "" {
			continue
		}
		if e.OpenAI.FetchImage != nil {
			// Retry this image fetch under the execution context
			// while retaining its
			// signed URL verbatim. The generation POST has
			// completed and is not part
			// of these attempts, so a transient download error does
			// not generate again.
			for attempt := 0; attempt < 3 && ctx.Err() == nil; attempt++ {
				encoded, err := e.OpenAI.FetchImage(ctx, rawURL)
				if err == nil {
					decoded, decodeErr := DecodeBase64Image(encoded)
					if decodeErr == nil && len(decoded.Bytes) > 0 {
						return []creative.CreativeOutput{{Index: 0, Bytes: decoded.Bytes, Mime: decoded.Mime}}, nil
					}
				}
				if attempt < 2 {
					timer := time.NewTimer(time.Second)
					select {
					case <-ctx.Done():
						timer.Stop()
					case <-timer.C:
					}
				}
			}
		}
		// A download failure can contain the signed address supplied by
		// the response.
		// Return fixed result text so task error metadata can be
		// exposed without that
		// supplied address or its signature.
		return nil, creative.CreativeImageResultError("IMAGE_DOWNLOAD_FAILED", "creative image download failed")
	}
	return nil, creative.CreativeImageResultError("INVALID_IMAGE_RESPONSE", "creative platform returned no decodable image output")
}

// CreativeOpenAIURL delegates generation endpoint resolution to the selected
// OpenAI callback.
func (e *Target) CreativeOpenAIURL(endpoint string) (string, error) { return e.OpenAI.URL(endpoint) }

// BuildCreativeOpenAIRequestBody returns the encoded OpenAI body and its
// content type. Generate uses JSON, while every other operation uses
// multipart, so callers validate operation names and source requirements
// before calling.
//
// Both representations carry the fixed upstream model, prompt, size mapping,
// and one output in the saved task format. Only DALL-E names request the legacy base64
// response_format field. Editing repeats the image form field for sources and
// adds a supplied mask; closing the multipart writer completes its boundary
// before bytes and content type are returned. Optional quality and background
// are forwarded after trimming.
func BuildCreativeOpenAIRequestBody(run creative.CreativeRun, payload creative.CreativeRunPayload, upstreamModel string) ([]byte, string, error) {
	if run.Operation == creative.CreativeOperationGenerate {
		bodyMap := map[string]any{
			"model":         upstreamModel,
			"prompt":        payload.Prompt,
			"n":             1,
			"output_format": creative.OutputFormatFromMIME(run.ResponseMIMEType),
			"size":          CreativeOpenAIImageSize(run.ImageSize, run.AspectRatio),
		}
		if run.FrameCount > 0 {
			bodyMap["frame_count"] = run.FrameCount
			bodyMap["frame_duration_ms"] = run.FrameDurationMS
		}
		// DALL-E selects base64 delivery with its legacy response_format field.
		// Restrict that field to its model family so GPT Image keeps the selected
		// output_format request already constructed above.
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
	// Multipart edits use the same model-family response option as JSON generation.
	// Include the legacy base64 field for DALL-E while GPT Image receives the selected
	// output_format written below.
	if CreativeOpenAIUsesResponseFormat(upstreamModel) {
		if err := writer.WriteField("response_format", "b64_json"); err != nil {
			return nil, "", err
		}
	}
	if err := writer.WriteField("output_format", creative.OutputFormatFromMIME(run.ResponseMIMEType)); err != nil {
		return nil, "", err
	}
	if run.FrameCount > 0 {
		if err := writer.WriteField("frame_count", strconv.Itoa(run.FrameCount)); err != nil {
			return nil, "", err
		}
		if err := writer.WriteField("frame_duration_ms", strconv.Itoa(run.FrameDurationMS)); err != nil {
			return nil, "", err
		}
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
	// Closing writes the final multipart boundary. Return body bytes only after
	// that succeeds so the edit endpoint receives a complete form containing the
	// source images, optional mask, and generation fields.
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

// CreativeOpenAIUsesResponseFormat reports whether a trimmed lowercase model
// begins with dall-e. JSON and multipart builders use that check to send the
// legacy base64 response_format field for those model names.
func CreativeOpenAIUsesResponseFormat(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "dall-e")
}

// ParseCreativeOpenAIImageOutputs returns the first nonempty decodable base64
// image from data. Empty arrays or arrays with no usable item return a
// retryable 502 creative error; OpenAI result handling reclassifies that
// failure before trying a URL.
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
		// Skip unusable base64 entries while preserving the first later decodable
		// result. URL delivery is evaluated separately by the OpenAI target when
		// this shared parser finds no usable base64 image.
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
