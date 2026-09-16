// Gemini 模型资源保留 scope 回退、别名元数据和原始上游响应，不进入生成链。
package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
)

func (h *ModelsHandler) GeminiV1BetaListModels(c *gin.Context) {
	done, accepted := h.beginRequest(c, "google")
	if !accepted {
		return
	}
	defer done()

	apiKey, ok := h.backend.Access(c)
	if !ok || apiKey == nil {
		WriteGoogleError(c, http.StatusUnauthorized, "Invalid API key")
		return
	}
	if apiKey.IsComposite {
		requiredPlatform := capability.PlatformGemini
		if forcePlatform, ok := h.backend.ForcedPlatform(c); ok && forcePlatform == capability.PlatformAntigravity {
			requiredPlatform = capability.PlatformAntigravity
		}
		models := h.CompositeRequestableModels(c, apiKey, requiredPlatform)
		out := make([]GeminiModel, 0, len(models))
		for _, model := range models {
			out = append(out, GeminiModel{
				Name:                       "models/" + model,
				DisplayName:                model,
				SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"},
			})
		}
		c.JSON(http.StatusOK, GeminiModelsList{Models: out})
		return
	}
	// 检查平台：优先使用强制平台（/antigravity 路由），否则要求 gemini 分组
	forcePlatform, hasForcePlatform := h.backend.ForcedPlatform(c)
	if !hasForcePlatform && (apiKey.Group == nil || apiKey.Group.Platform != capability.PlatformGemini) {
		WriteGoogleError(c, http.StatusBadRequest, "API key group platform is not gemini")
		return
	}

	// 强制 antigravity 模式：返回 antigravity 支持的模型列表
	if forcePlatform == capability.PlatformAntigravity {
		h.WriteGeminiModelsListWithAPIKeyAliases(c, h.catalog.GeminiList(true), apiKey)
		return
	}

	if models, ok := h.CustomGeminiModelsList(apiKey.Group); ok {
		h.WriteGeminiModelsListWithAPIKeyAliases(c, models, apiKey)
		return
	}

	account, err := h.backend.SelectGemini(c.Request.Context(), apiKey.GroupID)
	if err != nil {
		// 没有 gemini 账户，检查是否有 antigravity 账户可用
		hasAntigravity, _ := h.backend.HasAntigravity(c.Request.Context(), apiKey.GroupID)
		if hasAntigravity {
			// antigravity 账户使用静态模型列表
			h.WriteGeminiModelsListWithAPIKeyAliases(c, h.catalog.GeminiList(false), apiKey)
			return
		}
		h.backend.CapacityLimited(c, err)
		WriteGoogleError(c, http.StatusServiceUnavailable, "No available Gemini accounts: "+err.Error())
		return
	}

	res, err := account.Read(c.Request.Context(), "/v1beta/models")
	if err != nil {
		WriteGoogleError(c, http.StatusBadGateway, err.Error())
		return
	}
	if h.ShouldFallbackGeminiModels(res) {
		h.WriteGeminiModelsListWithAPIKeyAliases(c, h.catalog.GeminiList(false), apiKey)
		return
	}
	res.Body = h.AppendAPIKeyAliasesToGeminiModelsJSON(res.Body, apiKey.ModelMapping)
	h.WriteUpstreamResponse(c, res)
}

func (h *ModelsHandler) GeminiV1BetaGetModel(c *gin.Context) {
	done, accepted := h.beginRequest(c, "google")
	if !accepted {
		return
	}
	defer done()

	apiKey, ok := h.backend.Access(c)
	if !ok || apiKey == nil {
		WriteGoogleError(c, http.StatusUnauthorized, "Invalid API key")
		return
	}
	// 检查平台：优先使用强制平台（/antigravity 路由），否则要求 gemini 分组
	forcePlatform, hasForcePlatform := h.backend.ForcedPlatform(c)
	if !hasForcePlatform && (apiKey.Group == nil || apiKey.Group.Platform != capability.PlatformGemini) {
		WriteGoogleError(c, http.StatusBadRequest, "API key group platform is not gemini")
		return
	}

	modelName := strings.TrimPrefix(strings.TrimSpace(c.Param("model")), "/")
	if modelName == "" {
		WriteGoogleError(c, http.StatusBadRequest, "Missing model in URL")
		return
	}
	// 模型名会被拼进上游 URL 的 path，先在入口校验片段合规性，
	// 通过路径校验端口保持原限制。
	if !h.backend.SafeModelSegment(modelName) {
		WriteGoogleError(c, http.StatusBadRequest, "Invalid model in URL")
		return
	}

	// 强制 antigravity 模式：返回 antigravity 模型信息
	if forcePlatform == capability.PlatformAntigravity {
		c.JSON(http.StatusOK, h.catalog.GeminiModel(modelName, true))
		return
	}

	account, err := h.backend.SelectGemini(c.Request.Context(), apiKey.GroupID)
	if err != nil {
		// 没有 gemini 账户，检查是否有 antigravity 账户可用
		hasAntigravity, _ := h.backend.HasAntigravity(c.Request.Context(), apiKey.GroupID)
		if hasAntigravity {
			// antigravity 账户使用静态模型信息
			c.JSON(http.StatusOK, h.catalog.GeminiModel(modelName, false))
			return
		}
		h.backend.CapacityLimited(c, err)
		WriteGoogleError(c, http.StatusServiceUnavailable, "No available Gemini accounts: "+err.Error())
		return
	}

	res, err := account.Read(c.Request.Context(), "/v1beta/models/"+modelName)
	if err != nil {
		WriteGoogleError(c, http.StatusBadGateway, err.Error())
		return
	}
	if h.ShouldFallbackGeminiModel(modelName, res) {
		c.JSON(http.StatusOK, h.catalog.GeminiModel(modelName, false))
		return
	}
	h.WriteUpstreamResponse(c, res)
}

func (h *ModelsHandler) WriteGeminiModelsListWithAPIKeyAliases(c *gin.Context, payload GeminiModelsList, apiKey *apikey.APIKey) {
	body, err := json.Marshal(payload)
	if err != nil {
		WriteGoogleError(c, http.StatusInternalServerError, "Failed to encode models")
		return
	}
	if apiKey != nil {
		body = h.AppendAPIKeyAliasesToGeminiModelsJSON(body, apiKey.ModelMapping)
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func (h *ModelsHandler) AppendAPIKeyAliasesToGeminiModelsJSON(body []byte, mapping map[string]string) []byte {
	if len(body) == 0 || len(mapping) == 0 {
		return body
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	var models []map[string]json.RawMessage
	if err := json.Unmarshal(payload["models"], &models); err != nil {
		return body
	}

	modelIDs := make([]string, 0, len(models))
	templates := make(map[string]map[string]json.RawMessage, len(models))
	for _, model := range models {
		var name string
		if err := json.Unmarshal(model["name"], &name); err != nil {
			continue
		}
		modelID := strings.TrimPrefix(strings.TrimSpace(name), "models/")
		if modelID == "" {
			continue
		}
		modelIDs = append(modelIDs, modelID)
		if _, exists := templates[modelID]; !exists {
			templates[modelID] = model
		}
	}

	for _, alias := range apikey.AvailableAPIKeyModelAliases(modelIDs, mapping) {
		template, exists := templates[mapping[alias]]
		if !exists {
			continue
		}
		cloned := make(map[string]json.RawMessage, len(template))
		for key, value := range template {
			cloned[key] = value
		}
		cloned["name"], _ = json.Marshal("models/" + alias)
		cloned["displayName"], _ = json.Marshal(alias)
		models = append(models, cloned)
	}

	encodedModels, err := json.Marshal(models)
	if err != nil {
		return body
	}
	payload["models"] = encodedModels
	updated, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return updated
}

func (h *ModelsHandler) CustomGeminiModelsList(group *apikey.Group) (GeminiModelsList, bool) {
	if group == nil || !customListEnabled(group) {
		return GeminiModelsList{}, false
	}
	models := make([]GeminiModel, 0, len(group.ModelsListConfig.Models))
	for _, modelID := range group.ModelsListConfig.Models {
		models = append(models, h.catalog.GeminiModel(modelID, false))
	}
	return GeminiModelsList{Models: models}, true
}

func (h *ModelsHandler) WriteUpstreamResponse(c *gin.Context, res *ModelHTTPResponse) {
	if res == nil {
		WriteGoogleError(c, http.StatusBadGateway, "Empty upstream response")
		return
	}
	for k, vv := range res.Headers {
		// 不覆盖内容长度和逐跳 Header。
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") || strings.EqualFold(k, "Connection") {
			continue
		}
		for _, v := range vv {
			c.Writer.Header().Add(k, v)
		}
	}
	contentType := res.Headers.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(res.StatusCode, contentType, res.Body)
}

func (h *ModelsHandler) ShouldFallbackGeminiModels(res *ModelHTTPResponse) bool {
	if res == nil {
		return true
	}
	if res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusForbidden {
		return false
	}
	if strings.Contains(strings.ToLower(res.Headers.Get("Www-Authenticate")), "insufficient_scope") {
		return true
	}
	if strings.Contains(strings.ToLower(string(res.Body)), "insufficient authentication scopes") {
		return true
	}
	if strings.Contains(strings.ToLower(string(res.Body)), "access_token_scope_insufficient") {
		return true
	}
	return false
}

func (h *ModelsHandler) ShouldFallbackGeminiModel(modelName string, res *ModelHTTPResponse) bool {
	if h.ShouldFallbackGeminiModels(res) {
		return true
	}
	if res == nil || res.StatusCode != http.StatusNotFound {
		return false
	}
	return h.catalog.HasGeminiFallback(modelName)
}
