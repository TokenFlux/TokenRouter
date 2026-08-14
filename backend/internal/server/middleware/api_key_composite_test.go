package middleware

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func compositeMiddlewareTestKey() *service.APIKey {
	group := &service.Group{
		ID: 7, Name: "OpenAI", Platform: service.PlatformOpenAI, Status: service.StatusActive, IsExclusive: true,
		AllowedClientProtocols: []service.GroupClientProtocol{
			service.GroupClientProtocolOpenAIResponses,
			service.GroupClientProtocolOpenAIChatCompletions,
		},
	}
	return &service.APIKey{
		ID: 1, UserID: 2, IsComposite: true, User: &service.User{ID: 2, Status: service.StatusActive},
		CompositeGroups: []service.APIKeyCompositeGroup{{GroupID: 7, Prefix: "GPT", NormalizedPrefix: "gpt", Group: group}},
	}
}

// compositeMiddlewareMultiGroupTestKey 构造可验证单请求跨分组模型拒绝行为的复合 Key。
func compositeMiddlewareMultiGroupTestKey() *service.APIKey {
	key := compositeMiddlewareTestKey()
	group := &service.Group{ID: 8, Name: "Claude", Platform: service.PlatformAnthropic, Status: service.StatusActive, IsExclusive: true}
	key.CompositeGroups = append(key.CompositeGroups, service.APIKeyCompositeGroup{
		GroupID: 8, Prefix: "Claude", NormalizedPrefix: "claude", Group: group,
	})
	return key
}

func TestResolveCompositeAPIKeyRequestJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gPt/vendor/model","messages":[]}`))
	c.Request.Header.Set("Content-Type", "application/json")

	apiKeyService := service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)
	selected, err := resolveCompositeAPIKeyRequest(c, apiKeyService, compositeMiddlewareTestKey())
	require.NoError(t, err)
	require.NotNil(t, selected.GroupID)
	require.Equal(t, int64(7), *selected.GroupID)
	// 后续路由门禁必须读取复合 Key 最终选中分组的协议策略。
	require.NotNil(t, selected.Group)
	require.True(t, selected.Group.AllowsClientProtocol(service.GroupClientProtocolOpenAIResponses))
	require.False(t, selected.Group.AllowsClientProtocol(service.GroupClientProtocolAnthropicMessages))
	body, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.Equal(t, "vendor/model", gjson.GetBytes(body, "model").String())
	clientModel, actualModel, ok := GetCompositeModelFromContext(c)
	require.True(t, ok)
	require.Equal(t, "gPt/vendor/model", clientModel)
	require.Equal(t, "vendor/model", actualModel)
}

func TestResolveCompositeAPIKeyRequestMultipart(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "GPT/gpt-image-1"))
	file, err := writer.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	_, err = file.Write([]byte("image-data"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	apiKeyService := service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)
	_, err = resolveCompositeAPIKeyRequest(c, apiKeyService, compositeMiddlewareTestKey())
	require.NoError(t, err)
	require.NoError(t, c.Request.ParseMultipartForm(1024))
	require.Equal(t, "gpt-image-1", c.Request.FormValue("model"))
	fileHeader := c.Request.MultipartForm.File["image"][0]
	opened, err := fileHeader.Open()
	require.NoError(t, err)
	// 确保测试结束前关闭 multipart 文件句柄，并校验关闭错误。
	defer func() {
		require.NoError(t, opened.Close())
	}()
	content, err := io.ReadAll(opened)
	require.NoError(t, err)
	require.Equal(t, []byte("image-data"), content)
}

func TestResolveCompositeAPIKeyRequestGeminiURL(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/GPT/vendor/model:generateContent", bytes.NewBufferString(`{}`))
	c.Params = gin.Params{{Key: "modelAction", Value: "/GPT/vendor/model:generateContent"}}
	apiKeyService := service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)
	selected, err := resolveCompositeAPIKeyRequest(c, apiKeyService, compositeMiddlewareTestKey())
	require.NoError(t, err)
	require.Equal(t, int64(7), *selected.GroupID)
	require.Equal(t, "/vendor/model:generateContent", c.Param("modelAction"))
}

func TestResolveCompositeAPIKeyRequestAdditionalModels(t *testing.T) {
	apiKeyService := service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)

	t.Run("rewrites additional model from selected group", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(
			`{"model":"GPT/gpt-5","tools":[{"type":"image_generation","model":"gpt/gpt-image-2"}]}`,
		))
		c.Request.Header.Set("Content-Type", "application/json")

		_, err := resolveCompositeAPIKeyRequest(c, apiKeyService, compositeMiddlewareMultiGroupTestKey())
		require.NoError(t, err)
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, "gpt-5", gjson.GetBytes(body, "model").String())
		require.Equal(t, "gpt-image-2", gjson.GetBytes(body, "tools.0.model").String())
	})

	t.Run("rejects additional model from another group", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(
			`{"model":"GPT/gpt-5","tools":[{"type":"image_generation","model":"Claude/claude-image"}]}`,
		))
		c.Request.Header.Set("Content-Type", "application/json")

		_, err := resolveCompositeAPIKeyRequest(c, apiKeyService, compositeMiddlewareMultiGroupTestKey())
		require.ErrorIs(t, err, service.ErrCompositeKeyUnsupported)
	})
}

func TestResolveCompositeAPIKeyRequestSpecialEndpoints(t *testing.T) {
	apiKeyService := service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)

	listContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	listContext.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	selected, err := resolveCompositeAPIKeyRequest(listContext, apiKeyService, compositeMiddlewareTestKey())
	require.NoError(t, err)
	require.Nil(t, selected.GroupID)
	_, marked := listContext.Get(compositeKeyNoGroupContextKey)
	require.True(t, marked)
	require.False(t, isCompositeKeyBillingBypassEndpoint(http.MethodGet, "/v1/models"))
	require.False(t, isCompositeKeyBillingBypassEndpoint(http.MethodGet, "/v1/images/batches/models"))

	videoContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	videoContext.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/video-123/content", nil)
	selected, err = resolveCompositeAPIKeyRequest(videoContext, apiKeyService, compositeMiddlewareTestKey())
	require.NoError(t, err)
	require.Nil(t, selected.GroupID)
	require.True(t, isCompositeKeyBillingBypassEndpoint(http.MethodGet, "/v1/videos/video-123/content"))
	require.True(t, isCompositeKeyBillingBypassEndpoint(http.MethodGet, "/videos/video-123"))
	require.True(t, isCompositeKeyBillingBypassEndpoint(http.MethodGet, "/videos/video-123/content"))

	antigravityUsageContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	antigravityUsageContext.Request = httptest.NewRequest(http.MethodGet, "/antigravity/v1/usage", nil)
	selected, err = resolveCompositeAPIKeyRequest(antigravityUsageContext, apiKeyService, compositeMiddlewareTestKey())
	require.NoError(t, err)
	require.Nil(t, selected.GroupID)

	realtimeContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	realtimeContext.Request = httptest.NewRequest(http.MethodPost, "/v1/live", nil)
	_, err = resolveCompositeAPIKeyRequest(realtimeContext, apiKeyService, compositeMiddlewareTestKey())
	require.ErrorIs(t, err, service.ErrCompositeKeyUnsupported)

	require.True(t, isCompositeKeyUnsupportedEndpoint(
		http.MethodGet,
		"/backend-api/codex/call_123",
		"/backend-api/codex/:call_id",
	))
	require.False(t, isCompositeKeyUnsupportedEndpoint(
		http.MethodPost,
		"/backend-api/codex/responses",
		"/backend-api/codex/responses",
	))
}

func TestReplaceCompositeResponseModel(t *testing.T) {
	response := []byte("data: {\"id\":\"gpt-5\",\"model\":\"gpt-5\",\"modelVersion\":\"gpt-5\",\"model_version\":\"gpt-5\",\"name\":\"models/gpt-5\",\"text\":\"gpt-5\"}\n\n")

	// 只恢复协议中的模型标识，正文里相同的普通文本必须保持不变。
	rewritten := replaceCompositeResponseModel(response, "gpt-5", "GPT/gpt-5")
	require.Contains(t, string(rewritten), `"id":"GPT/gpt-5"`)
	require.Contains(t, string(rewritten), `"model":"GPT/gpt-5"`)
	require.Contains(t, string(rewritten), `"modelVersion":"GPT/gpt-5"`)
	require.Contains(t, string(rewritten), `"model_version":"GPT/gpt-5"`)
	require.Contains(t, string(rewritten), `"name":"models/GPT/gpt-5"`)
	require.Contains(t, string(rewritten), `"text":"gpt-5"`)

	// 模型名允许普通字符串字符，恢复时必须保持 JSON 转义有效。
	escaped := replaceCompositeResponseModel([]byte(`{"model":"safe-target","text":"safe-target"}`), "safe-target", `review"alias`)
	require.JSONEq(t, `{"model":"review\"alias","text":"safe-target"}`, string(escaped))
}

func TestAbortCompositeKeyErrorPreservesProtocolShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	openAIRecorder := httptest.NewRecorder()
	openAIContext, _ := gin.CreateTestContext(openAIRecorder)
	openAIContext.Request = httptest.NewRequest(http.MethodPost, "/v1/live", nil)
	abortCompositeKeyError(openAIContext, service.ErrCompositeKeyUnsupported)
	require.Equal(t, http.StatusBadRequest, openAIRecorder.Code)
	require.Equal(t, "COMPOSITE_KEY_ENDPOINT_UNSUPPORTED", gjson.Get(openAIRecorder.Body.String(), "error.code").String())

	anthropicRecorder := httptest.NewRecorder()
	anthropicContext, _ := gin.CreateTestContext(anthropicRecorder)
	anthropicContext.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	abortCompositeKeyError(anthropicContext, service.ErrCompositeKeyPrefixRequired)
	require.Equal(t, http.StatusBadRequest, anthropicRecorder.Code)
	require.Equal(t, "error", gjson.Get(anthropicRecorder.Body.String(), "type").String())
	require.Equal(t, "COMPOSITE_KEY_MODEL_PREFIX_REQUIRED", gjson.Get(anthropicRecorder.Body.String(), "error.code").String())
}
