package httpapi

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	billingdto "github.com/TokenFlux/TokenRouter/internal/billing/httpapi/dto"
	idempotencyhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// admin_user_handler.go handles administrator account queries and mutations,
// including role checks and idempotent balance changes.
// Administrator account requests combine editable identity fields with account
// keys, activity, and current concurrency data. Optional pointer fields carry
// zero and empty updates separately from omitted values, while list responses
// include stored account data even when the optional concurrency observation
// fails.
//
// Creating or setting the administrator role invokes the supplied step-up
// checker before the administration service. Updates also reject self-demotion
// using the authenticated actor ID. Balance writes pass both target account
// and submitted operation into the idempotency executor. Batch all-user
// operations first enumerate accounts by ID in pages, then submit the gathered
// IDs; enumeration and mutation are separate observations.

// UserAdministration supplies account queries and mutations consumed by
// administrator HTTP handlers.
type UserAdministration interface {
	// ListUsers returns the filtered account page and its total using the
	// supplied ordering. HTTP callers pass pagination and normalized query
	// choices; repository and business errors stop the panel result.
	ListUsers(ctx context.Context, page, pageSize int, filters identity.UserListFilters, sortBy, sortOrder string) ([]identity.User, int64, error)
	// GetUser returns the current account selected by the supplied ID.
	// Administrator HTTP uses this read by default and maps missing or other
	// lookup failures through the panel error helper.
	GetUser(ctx context.Context, id int64) (*identity.User, error)
	// GetUserIncludeDeleted returns the selected account including deleted rows
	// for administrator inspection. HTTP callers select this read for exact
	// include_deleted=true and retain its lookup failures for response mapping.
	GetUserIncludeDeleted(ctx context.Context, id int64) (*identity.User, error)
	// CreateUser creates an account from the prepared administrator input and
	// returns account data. HTTP callers apply their role step-up check first and
	// include the authenticated actor ID for the service.
	CreateUser(ctx context.Context, input *identity.CreateUserInput) (*identity.User, error)
	// UpdateUser applies supplied administrator account changes and returns
	// account data. Pointer inputs retain omitted versus zero values; HTTP
	// callers enforce self-demotion and requested-administrator step-up checks
	// before delegation. Production UserAdmin leaves Balance unread in this
	// input; UpdateUserBalance handles account funds changes.
	UpdateUser(ctx context.Context, id int64, input *identity.UpdateUserInput) (*identity.User, error)
	// DeleteUser performs administrator account deletion for the selected user
	// ID. HTTP callers parse the route identifier first, then map service
	// failures or report completion through the panel envelope.
	DeleteUser(ctx context.Context, id int64) error
	// BatchUpdateConcurrency sets or adds the supplied concurrency value for
	// selected accounts and returns an affected count. HTTP callers gather
	// all-user IDs before delegation when the request selects that mode.
	BatchUpdateConcurrency(ctx context.Context, userIDs []int64, value int, mode string) (int, error)
	// BatchUpdateLimits replaces the supplied optional concurrency and RPM limits
	// and returns an affected count. HTTP callers require at least one nonnil
	// limit and preserve zero as a requested value.
	BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error)
	// UpdateUserBalance applies the selected amount operation and returns the
	// account result. HTTP callers validate a positive amount, include notes, and
	// invoke this operation inside their administrator idempotency executor.
	UpdateUserBalance(ctx context.Context, userID int64, balance float64, operation string, notes string) (*identity.User, error)
	// GetUserRPMStatus returns current-minute request use and limits for the
	// selected account. HTTP callers parse the route user ID, then forward the
	// service result or error in the panel response.
	GetUserRPMStatus(ctx context.Context, userID int64) (*identity.UserRPMStatus, error)
	// GetUserUsageStats returns the selected account usage report for the
	// supplied period. HTTP defaults the missing period to month and writes the
	// service result using the panel response envelope.
	GetUserUsageStats(ctx context.Context, userID int64, period string) (any, error)
	// GetUserBalanceHistory returns paged adjustment records, their total, and
	// the cumulative recharge amount. HTTP callers supply the requested record
	// type and expose administrator DTO notes with pagination and recharge data.
	GetUserBalanceHistory(ctx context.Context, userID int64, page, pageSize int, codeType string) ([]identity.RedeemCode, int64, float64, error)
	// BindUserAuthIdentity binds the prepared provider subject and optional
	// channel to the selected account. HTTP callers transport issuer and metadata
	// fields, while the service enforces provider and ownership rules.
	BindUserAuthIdentity(ctx context.Context, userID int64, input identity.AdminBindAuthIdentityInput) (*identity.AdminBoundAuthIdentity, error)
	// ReplaceUserGroup migrates the account group and reports its affected key
	// count. HTTP callers supply positive old and new group IDs and use service
	// errors for the panel response.
	ReplaceUserGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (*identity.ReplaceUserGroupResult, error)
}

// AdminUserHandler serves account administration with caller-selected API Key
// response elements.
type AdminUserHandler[K any] struct {
	idempotencyhttp.Executor // administrator idempotent JSON writer

	adminService UserAdministration // administrator account operation service
	// listKeys supplies the requested account key page and is required when
	// GetUserAPIKeys is exposed. The handler forwards its pagination and order
	// inputs unchanged.
	listKeys func(context.Context, int64, int, int, string, string) ([]K, int64, error) // paged account key reader
	// concurrency is optional for List. Nil or failed reads leave account data
	// available, with missing current-concurrency map entries displayed as zero.
	concurrency func(context.Context, []identity.User) (map[int64]int, error) // optional current concurrency reader
	// stepUp is required when Create or Update receives administrator role. False
	// means the callback handled rejection, so the handler stops before the
	// account mutation.
	stepUp func(*gin.Context) bool // requested administrator-role verification callback
}

// UserWithConcurrency adds the current observed request concurrency to
// administrator account data.
type UserWithConcurrency[K any] struct {
	dto.AdminUser[K]       // administrator account response data
	CurrentConcurrency int `json:"current_concurrency"` // observed occupied request count
}

// CreateUserRequest carries administrator-created account credentials, limits,
// and group permissions.
type CreateUserRequest struct {
	Email    string `json:"email" binding:"required,email"`    // account login email address
	Password string `json:"password" binding:"required,min=6"` // account plaintext password input
	Username string `json:"username"`                          // account display name edit
	Notes    string `json:"notes"`                             // administrator operation note text
	// Role selects the created account's global privilege. Blank input lets the
	// administration service choose the ordinary user role before persistence.
	Role string `json:"role" binding:"omitempty,oneof=admin user"` // global account role selection
	// Balance supplies an initial USD amount when present. Nil selects the
	// administration service's configured default, or zero when settings are absent.
	Balance     *float64 `json:"balance"`     // USD
	Concurrency int      `json:"concurrency"` // account concurrent request allowance
	RPMLimit    int      `json:"rpm_limit"`   // account requests-per-minute allowance
	// APIKeyLimit overrides the service's creation default when present. Nil uses
	// that captured default, while a supplied zero permits unlimited owned keys.
	APIKeyLimit          *int    `json:"api_key_limit" binding:"omitempty,gte=0,lte=2147483647"` // account-owned key count limit
	AllowedGroups        []int64 `json:"allowed_groups"`                                         // exclusive group grant identifiers
	DisabledPublicGroups []int64 `json:"disabled_public_groups"`                                 // public group denial identifiers
}

// UpdateUserRequest carries account edits with pointer values retaining
// omission versus zero or empty updates. The raw recharge member additionally
// distinguishes clearing with null from leaving an existing override unchanged.
type UpdateUserRequest struct {
	// BalanceRechargeMultiplier preserves JSON presence: omission retains the
	// current override, null clears it, and a number sets the recharge rate.
	BalanceRechargeMultiplier json.RawMessage `json:"balance_recharge_multiplier"`
	// Email, Password, Role, and Status retain current account values when their
	// submitted strings are empty. Pointer fields separately distinguish omission
	// from a requested zero or empty edit.
	Email    string `json:"email" binding:"omitempty,email"`    // account login email address
	Password string `json:"password" binding:"omitempty,min=6"` // account plaintext password input
	// Username, Notes, Concurrency, RPMLimit, APIKeyLimit,
	// AllowedGroups, and DisabledPublicGroups use pointers to retain supplied
	// zero or empty values. Nil leaves those account fields unchanged at service
	// update.
	Username *string `json:"username"`                                  // account display name edit
	Notes    *string `json:"notes"`                                     // administrator operation note text
	Role     string  `json:"role" binding:"omitempty,oneof=admin user"` // global account role selection
	// Balance remains a transported compatibility field. Production UserAdmin
	// leaves it unread in UpdateUser; account funds changes use UpdateBalance.
	Balance              *float64 `json:"balance"`                                                // USD
	Concurrency          *int     `json:"concurrency"`                                            // account concurrent request allowance
	RPMLimit             *int     `json:"rpm_limit"`                                              // account requests-per-minute allowance
	APIKeyLimit          *int     `json:"api_key_limit" binding:"omitempty,gte=0,lte=2147483647"` // account-owned key count limit
	Status               string   `json:"status" binding:"omitempty,oneof=active disabled"`       // account lifecycle status selection
	AllowedGroups        *[]int64 `json:"allowed_groups"`                                         // exclusive group grant identifiers
	DisabledPublicGroups *[]int64 `json:"disabled_public_groups"`                                 // public group denial identifiers
	// GroupRates supplies per-group multiplier edits. A nil map value removes
	// that group's override; groups absent from the map retain their current
	// service values.
	GroupRates map[int64]*float64 `json:"group_rates"` // group price override changes
}

// UpdateBalanceRequest supplies a positive account amount and its
// administrator adjustment operation.
type UpdateBalanceRequest struct {
	Balance   float64 `json:"balance" binding:"required,gt=0"`                     // USD
	Operation string  `json:"operation" binding:"required,oneof=set add subtract"` // set add subtract operation
	Notes     string  `json:"notes"`                                               // administrator operation note text
}

// BatchUpdateConcurrencyRequest selects accounts and a set or additive
// allowance operation.
type BatchUpdateConcurrencyRequest struct {
	// All selects server enumeration and ignores UserIDs as mutation targets.
	// UserIDs still has the HTTP length cap in that mode; a false All value
	// requires a nonempty explicit selection.
	UserIDs     []int64 `json:"user_ids"`                              // selected account row identifiers
	All         bool    `json:"all"`                                   // all listed accounts selected
	Concurrency int     `json:"concurrency"`                           // account concurrent request allowance
	Mode        string  `json:"mode" binding:"required,oneof=set add"` // set or additive concurrency
}

// BindUserAuthIdentityRequest supplies the provider subject and optional
// channel for manual binding.
type BindUserAuthIdentityRequest struct {
	ProviderType    string `json:"provider_type"`    // provider identity type label
	ProviderKey     string `json:"provider_key"`     // provider issuer or namespace
	ProviderSubject string `json:"provider_subject"` // stable external provider subject
	// Issuer and Metadata replace existing identity observations when present.
	// Channel requests a scoped binding when supplied; absence leaves channel
	// handling outside this manual binding request.
	Issuer   *string                             `json:"issuer"`   // optional provider issuer text
	Metadata map[string]any                      `json:"metadata"` // submitted provider metadata map
	Channel  *BindUserAuthIdentityChannelRequest `json:"channel"`  // optional external channel binding
}

// BindUserAuthIdentityChannelRequest identifies the provider channel and its
// scoped external subject.
type BindUserAuthIdentityChannelRequest struct {
	Channel        string `json:"channel"`         // provider channel type label
	ChannelAppID   string `json:"channel_app_id"`  // channel application identity text
	ChannelSubject string `json:"channel_subject"` // channel-scoped external subject text
	// Metadata replaces a reused channel's saved map when supplied. Nil leaves
	// that existing channel metadata available to the binding response.
	Metadata map[string]any `json:"metadata"` // submitted provider metadata map
}

// ReplaceGroupRequest identifies the exclusive group being replaced and its
// destination grant.
type ReplaceGroupRequest struct {
	OldGroupID int64 `json:"old_group_id" binding:"required,gt=0"` // preceding exclusive group identifier
	NewGroupID int64 `json:"new_group_id" binding:"required,gt=0"` // destination exclusive group identifier
}

// BatchUpdateLimitsRequest selects accounts and optional replacement
// concurrency and RPM limits.
type BatchUpdateLimitsRequest struct {
	// All selects server enumeration and ignores UserIDs, including its explicit
	// selection cap. A false All value requires nonempty UserIDs within that cap
	// before limits can reach the service.
	UserIDs []int64 `json:"user_ids"` // selected account row identifiers
	All     bool    `json:"all"`      // all listed accounts selected
	// Concurrency and RPMLimit distinguish omitted limits from requested zero.
	// At least one needs to be present; the handler passes each supplied value
	// through to the batch service operation.
	Concurrency *int `json:"concurrency" binding:"omitempty,min=0"` // account concurrent request allowance
	RPMLimit    *int `json:"rpm_limit" binding:"omitempty,min=0"`   // account requests-per-minute allowance
}

// NewAdminUserHandler retains account operations and caller callbacks for
// keys, concurrency, and step-up. Key routes require listKeys, requested
// administrator roles require stepUp, and a nil concurrency reader yields zero
// observations.
func NewAdminUserHandler[K any](users UserAdministration, keys func(context.Context, int64, int, int, string, string) ([]K, int64, error), concurrency func(context.Context, []identity.User) (map[int64]int, error), stepUp func(*gin.Context) bool) *AdminUserHandler[K] {
	return &AdminUserHandler[K]{adminService: users, listKeys: keys, concurrency: concurrency, stepUp: stepUp}
}

// userResponse maps account data to administrator DTO fields with no supplied
// API Key elements.
func (h *AdminUserHandler[K]) userResponse(u *identity.User) *dto.AdminUser[K] {
	return dto.AdminUserFromIdentity[K](u, nil)
}

// adminID returns the Gin account subject ID used to attribute administrator
// mutations. Missing authenticated subject returns zero, leaving the service
// to apply its configured actor-dependent safeguards.
func adminID(c *gin.Context) int64 {
	s, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		return 0
	}
	return s.UserID
}

// List writes a filtered administrator account page with optional live
// concurrency observations. Search is trimmed and capped at one hundred runes;
// unsupported API-key group IDs are ignored, and failed concurrency reads
// leave the account page available.
func (h *AdminUserHandler[K]) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)

	search := c.Query("search")
	// Limit search input by rune count so truncation preserves UTF-8.
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

	// Current concurrency is an optional observation beside the stored account
	// page. Discard reader errors so the page remains available, with missing map
	// entries displayed as zero.
	var loadInfo map[int64]int
	if len(users) > 0 && h.concurrency != nil {
		loadInfo, _ = h.concurrency(c.Request.Context(), users)
	}

	out := make([]UserWithConcurrency[K], len(users))
	for i := range users {
		out[i] = UserWithConcurrency[K]{
			AdminUser: *h.userResponse(&users[i]),
		}
		out[i].CurrentConcurrency = loadInfo[users[i].ID]
	}

	response.Paginated(c, out, total, page, pageSize)
}

// ParseAttributeFilters returns positive attribute IDs from attr[id] query
// keys when their first supplied value is nonempty. An empty first value drops
// that key even when later values exist; malformed IDs and unrelated keys are
// skipped, while selected value text is retained as supplied.
func ParseAttributeFilters(c *gin.Context) map[int64]string {
	result := make(map[int64]string)

	for key, values := range c.Request.URL.Query() {
		if len(values) == 0 || values[0] == "" {
			continue
		}

		if len(key) > 5 && key[:5] == "attr[" && key[len(key)-1] == ']' {
			idStr := key[5 : len(key)-1]
			// An invalid attribute-ID spelling contributes no filter, keeping malformed
			// query keys from preventing otherwise valid filters from reaching the
			// account list query.
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err == nil && id > 0 {
				result[id] = values[0]
			}
		}
	}

	return result
}

// GetByID writes administrator account details for the parsed route ID. Exact
// include_deleted=true chooses deleted-row inspection; other values use the
// current-account read and its normal missing-user result.
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

// BindAuthIdentity transports provider subject, issuer, metadata, and an
// optional channel to account administration. Malformed route IDs or JSON stop
// before delegation; the service owns canonical provider and ownership
// validation.
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

// Create validates administrator account JSON and invokes step-up when the
// requested role is admin. The authenticated actor ID accompanies the prepared
// service input; a failed step-up checker has already handled the response.
func (h *AdminUserHandler[K]) Create(c *gin.Context) {
	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// Creating an administrator requires the configured step-up check.
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

// Update validates account edits and rejects the authenticated administrator
// own demotion. Any submitted administrator role invokes step-up before the
// mutation, including when the current account already has that role.
//
// The handler preserves pointer fields so an omitted edit remains distinct
// from an explicit zero or empty collection. It bases role step-up on the
// requested role instead of a preliminary current-role read, keeping a
// concurrent demotion from turning a later promotion into an unchecked write.
// Raw recharge JSON preserves explicit null as clearing independently of
// omission. Service errors remain panel failures.
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

	// Block self-demotion so the caller retains administrator access.
	// Demotion of another administrator is checked by the service.
	if req.Role == identity.RoleUser && userID == adminID(c) {
		response.BadRequest(c, "cannot demote yourself from admin")
		return
	}

	// Run step-up whenever the request sets the administrator role.
	// Reading the current role first would permit a concurrent demotion and
	// unchecked re-promotion.
	if req.Role == identity.RoleAdmin {
		if !h.stepUp(c) {
			return
		}
	}

	var balanceRechargeMultiplier *float64
	if len(req.BalanceRechargeMultiplier) > 0 {
		if err := json.Unmarshal(req.BalanceRechargeMultiplier, &balanceRechargeMultiplier); err != nil {
			response.ErrorFrom(c, identity.ErrBalanceRechargeMultiplierInvalid)
			return
		}
	}

	// Preserve raw member presence separately so clearing the recharge override
	// cannot be confused with an older client's unrelated profile update.
	user, err := h.adminService.UpdateUser(c.Request.Context(), userID, &identity.UpdateUserInput{
		BalanceRechargeMultiplier:      balanceRechargeMultiplier,
		ClearBalanceRechargeMultiplier: len(req.BalanceRechargeMultiplier) > 0 && balanceRechargeMultiplier == nil,
		Email:                          req.Email,
		Password:                       req.Password,
		Username:                       req.Username,
		Notes:                          req.Notes,
		Role:                           req.Role,
		Balance:                        req.Balance,
		Concurrency:                    req.Concurrency,
		RPMLimit:                       req.RPMLimit,
		APIKeyLimit:                    req.APIKeyLimit,
		Status:                         req.Status,
		AllowedGroups:                  req.AllowedGroups,
		DisabledPublicGroups:           req.DisabledPublicGroups,
		GroupRates:                     req.GroupRates,
		ActorAdminID:                   adminID(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, h.userResponse(user))
}

// Delete delegates account deletion for the parsed administrator route user
// ID. Malformed identifiers stop before the service, and a successful deletion
// returns the panel completion message.
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

// UpdateBalance validates a positive amount and operation before delegating
// the balance write to the administrator idempotency executor. The payload
// contains both target user ID and request body for replay comparison when
// application assembly configures the executor; its zero value invokes the
// operation directly.
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
		UserID int64                `json:"user_id"` // balance target account identifier
		Body   UpdateBalanceRequest `json:"body"`    // submitted balance adjustment payload
	}{
		UserID: userID,
		Body:   req,
	}
	h.ExecuteAdminIdempotentJSON(c, "admin.users.balance.update", idempotencyPayload, h.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		user, execErr := h.adminService.UpdateUserBalance(ctx, userID, req.Balance, req.Operation, req.Notes)
		if execErr != nil {
			return nil, execErr
		}
		return h.userResponse(user), nil
	})
}

// BatchUpdateConcurrency sets or adds allowances for selected users or all
// listed accounts. Explicit ID input is capped at five hundred even with all
// selected; all mode enumerates accounts in ID order before one service
// mutation.
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
	// Cap explicit ID payloads at five hundred before collecting or mutating
	// account allowances.
	if len(req.UserIDs) > 500 {
		response.BadRequest(c, "user_ids cannot exceed 500")
		return
	}

	var userIDs []int64
	if req.All {
		page := 1
		// Enumerate bounded pages before building the all-user mutation set. These
		// list reads are separate observations from the later service write and
		// do not reserve a fixed account snapshot.
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

// GetUserAPIKeys returns the selected account key page using caller-supplied
// response element conversion. Missing sort inputs choose created_at
// descending; malformed user IDs and key-list errors produce panel failures.
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

// GetUserUsage returns the selected account usage result for the requested
// period. A missing period uses month, while malformed user IDs and service
// errors stop the panel result.
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

// GetBalanceHistory returns administrator adjustment DTOs, pagination, and
// total recharge for one account. The requested type passes to the service,
// notes remain visible, and the displayed page count is at least one.
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
	// Round up partial history pages and retain one displayed page for an empty
	// result.
	pages := max(int((total+int64(pageSize)-1)/int64(pageSize)), 1)
	response.Success(c, gin.H{
		"items":           out,
		"total":           total,
		"page":            page,
		"page_size":       pageSize,
		"pages":           pages,
		"total_recharged": totalRecharged,
	})
}

// ReplaceGroup submits positive old and new group IDs for the selected account
// migration. The panel response reports migrated_keys from the service result;
// malformed JSON or route IDs stop before mutation.
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

// GetUserRPMStatus writes current-minute request counts and limits for the
// selected account. Malformed route IDs stop before lookup, and service
// failures use the shared panel error mapping.
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

// BatchUpdateLimits replaces at least one supplied concurrency or RPM limit
// for selected accounts. Nil fields are omitted and zero remains a requested
// limit; all mode ignores submitted IDs and enumerates accounts before the
// service mutation.
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
	// Cap explicit ID selections; all mode obtains its account set from paged
	// server-side enumeration.
	if !req.All && len(req.UserIDs) > 500 {
		response.BadRequest(c, "user_ids cannot exceed 500")
		return
	}

	userIDs := req.UserIDs
	if req.All {
		userIDs = nil
		page := 1
		// Enumerate bounded pages before building the all-user mutation set. These
		// list reads are separate observations from the later service write and
		// do not reserve a fixed account snapshot.
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
