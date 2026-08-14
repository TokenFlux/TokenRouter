package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestApplyAPIKeyModelRedirectRewritesJSONAndRestoresMetadataOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"codex-auto-review"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	applyAPIKeyModelRedirect(c, &service.APIKey{ModelMapping: map[string]string{
		"codex-auto-review": "gpt-5.6-luna",
	}})

	body, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-luna", gjson.GetBytes(body, "model").String())
	require.Equal(t, "codex-auto-review", c.Request.Context().Value(ctxkey.ClientModel))

	_, err = c.Writer.Write([]byte(`{"model":"upstream-luna","output_text":"upstream-luna"}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"codex-auto-review","output_text":"upstream-luna"}`, recorder.Body.String())
}

func TestApplyAPIKeyModelRedirectDiscoversStreamingUpstreamModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"review"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	applyAPIKeyModelRedirect(c, &service.APIKey{ModelMapping: map[string]string{"review": "key-target"}})

	_, err := c.Writer.WriteString("data: {\"type\":\"response.completed\",\"response\":{\"model\":\"upstream-target\",\"output_text\":\"upstream-target\"}}\n\n")
	require.NoError(t, err)
	require.Contains(t, recorder.Body.String(), `"model":"review"`)
	require.Contains(t, recorder.Body.String(), `"output_text":"upstream-target"`)
}

func TestApplyAPIKeyModelRedirectRewritesCompressedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write([]byte(`{"model":"codex-auto-review","tools":[{"model":"tool-alias"}]}`))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(compressed.Bytes()))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Content-Encoding", "gzip")
	applyAPIKeyModelRedirect(c, &service.APIKey{ModelMapping: map[string]string{
		"codex-auto-review": "gpt-5.6-luna",
		"tool-alias":        "tool-target",
	}})

	body, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.Empty(t, c.Request.Header.Get("Content-Encoding"))
	require.Equal(t, "gpt-5.6-luna", gjson.GetBytes(body, "model").String())
	require.Equal(t, "tool-target", gjson.GetBytes(body, "tools.0.model").String())
}

func TestApplyAPIKeyModelRedirectRewritesMultipartModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "image-alias"))
	require.NoError(t, writer.WriteField("prompt", "keep image-alias in text"))
	require.NoError(t, writer.Close())

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	applyAPIKeyModelRedirect(c, &service.APIKey{ModelMapping: map[string]string{"image-alias": "gpt-image-1"}})

	require.NoError(t, c.Request.ParseMultipartForm(1<<20))
	require.Equal(t, "gpt-image-1", c.Request.FormValue("model"))
	require.Equal(t, "keep image-alias in text", c.Request.FormValue("prompt"))
}

func TestApplyAPIKeyModelRedirectRewritesGeminiURLModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-alias:generateContent", nil)
	c.Params = gin.Params{{Key: "modelAction", Value: "/gemini-alias:generateContent"}}

	applyAPIKeyModelRedirect(c, &service.APIKey{ModelMapping: map[string]string{"gemini-alias": "gemini-3.1-pro-preview"}})

	require.Equal(t, "/gemini-3.1-pro-preview:generateContent", c.Param("modelAction"))
}

func TestApplyAPIKeyModelRedirectRewritesAdditionalToolModelsWithoutMainMatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"main-model","tools":[{"type":"namespace","model":"tool-alias"}]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	applyAPIKeyModelRedirect(c, &service.APIKey{ModelMapping: map[string]string{"tool-alias": "tool-target"}})
	body, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.Equal(t, "main-model", gjson.GetBytes(body, "model").String())
	require.Equal(t, "tool-target", gjson.GetBytes(body, "tools.0.model").String())
}

func TestApplyAPIKeyModelRedirectComposesWithCompositeResponseRestore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"review"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	SetCompositeModelContext(c, "GPT/review", "review")

	applyAPIKeyModelRedirect(c, &service.APIKey{ModelMapping: map[string]string{"review": "gpt-5.6-luna"}})
	_, err := c.Writer.Write([]byte(`{"model":"gpt-5.6-luna"}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"GPT/review"}`, recorder.Body.String())
}
