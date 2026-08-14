package middleware

import (
	"context"
	"mime"
	"net/http"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// applyAPIKeyModelRedirect 在复合 Key 选组后应用单 Key 模型重定向。
// 解析失败时保留原请求，让现有协议处理器继续返回原有校验错误。
// @project-doc docs/domains/api_key_model_redirects.md#redirect_order
func applyAPIKeyModelRedirect(c *gin.Context, apiKey *service.APIKey) {
	if c == nil || c.Request == nil || apiKey == nil || len(apiKey.ModelMapping) == 0 {
		return
	}

	sourceModel, geminiModel, ok := apiKeyRequestModel(c)
	if !ok {
		_ = rewriteAPIKeyAdditionalModels(c.Request, apiKey)
		return
	}
	targetModel, matched := apiKey.ResolveModelMapping(sourceModel)
	if !matched {
		_ = rewriteAPIKeyAdditionalModels(c.Request, apiKey)
		return
	}

	if geminiModel {
		rewriteCompositeGeminiParams(c, targetModel)
	} else if err := rewriteCompositeRequestModel(c.Request, targetModel); err != nil {
		return
	}
	_ = rewriteAPIKeyAdditionalModels(c.Request, apiKey)
	setAPIKeyModelRedirectContext(c, sourceModel, targetModel)
}

// apiKeyRequestModel 读取当前协议的主模型；没有模型的管理类入口直接跳过。
func apiKeyRequestModel(c *gin.Context) (string, bool, bool) {
	if isGeminiNativeModelEndpoint(c.Request.URL.Path) {
		model, err := compositeGeminiModelFromParams(c)
		return model, true, err == nil && strings.TrimSpace(model) != ""
	}
	model, err := compositeModelFromRequest(c.Request)
	return model, false, err == nil && strings.TrimSpace(model) != ""
}

// rewriteAPIKeyAdditionalModels 重定向 Responses 工具声明中的附加模型。
func rewriteAPIKeyAdditionalModels(request *http.Request, apiKey *service.APIKey) error {
	if request == nil || apiKey == nil {
		return nil
	}
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		return nil
	}
	body, err := readAndRestoreRequestBody(request)
	if err != nil {
		return err
	}
	rewritten, err := service.RewriteAPIKeyAdditionalModels(body, apiKey.ModelMapping)
	if err != nil {
		return err
	}
	setRequestBody(request, rewritten)
	return nil
}

// setAPIKeyModelRedirectContext 保存日志模型并安装响应恢复写入器。
func setAPIKeyModelRedirectContext(c *gin.Context, sourceModel, targetModel string) {
	clientModel := strings.TrimSpace(sourceModel)
	responseModel := clientModel
	if compositeClient, compositeActual, ok := GetCompositeModelFromContext(c); ok {
		clientModel = compositeClient
		responseModel = compositeActual
	}

	trace := service.NewAPIKeyModelRedirectTrace(clientModel, sourceModel, targetModel)
	ctx := service.WithAPIKeyModelRedirectTrace(c.Request.Context(), trace)
	if existing, ok := ctx.Value(ctxkey.ClientModel).(string); !ok || strings.TrimSpace(existing) == "" {
		ctx = context.WithValue(ctx, ctxkey.ClientModel, clientModel)
	}
	c.Request = c.Request.WithContext(ctx)
	c.Writer = &apiKeyModelResponseWriter{
		ResponseWriter: c.Writer,
		trace:          trace,
		clientModel:    responseModel,
	}
}

// apiKeyModelResponseWriter 只恢复已登记内部模型对应的协议元数据字段。
type apiKeyModelResponseWriter struct {
	gin.ResponseWriter
	trace       *service.APIKeyModelRedirectTrace
	clientModel string
}

func (w *apiKeyModelResponseWriter) Write(data []byte) (int, error) {
	w.Header().Del("Content-Length")
	w.trace.RegisterResponsePayload(data)
	rewritten := data
	for _, model := range w.trace.ResponseModels() {
		rewritten = replaceCompositeResponseModel(rewritten, model, w.clientModel)
	}
	_, err := w.ResponseWriter.Write(rewritten)
	return len(data), err
}

func (w *apiKeyModelResponseWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}
