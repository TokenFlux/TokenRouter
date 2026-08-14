package middleware

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

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	pkghttputil "github.com/BrandonVee/TokenRouter/internal/pkg/httputil"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const compositeKeyNoGroupContextKey = "composite_key_no_group"

// resolveCompositeAPIKeyRequest 根据客户端模型选择复合 Key 分组，并改写为真实模型。
// @project-doc docs/domains/composite_api_keys.md#group_selection
func resolveCompositeAPIKeyRequest(c *gin.Context, apiKeyService *service.APIKeyService, apiKey *service.APIKey) (*service.APIKey, error) {
	if apiKey == nil || !apiKey.IsComposite {
		return apiKey, nil
	}
	if isCompositeKeyUnsupportedEndpoint(c.Request.Method, c.Request.URL.Path, c.FullPath()) {
		return nil, service.ErrCompositeKeyUnsupported
	}
	if isCompositeKeyNoModelEndpoint(c.Request.Method, c.Request.URL.Path) {
		c.Set(compositeKeyNoGroupContextKey, true)
		return apiKey, nil
	}

	var originalModel string
	var actualModel string
	var binding *service.APIKeyCompositeGroup
	var err error
	if isGeminiNativeModelEndpoint(c.Request.URL.Path) {
		originalModel, err = compositeGeminiModelFromParams(c)
		if err == nil {
			binding, actualModel, err = apiKey.ResolveCompositeModel(originalModel)
		}
		if err == nil {
			rewriteCompositeGeminiParams(c, actualModel)
		}
	} else {
		originalModel, err = compositeModelFromRequest(c.Request)
		if err == nil {
			binding, actualModel, err = apiKey.ResolveCompositeModel(originalModel)
		}
		if err == nil {
			err = rewriteCompositeAdditionalModels(c.Request, apiKey, binding)
		}
		if err == nil {
			err = rewriteCompositeRequestModel(c.Request, actualModel)
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
	c.Writer = &compositeModelResponseWriter{
		ResponseWriter: c.Writer,
		clientModel:    clientModel,
		actualModel:    actualModel,
	}
	if c.Request != nil {
		ctx := context.WithValue(c.Request.Context(), ctxkey.ClientModel, clientModel)
		c.Request = c.Request.WithContext(ctx)
	}
}

// compositeModelResponseWriter 将常见协议响应中的真实模型恢复为客户端复合模型。
type compositeModelResponseWriter struct {
	gin.ResponseWriter
	clientModel string
	actualModel string
}

func (w *compositeModelResponseWriter) Write(data []byte) (int, error) {
	w.Header().Del("Content-Length")
	rewritten := replaceCompositeResponseModel(data, w.actualModel, w.clientModel)
	_, err := w.ResponseWriter.Write(rewritten)
	return len(data), err
}

func (w *compositeModelResponseWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

// replaceCompositeResponseModel 只改写模型字段，避免影响正文中恰好相同的文本。
func replaceCompositeResponseModel(data []byte, actualModel, clientModel string) []byte {
	return service.ReplaceModelMetadata(data, actualModel, clientModel)
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

// isCompositeKeyNoModelEndpoint 识别仅按 Key 身份工作或聚合全部映射的入口。
func isCompositeKeyNoModelEndpoint(method, path string) bool {
	if isCompositeKeyModelListEndpoint(method, path) || isAPIKeyUsageRequest(method, path) {
		return true
	}
	return isBatchImageBillingBypassRequest(method, path) || isGrokVideoTaskRead(method, path)
}

// isCompositeKeyModelListEndpoint 识别复合 Key 需要聚合映射的模型列表入口。
func isCompositeKeyModelListEndpoint(method, path string) bool {
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

// isCompositeKeyBillingBypassEndpoint 仅识别按 Key 身份读取既有数据的入口。
// 模型列表虽然不需要选择分组，但仍必须执行 Key 额度、余额和订阅校验。
func isCompositeKeyBillingBypassEndpoint(method, path string) bool {
	if isAPIKeyUsageRequest(method, path) {
		return true
	}
	return isBatchImageBillingBypassRequest(method, path) || isGrokVideoTaskRead(method, path)
}

// isGrokVideoTaskRead 识别不携带模型、仅通过任务归属查询的 Grok 视频入口。
func isGrokVideoTaskRead(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	cleanPath := strings.TrimSuffix(path, "/")
	return strings.HasPrefix(cleanPath, "/v1/videos/") || strings.HasPrefix(cleanPath, "/videos/")
}

// isCompositeKeyUnsupportedEndpoint 识别一个连接可能携带多模型的实时入口。
func isCompositeKeyUnsupportedEndpoint(method, path, routePath string) bool {
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

func isGeminiNativeModelEndpoint(path string) bool {
	return strings.Contains(path, "/v1beta/models/")
}

// compositeGeminiModelFromParams 从 Gemini URL 提取带前缀模型。
func compositeGeminiModelFromParams(c *gin.Context) (string, error) {
	if c.Request.Method == http.MethodGet {
		model := strings.TrimPrefix(strings.TrimSpace(c.Param("model")), "/")
		if model == "" {
			return "", service.ErrCompositeKeyPrefixRequired
		}
		return model, nil
	}
	if prefix := strings.TrimSpace(c.Param("prefix")); prefix != "" {
		model := strings.TrimPrefix(strings.TrimSpace(c.Param("model")), "/")
		if model == "" {
			return "", service.ErrCompositeKeyPrefixRequired
		}
		return prefix + "/" + model, nil
	}
	modelAction := strings.TrimPrefix(strings.TrimSpace(c.Param("modelAction")), "/")
	separator := strings.LastIndex(modelAction, ":")
	if separator <= 0 {
		return "", service.ErrCompositeKeyPrefixRequired
	}
	return modelAction[:separator], nil
}

// rewriteCompositeGeminiParams 同步更新 Gin 参数，让现有处理器只看到真实模型。
func rewriteCompositeGeminiParams(c *gin.Context, actualModel string) {
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

// compositeModelFromRequest 读取 JSON 或 multipart 请求的顶层 model。
func compositeModelFromRequest(request *http.Request) (string, error) {
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		return multipartModel(request)
	}
	body, err := readAndRestoreRequestBody(request)
	if err != nil {
		return "", err
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		return "", service.ErrCompositeKeyPrefixRequired
	}
	return model, nil
}

func rewriteCompositeRequestModel(request *http.Request, actualModel string) error {
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		return rewriteMultipartModel(request, actualModel)
	}
	body, err := readAndRestoreRequestBody(request)
	if err != nil {
		return err
	}
	rewritten, err := sjson.SetBytes(body, "model", actualModel)
	if err != nil {
		return service.ErrCompositeKeyPrefixRequired
	}
	setRequestBody(request, rewritten)
	return nil
}

// rewriteCompositeAdditionalModels 处理 Responses 工具中的附加模型。
// 同一分组的前缀会被剥离；跨分组模型会使一次请求需要多套路由，因此明确拒绝。
func rewriteCompositeAdditionalModels(request *http.Request, apiKey *service.APIKey, selected *service.APIKeyCompositeGroup) error {
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") || apiKey == nil || selected == nil {
		return nil
	}
	body, err := readAndRestoreRequestBody(request)
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
			return service.ErrCompositeKeyUnsupported
		}
		rewritten, err = sjson.SetBytes(rewritten, fmt.Sprintf("tools.%d.model", index), actualModel)
		if err != nil {
			return service.ErrCompositeKeyUnsupported
		}
	}
	setRequestBody(request, rewritten)
	return nil
}

func readAndRestoreRequestBody(request *http.Request) ([]byte, error) {
	if request == nil || request.Body == nil {
		return nil, service.ErrCompositeKeyPrefixRequired
	}
	rawBody, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	setRequestBody(request, rawBody)

	encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding")))
	if encoding == "" || encoding == "identity" {
		return rawBody, nil
	}

	// 使用临时请求解压，失败时原请求仍保留完整压缩体，便于后续处理器返回原有错误。
	decodeRequest := request.Clone(request.Context())
	decodeRequest.Body = io.NopCloser(bytes.NewReader(rawBody))
	decodeRequest.ContentLength = int64(len(rawBody))
	decodedBody, err := pkghttputil.ReadRequestBodyWithPrealloc(decodeRequest)
	if err != nil {
		return nil, err
	}
	request.Header.Del("Content-Encoding")
	setRequestBody(request, decodedBody)
	return decodedBody, nil
}

func setRequestBody(request *http.Request, body []byte) {
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
}

func multipartModel(request *http.Request) (string, error) {
	body, err := readAndRestoreRequestBody(request)
	if err != nil {
		return "", err
	}
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return "", service.ErrCompositeKeyPrefixRequired
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
				return "", service.ErrCompositeKeyPrefixRequired
			}
			return model, nil
		}
	}
	return "", service.ErrCompositeKeyPrefixRequired
}

func rewriteMultipartModel(request *http.Request, actualModel string) error {
	body, err := readAndRestoreRequestBody(request)
	if err != nil {
		return err
	}
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return service.ErrCompositeKeyPrefixRequired
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
		return service.ErrCompositeKeyPrefixRequired
	}
	if err := writer.Close(); err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	setRequestBody(request, output.Bytes())
	return nil
}

// abortCompositeKeyError 按通用网关格式输出结构化复合 Key 错误。
func abortCompositeKeyError(c *gin.Context, err error) {
	status := infraerrors.Code(err)
	code := infraerrors.Reason(err)
	message := infraerrors.Message(err)
	if isOpenAICompositeEndpoint(c.Request.URL.Path) {
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

func isOpenAICompositeEndpoint(path string) bool {
	return strings.Contains(path, "/chat/completions") || strings.Contains(path, "/responses") ||
		strings.Contains(path, "/embeddings") || strings.Contains(path, "/images/") ||
		strings.Contains(path, "/videos/") || strings.Contains(path, "/alpha/search") ||
		strings.Contains(path, "/live") || strings.Contains(path, "/realtime/") ||
		strings.HasPrefix(path, "/backend-api/codex/")
}

// abortCompositeKeyGoogleError 按 Google 协议格式输出复合 Key 错误。
func abortCompositeKeyGoogleError(c *gin.Context, err error) {
	abortWithGoogleError(c, infraerrors.Code(err), infraerrors.Reason(err)+": "+infraerrors.Message(err))
}
