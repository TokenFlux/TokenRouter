package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// TestImagesURLToB64JSONEnabled checks missing providers and differently
// typed extra settings. Backfill is enabled by the tested boolean true value,
// while missing, string and false settings leave it disabled.
func TestImagesURLToB64JSONEnabled(t *testing.T) {
	require.False(t, gatewayprovider.ImagesURLToB64JSONEnabled(nil))
	require.False(t, gatewayprovider.ImagesURLToB64JSONEnabled(&gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation}}))
	require.False(t, gatewayprovider.ImagesURLToB64JSONEnabled(&gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Extra: map[string]any{gatewayprovider.ProviderExtraImagesURLToB64JSON: "true"}}}))
	require.False(t, gatewayprovider.ImagesURLToB64JSONEnabled(&gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Extra: map[string]any{gatewayprovider.ProviderExtraImagesURLToB64JSON: false}}}))
	require.True(t, gatewayprovider.ImagesURLToB64JSONEnabled(b64BackfillProvider(true)))
}

// TestBackfillOpenAIImagesB64JSON mixes eligible URL entries, existing
// Base64, data URIs and failed downloads under response-format restrictions.
// Expected fields and download counts protect selective filling and retention
// of original item metadata.
func TestBackfillOpenAIImagesB64JSON(t *testing.T) {
	wantB64 := base64.StdEncoding.EncodeToString(b64BackfillPNGBytes)

	tests := []struct {
		name          string                 // backfill eligibility case label
		enabled       bool                   // provider backfill enable flag
		parsed        *media.ImageRequest    // client image format restrictions
		body          string                 // supplied upstream image JSON
		upstream      *auxiliaryHTTPRecorder // configured download response fixture
		wantB64       []string               // expected per-item Base64 values
		wantDownloads int                    // expected HTTP download count
	}{
		{
			name:          "开关关闭时原样返回",
			enabled:       false,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{""},
			wantDownloads: 0,
		},
		{
			name:          "缺少 b64_json 且有 url 时下载回填并保留 url",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png","revised_prompt":"a cat"}]}`,
			upstream:      &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{wantB64},
			wantDownloads: 1,
		},
		{
			name:          "b64_json 为空串时同样回填",
			enabled:       true,
			body:          `{"created":1,"data":[{"b64_json":"","url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{wantB64},
			wantDownloads: 1,
		},
		{
			name:          "b64_json 已有值时不下载",
			enabled:       true,
			body:          `{"created":1,"data":[{"b64_json":"aW1n","url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{"aW1n"},
			wantDownloads: 0,
		},
		{
			name:          "多项混合时只回填缺失项",
			enabled:       true,
			body:          `{"created":1,"data":[{"b64_json":"aW1n"},{"url":"https://cdn.example.com/b.png"},{"revised_prompt":"none"}]}`,
			upstream:      &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{"aW1n", wantB64, ""},
			wantDownloads: 1,
		},
		{
			name:          "data URL 直接取载荷不下载",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"data:image/png;base64,` + wantB64 + `"}]}`,
			upstream:      &auxiliaryHTTPRecorder{},
			wantB64:       []string{wantB64},
			wantDownloads: 0,
		},
		{
			name:          "客户端显式要求 url 时不回填",
			enabled:       true,
			parsed:        &media.ImageRequest{ResponseFormat: "url"},
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{""},
			wantDownloads: 0,
		},
		{
			name:          "下载非 2xx 时保留原样",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusNotFound, "text/plain", []byte("gone"))},
			wantB64:       []string{""},
			wantDownloads: 1,
		},
		{
			name:          "下载内容不是图片时保留原样",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "text/html", []byte("<html>login required</html>"))},
			wantB64:       []string{""},
			wantDownloads: 1,
		},
		{
			name:          "响应头未声明图片类型但字节嗅探为图片时回填",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a"}]}`,
			upstream:      &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "application/octet-stream", b64BackfillPNGBytes)},
			wantB64:       []string{wantB64},
			wantDownloads: 1,
		},
		{
			name:          "响应头声明图片但字节不是图片时保留原样",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))},
			wantB64:       []string{""},
			wantDownloads: 1,
		},
		{
			name:          "非 http(s) 协议的 url 不下载",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"ftp://cdn.example.com/a.png"}]}`,
			upstream:      &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{""},
			wantDownloads: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newImagesFixture(imagesFixtureInputs{transport: tt.upstream})
			got := svc.backfillOpenAIImagesB64JSON(context.Background(), b64BackfillProvider(tt.enabled), tt.parsed, []byte(tt.body))
			require.True(t, gjson.ValidBytes(got))
			items := gjson.GetBytes(got, "data").Array()
			require.Len(t, items, len(tt.wantB64))
			for i, want := range tt.wantB64 {
				require.Equal(t, want, items[i].Get("b64_json").String(), "data.%d.b64_json", i)
			}
			require.Len(t, tt.upstream.requests, tt.wantDownloads)
			// Compare original item fields besides Base64 and the creation value, since
			// delivery filling should retain upstream URL, prompt and other supplied metadata.
			original := gjson.Parse(tt.body)
			original.Get("data").ForEach(func(key, item gjson.Result) bool {
				item.ForEach(func(field, value gjson.Result) bool {
					if field.String() == "b64_json" {
						return true
					}
					require.Equal(t, value.Raw, gjson.GetBytes(got, "data."+key.String()+"."+field.String()).Raw)
					return true
				})
				return true
			})
			require.Equal(t, original.Get("created").Raw, gjson.GetBytes(got, "created").Raw)
		})
	}
}

// TestBackfillOpenAIImagesB64JSON_DownloadRequestShape downloads a signed
// escaped URL through the selected provider proxy. The GET retains URL
// spelling, omits authentication and cookies, and carries a deadline plus
// public-host redirect policy.
func TestBackfillOpenAIImagesB64JSON_DownloadRequestShape(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)}
	svc := newImagesFixture(imagesFixtureInputs{transport: upstream})
	provider := b64BackfillProvider(true)
	proxyID := int64(3)
	provider.Record.ProxyID = &proxyID
	provider.Record.Proxy = &egress.Proxy{Protocol: "http", Host: "127.0.0.1", Port: 7890}

	body := []byte(`{"created":1,"data":[{"url":"https://cdn.example.com/a%2Fb/?sig=abc%2Fdef"}]}`)
	got := svc.backfillOpenAIImagesB64JSON(context.Background(), provider, nil, body)
	require.Equal(t, base64.StdEncoding.EncodeToString(b64BackfillPNGBytes), gjson.GetBytes(got, "data.0.b64_json").String())

	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	require.Equal(t, http.MethodGet, req.Method)
	require.Empty(t, req.Header.Get("Authorization"))
	require.Empty(t, req.Header.Get("Cookie"))
	require.Empty(t, req.Header.Get("X-Api-Key"))
	require.Equal(t, "https://cdn.example.com/a%2Fb/?sig=abc%2Fdef", req.URL.String())
	require.Equal(t, "http://127.0.0.1:7890", upstream.lastProxyURL)
	_, hasDeadline := req.Context().Deadline()
	require.True(t, hasDeadline)
	// Download requests permit redirects and require public destinations at every hop.
	require.True(t, upstreamcore.HTTPUpstreamPublicHostsOnly(req.Context()))
	require.False(t, upstreamcore.HTTPUpstreamRedirectsDisabled(req.Context()))
}

// TestBackfillOpenAIImagesB64JSON_RejectsPrivateHosts compares local and
// private literals with a public hostname under the same HTTP configuration.
// Blocked destinations produce no download, while the public control case
// receives Base64 data.
func TestBackfillOpenAIImagesB64JSON_RejectsPrivateHosts(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)}
	svc := newImagesFixture(imagesFixtureInputs{allowHTTP: true, transport: upstream})
	provider := b64BackfillProvider(true)

	for _, rawURL := range []string{
		"http://localhost:8080/api/v1/admin/providers",
		"http://Foo.LocalHost/a.png",
		"http://127.0.0.1:8080/a.png",
		"https://127.0.0.1/a.png",
		"http://[::1]:8080/a.png",
		"http://10.0.0.5/a.png",
		"http://172.16.0.9:9000/bucket/a.png",
		"http://192.168.1.10:9000/bucket/a.png",
		"http://169.254.169.254/latest/meta-data/",
		"http://0.0.0.0:8080/a.png",
		"http://[fe80::1]/a.png",
		"http://[::ffff:127.0.0.1]/a.png",
	} {
		body := `{"created":1,"data":[{"url":"` + rawURL + `"}]}`
		got := svc.backfillOpenAIImagesB64JSON(context.Background(), provider, nil, []byte(body))
		require.Equal(t, body, string(got), "url=%s", rawURL)
	}
	require.Empty(t, upstream.requests, "private destinations must never be requested")

	// The same configuration accepts a public hostname, isolating the host validation check.
	got := svc.backfillOpenAIImagesB64JSON(context.Background(), provider, nil, []byte(`{"created":1,"data":[{"url":"http://cdn.example.com/a.png"}]}`))
	require.Equal(t, base64.StdEncoding.EncodeToString(b64BackfillPNGBytes), gjson.GetBytes(got, "data.0.b64_json").String())
	require.Len(t, upstream.requests, 1)
}

// TestIsBackfillImageContent supplies recognized image signatures and
// unsupported binary or text bodies. Byte sniffing accepts the tested PNG,
// JPEG, WebP and GIF signatures and rejects the other fixture formats.
func TestIsBackfillImageContent(t *testing.T) {
	webp := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 8)...)
	tests := []struct {
		name string // content signature case label
		data []byte // supplied content sniffing bytes
		want bool   // expected image-signature acceptance
	}{
		{name: "png", data: b64BackfillPNGBytes, want: true},
		{name: "jpeg", data: []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}, want: true},
		{name: "webp", data: webp, want: true},
		{name: "gif", data: []byte("GIF89a\x01\x00\x01\x00"), want: true},
		{name: "bmp 不在允许列表", data: []byte("BM\x00\x00\x00\x00\x00\x00\x00\x00"), want: false},
		{name: "ico 不在允许列表", data: []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00}, want: false},
		{name: "svg 文本", data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), want: false},
		{name: "html", data: []byte("<html>login required</html>"), want: false},
		{name: "json", data: []byte(`{"error":"unauthorized"}`), want: false},
		{name: "空", data: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, upstreamopenai.IsBackfillImageContent(tt.data))
		})
	}
}

// TestBackfillOpenAIImagesB64JSON_NonObjectBodies supplies malformed or
// unsupported image-list shapes, including an empty list and scalar items.
// Each tested body stays unchanged and causes no upstream download.
func TestBackfillOpenAIImagesB64JSON_NonObjectBodies(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)}
	svc := newImagesFixture(imagesFixtureInputs{transport: upstream})
	provider := b64BackfillProvider(true)

	for _, body := range []string{``, `not json`, `{"created":1}`, `{"created":1,"data":{}}`, `{"created":1,"data":[]}`, `{"created":1,"data":["x"]}`} {
		got := svc.backfillOpenAIImagesB64JSON(context.Background(), provider, nil, []byte(body))
		require.Equal(t, body, string(got), "body=%q", body)
	}
	require.Empty(t, upstream.requests)
}

// TestBackfillOpenAIImagesB64JSON_ForkBoundaries combines streaming,
// malformed JSON, unsupported provider identity and a blocked URL policy. All
// tested ineligible requests retain their original body and issue no
// downloads.
func TestBackfillOpenAIImagesB64JSON_ForkBoundaries(t *testing.T) {
	body := []byte(`{"data":[{"url":"https://cdn.example.com/a.png"}]}`)
	upstream := &auxiliaryHTTPRecorder{}
	svc := newImagesFixture(imagesFixtureInputs{transport: upstream})
	provider := b64BackfillProvider(true)
	require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), provider, &media.ImageRequest{Stream: true}, body))
	invalid := append(append([]byte{}, body...), []byte("invalid")...)
	require.Equal(t, invalid, svc.backfillOpenAIImagesB64JSON(t.Context(), provider, nil, invalid))
	provider.Record.Type = capability.ProviderTypeOAuth
	require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), provider, nil, body))
	provider.Record.Type, provider.Record.Platform = capability.ProviderTypeAPIKey, capability.PlatformGrok
	require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), provider, nil, body))
	provider = b64BackfillProvider(true)
	svc.Requests.Options.URLPolicy.Enabled = true
	svc.Requests.Options.URLPolicy.UpstreamHosts = []string{"relay.example.com"}
	require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), provider, nil, body))
	require.Empty(t, upstream.requests)
}

// TestBackfillOpenAIImagesB64JSON_SizeAndDataURLValidation supplies oversized
// downloaded bytes and invalid inline image data. The response remains
// unchanged, with exactly the one attempted HTTP download recorded by the
// fixture.
func TestBackfillOpenAIImagesB64JSON_SizeAndDataURLValidation(t *testing.T) {
	oversized := make([]byte, upstreamopenai.OpenAIImageMaxDownloadBytes+1)
	copy(oversized, b64BackfillPNGBytes)
	upstream := &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(200, "image/png", oversized)}
	svc := newImagesFixture(imagesFixtureInputs{transport: upstream})
	provider := b64BackfillProvider(true)
	body := []byte(`{"data":[{"url":"https://cdn.example.com/a.png"}]}`)
	require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), provider, nil, body))
	for _, payload := range []string{"aGVsbG8=", "%%%", ""} {
		body = []byte(`{"data":[{"url":"data:image/png;base64,` + payload + `"}]}`)
		require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), provider, nil, body))
	}
	require.Len(t, upstream.requests, 1)
}

// TestBackfillOpenAIImagesB64JSON_PreservesBillingMetadata fills a URL entry
// with a downloaded image of different dimensions. Usage and output count
// still come from the pre-backfill response, with no newly inferred size in
// those observations.
func TestBackfillOpenAIImagesB64JSON_PreservesBillingMetadata(t *testing.T) {
	var img bytes.Buffer
	require.NoError(t, png.Encode(&img, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	upstream := &auxiliaryHTTPRecorder{resp: b64BackfillImageResponse(200, "image/png", img.Bytes())}
	svc := newImagesFixture(imagesFixtureInputs{transport: upstream})
	body := `{"data":[{"url":"https://cdn.example.com/a.png"}],"usage":{"input_tokens":10,"output_tokens":20}}`
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := b64BackfillImageResponse(200, "application/json", []byte(body))
	options := svc.Output.ImageOptions(c)
	options.Backfill = func(body []byte) []byte {
		return svc.backfillOpenAIImagesB64JSON(t.Context(), b64BackfillProvider(true), &media.ImageRequest{Size: "1024x1024"}, body)
	}
	usage, count, sizes, err := upstreamopenai.ReadImagesNonStreaming(resp, ResponseSink{Writer: c.Writer}, options)
	require.NoError(t, err)
	require.Equal(t, 10, usage.InputTokens)
	require.Equal(t, 20, usage.OutputTokens)
	require.Equal(t, 1, count)
	require.Empty(t, sizes)
	require.Equal(t, base64.StdEncoding.EncodeToString(img.Bytes()), gjson.Get(rec.Body.String(), "data.0.b64_json").String())
}

// TestCollectOpenAIImagePointers_RecognizesDirectAssets mixes inline data, a
// signed download URL and a file-service pointer under one prompt. Collection
// retains all three identities and associates the expected prompt with the
// inline item.
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

// TestResolveOpenAIImageBytes_PrefersInlineBase64 resolves inline encoded
// bytes with no download client. The helper returns the decoded ABC payload
// from its data-URI input, exercising byte resolution independently of
// content sniffing.
func TestResolveOpenAIImageBytes_PrefersInlineBase64(t *testing.T) {
	data, err := upstreamopenai.ResolveOpenAIImageBytes(context.Background(), nil, nil, "", upstreamopenai.ImagePointerInfo{
		B64JSON: "data:image/png;base64,QUJD",
	}, int64(512<<10))
	require.NoError(t, err)
	require.Equal(t, []byte("ABC"), data)
}

// TestNewOpenAIImageStatusError_UsesProvidedReadLimit places an error message
// beyond the ordinary short read budget. The supplied larger limit retains
// the complete response body and exposes its diagnostic message.
func TestNewOpenAIImageStatusError_UsesProvidedReadLimit(t *testing.T) {
	// Put the diagnostic beyond the shorter budget to expose ignored caller limits.
	padding := strings.Repeat("x", int(int64(512<<10))+1024)
	body := fmt.Sprintf(`{"error":{"padding":"%s","message":"diagnostic-marker"}}`, padding)
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	err := upstreamopenai.NewOpenAIImageStatusError(resp, "download image bytes failed", int64(len(body)))
	require.Error(t, err)
	require.Equal(t, "diagnostic-marker", err.Error())

	var statusErr *upstreamopenai.ImageStatusError
	require.ErrorAs(t, err, &statusErr)
	require.Len(t, statusErr.ResponseBody, len(body))
}
