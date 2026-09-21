// 模型目录固定端口只连接 routing 查询、平台元数据和只读 Gemini 传输。
package handler

import (
	"context"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
)

type modelsHTTPBackend struct{ messagesHTTPBackend }

// NewModelsHTTPHandler 可由 app 构造一次并直接绑定四个非消费目录入口。
func (h *GatewayHandler) NewModelsHTTPHandler() *gatewayhttp.ModelsHandler {
	return gatewayhttp.NewModelsHandler(modelsHTTPBackend{messagesHTTPBackend{h}}, modelCatalogProjection{})
}
func newModelDisplayHandler() *gatewayhttp.ModelsHandler {
	return gatewayhttp.NewModelsHandler(nil, modelCatalogProjection{})
}
func (p modelsHTTPBackend) Available() bool { return p.h != nil && p.h.gatewayService != nil }
func (p modelsHTTPBackend) ForcedPlatform(c *gin.Context) (string, bool) {
	return middleware.GetForcePlatformFromContext(c)
}
func (p modelsHTTPBackend) Resolve(ctx context.Context, groupID *int64, platform string) routing.RequestableModelsResult {
	return p.h.gatewayService.ResolveRequestableModels(ctx, groupID, platform)
}
func (p modelsHTTPBackend) PreferredSubscription(c *gin.Context) (*billing.UserSubscription, bool) {
	bc, ok := middleware.GetAPIKeyBillingContext(c)
	if !ok || bc == nil || bc.Mode != apikey.APIKeyBillingModeSubscription || bc.Subscription == nil {
		return nil, false
	}
	return bc.Subscription, true
}
func (p modelsHTTPBackend) SelectGemini(ctx context.Context, groupID *int64) (gatewayhttp.GeminiModelReader, error) {
	account, err := p.h.geminiCompatService.SelectAccountForAIStudioEndpoints(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return geminiModelReadTarget{p.h.geminiCompatService, account}, nil
}

// geminiModelReadTarget 固化同一次选择的执行目标，只提供读取能力，不回读或重新选择账号。
type geminiModelReadTarget struct {
	service *service.GeminiMessagesCompatService
	account *service.Account
}

func (p geminiModelReadTarget) Read(ctx context.Context, path string) (*gatewayhttp.ModelHTTPResponse, error) {
	res, err := p.service.ForwardAIStudioGET(ctx, p.account, path)
	return modelHTTPResponse(res), err
}
func modelHTTPResponse(res *gemini.HTTPResult) *gatewayhttp.ModelHTTPResponse {
	if res == nil {
		return nil
	}
	return &gatewayhttp.ModelHTTPResponse{StatusCode: res.StatusCode, Headers: res.Headers, Body: res.Body}
}
func (p modelsHTTPBackend) HasAntigravity(ctx context.Context, id *int64) (bool, error) {
	return p.h.geminiCompatService.HasAntigravityAccounts(ctx, id)
}
func (p modelsHTTPBackend) CapacityLimited(c *gin.Context, err error) {
	gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(c, err)
}
func (p modelsHTTPBackend) SafeModelSegment(model string) bool {
	return gemini.IsSafeGeminiModelPathSegment(model)
}

// 平台提供目录数据；筛选、默认回退与 HTTP 形状由目标 handler 唯一拥有。
type modelCatalogProjection struct{}

func (modelCatalogProjection) OpenAIModels() []gatewayhttp.OpenAIModel {
	out := make([]gatewayhttp.OpenAIModel, len(openai.DefaultModels))
	for i, m := range openai.DefaultModels {
		out[i] = gatewayhttp.OpenAIModel(m)
	}
	return out
}
func (modelCatalogProjection) OpenAIModelIDs() []string { return openai.DefaultModelIDs() }
func (modelCatalogProjection) GrokModels() []gatewayhttp.GrokModel {
	models := grok.DefaultModels()
	out := make([]gatewayhttp.GrokModel, len(models))
	for i, m := range models {
		out[i] = gatewayhttp.GrokModel(m)
	}
	return out
}
func (modelCatalogProjection) GrokModelIDs() []string { return grok.DefaultModelIDs() }
func (modelCatalogProjection) GrokSupportsXHigh(model string) bool {
	return service.GrokSupportsXHighReasoningEffort(model)
}
func (modelCatalogProjection) QoderModelIDs() []string { return qoder.DefaultRequestModelIDs() }
func (modelCatalogProjection) ClaudeModels(platform string) []gatewayhttp.ClaudeModel {
	var out []gatewayhttp.ClaudeModel
	switch platform {
	case capability.PlatformGemini:
		for _, m := range codeassist.DefaultModels {
			out = append(out, gatewayhttp.ClaudeModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
		}
	case capability.PlatformAntigravity:
		for _, m := range antigravity.DefaultModels() {
			out = append(out, gatewayhttp.ClaudeModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
		}
	case capability.PlatformQoder:
		for _, m := range qoder.DefaultModels {
			out = append(out, gatewayhttp.ClaudeModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
		}
	default:
		for _, m := range anthropic.DefaultModels {
			out = append(out, gatewayhttp.ClaudeModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
		}
	}
	return out
}
func (modelCatalogProjection) GeminiList(ag bool) gatewayhttp.GeminiModelsList {
	models := gemini.FallbackModelsList().Models
	if ag {
		values := antigravity.FallbackGeminiModelsList().Models
		out := make([]gatewayhttp.GeminiModel, len(values))
		for i, m := range values {
			out[i] = gatewayhttp.GeminiModel{Name: m.Name, DisplayName: m.DisplayName, SupportedGenerationMethods: slices.Clone(m.SupportedGenerationMethods)}
		}
		return gatewayhttp.GeminiModelsList{Models: out}
	}
	out := make([]gatewayhttp.GeminiModel, len(models))
	for i, m := range models {
		out[i] = projectGeminiDisplayModel(m)
	}
	return gatewayhttp.GeminiModelsList{Models: out}
}
func (modelCatalogProjection) GeminiModel(name string, ag bool) gatewayhttp.GeminiModel {
	if ag {
		m := antigravity.FallbackGeminiModel(name)
		return gatewayhttp.GeminiModel{Name: m.Name, DisplayName: m.DisplayName, SupportedGenerationMethods: slices.Clone(m.SupportedGenerationMethods)}
	}
	return projectGeminiDisplayModel(gemini.FallbackModel(name))
}
func projectGeminiDisplayModel(m gemini.Model) gatewayhttp.GeminiModel {
	return gatewayhttp.GeminiModel{Name: m.Name, DisplayName: m.DisplayName, Description: m.Description, SupportedGenerationMethods: slices.Clone(m.SupportedGenerationMethods)}
}
func (modelCatalogProjection) HasGeminiFallback(name string) bool {
	return gemini.HasFallbackModel(name)
}
