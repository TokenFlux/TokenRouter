//go:build unit

package googleforward_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const geminiTestPNG = "iVBORw0KGgoAAAANSUhEUg=="

func newGeminiImageTestContext(t *testing.T) *googleforward.AttemptForTest {
	t.Helper()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost,
		"/v1beta/models/nana-banana-2:generateContent", strings.NewReader("{}"))
	return &googleforward.AttemptForTest{Output: gatewayhttp.NewGoogleBoundary(c, googleforward.Options{ResponseReadLimit: 128 << 20}, false)}
}

func geminiImageResponse(parts string) string {
	return `{"candidates":[{"content":{"role":"model","parts":[` + parts + `]},"finishReason":"STOP"}]}`
}

func TestCountGeminiInlineImageOutputs(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    int
	}{

		{

			name: "camelCase inlineData",

			payload: geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`),

			want: 1,
		},

		{
			// 官方 SDK 与部分中转把字段回成 snake_case。

			name: "snake_case inline_data",

			payload: geminiImageResponse(`{"inline_data":{"mime_type":"image/png","data":"` + geminiTestPNG + `"}}`),

			want: 1,
		},

		{

			name: "text and image mixed",

			payload: geminiImageResponse(`{"text":"here you go"},` +
				`{"inlineData":{"mimeType":"image/jpeg","data":"` + geminiTestPNG + `"}}`),

			want: 1,
		},

		{

			name: "multiple images",

			payload: geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}},` +
				`{"inlineData":{"mimeType":"image/webp","data":"` + geminiTestPNG + `"}}`),

			want: 2,
		},

		{

			name: "uppercase mime type",

			payload: geminiImageResponse(`{"inlineData":{"mimeType":"IMAGE/PNG","data":"` + geminiTestPNG + `"}}`),

			want: 1,
		},

		{
			name:    "text only",
			payload: geminiImageResponse(`{"text":"no image here"}`),
			want:    0,
		},

		{
			// 非图片的内联附件（例如音频）不能按图片计费。

			name: "non image mime type",

			payload: geminiImageResponse(`{"inlineData":{"mimeType":"audio/mpeg","data":"` + geminiTestPNG + `"}}`),

			want: 0,
		},

		{

			name: "empty data is not billable",

			payload: geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":""}}`),

			want: 0,
		},

		{name: "empty payload", payload: "", want: 0},

		{name: "invalid json", payload: "not-json", want: 0},

		{name: "error response", payload: `{"error":{"code":429,"message":"quota"}}`, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, gemini.CountGeminiInlineImageOutputs([]byte(tc.payload)))
		})
	}
}

// 累积式 SSE 会把同一张图在后续 chunk 里整段重发，逐 chunk 累加会重复计费。
// 计数器取单个 payload 内的最大值，正是为了挡住这一点。
func TestObserveGeminiImageOutputs_CumulativeChunksDoNotDoubleCount(t *testing.T) {
	c := newGeminiImageTestContext(t)
	c.Images = 0

	oneImage := geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`)
	for range 4 {
		c.ObserveImagesForTest([]byte(oneImage))
	}

	require.Equal(t, 1, c.Images)
}

func TestObserveGeminiImageOutputs_KeepsLargestChunk(t *testing.T) {
	c := newGeminiImageTestContext(t)
	c.Images = 0

	c.ObserveImagesForTest([]byte(geminiImageResponse(`{"text":"working"}`)))
	c.ObserveImagesForTest([]byte(geminiImageResponse(
		`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}},` +
			`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`)))
	// 收尾 chunk 只带 usageMetadata，不能把已数到的张数抹掉。
	c.ObserveImagesForTest([]byte(`{"usageMetadata":{"promptTokenCount":9}}`))

	require.Equal(t, 2, c.Images)
}

// failover 会拿同一个 gin.Context 重跑 Forward，计数器必须按次重置，
// 否则失败账号已经回吐的图会被叠加到成功账号的账单上。
func TestBeginGeminiImageOutputObservation_ResetsPerForward(t *testing.T) {
	c := newGeminiImageTestContext(t)
	oneImage := []byte(geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`))

	c.Images = 0
	c.ObserveImagesForTest(oneImage)
	require.Equal(t, 1, c.Images)

	c.Images = 0
	require.Equal(t, 0, c.Images)
	c.ObserveImagesForTest(oneImage)
	require.Equal(t, 1, c.Images)
}

// issue #5358：自定义模型名（客户端名与上游映射名都不在白名单里）走 Gemini 原生
// generateContent 生图，改动前 ImageCount 恒为 0，calculateRecordUsageCost 的按次
// 计费分支整条不触发，四次生图全部记 $0。
func TestResolveGeminiImageCount(t *testing.T) {
	oneImage := []byte(geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`))
	textOnly := []byte(geminiImageResponse(`{"text":"hello"}`))

	t.Run("custom model name bills by observed images", func(t *testing.T) {
		c := newGeminiImageTestContext(t)
		c.Images = 0
		c.ObserveImagesForTest(oneImage)

		require.False(t, antigravity.IsImageGenerationModel("nana-banana-2"), "前置条件：白名单判不出自定义名")
		require.Equal(t, 1, c.ImageCountForTest("nana-banana-2", "nana-banana-2"))
	})

	t.Run("falls back to requested model name", func(t *testing.T) {
		c := newGeminiImageTestContext(t)
		c.Images = 0
		c.ObserveImagesForTest(textOnly)

		require.Equal(t, 1, c.ImageCountForTest("gemini-3-pro-image-preview", "gemini-3-pro-image-preview"))
	})

	t.Run("falls back to mapped upstream model name", func(t *testing.T) {
		c := newGeminiImageTestContext(t)
		c.Images = 0
		c.ObserveImagesForTest(textOnly)

		require.Equal(t, 1, c.ImageCountForTest("my-image-alias", "gemini-2.5-flash-image"))
	})

	t.Run("text model stays unbilled", func(t *testing.T) {
		c := newGeminiImageTestContext(t)
		c.Images = 0
		c.ObserveImagesForTest(textOnly)

		require.Equal(t, 0, c.ImageCountForTest("gemini-2.5-pro", "gemini-2.5-pro"))
	})

	t.Run("no counter on context degrades to name heuristic", func(t *testing.T) {
		c := newGeminiImageTestContext(t)
		require.Equal(t, 0, c.ImageCountForTest("nana-banana-2", "nana-banana-2"))
		require.Equal(t, 1, c.ImageCountForTest("gemini-3-pro-image", "gemini-3-pro-image"))
	})
}

// 端到端守住接线：/v1beta/models/{model}:generateContent 的非流式响应体
// 必须真的喂进计数器，否则上面的单测全绿而线上依然记 $0。
func TestHandleNativeNonStreamingResponse_FeedsImageCounter(t *testing.T) {
	c := newGeminiImageTestContext(t)
	c.Images = 0

	body := geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`)
	resp := &http.Response{

		StatusCode: http.StatusOK,

		Header: http.Header{"Content-Type": []string{"application/json"}},

		Body: io.NopCloser(strings.NewReader(body)),
	}

	svc := newGeminiFixture(geminiDependencies{})
	usage, err := googleforward.GeminiResponseForTest(svc, c).HandleNativeNonStreamingResponse(upstream.NewOutputContext(c.Sink()), resp, false)
	require.NoError(t, err)
	require.NotNil(t, usage)

	require.Equal(t, 1, c.Images)
	require.Equal(t, 1, c.ImageCountForTest("nana-banana-2", "nana-banana-2"))
}
