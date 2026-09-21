// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	strconv "strconv"
	strings "strings"

	billingdto "github.com/TokenFlux/TokenRouter/internal/billing/httpapi/dto"
	idempotency "github.com/TokenFlux/TokenRouter/internal/idempotency"
	idempotencyhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	dto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

// UserAdministration 是管理员 HTTP 实际消费的身份用例接口。
type UserAdministration interface {
	ListUsers(ctx context.Context, page, pageSize int, filters identity.UserListFilters, sortBy, sortOrder string) ([]identity.User, int64, error)
	GetUser(ctx context.Context, id int64) (*identity.User, error)
	GetUserIncludeDeleted(ctx context.Context, id int64) (*identity.User, error)
	CreateUser(ctx context.Context, input *identity.CreateUserInput) (*identity.User, error)
	UpdateUser(ctx context.Context, id int64, input *identity.UpdateUserInput) (*identity.User, error)
	DeleteUser(ctx context.Context, id int64) error
	BatchUpdateConcurrency(ctx context.Context, userIDs []int64, value int, mode string) (int, error)
	BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error)
	UpdateUserBalance(ctx context.Context, userID int64, balance float64, operation string, notes string) (*identity.User, error)
	GetUserRPMStatus(ctx context.Context, userID int64) (*identity.UserRPMStatus, error)
	GetUserUsageStats(ctx context.Context, userID int64, period string) (any, error)
	GetUserBalanceHistory(ctx context.Context, userID int64, page, pageSize int, codeType string) ([]identity.RedeemCode, int64, float64, error)
	BindUserAuthIdentity(ctx context.Context, userID int64, input identity.AdminBindAuthIdentityInput) (*identity.AdminBoundAuthIdentity, error)
	ReplaceUserGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (*identity.ReplaceUserGroupResult, error)
}

// AdminUserHandler 不接收旧聚合管理服务；Key 展示和并发读取由独立端口提供。
type AdminUserHandler[K any] struct {
	adminService UserAdministration
	listKeys     func(context.Context, int64, int, int, string, string) ([]K, int64, error)
	concurrency  func(context.Context, []identity.User) (map[int64]int, error)
	stepUp       func(*gin.Context) bool
}

func NewAdminUserHandler[K any](users UserAdministration, keys func(context.Context, int64, int, int, string, string) ([]K, int64, error), concurrency func(context.Context, []identity.User) (map[int64]int, error), stepUp func(*gin.Context) bool) *AdminUserHandler[K] {
	return &AdminUserHandler[K]{users, keys, concurrency, stepUp}
}
func (h *AdminUserHandler[K]) userResponse(u *identity.User) *dto.AdminUser[K] {
	return dto.AdminUserFromIdentity[K](u, nil)
}
func adminID(c *gin.Context) int64 {
	s, ok := GetAuthSubjectFromContext(c)
	if !ok {
		return 0
	}
	return s.UserID
}

// UserWithConcurrency wraps AdminUser with current concurrency info
type UserWithConcurrency[K any] struct {
	dto.AdminUser[K]
	CurrentConcurrency int `json:"current_concurrency"`
}

// CreateUserRequest represents admin create user request
type CreateUserRequest struct {
	Email         string   `json:"email" binding:"required,email"`
	Password      string   `json:"password" binding:"required,min=6"`
	Username      string   `json:"username"`
	Notes         string   `json:"notes"`
	Role          string   `json:"role" binding:"omitempty,oneof=admin user"`
	Balance       *float64 `json:"balance"`
	Concurrency   int      `json:"concurrency"`
	RPMLimit      int      `json:"rpm_limit"`
	APIKeyLimit   *int     `json:"api_key_limit" binding:"omitempty,gte=0,lte=2147483647"`
	AllowedGroups []int64  `json:"allowed_groups"`
	// DisabledPublicGroups 记录该用户被禁止使用的公开分组。
	DisabledPublicGroups []int64 `json:"disabled_public_groups"`
}

// UpdateUserRequest represents admin update user request
// 使用指针类型来区分"未提供"和"设置为0"
type UpdateUserRequest struct {
	Email         string   `json:"email" binding:"omitempty,email"`
	Password      string   `json:"password" binding:"omitempty,min=6"`
	Username      *string  `json:"username"`
	Notes         *string  `json:"notes"`
	Role          string   `json:"role" binding:"omitempty,oneof=admin user"`
	Balance       *float64 `json:"balance"`
	Concurrency   *int     `json:"concurrency"`
	RPMLimit      *int     `json:"rpm_limit"`
	APIKeyLimit   *int     `json:"api_key_limit" binding:"omitempty,gte=0,lte=2147483647"`
	Status        string   `json:"status" binding:"omitempty,oneof=active disabled"`
	AllowedGroups *[]int64 `json:"allowed_groups"`
	// DisabledPublicGroups 使用指针区分未提供和清空禁用列表。
	DisabledPublicGroups *[]int64 `json:"disabled_public_groups"`
	// GroupRates 用户专属分组倍率配置
	// map[groupID]*rate，nil 表示删除该分组的专属倍率
	GroupRates map[int64]*float64 `json:"group_rates"`
}

// UpdateBalanceRequest represents balance update request
type UpdateBalanceRequest struct {
	Balance   float64 `json:"balance" binding:"required,gt=0"`
	Operation string  `json:"operation" binding:"required,oneof=set add subtract"`
	Notes     string  `json:"notes"`
}

// BatchUpdateConcurrencyRequest 表示管理员批量修改用户并发数的请求。
type BatchUpdateConcurrencyRequest struct {
	UserIDs     []int64 `json:"user_ids"`
	All         bool    `json:"all"`
	Concurrency int     `json:"concurrency"`
	Mode        string  `json:"mode" binding:"required,oneof=set add"`
}

type BindUserAuthIdentityRequest struct {
	ProviderType    string                              `json:"provider_type"`
	ProviderKey     string                              `json:"provider_key"`
	ProviderSubject string                              `json:"provider_subject"`
	Issuer          *string                             `json:"issuer"`
	Metadata        map[string]any                      `json:"metadata"`
	Channel         *BindUserAuthIdentityChannelRequest `json:"channel"`
}

type BindUserAuthIdentityChannelRequest struct {
	Channel        string         `json:"channel"`
	ChannelAppID   string         `json:"channel_app_id"`
	ChannelSubject string         `json:"channel_subject"`
	Metadata       map[string]any `json:"metadata"`
}

// List handles listing all users with pagination
// GET /api/v1/admin/users
// Query params:
//   - status: filter by user status
//   - role: filter by user role
//   - search: search in email, username
//   - attr[{id}]: filter by custom attribute value, e.g. attr[1]=company
//   - group_name: fuzzy filter by allowed group name
//   - api_key_group_id: 按用户 API Key 绑定的精确分组过滤
func (h *AdminUserHandler[K]) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)

	search := c.Query("search")
	// 标准化和验证 search 参数
	search = strings.TrimSpace(search)
	if runes := []rune(search); len(runes) > 100 {
		search = string(runes[:100])
	}

	filters := identity.UserListFilters{
		Status:     c.Query("status"),
		Role:       c.Query("role"),
		Search:     search,
		GroupName:  strings.TrimSpace(c.Query("group_name")),
		Attributes: ParseAttributeFilters(c),
	}
	if raw := strings.TrimSpace(c.Query("api_key_group_id")); raw != "" {
		if id, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil && id > 0 {
			filters.APIKeyGroupID = id
		}
	}
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "desc")
	if raw, ok := c.GetQuery("include_subscriptions"); ok {
		includeSubscriptions := response.ParseBoolQueryWithDefault(raw, true)
		filters.IncludeSubscriptions = &includeSubscriptions
	}

	users, total, err := h.adminService.ListUsers(c.Request.Context(), page, pageSize, filters, sortBy, sortOrder)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Batch get current concurrency (nil map if unavailable)
	var loadInfo map[int64]int
	if len(users) > 0 && h.concurrency != nil {
		loadInfo, _ = h.concurrency(c.Request.Context(), users)
	}

	// Build response with concurrency info
	out := make([]UserWithConcurrency[K], len(users))
	for i := range users {
		out[i] = UserWithConcurrency[K]{
			AdminUser: *h.userResponse(&users[i]),
		}
		out[i].CurrentConcurrency = loadInfo[users[i].ID]
	}

	response.Paginated(c, out, total, page, pageSize)
}

// ParseAttributeFilters extracts attribute filters from query params
// Format: attr[{attributeID}]=value, e.g. attr[1]=company&attr[2]=developer
func ParseAttributeFilters(c *gin.Context) map[int64]string {
	result := make(map[int64]string)

	// Get all query params and look for attr[*] pattern
	for key, values := range c.Request.URL.Query() {
		if len(values) == 0 || values[0] == "" {
			continue
		}
		// Check if key matches pattern attr[{id}]
		if len(key) > 5 && key[:5] == "attr[" && key[len(key)-1] == ']' {
			idStr := key[5 : len(key)-1]
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err == nil && id > 0 {
				result[id] = values[0]
			}
		}
	}

	return result
}

// GetByID handles getting a user by ID
// GET /api/v1/admin/users/:id
func (h *AdminUserHandler[K]) GetByID(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var user *identity.User
	if c.Query("include_deleted") == "true" {
		user, err = h.adminService.GetUserIncludeDeleted(c.Request.Context(), userID)
	} else {
		user, err = h.adminService.GetUser(c.Request.Context(), userID)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, h.userResponse(user))
}

// BindAuthIdentity manually binds a canonical auth identity to a user.
// POST /api/v1/admin/users/:id/auth-identities
func (h *AdminUserHandler[K]) BindAuthIdentity(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var req BindUserAuthIdentityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	input := identity.AdminBindAuthIdentityInput{
		ProviderType:    req.ProviderType,
		ProviderKey:     req.ProviderKey,
		ProviderSubject: req.ProviderSubject,
		Issuer:          req.Issuer,
		Metadata:        req.Metadata,
	}
	if req.Channel != nil {
		input.Channel = &identity.AdminBindAuthIdentityChannelInput{
			Channel:        req.Channel.Channel,
			ChannelAppID:   req.Channel.ChannelAppID,
			ChannelSubject: req.Channel.ChannelSubject,
			Metadata:       req.Channel.Metadata,
		}
	}

	result, err := h.adminService.BindUserAuthIdentity(c.Request.Context(), userID, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// Create handles creating a new user
// POST /api/v1/admin/users
func (h *AdminUserHandler[K]) Create(c *gin.Context) {
	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// 创建管理员账号属权限敏感操作：需最近完成 step-up 2FA 验证。
	if req.Role == identity.RoleAdmin {
		if !h.stepUp(c) {
			return
		}
	}

	user, err := h.adminService.CreateUser(c.Request.Context(), &identity.CreateUserInput{
		Email:                req.Email,
		Password:             req.Password,
		Username:             req.Username,
		Notes:                req.Notes,
		Role:                 req.Role,
		Balance:              req.Balance,
		Concurrency:          req.Concurrency,
		RPMLimit:             req.RPMLimit,
		APIKeyLimit:          req.APIKeyLimit,
		AllowedGroups:        req.AllowedGroups,
		DisabledPublicGroups: req.DisabledPublicGroups,
		ActorAdminID:         adminID(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, h.userResponse(user))
}

// Update handles updating a user
// PUT /api/v1/admin/users/:id
func (h *AdminUserHandler[K]) Update(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var req UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// 防锁死保护：管理员不能把自己降级为普通用户(单管理员场景下会失去后台访问权)。
	// 与既有"不能禁用/删除 admin"保护一致。降级其他管理员仍然允许。
	if req.Role == identity.RoleUser && userID == adminID(c) {
		response.BadRequest(c, "cannot demote yourself from admin")
		return
	}

	// 显式设置管理员角色属于权限敏感操作，必须执行 step-up；前端在角色未变化时不发送该字段。
	// 这里不先读取目标角色，避免目标被并发降级后又在无验证情况下重新提升的竞态窗口。
	if req.Role == identity.RoleAdmin {
		if !h.stepUp(c) {
			return
		}
	}

	// 使用指针类型直接传递，nil 表示未提供该字段
	user, err := h.adminService.UpdateUser(c.Request.Context(), userID, &identity.UpdateUserInput{
		Email:                req.Email,
		Password:             req.Password,
		Username:             req.Username,
		Notes:                req.Notes,
		Role:                 req.Role,
		Balance:              req.Balance,
		Concurrency:          req.Concurrency,
		RPMLimit:             req.RPMLimit,
		APIKeyLimit:          req.APIKeyLimit,
		Status:               req.Status,
		AllowedGroups:        req.AllowedGroups,
		DisabledPublicGroups: req.DisabledPublicGroups,
		GroupRates:           req.GroupRates,
		ActorAdminID:         adminID(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, h.userResponse(user))
}

// Delete handles deleting a user
// DELETE /api/v1/admin/users/:id
func (h *AdminUserHandler[K]) Delete(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	err = h.adminService.DeleteUser(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "User deleted successfully"})
}

// UpdateBalance handles updating user balance
// POST /api/v1/admin/users/:id/balance
func (h *AdminUserHandler[K]) UpdateBalance(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var req UpdateBalanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	idempotencyPayload := struct {
		UserID int64                `json:"user_id"`
		Body   UpdateBalanceRequest `json:"body"`
	}{
		UserID: userID,
		Body:   req,
	}
	idempotencyhttp.ExecuteAdminIdempotentJSON(c, "admin.users.balance.update", idempotencyPayload, idempotency.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		user, execErr := h.adminService.UpdateUserBalance(ctx, userID, req.Balance, req.Operation, req.Notes)
		if execErr != nil {
			return nil, execErr
		}
		return h.userResponse(user), nil
	})
}

// BatchUpdateConcurrency 批量修改用户并发数。
// POST /api/v1/admin/users/batch-concurrency
func (h *AdminUserHandler[K]) BatchUpdateConcurrency(c *gin.Context) {
	var req BatchUpdateConcurrencyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if !req.All && len(req.UserIDs) == 0 {
		response.BadRequest(c, "user_ids is required unless all=true")
		return
	}
	if len(req.UserIDs) > 500 {
		response.BadRequest(c, "user_ids cannot exceed 500")
		return
	}

	var userIDs []int64
	if req.All {
		page := 1
		const pageSize = 500
		for {
			users, _, err := h.adminService.ListUsers(c.Request.Context(), page, pageSize, identity.UserListFilters{}, "id", "asc")
			if err != nil {
				response.ErrorFrom(c, err)
				return
			}
			for _, user := range users {
				userIDs = append(userIDs, user.ID)
			}
			if len(users) < pageSize {
				break
			}
			page++
		}
	} else {
		userIDs = req.UserIDs
	}

	if len(userIDs) == 0 {
		response.Success(c, gin.H{"affected": 0})
		return
	}

	affected, err := h.adminService.BatchUpdateConcurrency(c.Request.Context(), userIDs, req.Concurrency, req.Mode)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"affected": affected})
}

// GetUserAPIKeys handles getting user's API keys
// GET /api/v1/admin/users/:id/api-keys
func (h *AdminUserHandler[K]) GetUserAPIKeys(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	page, pageSize := response.ParsePagination(c)
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "desc")

	keys, total, err := h.listKeys(c.Request.Context(), userID, page, pageSize, sortBy, sortOrder)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Paginated(c, keys, total, page, pageSize)
}

// GetUserUsage handles getting user's usage statistics
// GET /api/v1/admin/users/:id/usage
func (h *AdminUserHandler[K]) GetUserUsage(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	period := c.DefaultQuery("period", "month")

	stats, err := h.adminService.GetUserUsageStats(c.Request.Context(), userID, period)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, stats)
}

// GetBalanceHistory handles getting user's balance/concurrency change history
// GET /api/v1/admin/users/:id/balance-history
// Query params:
//   - type: filter by record type (balance, admin_balance, concurrency, admin_concurrency, subscription)
func (h *AdminUserHandler[K]) GetBalanceHistory(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	page, pageSize := response.ParsePagination(c)
	codeType := c.Query("type")

	codes, total, totalRecharged, err := h.adminService.GetUserBalanceHistory(c.Request.Context(), userID, page, pageSize, codeType)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Convert to admin DTO (includes notes field for admin visibility)
	out := make([]billingdto.AdminRedeemCode, 0, len(codes))
	for i := range codes {
		out = append(out, *billingdto.RedeemCodeFromServiceAdmin(&codes[i]))
	}

	// Custom response with total_recharged alongside pagination
	pages := int((total + int64(pageSize) - 1) / int64(pageSize))
	if pages < 1 {
		pages = 1
	}
	response.Success(c, gin.H{
		"items":           out,
		"total":           total,
		"page":            page,
		"page_size":       pageSize,
		"pages":           pages,
		"total_recharged": totalRecharged,
	})
}

// ReplaceGroupRequest represents the request to replace a user's exclusive group
type ReplaceGroupRequest struct {
	OldGroupID int64 `json:"old_group_id" binding:"required,gt=0"`
	NewGroupID int64 `json:"new_group_id" binding:"required,gt=0"`
}

// ReplaceGroup handles replacing a user's exclusive group
// POST /api/v1/admin/users/:id/replace-group
func (h *AdminUserHandler[K]) ReplaceGroup(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var req ReplaceGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	result, err := h.adminService.ReplaceUserGroup(c.Request.Context(), userID, req.OldGroupID, req.NewGroupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{
		"migrated_keys": result.MigratedKeys,
	})
}

// GetUserRPMStatus 返回指定用户当前分钟的 RPM 用量
// GET /api/v1/admin/users/:id/rpm-status
func (h *AdminUserHandler[K]) GetUserRPMStatus(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	status, err := h.adminService.GetUserRPMStatus(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, status)
}

// BatchUpdateLimitsRequest 表示管理员批量覆盖用户限制的请求。
type BatchUpdateLimitsRequest struct {
	UserIDs     []int64 `json:"user_ids"`
	All         bool    `json:"all"`
	Concurrency *int    `json:"concurrency" binding:"omitempty,min=0"`
	RPMLimit    *int    `json:"rpm_limit" binding:"omitempty,min=0"`
}

// BatchUpdateLimits 批量覆盖多个用户的并发数和/或 RPM 上限。
// 接口：POST /api/v1/admin/users/batch-limits。
func (h *AdminUserHandler[K]) BatchUpdateLimits(c *gin.Context) {
	var req BatchUpdateLimitsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Concurrency == nil && req.RPMLimit == nil {
		response.BadRequest(c, "at least one of concurrency or rpm_limit is required")
		return
	}
	if !req.All && len(req.UserIDs) == 0 {
		response.BadRequest(c, "user_ids is required unless all=true")
		return
	}
	if !req.All && len(req.UserIDs) > 500 {
		response.BadRequest(c, "user_ids cannot exceed 500")
		return
	}

	userIDs := req.UserIDs
	if req.All {
		userIDs = nil
		page := 1
		const pageSize = 500
		for {
			users, _, err := h.adminService.ListUsers(c.Request.Context(), page, pageSize, identity.UserListFilters{}, "id", "asc")
			if err != nil {
				response.ErrorFrom(c, err)
				return
			}
			for _, user := range users {
				userIDs = append(userIDs, user.ID)
			}
			if len(users) < pageSize {
				break
			}
			page++
		}
	}

	if len(userIDs) == 0 {
		response.Success(c, gin.H{"affected": 0})
		return
	}

	affected, err := h.adminService.BatchUpdateLimits(
		c.Request.Context(),
		userIDs,
		req.Concurrency,
		req.RPMLimit,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"affected": affected})
}
