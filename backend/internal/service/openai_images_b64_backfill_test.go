package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 8 字节 PNG 魔数足以让字节嗅探判定为 image/png。
var b64BackfillPNGBytes = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}

func b64BackfillImageResponse(status int, contentType string, payload []byte) *http.Response {
	header := http.Header{}
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(payload)),
	}
}

func b64BackfillAccount(enabled bool) *Account {
	account := &Account{
		ID:       7,
		Name:     "openai-apikey",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://relay.example.com/v1",
		},
	}
	if enabled {
		account.Extra = map[string]any{AccountExtraImagesURLToB64JSON: true}
	}
	return account
}

func TestImagesURLToB64JSONEnabled(t *testing.T) {
	require.False(t, ImagesURLToB64JSONEnabled(nil))
	require.False(t, ImagesURLToB64JSONEnabled(&Account{}))
	require.False(t, ImagesURLToB64JSONEnabled(&Account{Extra: map[string]any{AccountExtraImagesURLToB64JSON: "true"}}))
	require.False(t, ImagesURLToB64JSONEnabled(&Account{Extra: map[string]any{AccountExtraImagesURLToB64JSON: false}}))
	require.True(t, ImagesURLToB64JSONEnabled(b64BackfillAccount(true)))
}

func TestBackfillOpenAIImagesB64JSON(t *testing.T) {
	wantB64 := base64.StdEncoding.EncodeToString(b64BackfillPNGBytes)

	tests := []struct {
		name          string
		enabled       bool
		parsed        *media.ImageRequest
		body          string
		upstream      *httpUpstreamRecorder
		wantB64       []string
		wantDownloads int
	}{
		{
			name:          "开关关闭时原样返回",
			enabled:       false,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{""},
			wantDownloads: 0,
		},
		{
			name:          "缺少 b64_json 且有 url 时下载回填并保留 url",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png","revised_prompt":"a cat"}]}`,
			upstream:      &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{wantB64},
			wantDownloads: 1,
		},
		{
			name:          "b64_json 为空串时同样回填",
			enabled:       true,
			body:          `{"created":1,"data":[{"b64_json":"","url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{wantB64},
			wantDownloads: 1,
		},
		{
			name:          "b64_json 已有值时不下载",
			enabled:       true,
			body:          `{"created":1,"data":[{"b64_json":"aW1n","url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{"aW1n"},
			wantDownloads: 0,
		},
		{
			name:          "多项混合时只回填缺失项",
			enabled:       true,
			body:          `{"created":1,"data":[{"b64_json":"aW1n"},{"url":"https://cdn.example.com/b.png"},{"revised_prompt":"none"}]}`,
			upstream:      &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{"aW1n", wantB64, ""},
			wantDownloads: 1,
		},
		{
			name:          "data URL 直接取载荷不下载",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"data:image/png;base64,` + wantB64 + `"}]}`,
			upstream:      &httpUpstreamRecorder{},
			wantB64:       []string{wantB64},
			wantDownloads: 0,
		},
		{
			name:          "客户端显式要求 url 时不回填",
			enabled:       true,
			parsed:        &media.ImageRequest{ResponseFormat: "url"},
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{""},
			wantDownloads: 0,
		},
		{
			name:          "下载非 2xx 时保留原样",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusNotFound, "text/plain", []byte("gone"))},
			wantB64:       []string{""},
			wantDownloads: 1,
		},
		{
			name:          "下载内容不是图片时保留原样",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "text/html", []byte("<html>login required</html>"))},
			wantB64:       []string{""},
			wantDownloads: 1,
		},
		{
			name:          "响应头未声明图片类型但字节嗅探为图片时回填",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a"}]}`,
			upstream:      &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "application/octet-stream", b64BackfillPNGBytes)},
			wantB64:       []string{wantB64},
			wantDownloads: 1,
		},
		{
			name:          "响应头声明图片但字节不是图片时保留原样",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"https://cdn.example.com/a.png"}]}`,
			upstream:      &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))},
			wantB64:       []string{""},
			wantDownloads: 1,
		},
		{
			name:          "非 http(s) 协议的 url 不下载",
			enabled:       true,
			body:          `{"created":1,"data":[{"url":"ftp://cdn.example.com/a.png"}]}`,
			upstream:      &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)},
			wantB64:       []string{""},
			wantDownloads: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: tt.upstream}
			got := svc.backfillOpenAIImagesB64JSON(context.Background(), b64BackfillAccount(tt.enabled), tt.parsed, []byte(tt.body))
			require.True(t, gjson.ValidBytes(got))
			items := gjson.GetBytes(got, "data").Array()
			require.Len(t, items, len(tt.wantB64))
			for i, want := range tt.wantB64 {
				require.Equal(t, want, items[i].Get("b64_json").String(), "data.%d.b64_json", i)
			}
			require.Len(t, tt.upstream.requests, tt.wantDownloads)
			// 除 b64_json 外的字段必须原样保留。
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

func TestBackfillOpenAIImagesB64JSON_DownloadRequestShape(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := b64BackfillAccount(true)
	proxyID := int64(3)
	account.ProxyID = &proxyID
	account.Proxy = &egress.Proxy{Protocol: "http", Host: "127.0.0.1", Port: 7890}

	body := []byte(`{"created":1,"data":[{"url":"https://cdn.example.com/a%2Fb/?sig=abc%2Fdef"}]}`)
	got := svc.backfillOpenAIImagesB64JSON(context.Background(), account, nil, body)
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
	// 目的地与重定向各跳都必须解析到公网地址；重定向本身保持跟随。
	require.True(t, upstreamcore.HTTPUpstreamPublicHostsOnly(req.Context()))
	require.False(t, upstreamcore.HTTPUpstreamRedirectsDisabled(req.Context()))
}

func TestBackfillOpenAIImagesB64JSON_RejectsPrivateHosts(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
	account := b64BackfillAccount(true)

	for _, rawURL := range []string{
		"http://localhost:8080/api/v1/admin/accounts",
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
		got := svc.backfillOpenAIImagesB64JSON(context.Background(), account, nil, []byte(body))
		require.Equal(t, body, string(got), "url=%s", rawURL)
	}
	require.Empty(t, upstream.requests, "private destinations must never be requested")

	// 同一配置下公网主机照常下载，证明拒绝依据是目的地而非协议。
	got := svc.backfillOpenAIImagesB64JSON(context.Background(), account, nil, []byte(`{"created":1,"data":[{"url":"http://cdn.example.com/a.png"}]}`))
	require.Equal(t, base64.StdEncoding.EncodeToString(b64BackfillPNGBytes), gjson.GetBytes(got, "data.0.b64_json").String())
	require.Len(t, upstream.requests, 1)
}

func TestIsBackfillImageContent(t *testing.T) {
	webp := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 8)...)
	tests := []struct {
		name string
		data []byte
		want bool
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
			require.Equal(t, tt.want, openai.IsBackfillImageContent(tt.data))
		})
	}
}

func TestBackfillOpenAIImagesB64JSON_NonObjectBodies(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := b64BackfillAccount(true)

	for _, body := range []string{``, `not json`, `{"created":1}`, `{"created":1,"data":{}}`, `{"created":1,"data":[]}`, `{"created":1,"data":["x"]}`} {
		got := svc.backfillOpenAIImagesB64JSON(context.Background(), account, nil, []byte(body))
		require.Equal(t, body, string(got), "body=%q", body)
	}
	require.Empty(t, upstream.requests)
}

func TestOpenAIGatewayServiceForwardImages_APIKeyBackfillsB64JSONFromURL(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","size":"1024x1024"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set("api_key", &apikey.APIKey{ID: 42})

	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
					"X-Request-Id": []string{"req_img_url"},
				},
				Body: io.NopCloser(strings.NewReader(
					`{"created":1710000000,"data":[{"url":"https://cdn.example.com/cat.png","revised_prompt":"a cat"}],"usage":{"input_tokens":10,"output_tokens":20}}`,
				)),
			},
			b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes),
		},
	}
	svc.httpUpstream = upstream

	result, err := svc.ForwardImages(context.Background(), c, b64BackfillAccount(true), body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.False(t, result.Stream)

	require.Len(t, upstream.requests, 2)
	require.Equal(t, http.MethodPost, upstream.requests[0].Method)
	require.Equal(t, "https://relay.example.com/v1/images/generations", upstream.requests[0].URL.String())
	require.Equal(t, http.MethodGet, upstream.requests[1].Method)
	require.Equal(t, "https://cdn.example.com/cat.png", upstream.requests[1].URL.String())
	require.True(t, upstreamcore.HTTPUpstreamPublicHostsOnly(upstream.requests[1].Context()))

	require.Equal(t, http.StatusOK, rec.Code)
	wantB64 := base64.StdEncoding.EncodeToString(b64BackfillPNGBytes)
	require.Equal(t, wantB64, gjson.Get(rec.Body.String(), "data.0.b64_json").String())
	require.Equal(t, "https://cdn.example.com/cat.png", gjson.Get(rec.Body.String(), "data.0.url").String())
	require.Equal(t, "a cat", gjson.Get(rec.Body.String(), "data.0.revised_prompt").String())
	require.Equal(t, int64(1710000000), gjson.Get(rec.Body.String(), "created").Int())
}

func TestOpenAIGatewayServiceForwardImages_APIKeyLeavesURLOnlyResponseWhenDisabled(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","size":"1024x1024"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set("api_key", &apikey.APIKey{ID: 42})

	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	upstreamBody := `{"created":1710000000,"data":[{"url":"https://cdn.example.com/cat.png"}]}`
	upstream := &httpUpstreamRecorder{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(upstreamBody)),
			},
			b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes),
		},
	}
	svc.httpUpstream = upstream

	result, err := svc.ForwardImages(context.Background(), c, b64BackfillAccount(false), body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)

	require.Len(t, upstream.requests, 1)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, upstreamBody, rec.Body.String())
}

// 非 Images 同步交付、非法响应和非 OpenAI API Key 账号都不得触发额外网络请求。
func TestBackfillOpenAIImagesB64JSON_ForkBoundaries(t *testing.T) {
	body := []byte(`{"data":[{"url":"https://cdn.example.com/a.png"}]}`)
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := b64BackfillAccount(true)
	require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), account, &media.ImageRequest{Stream: true}, body))
	invalid := append(append([]byte{}, body...), []byte("invalid")...)
	require.Equal(t, invalid, svc.backfillOpenAIImagesB64JSON(t.Context(), account, nil, invalid))
	account.Type = capability.AccountTypeOAuth
	require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), account, nil, body))
	account.Type, account.Platform = capability.AccountTypeAPIKey, capability.PlatformGrok
	require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), account, nil, body))
	account = b64BackfillAccount(true)
	svc.cfg.Security.URLAllowlist.Enabled = true
	svc.cfg.Security.URLAllowlist.UpstreamHosts = []string{"relay.example.com"}
	require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), account, nil, body))
	require.Empty(t, upstream.requests)
}

// 超限下载及伪装为图片的 data URI 都保持原始结果，不能影响主请求成功。
func TestBackfillOpenAIImagesB64JSON_SizeAndDataURLValidation(t *testing.T) {
	oversized := make([]byte, openai.OpenAIImageMaxDownloadBytes+1)
	copy(oversized, b64BackfillPNGBytes)
	upstream := &httpUpstreamRecorder{resp: b64BackfillImageResponse(200, "image/png", oversized)}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := b64BackfillAccount(true)
	body := []byte(`{"data":[{"url":"https://cdn.example.com/a.png"}]}`)
	require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), account, nil, body))
	for _, payload := range []string{"aGVsbG8=", "%%%", ""} {
		body = []byte(`{"data":[{"url":"data:image/png;base64,` + payload + `"}]}`)
		require.Equal(t, body, svc.backfillOpenAIImagesB64JSON(t.Context(), account, nil, body))
	}
	require.Len(t, upstream.requests, 1)
}

// 下载图片可以补全响应，但不得改变 fork 已按原始响应确定的计费尺寸和用量。
func TestBackfillOpenAIImagesB64JSON_PreservesBillingMetadata(t *testing.T) {
	var img bytes.Buffer
	require.NoError(t, png.Encode(&img, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	upstream := &httpUpstreamRecorder{resp: b64BackfillImageResponse(200, "image/png", img.Bytes())}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	body := `{"data":[{"url":"https://cdn.example.com/a.png"}],"usage":{"input_tokens":10,"output_tokens":20}}`
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := b64BackfillImageResponse(200, "application/json", []byte(body))
	usage, count, sizes, err := svc.handleOpenAIImagesNonStreamingResponse(t.Context(), resp, c, b64BackfillAccount(true), &media.ImageRequest{Size: "1024x1024"})
	require.NoError(t, err)
	require.Equal(t, 10, usage.InputTokens)
	require.Equal(t, 20, usage.OutputTokens)
	require.Equal(t, 1, count)
	require.Empty(t, sizes)
	require.Equal(t, base64.StdEncoding.EncodeToString(img.Bytes()), gjson.Get(rec.Body.String(), "data.0.b64_json").String())
}
