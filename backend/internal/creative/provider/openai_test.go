package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// openai_test.go checks creative OpenAI request construction, OAuth conversion, and image delivery.
//
// OpenAI image fixtures simulate response bodies and download callbacks to
// observe delivery order without real transport. Tests inspect exact request
// fields, base64 preference, signed URL preservation, bounded download counts,
// and fixed result errors under synctest timing and injected cancellation.

// creativeImageResponseBody wraps the fixture reader and records
// close-before-download observations.
type creativeImageResponseBody struct {
	io.Reader // simulated response body reader
	// closed records the simulated body Close call. The download fixture checks
	// this flag to observe cleanup order before fetching the returned image URL.
	closed bool // fixture body close observed flag
}

// TestParseCreativeOpenAIImageOutputs supplies two encoded results and then an
// empty array. It checks the first decoded bytes and zero index, while empty
// data returns the asserted retryable 502 error.
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
	// Empty data produces a retryable 502 upstream error.
	_, err = ParseCreativeOpenAIImageOutputs([]byte(`{"data":[]}`))
	require.Error(t, err)
	var upstreamErr *creative.CreativeUpstreamError
	require.True(t, errors.As(err, &upstreamErr))
	require.Equal(t, 502, upstreamErr.StatusCode)
	require.True(t, upstreamErr.Retryable)
}

// TestCreativeOpenAIUnexpectedGIF preserves a valid animation returned for a
// static request. The adapter must retain the real MIME and original bytes so
// outcome persistence can count frames before the funding layer captures costs.
func TestCreativeOpenAIUnexpectedGIF(t *testing.T) {
	encoded := "R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAkQBACwAAAAAAQABAAACAkQBADs="
	original, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	response := &creativeImageResponseBody{Reader: strings.NewReader(`{"data":[{"b64_json":"` + encoded + `"}]}`)}
	target := &Target{OpenAI: &OpenAIOptions{
		Token:        func(context.Context) (string, error) { return "fixture", nil },
		URL:          func(endpoint string) (string, error) { return "https://example.invalid" + endpoint, nil },
		Prepare:      func(req *http.Request) *http.Request { return req },
		AuthHeaders:  func(context.Context, string) (http.Header, error) { return http.Header{}, nil },
		ApplyHeaders: func(http.Header) {},
		Do: func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, "png", gjson.GetBytes(body, "output_format").String())
			require.False(t, gjson.GetBytes(body, "frame_count").Exists())
			return &http.Response{StatusCode: http.StatusOK, Body: response}, nil
		},
	}}
	outputs, err := target.ExecuteOpenAI(t.Context(), creative.CreativeRun{Operation: creative.CreativeOperationGenerate, ImageSize: "1K", ResponseMIMEType: "image/png"}, creative.CreativeRunPayload{Prompt: "fixture"}, "gpt-image-2.6-flare")
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, "image/gif", outputs[0].Mime)
	require.Equal(t, original, outputs[0].Bytes)
	require.True(t, response.closed)
}

// TestBuildCreativeOpenAIRequestBody inspects JSON generation and multipart
// inpaint fields using a two-output run. Requests still use one PNG output;
// the fixture checks DALL-E response_format and the listed four-K dimensions.
func TestBuildCreativeOpenAIRequestBody(t *testing.T) {
	// Generation uses JSON.
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
	// DALL-E selects Base64 with response_format.
	dalleBody, _, err := BuildCreativeOpenAIRequestBody(run, payload, "dall-e-3")
	require.NoError(t, err)
	var dalleJSON map[string]any
	require.NoError(t, json.Unmarshal(dalleBody, &dalleJSON))
	require.Equal(t, "b64_json", dalleJSON["response_format"])

	// GPT Image 2 uses 3840x2160 for landscape 4K output.
	run = creative.CreativeRun{Operation: creative.CreativeOperationGenerate, ImageSize: "4K", AspectRatio: "16:9", RequestedOutputCount: 1}
	body, contentType, err = BuildCreativeOpenAIRequestBody(run, payload, "gpt-image-2")
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Contains(t, string(body), `"size":"3840x2160"`)

	// Inpaint uses multipart image, mask, model, and prompt fields.
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

// Close records that the fixture response body was closed and returns success.
// URL-download callbacks inspect this flag to establish cleanup ordering after
// generation reads in the simulated exchange.
func (b *creativeImageResponseBody) Close() error {
	b.closed = true
	return nil
}

// TestOpenAIImageURLResults injects one failed download followed by usable
// bytes for the listed models and operations. Assertions require a closed
// generation body, preserved signed URL, one generation, and two fetches.
func TestOpenAIImageURLResults(t *testing.T) {
	for _, model := range []string{"gpt-image-2.5-flare", "gpt-image-2.5-sunburst"} {
		for _, operation := range []string{"generate", "edit", "inpaint"} {
			t.Run(model+"/"+operation, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					body := &creativeImageResponseBody{Reader: strings.NewReader(`{"data":[{"url":"https://cdn.example.com/image.png?sig=test"}]}`)}
					generations, downloads := 0, 0
					target := &Target{OpenAI: &OpenAIOptions{
						Token:        func(context.Context) (string, error) { return "token", nil },
						URL:          func(endpoint string) (string, error) { return "https://relay.example.com" + endpoint, nil },
						Prepare:      func(req *http.Request) *http.Request { return req },
						AuthHeaders:  func(context.Context, string) (http.Header, error) { return http.Header{}, nil },
						ApplyHeaders: func(http.Header) {},
						Do: func(req *http.Request) (*http.Response, error) {
							generations++
							require.Equal(t, http.MethodPost, req.Method)
							return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
						},
						FetchImage: func(ctx context.Context, raw string) (string, error) {
							downloads++
							require.True(t, body.closed)
							require.Equal(t, "https://cdn.example.com/image.png?sig=test", raw)
							if downloads == 1 {
								return "", errors.New("temporary download failure")
							}
							return base64.StdEncoding.EncodeToString([]byte("image bytes")), nil
						},
					}}
					outputs, err := target.ExecuteOpenAI(context.Background(), creative.CreativeRun{Operation: operation, ImageSize: "1K"}, creative.CreativeRunPayload{Prompt: "test"}, model)
					require.NoError(t, err)
					require.Len(t, outputs, 1)
					require.Equal(t, []byte("image bytes"), outputs[0].Bytes)
					require.Equal(t, 1, generations)
					require.Equal(t, 2, downloads)
				})
			})
		}
	}
}

// TestOpenAIImageResultFailures supplies malformed responses and failed or
// cancelled URL fetching under synctest. It checks result codes, nonretryable
// classification, exact fetch counts, and omission of the supplied secret
// text.
func TestOpenAIImageResultFailures(t *testing.T) {
	for _, test := range []struct {
		// name labels the case; body supplies response text; code names expected
		// failure
		name, body, code string
		cancel           bool // cancel injected during image fetch
		wantDownloads    int  // expected image fetch call count
	}{
		{name: "download exhausted", body: `{"data":[{"url":"https://cdn.example.com/private?sig=secret"}]}`, code: "IMAGE_DOWNLOAD_FAILED", wantDownloads: 3},
		{name: "cancelled download", body: `{"data":[{"url":"https://cdn.example.com/private?sig=secret"}]}`, code: "IMAGE_DOWNLOAD_FAILED", cancel: true, wantDownloads: 1},
		{name: "empty output", body: `{"data":[]}`, code: "INVALID_IMAGE_RESPONSE"},
		{name: "invalid base64", body: `{"data":[{"b64_json":"%%%"}]}`, code: "INVALID_IMAGE_RESPONSE"},
		{name: "invalid json", body: `not json`, code: "INVALID_IMAGE_RESPONSE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				// This scenario owns the cancellable context.
				// Release that lifetime on exit;
				// the fetch hook can also cancel it earlier to
				// stop further download retries.
				defer cancel()
				downloads := 0
				target := &Target{OpenAI: &OpenAIOptions{FetchImage: func(context.Context, string) (string, error) {
					downloads++
					// Cancel from the first injected fetch
					// so the adapter context, not a
					// generation retry, stops later
					// download attempts in this fixture
					// case.
					if test.cancel {
						cancel()
					}
					return "", errors.New("failed https://cdn.example.com/private?sig=secret")
				}}}
				_, err := target.parseOpenAIImageOutputs(ctx, []byte(test.body))
				var resultErr *creative.CreativeUpstreamError
				require.ErrorAs(t, err, &resultErr)
				require.Equal(t, test.code, resultErr.Code)
				require.False(t, creative.IsRetryableCreativeError(err))
				require.NotContains(t, err.Error(), "secret")
				require.Equal(t, test.wantDownloads, downloads)
			})
		})
	}
}

// TestOpenAIImageBase64Preferred supplies both a URL and decodable base64 in
// one item. The returned bytes match base64 and the download callback fails
// the test if unexpectedly invoked.
func TestOpenAIImageBase64Preferred(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := &Target{OpenAI: &OpenAIOptions{FetchImage: func(context.Context, string) (string, error) {
			t.Fatal("unexpected image download")
			return "", nil
		}}}
		outputs, err := target.parseOpenAIImageOutputs(context.Background(), []byte(`{"data":[{"url":"https://cdn.example.com/image.png","b64_json":"aW1hZ2U="}]}`))
		require.NoError(t, err)
		require.Equal(t, []byte("image"), outputs[0].Bytes)
	})
}

// TestCreativeOpenAIStreamingMode adapts a streaming-only provider for creative
// generation and editing. The adapter submits one stream request, aggregates its
// final image, and classifies truncated streams as nonretryable delivery failures.
// An unsolicited expired preview is ignored before final-image aggregation.
func TestCreativeOpenAIStreamingMode(t *testing.T) {
	for _, operation := range []string{"generate", "edit"} {
		for _, failed := range []bool{false, true} {
			calls := 0
			target := &Target{OpenAI: &OpenAIOptions{
				ImageMode: func(id protocol.ProtocolID) (protocol.ImageStreamMode, error) {
					if operation == "edit" {
						require.Equal(t, protocol.ProtocolImagesEdits, id)
					} else {
						require.Equal(t, protocol.ProtocolImagesGenerations, id)
					}
					return protocol.ImageModeStreaming, nil
				},
				Token:        func(context.Context) (string, error) { return "token", nil },
				URL:          func(endpoint string) (string, error) { return "https://relay.invalid" + endpoint, nil },
				Prepare:      func(req *http.Request) *http.Request { return req },
				AuthHeaders:  func(context.Context, string) (http.Header, error) { return http.Header{}, nil },
				ApplyHeaders: func(http.Header) {},
				Do: func(req *http.Request) (*http.Response, error) {
					calls++
					raw, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					if operation == "generate" {
						require.True(t, gjson.GetBytes(raw, "stream").Bool())
						require.EqualValues(t, 0, gjson.GetBytes(raw, "partial_images").Int())
					} else {
						require.Contains(t, string(raw), "name=\"stream\"\r\n\r\ntrue")
					}
					family := "image_generation"
					if operation == "edit" {
						family = "image_edit"
					}
					body := "data: {\"type\":\"image_generation.partial_image\",\"url\":\"https://preview.invalid/expired\"}\n\n" + fmt.Sprintf("data: {\"type\":%q,\"b64_json\":\"aW1hZ2U=\"}\n\n", family+".completed")
					if failed {
						body += "data: {"
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				},
			}}
			outputs, err := target.ExecuteOpenAI(t.Context(), creative.CreativeRun{Operation: operation, ImageSize: "1K"}, creative.CreativeRunPayload{Prompt: "test"}, "gpt-image-2")
			require.Equal(t, 1, calls)
			if failed {
				require.Error(t, err)
				var failure *creative.CreativeUpstreamError
				require.ErrorAs(t, err, &failure)
				require.False(t, failure.Retryable)
			} else {
				require.NoError(t, err)
				require.Len(t, outputs, 1)
				require.Equal(t, []byte("image"), outputs[0].Bytes)
			}
		}
	}
}

// TestCreativeOpenAIOAuthGIF executes a worker request through the shared
// Responses bridge. The returned two-frame animation remains byte-identical even
// when the task requested four frames for its initial funding reservation.
func TestCreativeOpenAIOAuthGIF(t *testing.T) {
	encoded := "R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAkQBACwAAAAAAQABAAACAkQBADs="
	target := &Target{OpenAI: &OpenAIOptions{
		OAuth: true, MainModel: "gpt-6-luna",
		Token: func(context.Context) (string, error) { return "test-token", nil },
		BuildOAuth: func(ctx context.Context, body []byte, token string) (*http.Request, error) {
			require.Equal(t, "test-token", token)
			require.Equal(t, "gpt-6-luna", gjson.GetBytes(body, "model").String())
			require.Equal(t, "gpt-image-2.6-flare", gjson.GetBytes(body, "tools.0.model").String())
			require.Equal(t, "gif", gjson.GetBytes(body, "tools.0.output_format").String())
			require.Equal(t, int64(4), gjson.GetBytes(body, "tools.0.frame_count").Int())
			require.True(t, gjson.GetBytes(body, "stream").Bool())
			return http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test/responses", strings.NewReader(string(body)))
		},
		Do: func(*http.Request) (*http.Response, error) {
			body := `data: {"type":"response.completed","response":{"output":[{"type":"image_generation_call","id":"gif-call","result":"` + encoded + `","output_format":"gif"}]}}` + "\n\n"
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		},
	}}
	run := creative.CreativeRun{Operation: creative.CreativeOperationGenerate, ImageSize: "1K", ResponseMIMEType: "image/gif", FrameCount: 4, FrameDurationMS: 200}
	outputs, err := target.ExecuteOpenAI(context.Background(), run, creative.CreativeRunPayload{Prompt: "A blinking star"}, "gpt-image-2.6-flare")
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, "image/gif", outputs[0].Mime)
	require.Equal(t, encoded, base64.StdEncoding.EncodeToString(outputs[0].Bytes))
}
