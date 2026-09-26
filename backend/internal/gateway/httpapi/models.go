// 模型资源入口只读取目录和授权快照，不拥有槽位、资金或完成执行器。
package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeldisplay"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
)

type ModelsBackend interface {
	Access(*gin.Context) (*apikey.APIKey, bool)
	ForcedPlatform(*gin.Context) (string, bool)
	Available() bool
	Resolve(context.Context, *int64, string) routing.RequestableModelsResult
	PreferredSubscription(*gin.Context) (*billing.UserSubscription, bool)
	SelectGemini(context.Context, *int64) (GeminiModelReader, error)
	HasAntigravity(context.Context, *int64) (bool, error)
	CapacityLimited(*gin.Context, error)
	SafeModelSegment(string) bool
}

// GeminiModelReader 只允许对已选择的执行目标读取模型资源，不暴露账号凭据。
type GeminiModelReader interface {
	Read(context.Context, string) (*ModelHTTPResponse, error)
}
type ModelHTTPResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}
type (
	ModelsCatalog = modeldisplay.Catalog
	ModelsHandler struct {
		requestLifetime

		backend ModelsBackend
		catalog ModelsCatalog
	}
)

func NewModelsHandler(backend ModelsBackend, catalog ModelsCatalog) *ModelsHandler {
	return &ModelsHandler{backend: backend, catalog: catalog}
}

// customListEnabled 复用 routing 的分组规则，仅投影列表配置。
func customListEnabled(g *routing.Group) bool {
	if g == nil {
		return false
	}
	return (&routing.Group{ModelsListConfig: g.ModelsListConfig}).CustomModelsListEnabled()
}

type (
	ClaudeModel      = modeldisplay.ClaudeModel
	OpenAIModel      = modeldisplay.OpenAIModel
	GrokModel        = modeldisplay.GrokModel
	GeminiModel      = modeldisplay.GeminiModel
	GeminiModelsList = modeldisplay.GeminiModelsList
)

func (h *ModelsHandler) Models(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	apiKey, _ := h.backend.Access(c)
	if apiKey != nil && apiKey.IsComposite {
		h.WriteCompositeModelsList(c, h.CompositeRequestableModels(c, apiKey, ""))
		return
	}

	var groupID *int64
	var platform string

	if apiKey != nil && apiKey.Group != nil {
		groupID = &apiKey.Group.ID
		platform = apiKey.Group.Platform
	}
	if forcedPlatform, ok := h.backend.ForcedPlatform(c); ok && strings.TrimSpace(forcedPlatform) != "" {
		platform = forcedPlatform
	}

	// 统一按分组映射、账号映射和分组白名单解析真实可请求模型。
	resolution := h.backend.Resolve(c.Request.Context(), groupID, platform)
	availableModels := routing.RequestableModelIDs(resolution.Models)
	if apiKey != nil && apiKey.Group != nil && customListEnabled(apiKey.Group) {
		// 自定义列表只能与已通过分组策略和账号校验的模型取交集，不能重新加入被拒绝的模型。
		availableModels = FilterModelsByCustomList(availableModels, nil, apiKey.Group.ModelsListConfig.Models)
		availableModels = apikey.AppendAPIKeyModelAliases(availableModels, apiKey.ModelMapping)
		h.WriteCustomModelsList(c, platform, availableModels)
		return
	}
	if apiKey != nil {
		availableModels = apikey.AppendAPIKeyModelAliases(availableModels, apiKey.ModelMapping)
	}

	if len(availableModels) > 0 {
		if resolution.HadExplicitAccountModels {
			if platform == capability.PlatformGrok {
				// Grok Build 需要 reasoning 元数据，同时保留显式列表的旧兼容字段。
				h.WriteGrokModelsList(c, availableModels)
			} else {
				// 其它平台的账号显式列表继续使用历史 Claude 兼容字段结构。
				h.WriteModelsList(c, availableModels)
			}
		} else {
			h.WriteDefaultModelsList(c, platform, availableModels)
		}
		return
	}
	if resolution.Restricted || groupID != nil {
		h.WriteModelsList(c, nil)
		return
	}

	// 未绑定分组时保留旧版默认模型，并按默认可请求集合追加精确别名。
	fallbackModels := h.DefaultModelIDsForPlatform(platform)
	if apiKey != nil {
		fallbackModels = apikey.AppendAPIKeyModelAliases(fallbackModels, apiKey.ModelMapping)
	}
	h.WriteDefaultModelsList(c, platform, fallbackModels)
}

func (h *ModelsHandler) AntigravityModels(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	apiKey, _ := h.backend.Access(c)
	if apiKey != nil && apiKey.IsComposite {
		h.WriteCompositeModelsList(c, h.CompositeRequestableModels(c, apiKey, capability.PlatformAntigravity))
		return
	}
	var groupID *int64
	if apiKey != nil && apiKey.Group != nil {
		value := apiKey.Group.ID
		groupID = &value
	}
	if h != nil && h.backend.Available() {
		resolution := h.backend.Resolve(c.Request.Context(), groupID, capability.PlatformAntigravity)
		modelIDs := routing.RequestableModelIDs(resolution.Models)
		if apiKey != nil && apiKey.Group != nil && customListEnabled(apiKey.Group) {
			modelIDs = FilterModelsByCustomList(modelIDs, nil, apiKey.Group.ModelsListConfig.Models)
		}
		if apiKey != nil {
			modelIDs = apikey.AppendAPIKeyModelAliases(modelIDs, apiKey.ModelMapping)
		}
		if len(modelIDs) > 0 || resolution.Restricted || groupID != nil {
			h.WriteClaudeCompatiblePlatformModelsList(c, capability.PlatformAntigravity, modelIDs)
			return
		}
	} else if groupID != nil {
		h.WriteClaudeCompatiblePlatformModelsList(c, capability.PlatformAntigravity, nil)
		return
	}
	modelIDs := h.DefaultModelIDsForPlatform(capability.PlatformAntigravity)
	if apiKey != nil {
		modelIDs = apikey.AppendAPIKeyModelAliases(modelIDs, apiKey.ModelMapping)
	}
	h.WriteClaudeCompatiblePlatformModelsList(c, capability.PlatformAntigravity, modelIDs)
}

func (h *ModelsHandler) CompositeRequestableModels(c *gin.Context, apiKey *apikey.APIKey, requiredPlatform string) []string {
	if h == nil || !h.backend.Available() || c == nil || c.Request == nil || apiKey == nil {
		return nil
	}
	preferredSubscription, ready := h.CompositePreferredSubscription(c, apiKey)
	if !ready {
		return nil
	}
	ctx := c.Request.Context()
	models := make([]string, 0)
	seen := make(map[string]struct{})
	for _, binding := range apiKey.CompositeGroups {
		group := binding.Group
		if !CompositeGroupAvailableToUser(apiKey, preferredSubscription, group) || (requiredPlatform != "" && group.Platform != requiredPlatform) {
			continue
		}
		groupID := group.ID
		resolution := h.backend.Resolve(ctx, &groupID, group.Platform)
		available := routing.RequestableModelIDs(resolution.Models)
		if customListEnabled(group) {
			available = FilterModelsByCustomList(available, nil, group.ModelsListConfig.Models)
		}
		available = apikey.AppendAPIKeyModelAliases(available, apiKey.ModelMapping)
		for _, model := range available {
			prefixed := binding.Prefix + "/" + model
			if _, exists := seen[prefixed]; exists {
				continue
			}
			seen[prefixed] = struct{}{}
			models = append(models, prefixed)
		}
	}
	return models
}

func (h *ModelsHandler) CompositePreferredSubscription(c *gin.Context, apiKey *apikey.APIKey) (*billing.UserSubscription, bool) {
	if apikey.APIKeyEffectiveBillingMode(apiKey) != apikey.APIKeyBillingModeSubscription {
		return nil, true
	}
	subscription, ok := h.backend.PreferredSubscription(c)
	if !ok || subscription == nil {
		return nil, false
	}
	return subscription, true
}

func (h *ModelsHandler) WriteCompositeModelsList(c *gin.Context, modelIDs []string) {
	models := make([]gin.H, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		models = append(models, gin.H{
			"id": modelID, "object": "model", "type": "model", "created": 1704067200,
			"created_at": "2024-01-01T00:00:00Z", "owned_by": "token-router", "display_name": modelID,
		})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": models})
}

func (h *ModelsHandler) WriteModelsList(c *gin.Context, modelIDs []string) {
	models := make([]ClaudeModel, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		models = append(models, ClaudeModel{
			ID:          modelID,
			Type:        "model",
			DisplayName: modelID,
			CreatedAt:   "2024-01-01T00:00:00Z",
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   models,
	})
}

func (h *ModelsHandler) WriteCustomModelsList(c *gin.Context, platform string, modelIDs []string) {
	switch platform {
	case capability.PlatformOpenAI:
		h.WriteOpenAIModelsList(c, modelIDs)
	case capability.PlatformGrok:
		h.WriteGrokModelsList(c, modelIDs)
	default:
		h.WriteModelsList(c, modelIDs)
	}
}

func (h *ModelsHandler) WriteGrokModelsList(c *gin.Context, modelIDs []string) {
	defaults := h.catalog.GrokModels()
	defaultsByID := make(map[string]GrokModel, len(defaults))
	for _, model := range defaults {
		defaultsByID[model.ID] = model
	}

	models := make([]grokModelListItem, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		model, ok := defaultsByID[modelID]
		if !ok {
			model = GrokModel{
				ID:          modelID,
				Object:      "model",
				OwnedBy:     "xai",
				DisplayName: modelID,
			}
		}
		item := grokModelListItem{
			GrokModel: model,
			Type:      "model",
			CreatedAt: "2024-01-01T00:00:00Z",
		}
		if GrokModelSupportsConfigurableReasoning(modelID) {
			item.SupportsReasoningEffort = true
			item.ReasoningEffort = "high"
			efforts := []grokReasoningEffortOption{
				{Value: "low", Label: "Low"},
				{Value: "medium", Label: "Medium"},
				{Value: "high", Label: "High", Default: true},
			}
			if h.catalog.GrokSupportsXHigh(modelID) {
				efforts = append(efforts, grokReasoningEffortOption{Value: "xhigh", Label: "xHigh"})
			}
			item.ReasoningEfforts = efforts
		}
		models = append(models, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   models,
	})
}

func (h *ModelsHandler) WriteDefaultModelsList(c *gin.Context, platform string, modelIDs []string) {
	switch platform {
	case capability.PlatformOpenAI:
		h.WriteOpenAIModelsList(c, modelIDs)
	case capability.PlatformGrok:
		h.WriteGrokModelsList(c, modelIDs)
	case capability.PlatformAnthropic, capability.PlatformGemini, capability.PlatformAntigravity, capability.PlatformQoder:
		h.WriteClaudeCompatiblePlatformModelsList(c, platform, modelIDs)
	default:
		h.WriteModelsList(c, modelIDs)
	}
}

func (h *ModelsHandler) WriteOpenAIModelsList(c *gin.Context, modelIDs []string) {
	defaultsByID := make(map[string]OpenAIModel, len(h.catalog.OpenAIModels()))
	for _, model := range h.catalog.OpenAIModels() {
		defaultsByID[model.ID] = model
	}

	models := make([]OpenAIModel, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		if model, ok := defaultsByID[modelID]; ok {
			models = append(models, model)
			continue
		}
		models = append(models, OpenAIModel{
			ID:          modelID,
			Object:      "model",
			Created:     1704067200,
			OwnedBy:     "openai",
			Type:        "model",
			DisplayName: modelID,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   models,
	})
}

func (h *ModelsHandler) WriteClaudeCompatiblePlatformModelsList(c *gin.Context, platform string, modelIDs []string) {
	defaultsByID := make(map[string]ClaudeModel)
	appendDefault := func(id, modelType, displayName, createdAt string) {
		defaultsByID[id] = ClaudeModel{
			ID:          id,
			Type:        modelType,
			DisplayName: displayName,
			CreatedAt:   createdAt,
		}
	}

	switch platform {
	case capability.PlatformGemini:
		for _, model := range h.catalog.ClaudeModels(capability.PlatformGemini) {
			appendDefault(model.ID, model.Type, model.DisplayName, model.CreatedAt)
		}
	case capability.PlatformAntigravity:
		for _, model := range h.catalog.ClaudeModels(capability.PlatformAntigravity) {
			appendDefault(model.ID, model.Type, model.DisplayName, model.CreatedAt)
		}
	case capability.PlatformQoder:
		for _, model := range h.catalog.ClaudeModels(capability.PlatformQoder) {
			appendDefault(model.ID, model.Type, model.DisplayName, model.CreatedAt)
		}
	default:
		for _, model := range h.catalog.ClaudeModels(capability.PlatformAnthropic) {
			appendDefault(model.ID, model.Type, model.DisplayName, model.CreatedAt)
		}
	}

	models := make([]ClaudeModel, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		if model, ok := defaultsByID[modelID]; ok {
			models = append(models, model)
			continue
		}
		models = append(models, ClaudeModel{
			ID:          modelID,
			Type:        "model",
			DisplayName: modelID,
			CreatedAt:   "2024-01-01T00:00:00Z",
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   models,
	})
}

func (h *ModelsHandler) DefaultModelIDsForPlatform(platform string) []string {
	return modeldisplay.DefaultModelIDs(h.catalog, platform)
}

func FilterModelsByCustomList(availableModels, fallbackModels, selectedModels []string) []string {
	if len(selectedModels) == 0 {
		return availableModels
	}
	source := availableModels
	if len(source) == 0 {
		source = fallbackModels
	}
	if len(source) == 0 {
		return nil
	}

	allowed := make([]string, 0, len(source))
	for _, model := range source {
		model = strings.TrimSpace(model)
		if model != "" {
			allowed = append(allowed, model)
		}
	}

	seen := make(map[string]struct{}, len(selectedModels))
	filtered := make([]string, 0, len(selectedModels))
	for _, model := range selectedModels {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if !CustomModelsListAllowsModel(allowed, model) {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		filtered = append(filtered, model)
	}
	return filtered
}

func CustomModelsListAllowsModel(availablePatterns []string, model string) bool {
	for _, pattern := range availablePatterns {
		if pattern == model {
			return true
		}
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(model, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

func MergeModelIDs(primary, secondary []string) []string {
	return modeldisplay.MergeModelIDs(primary, secondary)
}

func GrokModelSupportsConfigurableReasoning(modelID string) bool {
	switch strings.ToLower(strings.TrimSpace(modelID)) {
	case "grok-4.6", "grok-4.6-latest", "grok-4.5", "grok-4.5-latest", "grok", "grok-latest", "grok-build", "grok-build-latest", "grok-build-0.1":
		return true
	default:
		return false
	}
}

func CompositeGroupAvailableToUser(apiKey *apikey.APIKey, preferredSubscription *billing.UserSubscription, group *routing.Group) bool {
	if apiKey == nil || apiKey.User == nil || group == nil || !group.IsActive() {
		return false
	}
	if apikey.APIKeyEffectiveBillingMode(apiKey) == apikey.APIKeyBillingModeSubscription && !billing.SubscriptionAllowsGroup(preferredSubscription, group.ID) {
		return false
	}
	return apiKey.User.CanBindGroup(group.ID, group.IsExclusive)
}

type grokReasoningEffortOption struct {
	Value   string `json:"value"`
	Label   string `json:"label"`
	Default bool   `json:"default,omitempty"`
}

type grokModelListItem struct {
	GrokModel
	SupportsReasoningEffort bool                        `json:"supportsReasoningEffort,omitempty"`
	ReasoningEffort         string                      `json:"reasoningEffort,omitempty"`
	ReasoningEfforts        []grokReasoningEffortOption `json:"reasoningEfforts,omitempty"`
	Type                    string                      `json:"type"`
	CreatedAt               string                      `json:"created_at"`
}
