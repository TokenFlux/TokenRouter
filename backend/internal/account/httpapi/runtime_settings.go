package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountdto "github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// RuntimeSettingsHandler 只调用账号设置用例，不访问仓储或旧设置聚合。
type RuntimeSettingsHandler struct{ settingService *account.RuntimeSettings }

// NewRuntimeSettingsHandler 注入唯一的账号设置及其缓存。
func NewRuntimeSettingsHandler(service *account.RuntimeSettings) *RuntimeSettingsHandler {
	return &RuntimeSettingsHandler{settingService: service}
}

// GetOverloadCooldownSettings 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) GetOverloadCooldownSettings(c *gin.Context) {
	settings, err := h.settingService.GetOverloadCooldownSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, accountdto.OverloadCooldownSettings{
		Enabled:         settings.Enabled,
		CooldownMinutes: settings.CooldownMinutes,
	})
}

// UpdateOverloadCooldownSettings 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) UpdateOverloadCooldownSettings(c *gin.Context) {
	var req UpdateOverloadCooldownSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	settings := &account.OverloadCooldownSettings{
		Enabled:         req.Enabled,
		CooldownMinutes: req.CooldownMinutes,
	}

	if err := h.settingService.SetOverloadCooldownSettings(c.Request.Context(), settings); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}

	updatedSettings, err := h.settingService.GetOverloadCooldownSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, accountdto.OverloadCooldownSettings{
		Enabled:         updatedSettings.Enabled,
		CooldownMinutes: updatedSettings.CooldownMinutes,
	})
}

// UpdateOverloadCooldownSettingsRequest 保留原字段存在性与 JSON 类型。
type UpdateOverloadCooldownSettingsRequest struct {
	Enabled         bool `json:"enabled"`
	CooldownMinutes int  `json:"cooldown_minutes"`
}

// GetRateLimit429CooldownSettings 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) GetRateLimit429CooldownSettings(c *gin.Context) {
	settings, err := h.settingService.GetRateLimit429CooldownSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, accountdto.RateLimit429CooldownSettings{
		Enabled:         settings.Enabled,
		CooldownSeconds: settings.CooldownSeconds,
	})
}

// UpdateRateLimit429CooldownSettings 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) UpdateRateLimit429CooldownSettings(c *gin.Context) {
	var req UpdateRateLimit429CooldownSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	settings := &account.RateLimit429CooldownSettings{
		Enabled:         req.Enabled,
		CooldownSeconds: req.CooldownSeconds,
	}

	if err := h.settingService.SetRateLimit429CooldownSettings(c.Request.Context(), settings); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}

	updatedSettings, err := h.settingService.GetRateLimit429CooldownSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, accountdto.RateLimit429CooldownSettings{
		Enabled:         updatedSettings.Enabled,
		CooldownSeconds: updatedSettings.CooldownSeconds,
	})
}

// UpdateRateLimit429CooldownSettingsRequest 保留原字段存在性与 JSON 类型。
type UpdateRateLimit429CooldownSettingsRequest struct {
	Enabled         bool `json:"enabled"`
	CooldownSeconds int  `json:"cooldown_seconds"`
}

// GetOpenAIImagesOAuthUnavailableCooldownSettings 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) GetOpenAIImagesOAuthUnavailableCooldownSettings(c *gin.Context) {
	settings, err := h.settingService.GetOpenAIImagesOAuthUnavailableCooldownSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, accountdto.OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: settings.CooldownMinutes})
}

// UpdateOpenAIImagesOAuthUnavailableCooldownSettings 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) UpdateOpenAIImagesOAuthUnavailableCooldownSettings(c *gin.Context) {
	var req UpdateOpenAIImagesOAuthUnavailableCooldownSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings := &account.OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: req.CooldownMinutes}
	if err := h.settingService.SetOpenAIImagesOAuthUnavailableCooldownSettings(c.Request.Context(), settings); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	httpx.Success(c, accountdto.OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: settings.CooldownMinutes})
}

// UpdateOpenAIImagesOAuthUnavailableCooldownSettingsRequest 保留原字段存在性与 JSON 类型。
type UpdateOpenAIImagesOAuthUnavailableCooldownSettingsRequest struct {
	CooldownMinutes int `json:"cooldown_minutes"`
}

// GetStreamTimeoutSettings 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) GetStreamTimeoutSettings(c *gin.Context) {
	settings, err := h.settingService.GetStreamTimeoutSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, accountdto.StreamTimeoutSettings{
		Enabled:                settings.Enabled,
		Action:                 settings.Action,
		TempUnschedMinutes:     settings.TempUnschedMinutes,
		ThresholdCount:         settings.ThresholdCount,
		ThresholdWindowMinutes: settings.ThresholdWindowMinutes,
	})
}

// UpdateStreamTimeoutSettings 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) UpdateStreamTimeoutSettings(c *gin.Context) {
	var req UpdateStreamTimeoutSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	settings := &account.StreamTimeoutSettings{
		Enabled:                req.Enabled,
		Action:                 req.Action,
		TempUnschedMinutes:     req.TempUnschedMinutes,
		ThresholdCount:         req.ThresholdCount,
		ThresholdWindowMinutes: req.ThresholdWindowMinutes,
	}

	if err := h.settingService.SetStreamTimeoutSettings(c.Request.Context(), settings); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}

	// 重新获取设置返回
	updatedSettings, err := h.settingService.GetStreamTimeoutSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, accountdto.StreamTimeoutSettings{
		Enabled:                updatedSettings.Enabled,
		Action:                 updatedSettings.Action,
		TempUnschedMinutes:     updatedSettings.TempUnschedMinutes,
		ThresholdCount:         updatedSettings.ThresholdCount,
		ThresholdWindowMinutes: updatedSettings.ThresholdWindowMinutes,
	})
}

// UpdateStreamTimeoutSettingsRequest 保留原字段存在性与 JSON 类型。
type UpdateStreamTimeoutSettingsRequest struct {
	Enabled                bool   `json:"enabled"`
	Action                 string `json:"action"`
	TempUnschedMinutes     int    `json:"temp_unsched_minutes"`
	ThresholdCount         int    `json:"threshold_count"`
	ThresholdWindowMinutes int    `json:"threshold_window_minutes"`
}

// GetOpenAI403CooldownSettings 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) GetOpenAI403CooldownSettings(c *gin.Context) {
	settings, err := h.settingService.GetOpenAI403CooldownSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, accountdto.OpenAI403CooldownSettings{
		Enabled:                 settings.Enabled,
		CooldownMinutes:         settings.CooldownMinutes,
		ErrorOnThresholdEnabled: settings.ErrorOnThresholdEnabled,
		ThresholdCount:          settings.ThresholdCount,
		ThresholdWindowMinutes:  settings.ThresholdWindowMinutes,
	})
}

// UpdateOpenAI403CooldownSettings 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) UpdateOpenAI403CooldownSettings(c *gin.Context) {
	var req UpdateOpenAI403CooldownSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	defaults := account.DefaultOpenAI403CooldownSettings()
	errorOnThresholdEnabled := defaults.ErrorOnThresholdEnabled
	if req.ErrorOnThresholdEnabled != nil {
		errorOnThresholdEnabled = *req.ErrorOnThresholdEnabled
	}
	thresholdCount := defaults.ThresholdCount
	if req.ThresholdCount != nil {
		thresholdCount = *req.ThresholdCount
	}
	thresholdWindowMinutes := defaults.ThresholdWindowMinutes
	if req.ThresholdWindowMinutes != nil {
		thresholdWindowMinutes = *req.ThresholdWindowMinutes
	}

	settings := &account.OpenAI403CooldownSettings{
		Enabled:                 req.Enabled,
		CooldownMinutes:         req.CooldownMinutes,
		ErrorOnThresholdEnabled: errorOnThresholdEnabled,
		ThresholdCount:          thresholdCount,
		ThresholdWindowMinutes:  thresholdWindowMinutes,
	}

	if err := h.settingService.SetOpenAI403CooldownSettings(c.Request.Context(), settings); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}

	updatedSettings, err := h.settingService.GetOpenAI403CooldownSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, accountdto.OpenAI403CooldownSettings{
		Enabled:                 updatedSettings.Enabled,
		CooldownMinutes:         updatedSettings.CooldownMinutes,
		ErrorOnThresholdEnabled: updatedSettings.ErrorOnThresholdEnabled,
		ThresholdCount:          updatedSettings.ThresholdCount,
		ThresholdWindowMinutes:  updatedSettings.ThresholdWindowMinutes,
	})
}

// UpdateOpenAI403CooldownSettingsRequest 保留原字段存在性与 JSON 类型。
type UpdateOpenAI403CooldownSettingsRequest struct {
	Enabled                 bool  `json:"enabled"`
	CooldownMinutes         int   `json:"cooldown_minutes"`
	ErrorOnThresholdEnabled *bool `json:"error_on_threshold_enabled"`
	ThresholdCount          *int  `json:"threshold_count"`
	ThresholdWindowMinutes  *int  `json:"threshold_window_minutes"`
}

// GetOpenAIOAuthImportDefaults 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) GetOpenAIOAuthImportDefaults(c *gin.Context) {
	settings, err := h.settingService.GetOpenAIOAuthImportDefaults(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, openAIOAuthImportDefaultsToDTO(settings))
}

// UpdateOpenAIOAuthImportDefaults 保留原管理员设置的请求和响应语义。
func (h *RuntimeSettingsHandler) UpdateOpenAIOAuthImportDefaults(c *gin.Context) {
	var raw map[string]json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		httpx.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if raw == nil {
		httpx.BadRequest(c, "request body must be an object")
		return
	}
	if err := validateOpenAIOAuthImportDefaultsRequest(raw); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}

	data, err := json.Marshal(raw)
	if err != nil {
		httpx.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	var req accountdto.OpenAIOAuthImportDefaults
	if err := json.Unmarshal(data, &req); err != nil {
		httpx.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if err := h.settingService.SetOpenAIOAuthImportDefaults(c.Request.Context(), openAIOAuthImportDefaultsFromDTO(&req)); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}

	updatedSettings, err := h.settingService.GetOpenAIOAuthImportDefaults(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, openAIOAuthImportDefaultsToDTO(updatedSettings))
}

// openAIOAuthImportDefaultsFromDTO 仅处理此设置端点的 HTTP 形状。
func openAIOAuthImportDefaultsFromDTO(s *accountdto.OpenAIOAuthImportDefaults) *account.OpenAIOAuthImportDefaults {
	if s == nil {
		return nil
	}
	return &account.OpenAIOAuthImportDefaults{
		Account: account.OpenAIOAuthImportAccountDefaults{
			Notes:              s.Account.Notes,
			Concurrency:        s.Account.Concurrency,
			Priority:           s.Account.Priority,
			RateMultiplier:     s.Account.RateMultiplier,
			ExpiresAt:          s.Account.ExpiresAt,
			AutoPauseOnExpired: s.Account.AutoPauseOnExpired,
		},
		Credentials: s.Credentials,
		Extra:       s.Extra,
	}
}

// openAIOAuthImportDefaultsToDTO 仅处理此设置端点的 HTTP 形状。
func openAIOAuthImportDefaultsToDTO(s *account.OpenAIOAuthImportDefaults) accountdto.OpenAIOAuthImportDefaults {
	if s == nil {
		return accountdto.OpenAIOAuthImportDefaults{}
	}
	return accountdto.OpenAIOAuthImportDefaults{
		Account: accountdto.OpenAIOAuthImportAccountDefaults{
			Notes:              s.Account.Notes,
			Concurrency:        s.Account.Concurrency,
			Priority:           s.Account.Priority,
			RateMultiplier:     s.Account.RateMultiplier,
			ExpiresAt:          s.Account.ExpiresAt,
			AutoPauseOnExpired: s.Account.AutoPauseOnExpired,
		},
		Credentials: s.Credentials,
		Extra:       s.Extra,
	}
}

// openAIOAuthImportRawObject 仅处理此设置端点的 HTTP 形状。
func openAIOAuthImportRawObject(raw map[string]json.RawMessage, section string) (map[string]json.RawMessage, bool, error) {
	value, ok := raw[section]
	if !ok || string(value) == "null" {
		return nil, false, nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("%s must be an object", section)
		}
		return nil, false, err
	}
	return fields, true, nil
}

// rejectOpenAIOAuthImportFields 仅处理此设置端点的 HTTP 形状。
func rejectOpenAIOAuthImportFields(raw map[string]json.RawMessage, section string, forbidden map[string]struct{}) error {
	fields, ok, err := openAIOAuthImportRawObject(raw, section)
	if err != nil || !ok {
		return err
	}
	for key := range fields {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := forbidden[normalized]; ok {
			return fmt.Errorf("%s.%s is not allowed in import defaults", section, key)
		}
	}
	return nil
}

// validateOpenAIOAuthImportDefaultsRequest 仅处理此设置端点的 HTTP 形状。
func validateOpenAIOAuthImportDefaultsRequest(raw map[string]json.RawMessage) error {
	allowedRoot := map[string]struct{}{
		"account":     {},
		"credentials": {},
		"extra":       {},
	}
	for key := range raw {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized != key {
			return fmt.Errorf("%s is not allowed in import defaults", key)
		}
		if _, ok := allowedRoot[normalized]; !ok {
			return fmt.Errorf("%s is not allowed in import defaults", key)
		}
	}

	if err := validateOpenAIOAuthImportObject(raw, "account", map[string]struct{}{
		"notes":                 {},
		"concurrency":           {},
		"priority":              {},
		"rate_multiplier":       {},
		"expires_at":            {},
		"auto_pause_on_expired": {},
	}); err != nil {
		return err
	}
	if err := rejectOpenAIOAuthImportFields(raw, "credentials", map[string]struct{}{
		"access_token":            {},
		"refresh_token":           {},
		"id_token":                {},
		"expires_at":              {},
		"email":                   {},
		"client_id":               {},
		"chatgpt_account_id":      {},
		"chatgpt_user_id":         {},
		"organization_id":         {},
		"plan_type":               {},
		"subscription_expires_at": {},
	}); err != nil {
		return err
	}
	return rejectOpenAIOAuthImportFields(raw, "extra", map[string]struct{}{
		"email": {},
		"name":  {},
	})
}

// validateOpenAIOAuthImportObject 仅处理此设置端点的 HTTP 形状。
func validateOpenAIOAuthImportObject(raw map[string]json.RawMessage, section string, allowed map[string]struct{}) error {
	fields, ok, err := openAIOAuthImportRawObject(raw, section)
	if err != nil || !ok {
		return err
	}
	for key := range fields {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized != key {
			return fmt.Errorf("%s.%s is not allowed in import defaults", section, key)
		}
		if _, ok := allowed[normalized]; !ok {
			return fmt.Errorf("%s.%s is not allowed in import defaults", section, key)
		}
	}
	return nil
}
