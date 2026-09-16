package httpapi

import (
	"context"
	"mime"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/gin-gonic/gin"
)

// ApplyAPIKeyModelRedirect 在复合 Key 选组后应用单 Key 模型重定向。
// 解析失败时保留原请求，让现有协议处理器继续返回原有校验错误。
// @project-doc docs/domains/api_key_model_redirects.md#redirect_order
func ApplyAPIKeyModelRedirect(c *gin.Context, apiKey *apikey.APIKey) {
	if c == nil || c.Request == nil || apiKey == nil || len(apiKey.ModelMapping) == 0 {
		return
	}

	sourceModel, geminiModel, ok := ApiKeyRequestModel(c)
	if !ok {
		_ = RewriteAPIKeyAdditionalModels(c.Request, apiKey)
		return
	}
	targetModel, matched := apiKey.ResolveModelMapping(sourceModel)
	if !matched {
		_ = RewriteAPIKeyAdditionalModels(c.Request, apiKey)
		return
	}

	if geminiModel {
		RewriteCompositeGeminiParams(c, targetModel)
	} else if err := RewriteCompositeRequestModel(c.Request, targetModel); err != nil {
		return
	}
	_ = RewriteAPIKeyAdditionalModels(c.Request, apiKey)
	SetAPIKeyModelRedirectContext(c, sourceModel, targetModel)
}

// ApiKeyRequestModel 读取当前协议的主模型；没有模型的管理类入口直接跳过。
func ApiKeyRequestModel(c *gin.Context) (string, bool, bool) {
	if IsGeminiNativeModelEndpoint(c.Request.URL.Path) {
		model, err := CompositeGeminiModelFromParams(c)
		return model, true, err == nil && strings.TrimSpace(model) != ""
	}
	model, err := CompositeModelFromRequest(c.Request)
	return model, false, err == nil && strings.TrimSpace(model) != ""
}

// RewriteAPIKeyAdditionalModels 重定向 Responses 工具声明中的附加模型。
func RewriteAPIKeyAdditionalModels(request *http.Request, apiKey *apikey.APIKey) error {
	if request == nil || apiKey == nil {
		return nil
	}
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		return nil
	}
	body, err := ReadAndRestoreRequestBody(request)
	if err != nil {
		return err
	}
	rewritten, err := modeltrace.RewriteAPIKeyAdditionalModels(body, apiKey.ModelMapping)
	if err != nil {
		return err
	}
	SetRequestBody(request, rewritten)
	return nil
}

// SetAPIKeyModelRedirectContext 保存日志模型并安装响应恢复写入器。
func SetAPIKeyModelRedirectContext(c *gin.Context, sourceModel, targetModel string) {
	clientModel := strings.TrimSpace(sourceModel)
	responseModel := clientModel
	if compositeClient, compositeActual, ok := GetCompositeModelFromContext(c); ok {
		clientModel = compositeClient
		responseModel = compositeActual
	}

	trace := modeltrace.NewAPIKeyModelRedirectTrace(clientModel, sourceModel, targetModel)
	ctx := modeltrace.WithContext(c.Request.Context(), trace)
	if existing, ok := ctx.Value(telemetry.ClientModel).(string); !ok || strings.TrimSpace(existing) == "" {
		ctx = context.WithValue(ctx, telemetry.ClientModel, clientModel)
	}
	c.Request = c.Request.WithContext(ctx)
	c.Writer = &ApiKeyModelResponseWriter{
		ResponseWriter: c.Writer,
		trace:          trace,
		clientModel:    responseModel,
	}
}

// ApiKeyModelResponseWriter 只恢复已登记内部模型对应的协议元数据字段。
type ApiKeyModelResponseWriter struct {
	gin.ResponseWriter
	trace       *modeltrace.APIKeyModelRedirectTrace
	clientModel string
}

func (w *ApiKeyModelResponseWriter) Write(data []byte) (int, error) {
	w.Header().Del("Content-Length")
	w.trace.RegisterResponsePayload(data)
	rewritten := data
	for _, model := range w.trace.ResponseModels() {
		rewritten = ReplaceCompositeResponseModel(rewritten, model, w.clientModel)
	}
	_, err := w.ResponseWriter.Write(rewritten)
	return len(data), err
}

func (w *ApiKeyModelResponseWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}
