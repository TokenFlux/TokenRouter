// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	strconv "strconv"
	strings "strings"
	time "time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	dto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	idempotency "github.com/TokenFlux/TokenRouter/internal/idempotency"
	idempotencyhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

// APIKeyHandler 只持有 Key 用例与路由展示端口；分组算法和容量查询仍由其原所有者提供。
type APIKeyHandler[G any] struct {
	apiKeyService        *apikey.APIKeyService
	groupCapacityService GroupCapacityReader
	presentGroup         func(*routing.Group, *accessview.GroupCapacitySummary) *G
}
type GroupCapacityReader interface {
	GetGroupCapacityByIDs(context.Context, []int64) (map[int64]accessview.GroupCapacitySummary, error)
}

func NewAPIKeyHandler[G any](keys *apikey.APIKeyService, present func(*routing.Group, *accessview.GroupCapacitySummary) *G) *APIKeyHandler[G] {
	return &APIKeyHandler[G]{apiKeyService: keys, presentGroup: present}
}
func (h *APIKeyHandler[G]) SetGroupCapacityService(c GroupCapacityReader) { h.groupCapacityService = c }
func (h *APIKeyHandler[G]) keyResponse(k *apikey.APIKey) *dto.APIKey[G] {
	return dto.APIKeyFromKey(k, func(g *routing.Group) *G { return h.presentGroup(g, nil) })
}

// CreateAPIKeyRequest represents the create API key request payload
type CreateAPIKeyRequest struct {
	Name        string `json:"name" binding:"required"`
	Scope       string `json:"scope" binding:"omitempty,oneof=personal team"`
	GroupID     *int64 `json:"group_id"` // nullable
	IsComposite bool   `json:"is_composite"`
	// CompositeGroups 是复合 Key 的完整分组前缀映射。
	CompositeGroups         []apikey.APIKeyCompositeGroupInput `json:"composite_groups"`
	CustomKey               *string                            `json:"custom_key"`       // 可选的自定义key
	IPWhitelist             []string                           `json:"ip_whitelist"`     // IP 白名单
	IPBlacklist             []string                           `json:"ip_blacklist"`     // IP 黑名单
	FastModePolicy          string                             `json:"fast_mode_policy"` // Fast 模式策略，空值表示跟随请求
	BillingMode             string                             `json:"billing_mode"`     // 结算模式，空值表示自动选择
	PreferredSubscriptionID *int64                             `json:"preferred_subscription_id"`
	ModelMapping            map[string]string                  `json:"model_mapping"`   // 当前 Key 的完整模型重定向规则
	Quota                   *float64                           `json:"quota"`           // 配额限制 (USD)
	ExpiresInDays           *int                               `json:"expires_in_days"` // 过期天数

	// Rate limit fields (0 = unlimited)
	RateLimit5h *float64 `json:"rate_limit_5h"`
	RateLimit1d *float64 `json:"rate_limit_1d"`
	RateLimit7d *float64 `json:"rate_limit_7d"`
	// 绑定分组不可用时是否自动回退到同平台默认分组，nil 表示使用服务层默认值。
	FallbackToDefaultGroupWhenUnavailable *bool `json:"fallback_to_default_group_when_unavailable"`
}

// UpdateAPIKeyRequest represents the update API key request payload
type UpdateAPIKeyRequest struct {
	Name        string `json:"name"`
	GroupID     *int64 `json:"group_id"`
	IsComposite *bool  `json:"is_composite"`
	// CompositeGroups 非 nil 时完整替换复合映射。
	CompositeGroups         *[]apikey.APIKeyCompositeGroupInput `json:"composite_groups"`
	Status                  string                              `json:"status" binding:"omitempty,oneof=active inactive"`
	IPWhitelist             *[]string                           `json:"ip_whitelist"`     // IP 白名单（nil 不修改，空数组清空）
	IPBlacklist             *[]string                           `json:"ip_blacklist"`     // IP 黑名单（nil 不修改，空数组清空）
	FastModePolicy          *string                             `json:"fast_mode_policy"` // nil 表示保持原配置
	BillingMode             *string                             `json:"billing_mode"`     // nil 表示保持原配置
	PreferredSubscriptionID *int64                              `json:"preferred_subscription_id"`
	ModelMapping            *map[string]string                  `json:"model_mapping"` // nil 不修改，空对象清空
	Quota                   *float64                            `json:"quota"`         // 配额限制 (USD), 0=无限制
	ExpiresAt               *string                             `json:"expires_at"`    // 过期时间 (ISO 8601)
	ResetQuota              *bool                               `json:"reset_quota"`   // 重置已用配额

	// Rate limit fields (nil = no change, 0 = unlimited)
	RateLimit5h         *float64 `json:"rate_limit_5h"`
	RateLimit1d         *float64 `json:"rate_limit_1d"`
	RateLimit7d         *float64 `json:"rate_limit_7d"`
	ResetRateLimitUsage *bool    `json:"reset_rate_limit_usage"` // 重置限速用量
	// nil 表示保持原配置不变。
	FallbackToDefaultGroupWhenUnavailable *bool `json:"fallback_to_default_group_when_unavailable"`
}

type ApiKeyLimitInput struct {
	field string
	value *float64
}

// ValidateAPIKeyLimitFields 对 HTTP 请求中显式提供的限额执行服务层统一校验。
func ValidateAPIKeyLimitFields(limits ...ApiKeyLimitInput) error {
	for _, limit := range limits {
		if limit.value == nil {
			continue
		}
		if err := apikey.ValidateAPIKeyLimit(limit.field, *limit.value); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAPIKeyCreateRequest 校验创建请求中的限额与相对有效期。
func ValidateAPIKeyCreateRequest(req CreateAPIKeyRequest) error {
	if err := ValidateAPIKeyLimitFields(
		ApiKeyLimitInput{field: "quota", value: req.Quota},
		ApiKeyLimitInput{field: "rate_limit_5h", value: req.RateLimit5h},
		ApiKeyLimitInput{field: "rate_limit_1d", value: req.RateLimit1d},
		ApiKeyLimitInput{field: "rate_limit_7d", value: req.RateLimit7d},
	); err != nil {
		return err
	}
	if req.ExpiresInDays != nil {
		return apikey.ValidateAPIKeyExpiresInDays(*req.ExpiresInDays)
	}
	return nil
}

// ValidateAPIKeyUpdateRequest 只校验更新请求中实际出现的限额字段。
func ValidateAPIKeyUpdateRequest(req UpdateAPIKeyRequest) error {
	return ValidateAPIKeyLimitFields(
		ApiKeyLimitInput{field: "quota", value: req.Quota},
		ApiKeyLimitInput{field: "rate_limit_5h", value: req.RateLimit5h},
		ApiKeyLimitInput{field: "rate_limit_1d", value: req.RateLimit1d},
		ApiKeyLimitInput{field: "rate_limit_7d", value: req.RateLimit7d},
	)
}

// APIKeyBillingSubscriptionOptionResponse 是前端选择指定订阅时使用的安全摘要。
type APIKeyBillingSubscriptionOptionResponse struct {
	ID               int64     `json:"id"`
	PlanID           int64     `json:"plan_id"`
	PlanName         string    `json:"plan_name"`
	ExpiresAt        time.Time `json:"expires_at"`
	GroupsRestricted bool      `json:"groups_restricted"`
	ApplicableGroups []int64   `json:"applicable_groups"`
}

// List handles listing user's API keys with pagination
// GET /api/v1/api-keys
func (h *APIKeyHandler[G]) List(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
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
	var filters apikey.APIKeyListFilters
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		if len(search) > 100 {
			search = search[:100]
		}
		filters.Search = search
	}
	filters.Status = c.Query("status")
	filters.Scope = strings.ToLower(strings.TrimSpace(c.Query("scope")))
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

	out := make([]dto.APIKey[G], 0, len(keys))
	for i := range keys {
		out = append(out, *h.keyResponse(&keys[i]))
	}
	response.Paginated(c, out, result.Total, page, pageSize)
}

// GetByID handles getting a single API key
// GET /api/v1/api-keys/:id
func (h *APIKeyHandler[G]) GetByID(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
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
		response.ErrorFrom(c, apikey.ErrAPIKeyNotFound)
		return
	}

	response.Success(c, h.keyResponse(key))
}

// Create handles creating a new API key
// POST /api/v1/api-keys
func (h *APIKeyHandler[G]) Create(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req CreateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := ValidateAPIKeyCreateRequest(req); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	svcReq := apikey.CreateAPIKeyRequest{
		Name:                                  req.Name,
		Scope:                                 req.Scope,
		GroupID:                               req.GroupID,
		IsComposite:                           req.IsComposite,
		CompositeGroups:                       req.CompositeGroups,
		CustomKey:                             req.CustomKey,
		IPWhitelist:                           req.IPWhitelist,
		IPBlacklist:                           req.IPBlacklist,
		FastModePolicy:                        req.FastModePolicy,
		BillingMode:                           req.BillingMode,
		PreferredSubscriptionID:               req.PreferredSubscriptionID,
		ModelMapping:                          req.ModelMapping,
		ExpiresInDays:                         req.ExpiresInDays,
		FallbackToDefaultGroupWhenUnavailable: req.FallbackToDefaultGroupWhenUnavailable,
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

	idempotencyhttp.ExecuteUserIdempotentJSON(c, "user.api_keys.create", req, idempotency.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		key, err := h.apiKeyService.Create(ctx, subject.UserID, svcReq)
		if err != nil {
			return nil, err
		}
		return h.keyResponse(key), nil
	})
}

// Update handles updating an API key
// PUT /api/v1/api-keys/:id
func (h *APIKeyHandler[G]) Update(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
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
	if err := ValidateAPIKeyUpdateRequest(req); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	svcReq := apikey.UpdateAPIKeyRequest{
		IsComposite:                           req.IsComposite,
		CompositeGroups:                       req.CompositeGroups,
		IPWhitelist:                           req.IPWhitelist,
		IPBlacklist:                           req.IPBlacklist,
		FastModePolicy:                        req.FastModePolicy,
		BillingMode:                           req.BillingMode,
		PreferredSubscriptionID:               req.PreferredSubscriptionID,
		ModelMapping:                          req.ModelMapping,
		Quota:                                 req.Quota,
		ResetQuota:                            req.ResetQuota,
		RateLimit5h:                           req.RateLimit5h,
		RateLimit1d:                           req.RateLimit1d,
		RateLimit7d:                           req.RateLimit7d,
		ResetRateLimitUsage:                   req.ResetRateLimitUsage,
		FallbackToDefaultGroupWhenUnavailable: req.FallbackToDefaultGroupWhenUnavailable,
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

	response.Success(c, h.keyResponse(key))
}

// Delete handles deleting an API key
// DELETE /api/v1/api-keys/:id
func (h *APIKeyHandler[G]) Delete(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
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
func (h *APIKeyHandler[G]) GetAvailableGroups(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var subscriptionID *int64
	if rawSubscriptionID := strings.TrimSpace(c.Query("subscription_id")); rawSubscriptionID != "" {
		parsedID, err := strconv.ParseInt(rawSubscriptionID, 10, 64)
		if err != nil || parsedID <= 0 {
			response.BadRequest(c, "Invalid subscription ID")
			return
		}
		subscriptionID = &parsedID
	}

	groups, err := h.apiKeyService.GetAvailableGroupsForScopeWithSubscription(
		c.Request.Context(),
		subject.UserID,
		c.DefaultQuery("scope", "personal"),
		subscriptionID,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]G, 0, len(groups))
	capacityMap := h.getAvailableGroupCapacityMap(c.Request.Context(), groups)
	for i := range groups {
		var capacity *accessview.GroupCapacitySummary
		if value, ok := capacityMap[groups[i].ID]; ok {
			capacity = &value
		}
		groupDTO := h.presentGroup(&groups[i], capacity)
		out = append(out, *groupDTO)
	}
	response.Success(c, out)
}

// GetBillingOptions 返回当前作用域可用于指定结算的订阅列表。
// GET /api/v1/keys/billing-options?scope=personal|team
func (h *APIKeyHandler[G]) GetBillingOptions(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	options, err := h.apiKeyService.ListBillingSubscriptionsForScope(
		c.Request.Context(),
		subject.UserID,
		c.DefaultQuery("scope", "personal"),
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]APIKeyBillingSubscriptionOptionResponse, 0, len(options))
	for i := range options {
		option := options[i]
		out = append(out, APIKeyBillingSubscriptionOptionResponse{
			ID:               option.ID,
			PlanID:           option.PlanID,
			PlanName:         option.PlanName,
			ExpiresAt:        option.ExpiresAt,
			GroupsRestricted: option.GroupsRestricted,
			ApplicableGroups: append([]int64(nil), option.ApplicableGroups...),
		})
	}
	response.Success(c, out)
}

func (h *APIKeyHandler[G]) getAvailableGroupCapacityMap(ctx context.Context, groups []routing.Group) map[int64]accessview.GroupCapacitySummary {
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
func (h *APIKeyHandler[G]) GetUserGroupRates(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	rates, err := h.apiKeyService.GetUserGroupRatesForScope(c.Request.Context(), subject.UserID, c.DefaultQuery("scope", "personal"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, rates)
}
