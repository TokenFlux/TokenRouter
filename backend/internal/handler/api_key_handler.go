// Package handler provides HTTP request handlers for the application.
package handler

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/handler/dto"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/response"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

// APIKeyHandler handles API key-related requests
type APIKeyHandler struct {
	apiKeyService        *service.APIKeyService
	groupCapacityService *service.GroupCapacityService
}

// NewAPIKeyHandler creates a new APIKeyHandler
func NewAPIKeyHandler(apiKeyService *service.APIKeyService) *APIKeyHandler {
	return &APIKeyHandler{
		apiKeyService: apiKeyService,
	}
}

// SetGroupCapacityService 注入分组容量服务，用于用户侧分组选择展示聚合负载。
func (h *APIKeyHandler) SetGroupCapacityService(groupCapacityService *service.GroupCapacityService) {
	h.groupCapacityService = groupCapacityService
}

// CreateAPIKeyRequest represents the create API key request payload
type CreateAPIKeyRequest struct {
	Name           string   `json:"name" binding:"required"`
	GroupID        *int64   `json:"group_id"`         // nullable
	CustomKey      *string  `json:"custom_key"`       // 可选的自定义key
	IPWhitelist    []string `json:"ip_whitelist"`     // IP 白名单
	IPBlacklist    []string `json:"ip_blacklist"`     // IP 黑名单
	FastModePolicy string   `json:"fast_mode_policy"` // Fast 模式策略，空值表示跟随请求
	Quota          *float64 `json:"quota"`            // 配额限制 (USD)
	ExpiresInDays  *int     `json:"expires_in_days"`  // 过期天数

	// Rate limit fields (0 = unlimited)
	RateLimit5h *float64 `json:"rate_limit_5h"`
	RateLimit1d *float64 `json:"rate_limit_1d"`
	RateLimit7d *float64 `json:"rate_limit_7d"`
	// 绑定分组不可用时是否自动回退到同平台默认分组，nil 表示使用服务层默认值。
	FallbackToDefaultGroupWhenUnavailable *bool `json:"fallback_to_default_group_when_unavailable"`
	// 创建时直接选择数据共享分组也必须由弹窗确认。
	DataSharingConfirmed     bool `json:"data_sharing_confirmed"`
	DataSharingNoticeVersion int  `json:"data_sharing_notice_version"`
}

// UpdateAPIKeyRequest represents the update API key request payload
type UpdateAPIKeyRequest struct {
	Name           string   `json:"name"`
	GroupID        *int64   `json:"group_id"`
	Status         string   `json:"status" binding:"omitempty,oneof=active inactive"`
	IPWhitelist    []string `json:"ip_whitelist"`     // IP 白名单
	IPBlacklist    []string `json:"ip_blacklist"`     // IP 黑名单
	FastModePolicy *string  `json:"fast_mode_policy"` // nil 表示保持原配置
	Quota          *float64 `json:"quota"`            // 配额限制 (USD), 0=无限制
	ExpiresAt      *string  `json:"expires_at"`       // 过期时间 (ISO 8601)
	ResetQuota     *bool    `json:"reset_quota"`      // 重置已用配额

	// Rate limit fields (nil = no change, 0 = unlimited)
	RateLimit5h         *float64 `json:"rate_limit_5h"`
	RateLimit1d         *float64 `json:"rate_limit_1d"`
	RateLimit7d         *float64 `json:"rate_limit_7d"`
	ResetRateLimitUsage *bool    `json:"reset_rate_limit_usage"` // 重置限速用量
	// nil 表示保持原配置不变。
	FallbackToDefaultGroupWhenUnavailable *bool `json:"fallback_to_default_group_when_unavailable"`
	// 数据共享确认字段：倒计时弹窗确认后由前端传入。
	DataSharingConfirmed     bool `json:"data_sharing_confirmed"`
	DataSharingNoticeVersion int  `json:"data_sharing_notice_version"`
}

// List handles listing user's API keys with pagination
// GET /api/v1/api-keys
func (h *APIKeyHandler) List(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "created_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}

	// Parse filter parameters
	var filters service.APIKeyListFilters
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		if len(search) > 100 {
			search = search[:100]
		}
		filters.Search = search
	}
	filters.Status = c.Query("status")
	if groupIDStr := c.Query("group_id"); groupIDStr != "" {
		gid, err := strconv.ParseInt(groupIDStr, 10, 64)
		if err == nil {
			filters.GroupID = &gid
		}
	}

	keys, result, err := h.apiKeyService.List(c.Request.Context(), subject.UserID, params, filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.APIKey, 0, len(keys))
	for i := range keys {
		out = append(out, *dto.APIKeyFromService(&keys[i]))
	}
	response.Paginated(c, out, result.Total, page, pageSize)
}

// GetByID handles getting a single API key
// GET /api/v1/api-keys/:id
func (h *APIKeyHandler) GetByID(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid key ID")
		return
	}

	key, err := h.apiKeyService.GetByID(c.Request.Context(), keyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// 验证所有权
	if key.UserID != subject.UserID {
		response.ErrorFrom(c, service.ErrAPIKeyNotFound)
		return
	}

	response.Success(c, dto.APIKeyFromService(key))
}

// Create handles creating a new API key
// POST /api/v1/api-keys
func (h *APIKeyHandler) Create(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req CreateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	svcReq := service.CreateAPIKeyRequest{
		Name:                                  req.Name,
		GroupID:                               req.GroupID,
		CustomKey:                             req.CustomKey,
		IPWhitelist:                           req.IPWhitelist,
		IPBlacklist:                           req.IPBlacklist,
		FastModePolicy:                        req.FastModePolicy,
		ExpiresInDays:                         req.ExpiresInDays,
		FallbackToDefaultGroupWhenUnavailable: req.FallbackToDefaultGroupWhenUnavailable,
		DataSharingConfirmed:                  req.DataSharingConfirmed,
		DataSharingNoticeVersion:              req.DataSharingNoticeVersion,
	}
	if req.Quota != nil {
		svcReq.Quota = *req.Quota
	}
	if req.RateLimit5h != nil {
		svcReq.RateLimit5h = *req.RateLimit5h
	}
	if req.RateLimit1d != nil {
		svcReq.RateLimit1d = *req.RateLimit1d
	}
	if req.RateLimit7d != nil {
		svcReq.RateLimit7d = *req.RateLimit7d
	}

	executeUserIdempotentJSON(c, "user.api_keys.create", req, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		key, err := h.apiKeyService.Create(ctx, subject.UserID, svcReq)
		if err != nil {
			return nil, err
		}
		return dto.APIKeyFromService(key), nil
	})
}

// Update handles updating an API key
// PUT /api/v1/api-keys/:id
func (h *APIKeyHandler) Update(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid key ID")
		return
	}

	var req UpdateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	svcReq := service.UpdateAPIKeyRequest{
		IPWhitelist:                           req.IPWhitelist,
		IPBlacklist:                           req.IPBlacklist,
		FastModePolicy:                        req.FastModePolicy,
		Quota:                                 req.Quota,
		ResetQuota:                            req.ResetQuota,
		RateLimit5h:                           req.RateLimit5h,
		RateLimit1d:                           req.RateLimit1d,
		RateLimit7d:                           req.RateLimit7d,
		ResetRateLimitUsage:                   req.ResetRateLimitUsage,
		FallbackToDefaultGroupWhenUnavailable: req.FallbackToDefaultGroupWhenUnavailable,
		DataSharingConfirmed:                  req.DataSharingConfirmed,
		DataSharingNoticeVersion:              req.DataSharingNoticeVersion,
	}
	if req.Name != "" {
		svcReq.Name = &req.Name
	}
	svcReq.GroupID = req.GroupID
	if req.Status != "" {
		svcReq.Status = &req.Status
	}
	// Parse expires_at if provided
	if req.ExpiresAt != nil {
		if *req.ExpiresAt == "" {
			// Empty string means clear expiration
			svcReq.ExpiresAt = nil
			svcReq.ClearExpiration = true
		} else {
			t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				response.BadRequest(c, "Invalid expires_at format: "+err.Error())
				return
			}
			svcReq.ExpiresAt = &t
		}
	}

	key, err := h.apiKeyService.Update(c.Request.Context(), keyID, subject.UserID, svcReq)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.APIKeyFromService(key))
}

// Delete handles deleting an API key
// DELETE /api/v1/api-keys/:id
func (h *APIKeyHandler) Delete(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid key ID")
		return
	}

	err = h.apiKeyService.Delete(c.Request.Context(), keyID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "API key deleted successfully"})
}

// GetAvailableGroups 获取用户可以绑定的分组列表
// GET /api/v1/groups/available
func (h *APIKeyHandler) GetAvailableGroups(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	groups, err := h.apiKeyService.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.Group, 0, len(groups))
	capacityMap := h.getAvailableGroupCapacityMap(c.Request.Context(), groups)
	for i := range groups {
		groupDTO := dto.GroupFromService(&groups[i])
		if capacity, ok := capacityMap[groups[i].ID]; ok {
			groupDTO.Capacity = dto.GroupCapacityFromService(&capacity)
		}
		out = append(out, *groupDTO)
	}
	response.Success(c, out)
}

func (h *APIKeyHandler) getAvailableGroupCapacityMap(ctx context.Context, groups []service.Group) map[int64]service.GroupCapacitySummary {
	if h.groupCapacityService == nil || len(groups) == 0 {
		return nil
	}
	groupIDs := make([]int64, 0, len(groups))
	for i := range groups {
		groupIDs = append(groupIDs, groups[i].ID)
	}

	// 容量只作为分组选项的辅助负载信息，失败时保持原分组列表可用。
	capacityMap, err := h.groupCapacityService.GetGroupCapacityByIDs(ctx, groupIDs)
	if err != nil {
		return nil
	}
	return capacityMap
}

// GetUserGroupRates 获取当前用户的专属分组倍率配置
// GET /api/v1/groups/rates
func (h *APIKeyHandler) GetUserGroupRates(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	rates, err := h.apiKeyService.GetUserGroupRates(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, rates)
}
