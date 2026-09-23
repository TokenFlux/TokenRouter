package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSanitizedUpstreamPathSuffixRejectsNonConformingSegments(t *testing.T) {
	// 到达业务代码的 URL.Path 已是百分号解码后的结果，因此用例按解码后的形态书写。
	rejected := []string{
		"/..",
		"/../..",
		"/../../x/y",
		"/./compact",
		"/compact/..",
		`/..\..\x`,
		`/compact\..`,
		"/?a=b",
		"/compact?a=b",
		"/compact#frag",
		"/compact%2f..",
		"/100%",
		"//double",
		"/compact//detail",
		"/compact/",
		"/ compact",
		"/compact\x00",
		"/compact\nX-Injected: 1",
		"/模型",
		"compact",
		"/a:b",
		"/a;b",
		"/a,b",
		"/a=b",
		"/a&b",
		// 允许清单是闭集：`\w` + `-` + `.` 以外的字符一律拒绝，
		// 不依赖任何"已知坏字符"清单。
		"/a~b",
		"/a@b",
		"/a+b",
		"/a|b",
		"/a*b",
		"/a$b",
		"/a(b)",
		"/a'b",
		"/a\"b",
		"/a<b",
		"/a\tb",
		"/a b",
		"/a∕b", // DIVISION SLASH
		"/a／b", // FULLWIDTH SOLIDUS
		// 只由点组成的片段一律拒绝（各实现对其解释不一致）。
		"/...",
		"/....",
		"/compact/...",
	}
	for _, suffix := range rejected {
		t.Run("reject_"+suffix, func(t *testing.T) {
			got, ok := upstream.SanitizedUpstreamPathSuffix(suffix)
			require.False(t, ok, "suffix %q must be rejected", suffix)
			require.Empty(t, got)
		})
	}

	accepted := map[string]string{
		"":                          "",
		"/compact":                  "/compact",
		"/compact/detail":           "/compact/detail",
		"/resp_68f0a1b2c3d4/cancel": "/resp_68f0a1b2c3d4/cancel",
		"/gemini-2.5-pro_v1.2":      "/gemini-2.5-pro_v1.2",
		"/a.b.c":                    "/a.b.c",
	}
	for suffix, want := range accepted {
		t.Run("accept_"+suffix, func(t *testing.T) {
			got, ok := upstream.SanitizedUpstreamPathSuffix(suffix)
			require.True(t, ok, "suffix %q must be accepted", suffix)
			require.Equal(t, want, got)
		})
	}
}

func TestSanitizedUpstreamPathSuffixEnforcesBounds(t *testing.T) {
	longSegment := "/"
	for i := 0; i < upstream.MaxUpstreamPathSegmentLen+1; i++ {
		longSegment += "a"
	}
	_, ok := upstream.SanitizedUpstreamPathSuffix(longSegment)
	require.False(t, ok, "over-long segment must be rejected")

	deep := ""
	for i := 0; i <= upstream.MaxUpstreamPathSegments; i++ {
		deep += "/a"
	}
	_, ok = upstream.SanitizedUpstreamPathSuffix(deep)
	require.False(t, ok, "over-deep suffix must be rejected")
}

// TestOpenAIResponsesRequestPathSuffixRejectsNonConformingSubpaths 锁定不变式：
// /responses/*subpath 的子路径不得改变上游请求的路径结构；不合规时既不参与拼接，
// 也不会被误判成 compact 请求。
func TestOpenAIResponsesRequestPathSuffixRejectsNonConformingSubpaths(t *testing.T) {

	nonConformingPaths := []string{
		"/v1/responses/../../x/y",
		"/v1/responses/..%2f..%2fx/y",
		"/v1/responses/%2e%2e/%2e%2e/x",
		"/responses/%2e%2e%2fx",
		"/backend-api/codex/responses/../../../x",
		`/v1/responses/..\..\x`,
		"/v1/responses/%3fa=b",
		"/v1/responses/x%23frag",
		"/v1/responses//double",
	}
	for _, path := range nonConformingPaths {
		t.Run(path, func(t *testing.T) {
			c := newResponsesSuffixTestContext(t, path)

			require.False(t, httpapi.IsForwardableOpenAIResponsesRequestPath(c),
				"path %q must be rejected at the gateway edge", path)
			require.Empty(t, httpapi.OpenAIResponsesRequestPathSuffix(c),
				"path %q must never contribute an upstream path suffix", path)
			require.Equal(t, chatgptCodexURL,
				appendOpenAIResponsesRequestPathSuffix(chatgptCodexURL, httpapi.OpenAIResponsesRequestPathSuffix(c)))
			require.False(t, httpapi.IsOpenAIResponsesCompactPath(c))
		})
	}

	// 合法子路径必须保持原样转发。
	for path, want := range map[string]string{
		"/v1/responses":                        "",
		"/v1/responses/input_tokens":           "/input_tokens",
		"/v1/responses/compact":                "/compact",
		"/responses/compact/":                  "/compact",
		"/backend-api/codex/responses/compact": "/compact",
	} {
		t.Run("forwardable_"+path, func(t *testing.T) {
			c := newResponsesSuffixTestContext(t, path)
			require.True(t, httpapi.IsForwardableOpenAIResponsesRequestPath(c))
			require.Equal(t, want, httpapi.OpenAIResponsesRequestPathSuffix(c))
		})
	}
}

func TestIsOpenAIResponsesInputTokensRequestPath(t *testing.T) {
	for _, path := range []string{"/v1/responses/input_tokens", "/responses/input_tokens", "/backend-api/codex/responses/input_tokens"} {
		c := newResponsesSuffixTestContext(t, path)
		require.True(t, httpapi.IsOpenAIResponsesInputTokensRequestPath(c), "path=%s", path)
	}
	c := newResponsesSuffixTestContext(t, "/v1/responses/compact")
	require.False(t, httpapi.IsOpenAIResponsesInputTokensRequestPath(c))
}

func TestAppendOpenAIResponsesRequestPathSuffixRefusesUnsafeSuffix(t *testing.T) {
	// 调用方漏了校验时，拼接函数本身也不得把不合规片段带进上游 URL。
	require.Equal(t, chatgptCodexURL, appendOpenAIResponsesRequestPathSuffix(chatgptCodexURL, "/../../x"))
	require.Equal(t, chatgptCodexURL, appendOpenAIResponsesRequestPathSuffix(chatgptCodexURL, "/?a=b"))
	require.Equal(t, chatgptCodexURL+"/compact", appendOpenAIResponsesRequestPathSuffix(chatgptCodexURL, "/compact"))
}

func newResponsesSuffixTestContext(t *testing.T, path string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c
}
