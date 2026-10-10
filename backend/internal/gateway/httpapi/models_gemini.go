package httpapi

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// @project-doc docs/domains/api_key_model_redirects.md#model_list_projection
func (h *ModelsHandler) GeminiV1BetaListModels(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "google")
	if !accepted {
		return
	}
	defer done()
	key, ok := h.backend.Access(c)
	if !ok || key == nil {
		WriteGoogleError(c, http.StatusUnauthorized, "Invalid API key")
		return
	}
	models, allowed := h.requestableGeminiModels(c, key)
	if !allowed {
		return
	}
	c.JSON(http.StatusOK, GeminiModelsList{Models: models})
}

func (h *ModelsHandler) GeminiV1BetaGetModel(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "google")
	if !accepted {
		return
	}
	defer done()
	key, ok := h.backend.Access(c)
	if !ok || key == nil {
		WriteGoogleError(c, http.StatusUnauthorized, "Invalid API key")
		return
	}
	name := strings.TrimPrefix(strings.TrimSpace(c.Param("model")), "/")
	segment := name
	if key.IsComposite {
		_, rest, found := strings.Cut(name, "/")
		if found {
			segment = rest
		}
	}
	if segment == "" || strings.ContainsAny(segment, "*?") || !h.backend.SafeModelSegment(segment) {
		WriteGoogleError(c, http.StatusBadRequest, "Invalid model in URL")
		return
	}
	group := key.Group
	if key.IsComposite {
		prefix, rest, found := strings.Cut(name, "/")
		group = nil
		if found {
			preferred, ready := h.CompositePreferredSubscription(c, key)
			if ready {
				for _, binding := range key.CompositeGroups {
					if binding.Prefix == prefix && CompositeGroupAvailableToUser(key, preferred, binding.Group) {
						group = binding.Group
						segment = rest
						break
					}
				}
			}
		}
	} else if group == nil || !group.AllowsClientProtocol(protocol.ProtocolGeminiGenerateContent) {
		WriteGoogleError(c, http.StatusForbidden, "This group does not allow Gemini GenerateContent requests")
		return
	}
	if group != nil && group.AllowsClientProtocol(protocol.ProtocolGeminiGenerateContent) && h.backend.Available() {
		forced, _ := h.backend.ForcedPlatform(c)
		forced = strings.TrimSpace(forced)
		selected := []string{segment}
		target, alias := key.ModelMapping[segment]
		if alias {
			selected = append(selected, target)
		}
		resolved := h.backend.ResolveSelected(c.Request.Context(), &group.ID, forced, selected)
		visible := make(map[string]bool, len(resolved.Models))
		for _, model := range resolved.Models {
			if !slices.Contains(model.Protocols, protocol.ProtocolGeminiGenerateContent) {
				continue
			}
			if customListEnabled(group) && len(FilterModelsByCustomList([]string{model.ID}, nil, group.ModelsListConfig.Models)) == 0 {
				continue
			}
			visible[model.ID] = true
		}
		metadataID := segment
		if !visible[segment] && alias && visible[target] {
			metadataID = target
		}
		if visible[metadataID] {
			model := h.catalog.GeminiModel(metadataID, forced == capability.PlatformAntigravity)
			model.Name = "models/" + name
			if name != metadataID {
				model.DisplayName = name
			}
			c.JSON(http.StatusOK, model)
			return
		}
	}
	WriteGoogleError(c, http.StatusNotFound, "Model is not available in this group")
}

// requestableGeminiModels 把组级协议权限、逐候选能力和 Key 别名保持在同一目录路径。
func (h *ModelsHandler) requestableGeminiModels(c *gin.Context, key *apikey.APIKey) ([]GeminiModel, bool) {
	models := make([]GeminiModel, 0)
	if !key.IsComposite && (key.Group == nil || !key.Group.AllowsClientProtocol(protocol.ProtocolGeminiGenerateContent)) {
		WriteGoogleError(c, http.StatusForbidden, "This group does not allow Gemini GenerateContent requests")
		return nil, false
	}
	if !h.backend.Available() {
		return models, true
	}
	forced, _ := h.backend.ForcedPlatform(c)
	forced = strings.TrimSpace(forced)
	seen := make(map[string]bool)
	appendGroup := func(group *routing.Group, prefix string) {
		if group == nil || !group.AllowsClientProtocol(protocol.ProtocolGeminiGenerateContent) {
			return
		}
		id := group.ID
		resolved := h.backend.Resolve(c.Request.Context(), &id, forced)
		available := make([]string, 0, len(resolved.Models))
		for _, model := range resolved.Models {
			if slices.Contains(model.Protocols, protocol.ProtocolGeminiGenerateContent) && !strings.ContainsAny(model.ID, "*?") {
				available = append(available, model.ID)
			}
		}
		if customListEnabled(group) {
			available = FilterModelsByCustomList(available, nil, group.ModelsListConfig.Models)
		}
		aliases := apikey.AvailableAPIKeyModelAliases(available, key.ModelMapping)
		for _, modelID := range append(available, aliases...) {
			publicID := modelID
			if prefix != "" {
				publicID = prefix + "/" + publicID
			}
			if seen[publicID] {
				continue
			}
			seen[publicID] = true
			metadataID := modelID
			if slices.Contains(aliases, modelID) {
				metadataID = key.ModelMapping[modelID]
			}
			model := h.catalog.GeminiModel(metadataID, forced == capability.PlatformAntigravity)
			model.Name = "models/" + publicID
			if publicID != metadataID {
				model.DisplayName = publicID
			}
			models = append(models, model)
		}
	}
	if key.IsComposite {
		subscription, ready := h.CompositePreferredSubscription(c, key)
		if !ready {
			return models, true
		}
		for _, binding := range key.CompositeGroups {
			if CompositeGroupAvailableToUser(key, subscription, binding.Group) {
				appendGroup(binding.Group, binding.Prefix)
			}
		}
	} else {
		appendGroup(key.Group, "")
	}
	return models, true
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
		maps.Copy(cloned, template)
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
