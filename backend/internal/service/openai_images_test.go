package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/imroc/req/v3"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type failingOpenAIImageWriter struct {
	gin.ResponseWriter
	failAfter int
	writes    int
}

type openAIImagesReadErrorBody struct {
	err error
}

func (b *openAIImagesReadErrorBody) Read([]byte) (int, error) { return 0, b.err }
func (b *openAIImagesReadErrorBody) Close() error             { return nil }

func (w *failingOpenAIImageWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, errors.New("write failed: client disconnected")
	}
	w.writes++
	return w.ResponseWriter.Write(p)
}

func TestOpenAIGatewayServiceParseOpenAIImagesRequest_JSON(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","size":"1024x1024","quality":"high","stream":true}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.NotNil(t, parsed)
	require.Equal(t, "/v1/images/generations", parsed.Endpoint)
	require.Equal(t, "gpt-image-2", parsed.Model)
	require.Equal(t, "draw a cat", parsed.Prompt)
	require.True(t, parsed.Stream)
	require.Equal(t, "1024x1024", parsed.Size)
	require.Equal(t, "1K", parsed.SizeTier)
	require.Equal(t, OpenAIImagesCapabilityNative, parsed.RequiredCapability)
	require.False(t, parsed.Multipart)
}

func TestOpenAIGatewayServiceParseOpenAIImagesRequest_MultipartEdit(t *testing.T) {

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("prompt", "replace background"))
	require.NoError(t, writer.WriteField("size", "1536x1024"))
	part, err := writer.CreateFormFile("image", "source.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("fake-image-bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body.Bytes())
	require.NoError(t, err)
	require.NotNil(t, parsed)
	require.Equal(t, "/v1/images/edits", parsed.Endpoint)
	require.True(t, parsed.Multipart)
	require.Equal(t, "gpt-image-2", parsed.Model)
	require.Equal(t, "replace background", parsed.Prompt)
	require.Equal(t, "1536x1024", parsed.Size)
	require.Equal(t, "2K", parsed.SizeTier)
	require.Len(t, parsed.Uploads, 1)
	require.Equal(t, OpenAIImagesCapabilityNative, parsed.RequiredCapability)
}

func TestOpenAIImagesRequestModerationBody_JSONEditIncludesInputImageURLs(t *testing.T) {
	parsed := &media.ImageRequest{
		Endpoint:       upstreamcore.OpenAIImagesEditsEndpoint,
		Prompt:         "replace background",
		InputImageURLs: []string{"https://example.com/source.png"},
		MaskImageURL:   "https://example.com/mask.png",
	}

	input := moderation.ExtractContentModerationInput(moderation.ContentModerationProtocolOpenAIImages, parsed.ModerationBody())

	require.Equal(t, "replace background", input.Text)
	require.Equal(t, []string{"https://example.com/source.png", "https://example.com/mask.png"}, input.Images)
}

func TestOpenAIGatewayServiceParseOpenAIImagesRequest_NormalizesOfficialAndCustomSizes(t *testing.T) {

	tests := []struct {
		size     string
		wantTier string
	}{
		{size: "1024x1024", wantTier: "1K"},
		{size: "1536x1024", wantTier: "2K"},
		{size: "1024x1536", wantTier: "2K"},
		{size: "2048x2048", wantTier: "2K"},
		{size: "2048x1152", wantTier: "2K"},
		{size: "3840x2160", wantTier: "4K"},
		{size: "2160x3840", wantTier: "4K"},
		{size: "1024X768", wantTier: "1K"},
		{size: "1280x768", wantTier: "2K"},
		{size: "2560x1440", wantTier: "4K"},
		{size: "2560x1600", wantTier: "4K"},
		{size: "auto", wantTier: "2K"},
	}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","size":"` + tt.size + `"}`)

			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = req

			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.NotNil(t, parsed)
			require.Equal(t, tt.size, parsed.Size)
			require.Equal(t, tt.wantTier, parsed.SizeTier)
		})
	}
}

func TestOpenAIGatewayServiceParseOpenAIImagesRequest_UnknownSizesDoNotBlockPassthrough(t *testing.T) {

	tests := []struct {
		size     string
		wantTier string
	}{
		{size: "2048x1153", wantTier: "2K"},
		{size: "4096x1024", wantTier: "4K"},
		{size: "3840x1024", wantTier: "4K"},
		{size: "512x512", wantTier: "1K"},
		{size: "invalid", wantTier: "2K"},
		{size: "999999999999999999999999999x2", wantTier: "2K"},
	}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","size":"` + tt.size + `"}`)

			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = req

			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.NotNil(t, parsed)
			require.Equal(t, tt.size, parsed.Size)
			require.Equal(t, tt.wantTier, parsed.SizeTier)
		})
	}
}

func TestOpenAIGatewayServiceParseOpenAIImagesRequest_LegacyImageModelUnknownSizePassthrough(t *testing.T) {

	body := []byte(`{"model":"gpt-image-1.5","prompt":"draw a cat","size":"2048x1152"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.NotNil(t, parsed)
	require.Equal(t, "2048x1152", parsed.Size)
	require.Equal(t, "2K", parsed.SizeTier)
}

func TestOpenAIGatewayServiceParseOpenAIImagesRequest_MultipartEditWithMaskAndNativeOptions(t *testing.T) {

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("prompt", "replace foreground"))
	require.NoError(t, writer.WriteField("output_format", "png"))
	require.NoError(t, writer.WriteField("input_fidelity", "high"))
	require.NoError(t, writer.WriteField("output_compression", "80"))
	require.NoError(t, writer.WriteField("partial_images", "2"))

	imageHeader := make(textproto.MIMEHeader)
	imageHeader.Set("Content-Disposition", `form-data; name="image"; filename="source.png"`)
	imageHeader.Set("Content-Type", "image/png")
	imagePart, err := writer.CreatePart(imageHeader)
	require.NoError(t, err)
	_, err = imagePart.Write([]byte("source-image-bytes"))
	require.NoError(t, err)

	maskHeader := make(textproto.MIMEHeader)
	maskHeader.Set("Content-Disposition", `form-data; name="mask"; filename="mask.png"`)
	maskHeader.Set("Content-Type", "image/png")
	maskPart, err := writer.CreatePart(maskHeader)
	require.NoError(t, err)
	_, err = maskPart.Write([]byte("mask-image-bytes"))
	require.NoError(t, err)

	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body.Bytes())
	require.NoError(t, err)
	require.NotNil(t, parsed)
	require.Len(t, parsed.Uploads, 1)
	require.NotNil(t, parsed.MaskUpload)
	require.True(t, parsed.HasMask)
	require.Equal(t, "png", parsed.OutputFormat)
	require.Equal(t, "high", parsed.InputFidelity)
	require.NotNil(t, parsed.OutputCompression)
	require.Equal(t, 80, *parsed.OutputCompression)
	require.NotNil(t, parsed.PartialImages)
	require.Equal(t, 2, *parsed.PartialImages)
	require.Equal(t, OpenAIImagesCapabilityNative, parsed.RequiredCapability)
}

func TestOpenAIGatewayServiceParseOpenAIImagesRequest_PromptOnlyDefaultsRemainBasic(t *testing.T) {

	body := []byte(`{"prompt":"draw a cat"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.NotNil(t, parsed)
	require.Equal(t, "gpt-image-2", parsed.Model)
	require.Equal(t, OpenAIImagesCapabilityBasic, parsed.RequiredCapability)
}

func TestOpenAIGatewayServiceParseOpenAIImagesRequest_ExplicitSizeRequiresNativeCapability(t *testing.T) {

	body := []byte(`{"prompt":"draw a cat","size":"1024x1024"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.NotNil(t, parsed)
	require.Equal(t, OpenAIImagesCapabilityNative, parsed.RequiredCapability)
}

func TestOpenAIGatewayServiceParseOpenAIImagesRequest_RejectsNonImageModel(t *testing.T) {

	body := []byte(`{"model":"gpt-5.4","prompt":"draw a cat"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.Nil(t, parsed)
	require.ErrorContains(t, err, `images endpoint requires an image model, got "gpt-5.4"`)
}

// TestOpenAIGatewayServiceParseOpenAIImagesRequestForRouting 延后模型校验，确保渠道别名能先完成 R -> C。
func TestOpenAIGatewayServiceParseOpenAIImagesRequestForRouting(t *testing.T) {

	body := []byte(`{"model":"draw-alias","prompt":"draw a cat"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequestForRouting(c, body)
	require.NoError(t, err)
	require.NotNil(t, parsed)
	require.Equal(t, "draw-alias", parsed.Model)
	require.NoError(t, parsed.ValidateRoutingModel("gpt-image-1"))
	require.Equal(t, OpenAIImagesCapabilityNative, parsed.RequiredCapability)
	require.ErrorContains(t, parsed.ValidateRoutingModel("gpt-5.4"), `images endpoint requires an image model, got "gpt-5.4"`)
}

func TestOpenAIGatewayServiceParseOpenAIImagesRequest_AllowsGrokImageModels(t *testing.T) {

	for _, model := range []string{"grok-imagine", "grok-imagine-image", "grok-imagine-image-quality", "grok-imagine-edit"} {
		t.Run(model, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw a cat","response_format":"b64_json"}`, model))
			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = req

			svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.NotNil(t, parsed)
			require.Equal(t, model, parsed.Model)
			require.Equal(t, OpenAIImagesCapabilityNative, parsed.RequiredCapability)
		})
	}
}

func TestOpenAIGatewayServiceParseOpenAIImagesRequest_JSONEditURLs(t *testing.T) {

	body := []byte(`{
		"model":"gpt-image-2",
		"prompt":"replace the background",
		"images":[{"image_url":"https://example.com/source.png"}],
		"mask":{"image_url":"https://example.com/mask.png"},
		"input_fidelity":"high",
		"output_compression":90,
		"partial_images":2,
		"response_format":"url"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.NotNil(t, parsed)
	require.Equal(t, []string{"https://example.com/source.png"}, parsed.InputImageURLs)
	require.Equal(t, "https://example.com/mask.png", parsed.MaskImageURL)
	require.Equal(t, "high", parsed.InputFidelity)
	require.NotNil(t, parsed.OutputCompression)
	require.Equal(t, 90, *parsed.OutputCompression)
	require.NotNil(t, parsed.PartialImages)
	require.Equal(t, 2, *parsed.PartialImages)
	require.True(t, parsed.HasMask)
	require.Equal(t, OpenAIImagesCapabilityNative, parsed.RequiredCapability)
}

func TestCollectOpenAIImagePointers_RecognizesDirectAssets(t *testing.T) {
	items := upstreamopenai.CollectOpenAIImagePointers([]byte(`{
		"revised_prompt": "cat astronaut",
		"parts": [
			{"b64_json":"QUJD"},
			{"download_url":"https://files.example.com/image.png?sig=1"},
			{"asset_pointer":"file-service://file_123"}
		]
	}`))

	require.Len(t, items, 3)
	var sawBase64, sawURL, sawPointer bool
	for _, item := range items {
		if item.B64JSON == "QUJD" {
			sawBase64 = true
			require.Equal(t, "cat astronaut", item.Prompt)
		}
		if item.DownloadURL == "https://files.example.com/image.png?sig=1" {
			sawURL = true
		}
		if item.Pointer == "file-service://file_123" {
			sawPointer = true
		}
	}
	require.True(t, sawBase64)
	require.True(t, sawURL)
	require.True(t, sawPointer)
}

func TestResolveOpenAIImageBytes_PrefersInlineBase64(t *testing.T) {
	data, err := upstreamopenai.ResolveOpenAIImageBytes(context.Background(), nil, nil, "", upstreamopenai.ImagePointerInfo{
		B64JSON: "data:image/png;base64,QUJD",
	}, openAIUpstreamErrorBodyReadLimit)
	require.NoError(t, err)
	require.Equal(t, []byte("ABC"), data)
}

func TestNewOpenAIImageStatusError_UsesProvidedReadLimit(t *testing.T) {
	padding := strings.Repeat("x", int(openAIUpstreamErrorBodyReadLimit)+1024)
	body := fmt.Sprintf(`{"error":{"padding":"%s","message":"diagnostic-marker"}}`, padding)
	resp := &req.Response{Response: &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}}

	err := upstreamopenai.NewOpenAIImageStatusError(resp, "download image bytes failed", int64(len(body)))
	require.Error(t, err)
	require.Equal(t, "diagnostic-marker", err.Error())

	var statusErr *upstreamopenai.ImageStatusError
	require.ErrorAs(t, err, &statusErr)
	require.Len(t, statusErr.ResponseBody, len(body))
}

func TestAccountSupportsOpenAIImageCapability_OAuthSupportsNative(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth},
	}

	require.True(t, account.View().SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic))
	require.True(t, account.View().SupportsOpenAIImageCapability(OpenAIImagesCapabilityNative))
}

func TestAccountSupportsOpenAIImageCapability_SetupTokenSupportsNative(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeSetupToken},
	}

	require.True(t, account.View().SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic))
	require.True(t, account.View().SupportsOpenAIImageCapability(OpenAIImagesCapabilityNative))
	require.False(t, accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(account), accountcore.OpenAIEndpointCapabilityEmbeddings))
}

func TestAccountSupportsOpenAIImageCapability_EmptyRequirementDoesNotRejectGrok(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
		Type: capability.AccountTypeOAuth},
	}

	require.True(t, account.View().SupportsOpenAIImageCapability(""))
	require.False(t, account.View().SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic))
}

func TestBuildOpenAIImagesURL_HandlesVersionedBaseURL(t *testing.T) {
	require.Equal(t,
		"https://image-upstream.example/v1/images/generations",
		httpclient.BuildOpenAIEndpointURL("https://image-upstream.example/v1", upstreamcore.OpenAIImagesGenerationsEndpoint),
	)
	require.Equal(t,
		"https://open.bigmodel.cn/api/paas/v4/images/generations",
		httpclient.BuildOpenAIEndpointURL("https://open.bigmodel.cn/api/paas/v4", upstreamcore.OpenAIImagesGenerationsEndpoint),
	)
	require.Equal(t,
		"https://image-upstream.example/v1/images/edits",
		httpclient.BuildOpenAIEndpointURL("https://image-upstream.example/v1/", upstreamcore.OpenAIImagesEditsEndpoint),
	)
	require.Equal(t,
		"https://image-upstream.example/v1/images/generations",
		httpclient.BuildOpenAIEndpointURL("https://image-upstream.example", upstreamcore.OpenAIImagesGenerationsEndpoint),
	)
	require.Equal(t,
		"https://image-upstream.example/v1/images/generations",
		httpclient.BuildOpenAIEndpointURL("https://image-upstream.example/v1/images/generations", upstreamcore.OpenAIImagesGenerationsEndpoint),
	)
}

type openAIImageTestSSEEvent struct {
	Name string
	Data string
}

func parseOpenAIImageTestSSEEvents(body string) []openAIImageTestSSEEvent {
	chunks := strings.Split(body, "\n\n")
	events := make([]openAIImageTestSSEEvent, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		var event openAIImageTestSSEEvent
		for _, line := range strings.Split(chunk, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				event.Name = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				event.Data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			}
		}
		if event.Name != "" || event.Data != "" {
			events = append(events, event)
		}
	}
	return events
}

func findOpenAIImageTestSSEEvent(events []openAIImageTestSSEEvent, name string) (openAIImageTestSSEEvent, bool) {
	for _, event := range events {
		if event.Name == name {
			return event, true
		}
	}
	return openAIImageTestSSEEvent{}, false
}

func TestOpenAIGatewayServiceForwardImages_OAuthAppliesAccountMappingAndReturnsAllImages(t *testing.T) {

	body := []byte(`{"model":"gpt-image-1","prompt":"draw a cat","size":"1024x1024","quality":"high","n":3}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set("api_key", &apikey.APIKey{ID: 42})

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"req_img_123"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000000,\"usage\":{\"input_tokens\":11,\"output_tokens\":22,\"input_tokens_details\":{\"cached_tokens\":3},\"output_tokens_details\":{\"image_tokens\":7}},\"tool_usage\":{\"image_gen\":{\"input_tokens\":46,\"output_tokens\":2459,\"output_tokens_details\":{\"image_tokens\":2459},\"images\":3}},\"output\":[{\"type\":\"image_generation_call\",\"result\":\"aW1hZ2UtMQ==\",\"revised_prompt\":\"draw a cat 1\",\"output_format\":\"png\",\"quality\":\"high\",\"size\":\"1024x1024\"},{\"type\":\"image_generation_call\",\"result\":\"aW1hZ2UtMg==\",\"revised_prompt\":\"draw a cat 2\",\"output_format\":\"png\",\"quality\":\"high\",\"size\":\"1024x1024\"},{\"type\":\"image_generation_call\",\"result\":\"aW1hZ2UtMw==\",\"revised_prompt\":\"draw a cat 3\",\"output_format\":\"png\",\"quality\":\"high\",\"size\":\"1024x1024\"}]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	svc.httpUpstream = upstream
	if svc.Requests != nil {
		svc.Requests.Transport = svc.httpUpstream
	}
	if svc.Grok != nil {
		svc.Grok.Transport = svc.httpUpstream
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "token-123",
			"chatgpt_account_id": "acct-123",
			"model_mapping":      map[string]any{"gpt-image-1": "gpt-image-2"},
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-image-1", result.Model)
	require.Equal(t, "gpt-image-2", result.UpstreamModel)
	require.Equal(t, 3, result.ImageCount)
	require.Equal(t, 46, result.Usage.InputTokens)
	require.Equal(t, 2459, result.Usage.OutputTokens)
	require.Equal(t, 2459, result.Usage.ImageOutputTokens)

	require.NotNil(t, upstream.lastReq)
	require.Equal(t, chatgptCodexURL, upstream.lastReq.URL.String())
	require.Equal(t, "chatgpt.com", upstream.lastReq.Host)
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, "acct-123", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.Equal(t, "responses=experimental", upstream.lastReq.Header.Get("OpenAI-Beta"))

	require.Equal(t, upstreamopenai.ImagesResponsesMainModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "image_generation", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "generate", gjson.GetBytes(upstream.lastBody, "tools.0.action").String())
	require.Equal(t, "gpt-image-2", gjson.GetBytes(upstream.lastBody, "tools.0.model").String())
	require.Equal(t, "1024x1024", gjson.GetBytes(upstream.lastBody, "tools.0.size").String())
	require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "tools.0.quality").String())
	require.Equal(t, int64(3), gjson.GetBytes(upstream.lastBody, "tools.0.n").Int())
	require.Equal(t, "draw a cat", gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "gpt-image-1", gjson.Get(rec.Body.String(), "model").String())
	require.Len(t, gjson.Get(rec.Body.String(), "data").Array(), 3)
	require.Equal(t, "aW1hZ2UtMQ==", gjson.Get(rec.Body.String(), "data.0.b64_json").String())
	require.Equal(t, "aW1hZ2UtMg==", gjson.Get(rec.Body.String(), "data.1.b64_json").String())
	require.Equal(t, "aW1hZ2UtMw==", gjson.Get(rec.Body.String(), "data.2.b64_json").String())
	require.Equal(t, "draw a cat 1", gjson.Get(rec.Body.String(), "data.0.revised_prompt").String())
	require.Equal(t, "draw a cat 3", gjson.Get(rec.Body.String(), "data.2.revised_prompt").String())
}

func TestParseOpenAIImagesSSEUsageBytes_ToolUsagePrecedenceAndFallback(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	fallback := openai.ForwardUsage{InputTokens: 3, ImageInputTokens: 2, OutputTokens: 4, ImageOutputTokens: 2}
	tests := []struct {
		name      string
		toolUsage string
		want      openai.ForwardUsage
	}{
		{
			name:      "valid tool usage takes atomic precedence without mixing top-level image input",
			toolUsage: `{"input_tokens":4.6e1,"output_tokens":2459e0,"output_tokens_details":{"image_tokens":24590e-1}}`,
			want:      openai.ForwardUsage{InputTokens: 46, OutputTokens: 2459, ImageOutputTokens: 2459},
		},
		{
			name:      "valid tool image input takes precedence",
			toolUsage: `{"input_tokens":46,"input_tokens_details":{"image_tokens":35},"output_tokens":2459,"output_tokens_details":{"image_tokens":2459}}`,
			want:      openai.ForwardUsage{InputTokens: 46, ImageInputTokens: 35, OutputTokens: 2459, ImageOutputTokens: 2459},
		},
		{name: "absent", want: fallback},
		{name: "malformed field", toolUsage: `{"input_tokens":"46","output_tokens":2459,"output_tokens_details":{"image_tokens":2459}}`, want: fallback},
		{name: "fractional field", toolUsage: `{"input_tokens":46,"output_tokens":2459.5,"output_tokens_details":{"image_tokens":2459}}`, want: fallback},
		{name: "negative field", toolUsage: `{"input_tokens":46,"output_tokens":2459,"output_tokens_details":{"image_tokens":-1}}`, want: fallback},
		{name: "overflow field", toolUsage: `{"input_tokens":46,"output_tokens":9223372036854775808,"output_tokens_details":{"image_tokens":2459}}`, want: fallback},
		{name: "incomplete object", toolUsage: `{"input_tokens":46,"output_tokens":2459}`, want: fallback},
		{name: "malformed image input detail", toolUsage: `{"input_tokens":46,"input_tokens_details":{"image_tokens":1.5},"output_tokens":2459,"output_tokens_details":{"image_tokens":2459}}`, want: fallback},
		{name: "hostile huge exponent", toolUsage: `{"input_tokens":1e1000000000,"output_tokens":2459,"output_tokens_details":{"image_tokens":2459}}`, want: fallback},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolUsageField := ""
			if tt.toolUsage != "" {
				toolUsageField = `,"tool_usage":{"image_gen":` + tt.toolUsage + `}`
			}
			payload := []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":3,"input_tokens_details":{"image_tokens":2},"output_tokens":4,"output_tokens_details":{"image_tokens":2}}` + toolUsageField + `}}`)
			var got openai.ForwardUsage
			svc.parseOpenAIImagesSSEUsageBytes(payload, &got)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseOpenAIImagesSSEUsageBytes_MalformedCompletedDoesNotOverrideUsage(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	var usage openai.ForwardUsage

	svc.parseOpenAIImagesSSEUsageBytes([]byte(`{"type":"response.output_item.done","item":{"type":"image_generation_call","result":"aW1hZ2U="}}`), &usage)
	svc.parseOpenAIImagesSSEUsageBytes([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":4,"output_tokens_details":{"image_tokens":2}}}}`), &usage)
	svc.parseOpenAIImagesSSEUsageBytes([]byte(`{"type":"response.completed","response":{"tool_usage":{"image_gen":{"input_tokens":46,"output_tokens":2459,"output_tokens_details":{"image_tokens":2459}}}}} trailing`), &usage)

	require.Equal(t, openai.ForwardUsage{InputTokens: 3, OutputTokens: 4, ImageOutputTokens: 2}, usage)
}

func TestBoundedJSONNonNegativeInt(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
		ok   bool
	}{
		{name: "scale reduction before accumulation", raw: `10000000000000000000e-19`, want: 1, ok: true},
		{name: "decimal scale reduction", raw: `10000000000000000000.0e-19`, want: 1, ok: true},
		{name: "fractional after scale reduction", raw: `10000000000000000001e-19`, ok: false},
		{name: "overflow after scale reduction", raw: `92233720368547758080e-1`, ok: false},
		{name: "zero with negative exponent", raw: `0e-100`, want: 0, ok: true},
		{name: "zero beyond exponent bound", raw: `0e101`, want: 0, ok: true},
		{name: "zero padded decimal beyond exponent bound", raw: `0.000000e+000000000000000000000000000000000000000000000000101`, want: 0, ok: true},
		{name: "zero padded exponent", raw: `1e0000`, want: 1, ok: true},
		{name: "negative zero syntax", raw: `-0e101`, ok: false},
		{name: "hostile exponent", raw: `1e-1000`, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := openai.BoundedJSONNonNegativeInt(gjson.Parse(tt.raw))
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestOpenAIGatewayServiceForwardImages_OAuthUpstreamHTTPErrorSurfacesRealError(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set("api_key", &apikey.APIKey{ID: 42})

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	svc.httpUpstream = &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"X-Request-Id": []string{"req_img_badreq"},
			},
			Body: io.NopCloser(strings.NewReader(
				`{"error":{"message":"Invalid value for 'size': expected one of 1024x1024, 1536x1024.","type":"invalid_request_error","param":"size","code":"unknown_parameter"}}`,
			)),
		},
	}
	if svc.Requests != nil {
		svc.Requests.Transport = svc.httpUpstream
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.Nil(t, result)

	var upstreamErr *upstreamopenai.OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, http.StatusBadRequest, upstreamErr.StatusCode)
	require.Equal(t, "invalid_request_error", upstreamErr.ErrorType)
	require.Equal(t, "unknown_parameter", upstreamErr.Code)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Equal(t, "unknown_parameter", gjson.Get(rec.Body.String(), "error.code").String())
	require.Equal(t, "size", gjson.Get(rec.Body.String(), "error.param").String())
	require.Contains(t, gjson.Get(rec.Body.String(), "error.message").String(), "Invalid value for 'size'")
}

func TestOpenAIGatewayServiceForwardImages_OAuthNonStreamModerationBlockedReturnsClientError(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw blocked image","response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set("api_key", &apikey.APIKey{ID: 42})

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	svc.httpUpstream = &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"req_img_blocked"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.created\",\"response\":{\"created_at\":1710000020}}\n\n" +
					"data: {\"type\":\"error\",\"error\":{\"type\":\"image_generation_user_error\",\"code\":\"moderation_blocked\",\"message\":\"Your request was rejected by the safety system. safety_violations=[sexual].\"}}\n\n" +
					"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_blocked\",\"status\":\"failed\",\"error\":{\"type\":\"image_generation_user_error\",\"code\":\"moderation_blocked\",\"message\":\"Your request was rejected by the safety system. safety_violations=[sexual].\"}}}\n\n",
			)),
		},
	}
	if svc.Requests != nil {
		svc.Requests.Transport = svc.httpUpstream
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.Nil(t, result)
	var upstreamErr *upstreamopenai.OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, http.StatusBadRequest, upstreamErr.StatusCode)
	require.Equal(t, "moderation_blocked", upstreamErr.Code)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "image_generation_user_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Equal(t, "moderation_blocked", gjson.Get(rec.Body.String(), "error.code").String())
	require.Contains(t, gjson.Get(rec.Body.String(), "error.message").String(), "safety system")
}

func TestOpenAIGatewayServiceForwardImages_OAuthNonStreamServerErrorReturnsFailoverBeforeFlush(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"req_img_server_error"},
				},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.created\",\"response\":{\"created_at\":1710000021}}\n\n" +
						"data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"code\":\"server_error\",\"message\":\"The image service is temporarily unavailable.\"}}\n\n",
				)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 21,
		Name:     "openai-oauth-server-error",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "temporarily unavailable")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())

	rawEvents, ok := c.Get(gatewayhttp.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "failover", events[0].Kind)
	require.Equal(t, account.Record.ID, events[0].AccountID)
	require.Equal(t, http.StatusBadGateway, events[0].UpstreamStatusCode)
}

func TestOpenAIGatewayServiceForwardImages_OAuth429CarriesSameAccountRetryWindow(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","response_format":"b64_json"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"1"}, "X-Request-Id": []string{"req_img_oauth_429"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"rate limited"}}`)),
	}}})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 22, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"access_token": "token-123"}}}
	startedAt := time.Now()

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, time.Second, failoverErr.SameAccountRetryDelay)
	require.WithinDuration(t, startedAt.Add(accountcore.RuntimeRetryWindow), failoverErr.SameAccountRetryDeadline, time.Second)
}

func TestOpenAIImagesOAuthBodyReadTransportErrorFailover(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"X-Request-Id": []string{"req_h2_read_failure"},
			"X-Upstream":   []string{"preserved"},
		},
		Body: &openAIImagesReadErrorBody{err: errors.New("stream error: stream ID 11; INTERNAL_ERROR; received from peer")},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5400, Name: "openai-oauth", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})

	_, _, _, readErr := svc.handleOpenAIImagesOAuthNonStreamingResponse(resp, c, "b64_json", "gpt-image-2")
	require.Error(t, readErr)
	err := svc.handleOpenAIImagesOAuthResponseError(context.Background(), c, account, "gpt-image-2", "https://api.openai.com/v1/responses", resp, gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c), readErr)

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.JSONEq(t, `{"error":{"type":"upstream_error","code":"upstream_http2_stream_error","message":"Upstream HTTP/2 stream failed"}}`, string(failoverErr.ResponseBody))
	require.Equal(t, "req_h2_read_failure", http.Header(failoverErr.ResponseHeaders).Get("x-request-id"))
	require.Equal(t, "preserved", http.Header(failoverErr.ResponseHeaders).Get("x-upstream"))
	resp.Header.Set("X-Upstream", "mutated")
	require.Equal(t, "preserved", http.Header(failoverErr.ResponseHeaders).Get("x-upstream"))

	rawEvents, ok := c.Get(gatewayhttp.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "failover", events[0].Kind)
	require.Equal(t, "req_h2_read_failure", events[0].UpstreamRequestID)
	require.Equal(t, "Upstream HTTP/2 stream failed", events[0].Message)
}

// TestOpenAIImagesOAuthBodyReadErrorsNotMisclassified 验证本地取消、超限和业务错误不会伪装成传输故障。
func TestOpenAIImagesOAuthBodyReadErrorsNotMisclassified(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "context canceled", err: context.Canceled},
		{name: "response too large", err: fmt.Errorf("%w: limit=1", httpclient.ErrResponseBodyTooLarge)},
		{name: "semantic error", err: &upstreamopenai.OpenAIImagesUpstreamError{StatusCode: http.StatusBadRequest, ErrorType: "invalid_request_error", Code: "invalid_value", Message: "bad image request"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}
			err := tt.err
			if tt.name != "semantic error" && upstreamopenai.ShouldClassifyUpstreamStreamReadError(err, httpclient.ErrResponseBodyTooLarge, c.Request.Context()) {
				err = upstreamopenai.NewUpstreamStreamReadError(err)
			}

			got := (withSchedulerParametersForTest(&OpenAIGatewayService{})).handleOpenAIImagesOAuthResponseError(context.Background(), c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI}}, "gpt-image-2", "", resp, gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c), err)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.False(t, errors.As(got, &failoverErr))
			require.ErrorIs(t, got, tt.err)
		})
	}
}

// TestOpenAIImagesOAuthTransportErrorAfterDownstreamWriteDoesNotFailover 验证首个真实下游字节后不再换号。
func TestOpenAIImagesOAuthTransportErrorAfterDownstreamWriteDoesNotFailover(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	before := gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c)
	_, writeErr := c.Writer.Write([]byte("downstream image bytes"))
	require.NoError(t, writeErr)
	classifiedErr := upstreamopenai.NewUpstreamStreamReadError(errors.New("unexpected EOF"))
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5401, Name: "openai-oauth", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	resp := &http.Response{Header: http.Header{"X-Request-Id": []string{"req_after_write"}}}

	err := (withSchedulerParametersForTest(&OpenAIGatewayService{})).handleOpenAIImagesOAuthResponseError(context.Background(), c, account, "gpt-image-2", "", resp, before, classifiedErr)

	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.ErrorIs(t, err, classifiedErr)
	rawEvents, ok := c.Get(gatewayhttp.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "retry_exhausted_failover", events[0].Kind)
}

// TestShouldClassifyOpenAIUpstreamStreamReadErrorTransportStrings 验证常见传输错误和取消边界。
func TestShouldClassifyOpenAIUpstreamStreamReadErrorTransportStrings(t *testing.T) {
	for _, message := range []string{"unexpected EOF", "connection reset by peer", "broken pipe", "use of closed network connection"} {
		t.Run(message, func(t *testing.T) {
			require.True(t, upstreamopenai.ShouldClassifyUpstreamStreamReadError(errors.New(message), httpclient.ErrResponseBodyTooLarge))
		})
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, upstreamopenai.ShouldClassifyUpstreamStreamReadError(errors.New("unexpected EOF"), httpclient.ErrResponseBodyTooLarge, canceledCtx))
}

func TestOpenAIGatewayServiceForwardImages_APIKeyGenerationUsesConfiguredV1BaseURL(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{},
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
					"X-Request-Id": []string{"req_img_apikey"},
				},
				Body: io.NopCloser(strings.NewReader(`{"created":1710000007,"data":[{"b64_json":"aGVsbG8=","revised_prompt":"draw a cat"}]}`)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 6,
		Name:     "openai-apikey",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-api-key",
			"base_url": "https://image-upstream.example/v1",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, "gpt-image-2", result.Model)
	require.Equal(t, "gpt-image-2", result.UpstreamModel)

	upstream, ok := svc.httpUpstream.(*httpUpstreamRecorder)
	require.True(t, ok)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://image-upstream.example/v1/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer test-api-key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	require.Equal(t, "gpt-image-2", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "aGVsbG8=", gjson.Get(rec.Body.String(), "data.0.b64_json").String())
}

func TestOpenAIGatewayServiceForwardImages_APIKeyAccessStateUsesTypedFailover(t *testing.T) {

	body := []byte(`{"model":"gpt-image-1","prompt":"draw a cat"}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := []byte(`{"error":{"code":"organization_suspended","message":"Organization has been suspended"}}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusForbidden,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"req_images_access_state"},
		},
		Body: io.NopCloser(bytes.NewReader(upstreamBody)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 51,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "sk-test",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusForbidden, failoverErr.StatusCode)
	require.Equal(t, forwardcore.GatewayFailureStageAccountAuth, failoverErr.Stage)
	require.Equal(t, forwardcore.GatewayFailureScopeAccount, failoverErr.Scope)
	require.Equal(t, forwardcore.OpenAIUpstreamAccessStateReason, failoverErr.Reason)
	require.Equal(t, forwardcore.NextAccountRetry, failoverErr.NextAccountAction)
	require.Equal(t, http.StatusBadGateway, failoverErr.ClientStatusCode)
	require.Equal(t, "Upstream access is temporarily unavailable, please retry later", failoverErr.ClientMessage)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, "req_images_access_state", http.Header(failoverErr.ResponseHeaders).Get("x-request-id"))
	require.False(t, c.Writer.Written())
}

func TestOpenAIGatewayServiceForwardImages_APIKeyStreamJSONResponseBillsImage(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","stream":true,"response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{},
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
					"X-Request-Id": []string{"req_img_stream_json"},
				},
				Body: io.NopCloser(strings.NewReader(`{"created":1710000008,"usage":{"input_tokens":12,"output_tokens":21,"output_tokens_details":{"image_tokens":9}},"data":[{"b64_json":"aGVsbG8=","revised_prompt":"draw a cat"}]}`)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7,
		Name:     "openai-apikey",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-api-key",
			"base_url": "https://image-upstream.example/v1",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 21, result.Usage.OutputTokens)
	require.Equal(t, 9, result.Usage.ImageOutputTokens)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "aGVsbG8=", gjson.Get(rec.Body.String(), "data.0.b64_json").String())
}

func TestOpenAIGatewayServiceForwardImages_APIKeyStreamRawJSONEventStreamFallbackBillsImage(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","stream":true,"response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{},
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"req_img_stream_json_mislabeled"},
				},
				Body: io.NopCloser(strings.NewReader(`{"created":1710000009,"usage":{"input_tokens":10,"output_tokens":18,"output_tokens_details":{"image_tokens":8}},"data":[{"b64_json":"ZmluYWw="}]}`)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 8,
		Name:     "openai-apikey",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-api-key",
			"base_url": "https://image-upstream.example/v1",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 18, result.Usage.OutputTokens)
	require.Equal(t, 8, result.Usage.ImageOutputTokens)
	require.Equal(t, "ZmluYWw=", gjson.Get(rec.Body.String(), "data.0.b64_json").String())
}

func TestOpenAIGatewayServiceForwardImages_APIKeyStreamMultilineSSEDataBillsImage(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","stream":true,"response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{},
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"req_img_stream_multiline"},
				},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"image_generation.completed\",\n" +
						"data: \"usage\":{\"input_tokens\":10,\"output_tokens\":18,\"output_tokens_details\":{\"image_tokens\":8}},\n" +
						"data: \"b64_json\":\"ZmluYWw=\",\"output_format\":\"png\"}\n\n" +
						"data: [DONE]\n\n",
				)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 8,
		Name:     "openai-apikey",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test-api-key",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 18, result.Usage.OutputTokens)
	require.Equal(t, 8, result.Usage.ImageOutputTokens)
}

func TestExtractOpenAIImagesBillableCountFromJSONBytes_CompletedEvent(t *testing.T) {
	body := []byte(`{"type":"image_generation.completed","b64_json":"ZmluYWw=","usage":{"input_tokens":10,"output_tokens":18}}`)

	require.Equal(t, 1, extractOpenAIImagesBillableCountFromJSONBytes(body))
}

func TestOpenAIGatewayServiceForwardImages_APIKeyEditUsesConfiguredV1BaseURL(t *testing.T) {

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("prompt", "replace background"))
	imagePart, err := writer.CreateFormFile("image", "source.png")
	require.NoError(t, err)
	_, err = imagePart.Write([]byte("png-image-content"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{},
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
					"X-Request-Id": []string{"req_img_edit_apikey"},
				},
				Body: io.NopCloser(strings.NewReader(`{"created":1710000008,"data":[{"b64_json":"ZWRpdGVk","revised_prompt":"replace background"}]}`)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body.Bytes())
	require.NoError(t, err)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7,
		Name:     "openai-apikey",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-api-key",
			"base_url": "https://image-upstream.example/v1/",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body.Bytes(), parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)

	upstream, ok := svc.httpUpstream.(*httpUpstreamRecorder)
	require.True(t, ok)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://image-upstream.example/v1/images/edits", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer test-api-key", upstream.lastReq.Header.Get("Authorization"))
	require.Contains(t, upstream.lastReq.Header.Get("Content-Type"), "multipart/form-data")
	require.Contains(t, string(upstream.lastBody), `name="model"`)
	require.Contains(t, string(upstream.lastBody), "gpt-image-2")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ZWRpdGVk", gjson.Get(rec.Body.String(), "data.0.b64_json").String())
}

func TestOpenAIGatewayServiceForwardImages_OAuthStreamingTransformsEvents(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","stream":true,"response_format":"url"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"req_img_stream"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.created\",\"response\":{\"created_at\":1710000001,\"tools\":[{\"type\":\"image_generation\",\"model\":\"gpt-image-2\",\"background\":\"auto\",\"output_format\":\"png\",\"quality\":\"high\",\"size\":\"1024x1024\"}]}}\n\n" +
					"data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"cGFydGlhbA==\",\"partial_image_index\":0,\"output_format\":\"png\",\"background\":\"auto\"}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000001,\"usage\":{\"input_tokens\":5,\"output_tokens\":9,\"output_tokens_details\":{\"image_tokens\":4}},\"tool_usage\":{\"image_gen\":{\"input_tokens\":46,\"output_tokens\":2459,\"output_tokens_details\":{\"image_tokens\":2459},\"images\":1}},\"tools\":[{\"type\":\"image_generation\",\"model\":\"gpt-image-2\",\"background\":\"auto\",\"output_format\":\"png\",\"quality\":\"high\",\"size\":\"1024x1024\"}],\"output\":[{\"type\":\"image_generation_call\",\"result\":\"ZmluYWw=\",\"output_format\":\"png\"}]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	svc.httpUpstream = upstream
	if svc.Requests != nil {
		svc.Requests.Transport = svc.httpUpstream
	}
	if svc.Grok != nil {
		svc.Grok.Transport = svc.httpUpstream
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, openai.ForwardUsage{InputTokens: 46, OutputTokens: 2459, ImageOutputTokens: 2459}, result.Usage)
	events := parseOpenAIImageTestSSEEvents(rec.Body.String())
	partial, ok := findOpenAIImageTestSSEEvent(events, "image_generation.partial_image")
	require.True(t, ok)
	require.Equal(t, "image_generation.partial_image", gjson.Get(partial.Data, "type").String())
	require.Equal(t, int64(1710000001), gjson.Get(partial.Data, "created_at").Int())
	require.Equal(t, "cGFydGlhbA==", gjson.Get(partial.Data, "b64_json").String())
	require.Equal(t, "data:image/png;base64,cGFydGlhbA==", gjson.Get(partial.Data, "url").String())
	require.Equal(t, "gpt-image-2", gjson.Get(partial.Data, "model").String())
	require.Equal(t, "png", gjson.Get(partial.Data, "output_format").String())
	require.Equal(t, "high", gjson.Get(partial.Data, "quality").String())
	require.Equal(t, "1024x1024", gjson.Get(partial.Data, "size").String())
	require.Equal(t, "auto", gjson.Get(partial.Data, "background").String())

	completed, ok := findOpenAIImageTestSSEEvent(events, "image_generation.completed")
	require.True(t, ok)
	require.Equal(t, "image_generation.completed", gjson.Get(completed.Data, "type").String())
	require.Equal(t, int64(1710000001), gjson.Get(completed.Data, "created_at").Int())
	require.Equal(t, "ZmluYWw=", gjson.Get(completed.Data, "b64_json").String())
	require.Equal(t, "data:image/png;base64,ZmluYWw=", gjson.Get(completed.Data, "url").String())
	require.Equal(t, "gpt-image-2", gjson.Get(completed.Data, "model").String())
	require.Equal(t, "png", gjson.Get(completed.Data, "output_format").String())
	require.Equal(t, "high", gjson.Get(completed.Data, "quality").String())
	require.Equal(t, "1024x1024", gjson.Get(completed.Data, "size").String())
	require.Equal(t, "auto", gjson.Get(completed.Data, "background").String())
	require.JSONEq(t, `{"input_tokens":46,"output_tokens":2459,"output_tokens_details":{"image_tokens":2459},"images":1}`, gjson.Get(completed.Data, "usage").Raw)
	require.False(t, gjson.Get(completed.Data, "revised_prompt").Exists())
}

func TestOpenAIGatewayServiceForwardImages_OAuthStreamingModerationBlockedEmitsError(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw blocked image","stream":true,"response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	svc.httpUpstream = &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"req_img_stream_blocked"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_blocked_stream\",\"status\":\"failed\",\"error\":{\"type\":\"image_generation_user_error\",\"code\":\"moderation_blocked\",\"message\":\"Your request was rejected by the safety system.\"}}}\n\n",
			)),
		},
	}
	if svc.Requests != nil {
		svc.Requests.Transport = svc.httpUpstream
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.Nil(t, result)
	var upstreamErr *upstreamopenai.OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, http.StatusBadRequest, upstreamErr.StatusCode)
	require.Equal(t, "moderation_blocked", upstreamErr.Code)

	events := parseOpenAIImageTestSSEEvents(rec.Body.String())
	errorEvent, ok := findOpenAIImageTestSSEEvent(events, "error")
	require.True(t, ok)
	require.Equal(t, "error", gjson.Get(errorEvent.Data, "type").String())
	require.Equal(t, "image_generation_user_error", gjson.Get(errorEvent.Data, "error.type").String())
	require.Equal(t, "moderation_blocked", gjson.Get(errorEvent.Data, "error.code").String())
	require.Contains(t, gjson.Get(errorEvent.Data, "error.message").String(), "safety system")
}

func TestOpenAIGatewayServiceForwardImages_OAuthStreamingServerErrorBeforeFlushReturnsFailover(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","stream":true,"response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"req_img_stream_server_error"},
				},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_server_error\",\"status\":\"failed\",\"error\":{\"type\":\"server_error\",\"code\":\"server_error\",\"message\":\"The image service is temporarily unavailable.\"}}}\n\n",
				)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 23,
		Name:     "openai-oauth-stream-server-error",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "temporarily unavailable")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestOpenAIGatewayServiceForwardImages_OAuthStreamingServerErrorAfterFlushDoesNotFailover(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","stream":true,"response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"req_img_server_error_after_partial"},
				},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"cGFydGlhbA==\",\"partial_image_index\":0,\"output_format\":\"png\"}\n\n" +
						"data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"code\":\"server_error\",\"message\":\"The image service failed after partial output.\"}}\n\n",
				)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 22,
		Name:     "openai-oauth-partial-server-error",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	var upstreamErr *upstreamopenai.OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.True(t, upstreamopenai.IsOpenAIImagesRetryableUpstreamError(upstreamErr))
	require.True(t, c.Writer.Written())
	require.Contains(t, rec.Body.String(), "event: image_generation.partial_image")
	require.Contains(t, rec.Body.String(), "event: error")
	require.Contains(t, rec.Body.String(), "failed after partial output")

	rawEvents, ok := c.Get(gatewayhttp.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "retry_exhausted_failover", events[0].Kind)
}

func TestOpenAIImagesSSEErrorStatus(t *testing.T) {
	tests := []struct {
		name    string
		errType string
		code    string
		want    int
	}{
		{name: "rate limit", errType: "rate_limit_error", want: http.StatusTooManyRequests},
		{name: "auth", code: "invalid_api_key", want: http.StatusUnauthorized},
		{name: "permission", errType: "permission_error", want: http.StatusForbidden},
		{name: "not found", code: "model_not_found", want: http.StatusNotFound},
		{name: "moderation", errType: "image_generation_user_error", code: "moderation_blocked", want: http.StatusBadRequest},
		{name: "server", errType: "server_error", code: "server_error", want: http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, upstreamopenai.OpenAIImagesSSEErrorStatus(tt.errType, tt.code))
		})
	}
}

func TestOpenAIGatewayServiceForwardImages_APIKeyStreamingDrainsAfterClientDisconnect(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","stream":true}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Writer = &failingOpenAIImageWriter{ResponseWriter: c.Writer, failAfter: 1}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				ImageStreamDataIntervalTimeout: 1,
				ImageStreamKeepaliveInterval:   0,
			},
		},
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"req_img_stream_disconnect_apikey"},
				},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"cGFydGlhbA==\"}\n\n" +
						"data: {\"type\":\"image_generation.completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":4,\"output_tokens_details\":{\"image_tokens\":2}},\"b64_json\":\"ZmluYWw=\",\"output_format\":\"png\"}\n\n" +
						"data: [DONE]\n\n",
				)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 8,
		Name:     "openai-apikey",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test-api-key",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.ImageOutputTokens)
}

func TestOpenAIGatewayServiceForwardImages_OAuthEditsMultipartUsesResponsesAPI(t *testing.T) {

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("prompt", "replace background with aurora"))
	require.NoError(t, writer.WriteField("input_fidelity", "high"))
	require.NoError(t, writer.WriteField("output_format", "webp"))
	require.NoError(t, writer.WriteField("quality", "high"))

	imageHeader := make(textproto.MIMEHeader)
	imageHeader.Set("Content-Disposition", `form-data; name="image"; filename="source.png"`)
	imageHeader.Set("Content-Type", "image/png")
	imagePart, err := writer.CreatePart(imageHeader)
	require.NoError(t, err)
	_, err = imagePart.Write([]byte("png-image-content"))
	require.NoError(t, err)

	maskHeader := make(textproto.MIMEHeader)
	maskHeader.Set("Content-Disposition", `form-data; name="mask"; filename="mask.png"`)
	maskHeader.Set("Content-Type", "image/png")
	maskPart, err := writer.CreatePart(maskHeader)
	require.NoError(t, err)
	_, err = maskPart.Write([]byte("png-mask-content"))
	require.NoError(t, err)

	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set("api_key", &apikey.APIKey{ID: 100})

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body.Bytes())
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"req_img_edit_123"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000002,\"usage\":{\"input_tokens\":13,\"output_tokens\":21,\"output_tokens_details\":{\"image_tokens\":8}},\"tool_usage\":{\"image_gen\":{\"images\":1}},\"output\":[{\"type\":\"image_generation_call\",\"result\":\"ZWRpdGVk\",\"revised_prompt\":\"replace background with aurora\",\"output_format\":\"webp\",\"quality\":\"high\"}]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	svc.httpUpstream = upstream
	if svc.Requests != nil {
		svc.Requests.Transport = svc.httpUpstream
	}
	if svc.Grok != nil {
		svc.Grok.Transport = svc.httpUpstream
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body.Bytes(), parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, "gpt-image-2", gjson.GetBytes(upstream.lastBody, "tools.0.model").String())
	require.Equal(t, "edit", gjson.GetBytes(upstream.lastBody, "tools.0.action").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tools.0.input_fidelity").Exists())
	require.Equal(t, "webp", gjson.GetBytes(upstream.lastBody, "tools.0.output_format").String())
	require.True(t, strings.HasPrefix(gjson.GetBytes(upstream.lastBody, "input.0.content.1.image_url").String(), "data:image/png;base64,"))
	require.True(t, strings.HasPrefix(gjson.GetBytes(upstream.lastBody, "tools.0.input_image_mask.image_url").String(), "data:image/png;base64,"))
	require.Equal(t, "replace background with aurora", gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())
	require.Equal(t, "ZWRpdGVk", gjson.Get(rec.Body.String(), "data.0.b64_json").String())
	require.Equal(t, "replace background with aurora", gjson.Get(rec.Body.String(), "data.0.revised_prompt").String())
}

func TestOpenAIGatewayServiceForwardImages_OAuthEditsStreamingTransformsEvents(t *testing.T) {

	body := []byte(`{
		"model":"gpt-image-2",
		"prompt":"replace background with aurora",
		"images":[{"image_url":"https://example.com/source.png"}],
		"mask":{"image_url":"https://example.com/mask.png"},
		"stream":true,
		"response_format":"url"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.created\",\"response\":{\"created_at\":1710000003,\"tools\":[{\"type\":\"image_generation\",\"model\":\"gpt-image-2\",\"background\":\"transparent\",\"output_format\":\"webp\",\"quality\":\"high\",\"size\":\"1024x1024\"}]}}\n\n" +
					"data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"cGFydGlhbA==\",\"partial_image_index\":0,\"output_format\":\"webp\",\"background\":\"transparent\"}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000003,\"usage\":{\"input_tokens\":7,\"output_tokens\":10,\"output_tokens_details\":{\"image_tokens\":5}},\"tool_usage\":{\"image_gen\":{\"images\":1}},\"tools\":[{\"type\":\"image_generation\",\"model\":\"gpt-image-2\",\"background\":\"transparent\",\"output_format\":\"webp\",\"quality\":\"high\",\"size\":\"1024x1024\"}],\"output\":[{\"type\":\"image_generation_call\",\"result\":\"ZWRpdGVk\",\"revised_prompt\":\"replace background with aurora\",\"output_format\":\"webp\"}]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	svc.httpUpstream = upstream
	if svc.Requests != nil {
		svc.Requests.Transport = svc.httpUpstream
	}
	if svc.Grok != nil {
		svc.Grok.Transport = svc.httpUpstream
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, "edit", gjson.GetBytes(upstream.lastBody, "tools.0.action").String())
	require.Equal(t, "https://example.com/source.png", gjson.GetBytes(upstream.lastBody, "input.0.content.1.image_url").String())
	require.Equal(t, "https://example.com/mask.png", gjson.GetBytes(upstream.lastBody, "tools.0.input_image_mask.image_url").String())
	events := parseOpenAIImageTestSSEEvents(rec.Body.String())
	partial, ok := findOpenAIImageTestSSEEvent(events, "image_edit.partial_image")
	require.True(t, ok)
	require.Equal(t, "image_edit.partial_image", gjson.Get(partial.Data, "type").String())
	require.Equal(t, int64(1710000003), gjson.Get(partial.Data, "created_at").Int())
	require.Equal(t, "cGFydGlhbA==", gjson.Get(partial.Data, "b64_json").String())
	require.Equal(t, "data:image/webp;base64,cGFydGlhbA==", gjson.Get(partial.Data, "url").String())
	require.Equal(t, "gpt-image-2", gjson.Get(partial.Data, "model").String())
	require.Equal(t, "webp", gjson.Get(partial.Data, "output_format").String())
	require.Equal(t, "high", gjson.Get(partial.Data, "quality").String())
	require.Equal(t, "1024x1024", gjson.Get(partial.Data, "size").String())
	require.Equal(t, "transparent", gjson.Get(partial.Data, "background").String())

	completed, ok := findOpenAIImageTestSSEEvent(events, "image_edit.completed")
	require.True(t, ok)
	require.Equal(t, "image_edit.completed", gjson.Get(completed.Data, "type").String())
	require.Equal(t, int64(1710000003), gjson.Get(completed.Data, "created_at").Int())
	require.Equal(t, "ZWRpdGVk", gjson.Get(completed.Data, "b64_json").String())
	require.Equal(t, "data:image/webp;base64,ZWRpdGVk", gjson.Get(completed.Data, "url").String())
	require.Equal(t, "gpt-image-2", gjson.Get(completed.Data, "model").String())
	require.Equal(t, "webp", gjson.Get(completed.Data, "output_format").String())
	require.Equal(t, "high", gjson.Get(completed.Data, "quality").String())
	require.Equal(t, "1024x1024", gjson.Get(completed.Data, "size").String())
	require.Equal(t, "transparent", gjson.Get(completed.Data, "background").String())
	require.JSONEq(t, `{"images":1}`, gjson.Get(completed.Data, "usage").Raw)
	require.False(t, gjson.Get(completed.Data, "revised_prompt").Exists())
}

func TestBuildOpenAIImagesResponsesRequest_PassesThroughNForMultiImageModels(t *testing.T) {
	parsed := &media.ImageRequest{
		Endpoint: upstreamcore.OpenAIImagesGenerationsEndpoint,
		Model:    "gpt-image-2",
		Prompt:   "draw a cat",
		N:        2,
	}

	body, err := buildOpenAIImagesResponsesRequest(parsed, "gpt-image-2")
	require.NoError(t, err)
	require.NotNil(t, body)
	require.Equal(t, int64(2), gjson.GetBytes(body, "tools.0.n").Int())
	require.Equal(t, "gpt-image-2", gjson.GetBytes(body, "tools.0.model").String())
	require.Equal(t, "draw a cat", gjson.GetBytes(body, "input.0.content.0.text").String())
}

func TestBuildOpenAIImagesResponsesRequest_ForcesImageToolChoice(t *testing.T) {
	parsed := &media.ImageRequest{
		Endpoint: upstreamcore.OpenAIImagesGenerationsEndpoint,
		Model:    "gpt-image-2",
		Prompt:   "draw a cat",
	}

	body, err := buildOpenAIImagesResponsesRequest(parsed, "gpt-image-2")
	require.NoError(t, err)
	require.NotNil(t, body)
	require.Equal(t, "image_generation", gjson.GetBytes(body, "tool_choice.type").String())
	require.Equal(t, "image_generation", gjson.GetBytes(body, "tools.0.type").String())
	require.Equal(t, "gpt-image-2", gjson.GetBytes(body, "tools.0.model").String())
}

func TestBuildOpenAIImagesResponsesRequest_DoesNotPassNForDallE3(t *testing.T) {
	parsed := &media.ImageRequest{
		Endpoint: upstreamcore.OpenAIImagesGenerationsEndpoint,
		Model:    "dall-e-3",
		Prompt:   "draw a cat",
		N:        2,
	}

	body, err := buildOpenAIImagesResponsesRequest(parsed, "dall-e-3")
	require.NoError(t, err)
	require.NotNil(t, body)
	require.False(t, gjson.GetBytes(body, "tools.0.n").Exists())
	require.Equal(t, "dall-e-3", gjson.GetBytes(body, "tools.0.model").String())
}

func TestBuildOpenAIImagesResponsesRequest_StripsInputFidelity(t *testing.T) {
	parsed := &media.ImageRequest{
		Endpoint:      upstreamcore.OpenAIImagesEditsEndpoint,
		Model:         "gpt-image-2",
		Prompt:        "replace background",
		InputFidelity: "high",
		InputImageURLs: []string{
			"https://example.com/source.png",
		},
	}

	body, err := buildOpenAIImagesResponsesRequest(parsed, "gpt-image-2")
	require.NoError(t, err)
	require.NotNil(t, body)
	require.False(t, gjson.GetBytes(body, "tools.0.input_fidelity").Exists())
	require.Equal(t, "edit", gjson.GetBytes(body, "tools.0.action").String())
}

func TestBuildOpenAIImagesResponsesRequest_RequiresVerbatimUserPrompt(t *testing.T) {
	prompt := "画一个蓝色马克杯，杯身只写“SkelOT”，保持大小写；白色背景，不要增加其他文字。"
	parsed := &media.ImageRequest{
		Endpoint: upstreamcore.OpenAIImagesGenerationsEndpoint,
		Model:    "gpt-image-2",
		Prompt:   prompt,
		N:        1,
	}

	body, err := buildOpenAIImagesResponsesRequest(parsed, "gpt-image-2")
	require.NoError(t, err)
	require.Equal(t, upstreamopenai.ImagesVerbatimPromptInstructions, gjson.GetBytes(body, "instructions").String())
	require.Equal(t, prompt, gjson.GetBytes(body, "input.0.content.0.text").String())
}

func TestCollectOpenAIImagesFromResponsesBody_FallsBackToOutputItemDone(t *testing.T) {
	body := []byte(
		"data: {\"type\":\"response.created\",\"response\":{\"created_at\":1710000004}}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_123\",\"type\":\"image_generation_call\",\"result\":\"aGVsbG8=\",\"revised_prompt\":\"draw a cat\",\"output_format\":\"png\",\"quality\":\"high\"}}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000004,\"tool_usage\":{\"image_gen\":{\"images\":1}},\"output\":[]}}\n\n" +
			"data: [DONE]\n\n",
	)

	results, createdAt, usageRaw, firstMeta, foundFinal, err := collectOpenAIImagesFromResponsesBody(body)
	require.NoError(t, err)
	require.True(t, foundFinal)
	require.Equal(t, int64(1710000004), createdAt)
	require.Len(t, results, 1)
	require.Equal(t, "aGVsbG8=", results[0].Result)
	require.Equal(t, "draw a cat", results[0].RevisedPrompt)
	require.Equal(t, "png", firstMeta.OutputFormat)
	require.JSONEq(t, `{"images":1}`, string(usageRaw))
}

func TestCollectOpenAIImagesFromResponsesBody_MultilineSSE(t *testing.T) {
	body := []byte(
		"data: {\"type\":\"response.completed\",\n" +
			"data: \"response\":{\"created_at\":1710000010,\"usage\":{\"input_tokens\":5,\"output_tokens\":9,\"output_tokens_details\":{\"image_tokens\":4}},\"tool_usage\":{\"image_gen\":{\"images\":1}},\"output\":[{\"type\":\"image_generation_call\",\"result\":\"ZmluYWw=\",\"output_format\":\"png\"}]}}\n\n" +
			"data: [DONE]\n\n",
	)

	results, createdAt, usageRaw, firstMeta, foundFinal, err := collectOpenAIImagesFromResponsesBody(body)
	require.NoError(t, err)
	require.True(t, foundFinal)
	require.Equal(t, int64(1710000010), createdAt)
	require.Len(t, results, 1)
	require.Equal(t, "ZmluYWw=", results[0].Result)
	require.Equal(t, "png", firstMeta.OutputFormat)
	require.JSONEq(t, `{"images":1}`, string(usageRaw))
}

func TestOpenAIGatewayServiceForwardImages_OAuthStreamingHandlesOutputItemDoneFallback(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","stream":true,"response_format":"url"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"req_img_stream_output_item_done"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_123\",\"type\":\"image_generation_call\",\"result\":\"ZmluYWw=\",\"revised_prompt\":\"draw a cat\",\"output_format\":\"png\"}}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000005,\"usage\":{\"input_tokens\":5,\"output_tokens\":9,\"output_tokens_details\":{\"image_tokens\":4}},\"tool_usage\":{\"image_gen\":{\"images\":1}},\"output\":[]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	svc.httpUpstream = upstream
	if svc.Requests != nil {
		svc.Requests.Transport = svc.httpUpstream
	}
	if svc.Grok != nil {
		svc.Grok.Transport = svc.httpUpstream
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 1, result.ImageCount)
	events := parseOpenAIImageTestSSEEvents(rec.Body.String())
	completed, ok := findOpenAIImageTestSSEEvent(events, "image_generation.completed")
	require.True(t, ok)
	require.Equal(t, "image_generation.completed", gjson.Get(completed.Data, "type").String())
	require.Equal(t, int64(1710000005), gjson.Get(completed.Data, "created_at").Int())
	require.Equal(t, "ZmluYWw=", gjson.Get(completed.Data, "b64_json").String())
	require.Equal(t, "data:image/png;base64,ZmluYWw=", gjson.Get(completed.Data, "url").String())
	require.Equal(t, "gpt-image-2", gjson.Get(completed.Data, "model").String())
	require.JSONEq(t, `{"images":1}`, gjson.Get(completed.Data, "usage").Raw)
	require.NotContains(t, rec.Body.String(), "event: error")
}

func TestOpenAIGatewayServiceForwardImages_OAuthStreamingHandlesMultilineSSE(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","stream":true,"response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	svc.httpUpstream = &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"req_img_stream_multiline_oauth"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.completed\",\n" +
					"data: \"response\":{\"created_at\":1710000011,\"usage\":{\"input_tokens\":6,\"output_tokens\":10,\"output_tokens_details\":{\"image_tokens\":5}},\"tool_usage\":{\"image_gen\":{\"images\":1}},\"output\":[{\"type\":\"image_generation_call\",\"result\":\"TXVsdGlsaW5l\",\"output_format\":\"png\"}]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	if svc.Requests != nil {
		svc.Requests.Transport = svc.httpUpstream
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 11,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 6, result.Usage.InputTokens)
	require.Equal(t, 10, result.Usage.OutputTokens)
	require.Equal(t, 5, result.Usage.ImageOutputTokens)
	events := parseOpenAIImageTestSSEEvents(rec.Body.String())
	completed, ok := findOpenAIImageTestSSEEvent(events, "image_generation.completed")
	require.True(t, ok)
	require.Equal(t, "TXVsdGlsaW5l", gjson.Get(completed.Data, "b64_json").String())
	require.JSONEq(t, `{"images":1}`, gjson.Get(completed.Data, "usage").Raw)
	require.NotContains(t, rec.Body.String(), "event: error")
}

func TestOpenAIGatewayServiceForwardImages_OAuthStreamingDrainsAfterClientDisconnect(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","stream":true,"response_format":"url"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Writer = &failingOpenAIImageWriter{ResponseWriter: c.Writer, failAfter: 1}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				ImageStreamDataIntervalTimeout: 1,
				ImageStreamKeepaliveInterval:   0,
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"req_img_stream_disconnect_oauth"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"cGFydGlhbA==\",\"partial_image_index\":0,\"output_format\":\"png\"}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000009,\"usage\":{\"input_tokens\":5,\"output_tokens\":9,\"output_tokens_details\":{\"image_tokens\":4}},\"tool_usage\":{\"image_gen\":{\"images\":1}},\"output\":[{\"type\":\"image_generation_call\",\"result\":\"ZmluYWw=\",\"output_format\":\"png\"}]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	svc.httpUpstream = upstream
	if svc.Requests != nil {
		svc.Requests.Transport = svc.httpUpstream
	}
	if svc.Grok != nil {
		svc.Grok.Transport = svc.httpUpstream
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 9, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.ImageOutputTokens)
}
