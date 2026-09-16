package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const CompositeKeyNoGroupContextKey = "composite_key_no_group"

// ResolveCompositeAPIKeyRequest 根据客户端模型选择复合 Key 分组，并改写为真实模型。
// @project-doc docs/domains/composite_api_keys.md#group_selection
func ResolveCompositeAPIKeyRequest(c *gin.Context, apiKeyService *apikey.APIKeyService, apiKey *apikey.APIKey) (*apikey.APIKey, error) {
	if apiKey == nil || !apiKey.IsComposite {
		return apiKey, nil
	}
	if IsCompositeKeyUnsupportedEndpoint(c.Request.Method, c.Request.URL.Path, c.FullPath()) {
		return nil, apikey.ErrCompositeKeyUnsupported
	}
	if IsCompositeKeyNoModelEndpoint(c.Request.Method, c.Request.URL.Path) {
		c.Set(CompositeKeyNoGroupContextKey, true)
		return apiKey, nil
	}

	var originalModel string
	var actualModel string
	var binding *apikey.APIKeyCompositeGroup
	var err error
	if IsGeminiNativeModelEndpoint(c.Request.URL.Path) {
		originalModel, err = CompositeGeminiModelFromParams(c)
		if err == nil {
			binding, actualModel, err = apiKey.ResolveCompositeModel(originalModel)
		}
		if err == nil {
			RewriteCompositeGeminiParams(c, actualModel)
		}
	} else {
		originalModel, err = CompositeModelFromRequest(c.Request)
		if err == nil {
			binding, actualModel, err = apiKey.ResolveCompositeModel(originalModel)
		}
		if err == nil {
			err = RewriteCompositeAdditionalModels(c.Request, apiKey, binding)
		}
		if err == nil {
			err = RewriteCompositeRequestModel(c.Request, actualModel)
		}
	}
	if err != nil {
		return nil, err
	}

	selected, err := apiKeyService.SelectCompositeGroupForRequest(c.Request.Context(), apiKey, binding)
	if err != nil {
		return nil, err
	}
	SetCompositeModelContext(c, originalModel, actualModel)
	return selected, nil
}

// SetCompositeModelContext 记录客户端模型和内部真实模型，供日志及响应恢复使用。
func SetCompositeModelContext(c *gin.Context, clientModel, actualModel string) {
	if c == nil {
		return
	}
	c.Set("composite_client_model", clientModel)
	c.Set("composite_actual_model", actualModel)
	c.Writer = &CompositeModelResponseWriter{
		ResponseWriter: c.Writer,
		clientModel:    clientModel,
		actualModel:    actualModel,
	}
	if c.Request != nil {
		ctx := context.WithValue(c.Request.Context(), telemetry.ClientModel, clientModel)
		c.Request = c.Request.WithContext(ctx)
	}
}

// CompositeModelResponseWriter 将常见协议响应中的真实模型恢复为客户端复合模型。
type CompositeModelResponseWriter struct {
	gin.ResponseWriter
	clientModel string
	actualModel string
}

func (w *CompositeModelResponseWriter) Write(data []byte) (int, error) {
	w.Header().Del("Content-Length")
	rewritten := ReplaceCompositeResponseModel(data, w.actualModel, w.clientModel)
	_, err := w.ResponseWriter.Write(rewritten)
	return len(data), err
}

func (w *CompositeModelResponseWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

// ReplaceCompositeResponseModel 只改写模型字段，避免影响正文中恰好相同的文本。
func ReplaceCompositeResponseModel(data []byte, actualModel, clientModel string) []byte {
	return modeltrace.ReplaceModelMetadata(data, actualModel, clientModel)
}

// GetCompositeModelFromContext 返回复合 Key 的客户端模型与真实模型。
func GetCompositeModelFromContext(c *gin.Context) (clientModel, actualModel string, ok bool) {
	if c == nil {
		return "", "", false
	}
	clientModel = c.GetString("composite_client_model")
	actualModel = c.GetString("composite_actual_model")
	return clientModel, actualModel, clientModel != "" && actualModel != ""
}

// IsCompositeKeyNoModelEndpoint 识别仅按 Key 身份工作或聚合全部映射的入口。
func IsCompositeKeyNoModelEndpoint(method, path string) bool {
	if IsCompositeKeyModelListEndpoint(method, path) || IsAPIKeyUsageRequest(method, path) {
		return true
	}
	return IsBatchImageBillingBypassRequest(method, path) || IsGrokVideoTaskRead(method, path)
}

// IsCompositeKeyModelListEndpoint 识别复合 Key 需要聚合映射的模型列表入口。
func IsCompositeKeyModelListEndpoint(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	switch strings.TrimSuffix(path, "/") {
	case "/v1/models", "/models", "/v1beta/models", "/antigravity/models", "/antigravity/v1/models", "/antigravity/v1beta/models", "/v1/images/batches/models":
		return true
	default:
		return false
	}
}

// IsCompositeKeyBillingBypassEndpoint 仅识别按 Key 身份读取既有数据的入口。
// 模型列表虽然不需要选择分组，但仍必须执行 Key 额度、余额和订阅校验。
func IsCompositeKeyBillingBypassEndpoint(method, path string) bool {
	if IsAPIKeyUsageRequest(method, path) {
		return true
	}
	return IsBatchImageBillingBypassRequest(method, path) || IsGrokVideoTaskRead(method, path)
}

// IsGrokVideoTaskRead 识别不携带模型、仅通过任务归属查询的 Grok 视频入口。
func IsGrokVideoTaskRead(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	cleanPath := strings.TrimSuffix(path, "/")
	return strings.HasPrefix(cleanPath, "/v1/videos/") || strings.HasPrefix(cleanPath, "/videos/")
}

// IsCompositeKeyUnsupportedEndpoint 识别一个连接可能携带多模型的实时入口。
func IsCompositeKeyUnsupportedEndpoint(method, path, routePath string) bool {
	cleanPath := strings.TrimSuffix(path, "/")
	if cleanPath == "/v1/live" || cleanPath == "/backend-api/codex/realtime/calls" {
		return true
	}
	// Codex Live sideband 与普通 Codex HTTP 入口共享前缀，只能通过已匹配的路由模板区分。
	if method == http.MethodGet && routePath == "/backend-api/codex/:call_id" {
		return true
	}
	if method == http.MethodGet && (strings.HasPrefix(cleanPath, "/v1/live/") || cleanPath == "/v1/responses" || cleanPath == "/responses" || cleanPath == "/backend-api/codex/responses") {
		return true
	}
	return false
}

func IsGeminiNativeModelEndpoint(path string) bool {
	return strings.Contains(path, "/v1beta/models/")
}

// CompositeGeminiModelFromParams 从 Gemini URL 提取带前缀模型。
func CompositeGeminiModelFromParams(c *gin.Context) (string, error) {
	if c.Request.Method == http.MethodGet {
		model := strings.TrimPrefix(strings.TrimSpace(c.Param("model")), "/")
		if model == "" {
			return "", apikey.ErrCompositeKeyPrefixRequired
		}
		return model, nil
	}
	if prefix := strings.TrimSpace(c.Param("prefix")); prefix != "" {
		model := strings.TrimPrefix(strings.TrimSpace(c.Param("model")), "/")
		if model == "" {
			return "", apikey.ErrCompositeKeyPrefixRequired
		}
		return prefix + "/" + model, nil
	}
	modelAction := strings.TrimPrefix(strings.TrimSpace(c.Param("modelAction")), "/")
	separator := strings.LastIndex(modelAction, ":")
	if separator <= 0 {
		return "", apikey.ErrCompositeKeyPrefixRequired
	}
	return modelAction[:separator], nil
}

// RewriteCompositeGeminiParams 同步更新 Gin 参数，让现有处理器只看到真实模型。
func RewriteCompositeGeminiParams(c *gin.Context, actualModel string) {
	for i := range c.Params {
		switch c.Params[i].Key {
		case "model":
			c.Params[i].Value = actualModel
		case "modelAction":
			value := strings.TrimPrefix(c.Params[i].Value, "/")
			if separator := strings.LastIndex(value, ":"); separator > 0 {
				c.Params[i].Value = "/" + actualModel + value[separator:]
			}
		}
	}
}

// CompositeModelFromRequest 读取 JSON 或 multipart 请求的顶层 model。
func CompositeModelFromRequest(request *http.Request) (string, error) {
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		return MultipartModel(request)
	}
	body, err := ReadAndRestoreRequestBody(request)
	if err != nil {
		return "", err
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		return "", apikey.ErrCompositeKeyPrefixRequired
	}
	return model, nil
}

func RewriteCompositeRequestModel(request *http.Request, actualModel string) error {
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		return RewriteMultipartModel(request, actualModel)
	}
	body, err := ReadAndRestoreRequestBody(request)
	if err != nil {
		return err
	}
	rewritten, err := sjson.SetBytes(body, "model", actualModel)
	if err != nil {
		return apikey.ErrCompositeKeyPrefixRequired
	}
	SetRequestBody(request, rewritten)
	return nil
}

// RewriteCompositeAdditionalModels 处理 Responses 工具中的附加模型。
// 同一分组的前缀会被剥离；跨分组模型会使一次请求需要多套路由，因此明确拒绝。
func RewriteCompositeAdditionalModels(request *http.Request, apiKey *apikey.APIKey, selected *apikey.APIKeyCompositeGroup) error {
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") || apiKey == nil || selected == nil {
		return nil
	}
	body, err := ReadAndRestoreRequestBody(request)
	if err != nil {
		return err
	}
	rewritten := body
	for index, tool := range gjson.GetBytes(body, "tools").Array() {
		model := strings.TrimSpace(tool.Get("model").String())
		if model == "" {
			continue
		}
		binding, actualModel, resolveErr := apiKey.ResolveCompositeModel(model)
		if resolveErr != nil {
			// 附加模型本身可能合法地包含斜杠；只有命中已配置前缀时才参与复合路由。
			continue
		}
		if binding.GroupID != selected.GroupID {
			return apikey.ErrCompositeKeyUnsupported
		}
		rewritten, err = sjson.SetBytes(rewritten, fmt.Sprintf("tools.%d.model", index), actualModel)
		if err != nil {
			return apikey.ErrCompositeKeyUnsupported
		}
	}
	SetRequestBody(request, rewritten)
	return nil
}

func ReadAndRestoreRequestBody(request *http.Request) ([]byte, error) {
	if request == nil || request.Body == nil {
		return nil, apikey.ErrCompositeKeyPrefixRequired
	}
	rawBody, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	SetRequestBody(request, rawBody)

	encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding")))
	if encoding == "" || encoding == "identity" {
		return rawBody, nil
	}

	// 使用临时请求解压，失败时原请求仍保留完整压缩体，便于后续处理器返回原有错误。
	decodeRequest := request.Clone(request.Context())
	decodeRequest.Body = io.NopCloser(bytes.NewReader(rawBody))
	decodeRequest.ContentLength = int64(len(rawBody))
	decodedBody, err := ReadRequestBodyWithPrealloc(decodeRequest)
	if err != nil {
		return nil, err
	}
	request.Header.Del("Content-Encoding")
	SetRequestBody(request, decodedBody)
	return decodedBody, nil
}

func SetRequestBody(request *http.Request, body []byte) {
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
}

func MultipartModel(request *http.Request) (string, error) {
	body, err := ReadAndRestoreRequestBody(request)
	if err != nil {
		return "", err
	}
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return "", apikey.ErrCompositeKeyPrefixRequired
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return "", nextErr
		}
		if part.FormName() == "model" {
			value, readErr := io.ReadAll(part)
			if readErr != nil {
				return "", readErr
			}
			model := strings.TrimSpace(string(value))
			if model == "" {
				return "", apikey.ErrCompositeKeyPrefixRequired
			}
			return model, nil
		}
	}
	return "", apikey.ErrCompositeKeyPrefixRequired
}

func RewriteMultipartModel(request *http.Request, actualModel string) error {
	body, err := ReadAndRestoreRequestBody(request)
	if err != nil {
		return err
	}
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return apikey.ErrCompositeKeyPrefixRequired
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	found := false
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nextErr
		}
		target, createErr := writer.CreatePart(part.Header)
		if createErr != nil {
			return createErr
		}
		if part.FormName() == "model" {
			_, err = io.WriteString(target, actualModel)
			found = true
		} else {
			_, err = io.Copy(target, part)
		}
		if err != nil {
			return err
		}
	}
	if !found {
		return apikey.ErrCompositeKeyPrefixRequired
	}
	if err := writer.Close(); err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	SetRequestBody(request, output.Bytes())
	return nil
}

// AbortCompositeKeyError 按通用网关格式输出结构化复合 Key 错误。
func AbortCompositeKeyError(c *gin.Context, err error) {
	status := httpx.ErrorCode(err)
	code := infraerrors.Reason(err)
	message := infraerrors.Message(err)
	if IsOpenAICompositeEndpoint(c.Request.URL.Path) {
		c.JSON(status, gin.H{"error": gin.H{
			"message": message, "type": "invalid_request_error", "param": "model", "code": code,
		}})
		c.Abort()
		return
	}
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": "invalid_request_error", "message": message, "code": code},
	})
	c.Abort()
}

func IsOpenAICompositeEndpoint(path string) bool {
	return strings.Contains(path, "/chat/completions") || strings.Contains(path, "/responses") ||
		strings.Contains(path, "/embeddings") || strings.Contains(path, "/images/") ||
		strings.Contains(path, "/videos/") || strings.Contains(path, "/alpha/search") ||
		strings.Contains(path, "/live") || strings.Contains(path, "/realtime/") ||
		strings.HasPrefix(path, "/backend-api/codex/")
}

// AbortCompositeKeyGoogleError 按 Google 协议格式输出复合 Key 错误。
func AbortCompositeKeyGoogleError(c *gin.Context, err error) {
	keyhttp.AbortGoogleError(c, httpx.ErrorCode(err), infraerrors.Reason(err)+": "+infraerrors.Message(err))
}
