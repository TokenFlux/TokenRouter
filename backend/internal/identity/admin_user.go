// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// User management implementations
func (s *UserAdmin) ListUsers(ctx context.Context, page, pageSize int, filters UserListFilters, sortBy, sortOrder string) ([]User, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: sortBy, SortOrder: sortOrder}
	users, result, err := s.Users.ListWithFilters(ctx, params, filters)
	if err != nil {
		return nil, 0, err
	}
	if len(users) > 0 {
		userIDs := make([]int64, 0, len(users))
		for i := range users {
			userIDs = append(userIDs, users[i].ID)
		}
		lastUsedByUserID, latestErr := s.Users.GetLatestUsedAtByUserIDs(ctx, userIDs)
		if latestErr != nil {
			s.Observer.Printf("service.admin", "failed to load user last_used_at in batch: err=%v", latestErr)
		} else {
			for i := range users {
				users[i].LastUsedAt = lastUsedByUserID[users[i].ID]
			}
		}
	}
	// 批量加载用户专属分组倍率
	if s.Rates != nil && len(users) > 0 {
		if batchRepo, ok := s.Rates.(AdminGroupRateBatchReader); ok {
			userIDs := make([]int64, 0, len(users))
			for i := range users {
				userIDs = append(userIDs, users[i].ID)
			}
			ratesByUser, err := batchRepo.GetByUserIDs(ctx, userIDs)
			if err != nil {
				s.Observer.Printf("service.admin", "failed to load user group rates in batch: err=%v", err)
				s.AdminLoadUserGroupRatesOneByOne(ctx, users)
			} else {
				for i := range users {
					if rates, ok := ratesByUser[users[i].ID]; ok {
						users[i].GroupRates = rates
					}
				}
			}
		} else {
			s.AdminLoadUserGroupRatesOneByOne(ctx, users)
		}
	}
	return users, result.Total, nil
}

func (s *UserAdmin) AdminLoadUserGroupRatesOneByOne(ctx context.Context, users []User) {
	if s.Rates == nil {
		return
	}
	for i := range users {
		rates, err := s.Rates.GetByUserID(ctx, users[i].ID)
		if err != nil {
			s.Observer.Printf("service.admin", "failed to load user group rates: user_id=%d err=%v", users[i].ID, err)
			continue
		}
		users[i].GroupRates = rates
	}
}

func (s *UserAdmin) GetUser(ctx context.Context, id int64) (*User, error) {
	user, err := s.Users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	lastUsedAt, latestErr := s.Users.GetLatestUsedAtByUserID(ctx, id)
	if latestErr != nil {
		s.Observer.Printf("service.admin", "failed to load user last_used_at: user_id=%d err=%v", id, latestErr)
	} else {
		user.LastUsedAt = lastUsedAt
	}
	// 加载用户专属分组倍率
	if s.Rates != nil {
		rates, err := s.Rates.GetByUserID(ctx, id)
		if err != nil {
			s.Observer.Printf("service.admin", "failed to load user group rates: user_id=%d err=%v", id, err)
		} else {
			user.GroupRates = rates
		}
	}
	return user, nil
}

func (s *UserAdmin) GetUserIncludeDeleted(ctx context.Context, id int64) (*User, error) {
	return s.Users.GetByIDIncludeDeleted(ctx, id)
}

// AdminNormalizeUserRole 校验并归一化角色输入。
// 空字符串返回 fallback(未提供时的默认角色);非法值返回错误。
func AdminNormalizeUserRole(role, fallback string) (string, error) {
	if role == "" {
		return fallback, nil
	}
	if role != RoleAdmin && role != RoleUser {
		return "", fmt.Errorf("invalid role: %q (must be %s or %s)", role, RoleAdmin, RoleUser)
	}
	return role, nil
}

func (s *UserAdmin) CreateUser(ctx context.Context, input *CreateUserInput) (*User, error) {
	balance := 0.0
	if input.Balance != nil {
		balance = *input.Balance
	} else if s.Settings != nil {
		balance = s.Settings.GetDefaultBalance(ctx)
	}
	apiKeyLimit := DefaultUserAPIKeyLimit
	if s.Settings != nil {
		apiKeyLimit = s.Settings.GetDefaultUserAPIKeyLimit(ctx)
	}
	if input.APIKeyLimit != nil {
		if !IsValidUserAPIKeyLimit(*input.APIKeyLimit) {
			return nil, ErrUserAPIKeyLimitInvalid
		}
		apiKeyLimit = *input.APIKeyLimit
	}

	// 角色可由管理员在创建时指定(admin/user);未提供时默认 user。
	role, err := AdminNormalizeUserRole(input.Role, RoleUser)
	if err != nil {
		return nil, err
	}

	user := &User{
		Email:                input.Email,
		Username:             input.Username,
		Notes:                input.Notes,
		Role:                 role,
		Balance:              balance,
		Concurrency:          input.Concurrency,
		RPMLimit:             input.RPMLimit,
		APIKeyLimit:          apiKeyLimit,
		Status:               StatusActive,
		AllowedGroups:        input.AllowedGroups,
		DisabledPublicGroups: input.DisabledPublicGroups,
	}
	if err := user.SetPassword(input.Password); err != nil {
		return nil, err
	}
	createUser := s.Users.Create
	if s.Settings != nil && s.Settings.IsRegistrationEmailNormalizationEnabled(ctx) {
		normalizedEmail := NormalizeRegistrationEmailAddress(user.Email)
		if normalizedEmail != "" {
			createUser = func(createCtx context.Context, createUser *User) error {
				return s.Users.CreateWithNormalizedEmailGuard(createCtx, createUser, normalizedEmail)
			}
		}
	}
	if err := createUser(ctx, user); err != nil {
		return nil, err
	}
	// 创建管理员属权限敏感操作，落审计日志（含操作者），便于事后追溯。
	if user.Role == RoleAdmin {
		s.Observer.Printf("service.admin", "audit: admin user created actor_admin_id=%d target_user_id=%d",
			input.ActorAdminID, user.ID)
	}
	s.AdminAssignDefaultSubscriptions(ctx, user.ID)
	return user, nil
}

// AdminEnsureNotLastAdmin 降级管理员前确认系统中仍存在其他管理员，防止零 admin 锁死。
// 注：读取与写入之间存在竞态窗口，极端并发下仍可能双双降级；作为后台低频操作
// 的兜底保护足够，彻底防护需依赖数据库层约束。
func (s *UserAdmin) AdminEnsureNotLastAdmin(ctx context.Context) error {
	noSubs := false
	_, result, err := s.Users.ListWithFilters(ctx,
		pagination.PaginationParams{Page: 1, PageSize: 1},
		UserListFilters{Role: RoleAdmin, IncludeSubscriptions: &noSubs},
	)
	if err != nil {
		return fmt.Errorf("count admin users: %w", err)
	}
	if result == nil || result.Total <= 1 {
		return errors.New("cannot demote the last admin user")
	}
	return nil
}

func (s *UserAdmin) AdminAssignDefaultSubscriptions(ctx context.Context, userID int64) {
	if s.Settings == nil || s.Subscriptions == nil || userID <= 0 {
		return
	}
	items := s.Settings.GetDefaultSubscriptions(ctx)
	for _, item := range items {
		if _, _, err := s.Subscriptions.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
			UserID: userID,
			PlanID: item.PlanID,
			Notes:  "auto assigned by default user subscriptions setting",
		}); err != nil {
			s.Observer.Printf("service.admin", "failed to assign default subscription: user_id=%d plan_id=%d err=%v", userID, item.PlanID, err)
		}
	}
}

func (s *UserAdmin) UpdateUser(ctx context.Context, id int64, input *UpdateUserInput) (*User, error) {
	// 校验用户专属分组倍率：必须 > 0（nil 合法，表示清除专属倍率）
	if input.GroupRates != nil {
		for groupID, rate := range input.GroupRates {
			if rate != nil && *rate <= 0 {
				return nil, fmt.Errorf("rate_multiplier must be > 0 (group_id=%d)", groupID)
			}
		}
	}

	user, err := s.Users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Protect admin users: cannot disable admin accounts
	if user.Role == "admin" && input.Status == "disabled" {
		return nil, errors.New("cannot disable admin user")
	}

	oldConcurrency := user.Concurrency
	oldStatus := user.Status
	oldRole := user.Role
	normalizedEmail := ""
	emailChanged := false
	oldRPMLimit := user.RPMLimit
	oldAllowedGroups := append([]int64(nil), user.AllowedGroups...)
	oldDisabledPublicGroups := append([]int64(nil), user.DisabledPublicGroups...)

	// fields 与下面的 input.X 判空条件一一对应：管理员没提交的列不写回，
	// 避免这份快照回滚并发的扣费、状态变更或批量限额调整。
	var fields UserUpdateFields

	if input.Email != "" {
		emailChanged = input.Email != user.Email
		if emailChanged {
			if s.Settings != nil && s.Settings.IsRegistrationEmailNormalizationEnabled(ctx) {
				normalizedEmail = NormalizeRegistrationEmailAddress(input.Email)
			}
			if normalizedEmail == "" {
				exists, err := s.Users.ExistsByEmail(ctx, input.Email)
				if err != nil {
					return nil, err
				}
				if exists {
					return nil, ErrEmailExists
				}
			}
		}
		user.Email = input.Email
		fields.Email = true
	}
	if input.Password != "" {
		if err := user.SetPassword(input.Password); err != nil {
			return nil, err
		}
		fields.PasswordHash = true
	}

	if input.Username != nil {
		user.Username = *input.Username
		fields.Username = true
	}
	if input.Notes != nil {
		user.Notes = *input.Notes
		fields.Notes = true
	}

	if input.Status != "" {
		user.Status = input.Status
		fields.Status = true
	}

	// 角色变更(admin/user);空字符串表示不修改。
	if input.Role != "" {
		role, err := AdminNormalizeUserRole(input.Role, user.Role)
		if err != nil {
			return nil, err
		}
		// 防锁死保护：不允许降级系统中最后一个管理员（自我降级已在 handler 层拦截，
		// 此处兜底覆盖跨管理员互降导致零 admin 的场景）。
		if user.Role == RoleAdmin && role == RoleUser {
			if err := s.AdminEnsureNotLastAdmin(ctx); err != nil {
				return nil, err
			}
		}
		user.Role = role
		fields.Role = true
	}

	if input.Concurrency != nil {
		user.Concurrency = *input.Concurrency
		fields.Concurrency = true
	}

	if input.RPMLimit != nil {
		user.RPMLimit = *input.RPMLimit
		fields.RPMLimit = true
	}
	if input.APIKeyLimit != nil {
		if !IsValidUserAPIKeyLimit(*input.APIKeyLimit) {
			return nil, ErrUserAPIKeyLimitInvalid
		}
		user.APIKeyLimit = *input.APIKeyLimit
		fields.APIKeyLimit = true
	}

	if input.AllowedGroups != nil {
		user.AllowedGroups = *input.AllowedGroups
		fields.AllowedGroups = true
	}
	if input.DisabledPublicGroups != nil {
		user.DisabledPublicGroups = *input.DisabledPublicGroups
		fields.DisabledPublicGroups = true
	}

	updateUser := s.Users.Update
	if emailChanged && normalizedEmail != "" {
		updateUser = func(updateCtx context.Context, updateUser *User, updateFields UserUpdateFields) error {
			return s.Users.UpdateWithNormalizedEmailGuard(updateCtx, updateUser, normalizedEmail, updateFields)
		}
	}

	if err := updateUser(ctx, user, fields); err != nil {
		return nil, err
	}

	// 角色变更属权限敏感操作，落审计日志（含操作者），便于事后追溯。
	if user.Role != oldRole {
		s.Observer.Printf("service.admin", "audit: user role changed actor_admin_id=%d target_user_id=%d old_role=%s new_role=%s",
			input.ActorAdminID, user.ID, oldRole, user.Role)
	}

	// 同步用户专属分组倍率
	if input.GroupRates != nil && s.Rates != nil {
		if err := s.Rates.SyncUserGroupRates(ctx, user.ID, input.GroupRates); err != nil {
			s.Observer.Printf("service.admin", "failed to sync user group rates: user_id=%d err=%v", user.ID, err)
		}
	}

	if s.Invalidator != nil {
		// RPMLimit 直接参与 billing_cache_service.checkRPM 的三级级联，
		// allowed_groups/disabled_public_groups 参与 API Key 分组授权判断；
		// 不失效缓存会让修改在一个 L2 TTL 内失去效果。
		if user.Concurrency != oldConcurrency ||
			user.Status != oldStatus ||
			user.Role != oldRole ||
			user.RPMLimit != oldRPMLimit ||
			!AdminSameInt64Set(user.AllowedGroups, oldAllowedGroups) ||
			!AdminSameInt64Set(user.DisabledPublicGroups, oldDisabledPublicGroups) ||
			input.GroupRates != nil {
			s.Invalidator.InvalidateAuthCacheByUserID(ctx, user.ID)
		}
	}

	concurrencyDiff := user.Concurrency - oldConcurrency
	if concurrencyDiff != 0 {
		if err := s.Records.RecordAdjustment(ctx, user.ID, AdjustmentTypeAdminConcurrency, float64(concurrencyDiff), ""); err != nil {
			s.Observer.Printf("service.admin", "failed to create concurrency adjustment redeem code: %v", err)
		}
	}

	return user, nil
}

func AdminSameInt64Set(a, b []int64) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	normalize := func(values []int64) []int64 {
		seen := make(map[int64]struct{}, len(values))
		out := make([]int64, 0, len(values))
		for _, value := range values {
			if value <= 0 {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	left := normalize(a)
	right := normalize(b)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *UserAdmin) BatchUpdateConcurrency(ctx context.Context, userIDs []int64, value int, mode string) (int, error) {
	cleaned := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		cleaned = append(cleaned, userID)
	}
	if len(cleaned) == 0 {
		return 0, nil
	}

	var affected int
	var err error
	switch mode {
	case "set":
		affected, err = s.Users.BatchSetConcurrency(ctx, cleaned, value)
	case "add":
		affected, err = s.Users.BatchAddConcurrency(ctx, cleaned, value)
	default:
		return 0, errors.New("invalid mode: must be 'set' or 'add'")
	}
	if err != nil {
		return 0, err
	}

	if s.Invalidator != nil {
		// 并发数写入后立即失效认证缓存，避免 API Key 继续使用旧并发快照。
		for _, userID := range cleaned {
			s.Invalidator.InvalidateAuthCacheByUserID(ctx, userID)
		}
	}
	return affected, nil
}

// BatchUpdateLimits 清洗用户 ID 后批量覆盖限制，并在写入成功后失效认证缓存。
func (s *UserAdmin) BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error) {
	if concurrency == nil && rpmLimit == nil {
		return 0, fmt.Errorf("at least one of concurrency or rpm_limit is required")
	}

	cleaned := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		cleaned = append(cleaned, userID)
	}
	if len(cleaned) == 0 {
		return 0, nil
	}

	affected, err := s.Users.BatchUpdateLimits(ctx, cleaned, concurrency, rpmLimit)
	if err != nil {
		return 0, err
	}
	if s.Invalidator != nil {
		// 两个字段都会进入认证快照，必须在数据库写入成功后逐用户失效缓存。
		for _, userID := range cleaned {
			s.Invalidator.InvalidateAuthCacheByUserID(ctx, userID)
		}
	}
	return affected, nil
}

func (s *UserAdmin) UpdateUserBalance(ctx context.Context, userID int64, balance float64, operation string, notes string) (*User, error) {
	// 余额调整必须走原子接口：先读后整行写回会把并发的计费扣款覆盖掉。
	var (
		change BalanceChange
		err    error
	)
	switch operation {
	case "set":
		change, err = s.Balances.SetBalance(ctx, userID, balance)
	case "add":
		change, err = s.Balances.AdjustBalance(ctx, userID, balance)
	case "subtract":
		change, err = s.Balances.AdjustBalance(ctx, userID, -balance)
	default:
		return nil, fmt.Errorf("unsupported balance operation: %q", operation)
	}
	if errors.Is(err, ErrBalanceNegative) {
		return nil, fmt.Errorf("balance cannot be negative, current balance: %.2f, requested operation would result in: %.2f", change.Old, change.New)
	}
	if err != nil {
		return nil, err
	}

	user, err := s.Users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	balanceDiff := change.New - change.Old
	if s.Invalidator != nil && balanceDiff != 0 {
		s.Invalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	s.AdminTryAccrueAffiliateRebateForAdminRecharge(ctx, userID, operation, balance)

	if s.BalanceCache != nil {
		s.RunBackground("service/admin_user.go:UpdateUserBalance", func() {
			cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.BalanceCache.InvalidateUserBalance(cacheCtx, userID); err != nil {
				s.Observer.Printf("service.admin", "invalidate user balance cache failed: user_id=%d err=%v", userID, err)
			}
		})
	}

	if balanceDiff != 0 {
		if err := s.Records.RecordAdjustment(ctx, user.ID, AdjustmentTypeAdminBalance, balanceDiff, notes); err != nil {
			s.Observer.Printf("service.admin", "failed to create balance adjustment redeem code: %v", err)
		}
	}

	return user, nil
}

// AdminTryAccrueAffiliateRebateForAdminRecharge 以尽力而为方式计提返利，不回滚已完成的余额调整。
func (s *UserAdmin) AdminTryAccrueAffiliateRebateForAdminRecharge(ctx context.Context, userID int64, operation string, amount float64) {
	if operation != "add" || amount <= 0 || s.Settings == nil || s.Affiliates == nil {
		return
	}
	if !s.Settings.IsAffiliateAdminRechargeEnabled(ctx) {
		return
	}

	rebate, err := s.Affiliates.AccrueInviteRebate(ctx, userID, amount)
	if err != nil {
		s.Observer.Printf("service.admin", "affiliate rebate failed for admin recharge: user_id=%d amount=%.8f err=%v", userID, amount, err)
		return
	}
	if rebate > 0 {
		s.Observer.Printf("service.admin", "affiliate rebate accrued for admin recharge: user_id=%d amount=%.8f rebate=%.8f", userID, amount, rebate)
	}
}

func (s *UserAdmin) GetUserRPMStatus(ctx context.Context, userID int64) (*UserRPMStatus, error) {
	if s.RPM == nil {
		return nil, ErrRPMStatusUnavailable
	}

	user, err := s.Users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	userRPMUsed, err := s.RPM.GetUserRPM(ctx, userID)
	if err != nil {
		s.Observer.Printf("service.admin", "failed to get user rpm: user_id=%d err=%v", userID, err)
	}

	keys, _, err := s.Keys.List(ctx, userID, 1, 1000, "", "")
	if err != nil {
		return nil, err
	}

	groupIDSet := make(map[int64]struct{})
	for _, key := range keys {
		if key.GroupID != nil && *key.GroupID > 0 {
			groupIDSet[*key.GroupID] = struct{}{}
		}
	}

	groupIDs := make([]int64, 0, len(groupIDSet))
	for groupID := range groupIDSet {
		groupIDs = append(groupIDs, groupID)
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })

	var perGroup []UserGroupRPMStatus
	for _, groupID := range groupIDs {
		used, getErr := s.RPM.GetUserGroupRPM(ctx, userID, groupID)
		if getErr != nil {
			s.Observer.Printf("service.admin", "failed to get user group rpm: user_id=%d group_id=%d err=%v", userID, groupID, getErr)
		}

		entry := UserGroupRPMStatus{
			GroupID: groupID,
			Used:    used,
		}

		if s.Groups != nil {
			if group, groupErr := s.Groups.GetByIDLite(ctx, groupID); groupErr == nil && group != nil {
				entry.GroupName = group.Name
				entry.Limit = group.RPMLimit
				entry.Source = "group"
			} else if groupErr != nil {
				s.Observer.Printf("service.admin", "failed to get group rpm status metadata: group_id=%d err=%v", groupID, groupErr)
			}
		}

		if s.Rates != nil {
			override, overrideErr := s.Rates.GetRPMOverrideByUserAndGroup(ctx, userID, groupID)
			if overrideErr != nil {
				s.Observer.Printf("service.admin", "failed to get rpm override: user_id=%d group_id=%d err=%v", userID, groupID, overrideErr)
			} else if override != nil {
				entry.Limit = *override
				entry.Source = "override"
			}
		}

		perGroup = append(perGroup, entry)
	}

	return &UserRPMStatus{
		UserRPMUsed:  userRPMUsed,
		UserRPMLimit: user.RPMLimit,
		PerGroup:     perGroup,
	}, nil
}

func (s *UserAdmin) GetUserUsageStats(ctx context.Context, userID int64, period string) (any, error) {
	// Return mock data for now
	return map[string]any{
		"period":          period,
		"total_requests":  0,
		"total_cost":      0.0,
		"total_tokens":    0,
		"avg_duration_ms": 0,
	}, nil
}

func (s *UserAdmin) GetUserBalanceHistory(ctx context.Context, userID int64, page, pageSize int, codeType string) ([]RedeemCode, int64, float64, error) {
	return s.Records.GetUserBalanceHistory(ctx, userID, page, pageSize, codeType)
}

func AdminCompatibleAdminAuthIdentityProviderKeys(providerType, providerKey string) []string {
	providerType = strings.TrimSpace(strings.ToLower(providerType))
	providerKey = strings.TrimSpace(providerKey)
	if providerKey == "" {
		return []string{providerKey}
	}
	if providerType != "wechat" {
		return []string{providerKey}
	}

	keys := []string{providerKey}
	if !strings.EqualFold(providerKey, "wechat-main") {
		keys = append(keys, "wechat-main")
	}
	if !strings.EqualFold(providerKey, "wechat") {
		keys = append(keys, "wechat")
	}
	return keys
}

func AdminCanonicalAdminAuthIdentityProviderKey(providerType, existingKey, requestedKey string) string {
	providerType = strings.TrimSpace(strings.ToLower(providerType))
	existingKey = strings.TrimSpace(existingKey)
	requestedKey = strings.TrimSpace(requestedKey)
	if providerType != "wechat" {
		if requestedKey != "" {
			return requestedKey
		}
		return existingKey
	}
	if strings.EqualFold(existingKey, "wechat") || strings.EqualFold(existingKey, "wechat-main") || strings.EqualFold(requestedKey, "wechat-main") {
		return "wechat-main"
	}
	if requestedKey != "" {
		return requestedKey
	}
	return existingKey
}

func AdminAdminAuthIdentityProviderKeyRank(providerType, providerKey string) int {
	providerType = strings.TrimSpace(strings.ToLower(providerType))
	providerKey = strings.TrimSpace(providerKey)
	if providerType != "wechat" {
		return 0
	}
	switch {
	case strings.EqualFold(providerKey, "wechat-main"):
		return 0
	case strings.EqualFold(providerKey, "wechat"):
		return 2
	default:
		return 1
	}
}

func AdminNormalizeAdminBindChannelInput(input *AdminBindAuthIdentityChannelInput) *AdminBindAuthIdentityChannelInput {
	if input == nil {
		return nil
	}
	channel := &AdminBindAuthIdentityChannelInput{
		Channel:        strings.TrimSpace(input.Channel),
		ChannelAppID:   strings.TrimSpace(input.ChannelAppID),
		ChannelSubject: strings.TrimSpace(input.ChannelSubject),
		Metadata:       AdminCloneAdminAuthIdentityMetadata(input.Metadata),
	}
	if channel.Channel == "" || channel.ChannelAppID == "" || channel.ChannelSubject == "" {
		return nil
	}
	return channel
}

func AdminNormalizeAdminAuthIdentityProviderType(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "email":
		return "email"
	case "linuxdo":
		return "linuxdo"
	case "oidc":
		return "oidc"
	case "wechat":
		return "wechat"
	case "dingtalk":
		return "dingtalk"
	default:
		return ""
	}
}

func AdminCloneAdminAuthIdentityMetadata(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	if len(input) == 0 {
		return map[string]any{}
	}
	data, err := json.Marshal(input)
	if err != nil {
		out := make(map[string]any, len(input))
		for key, value := range input {
			out[key] = value
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		out = make(map[string]any, len(input))
		for key, value := range input {
			out[key] = value
		}
	}
	return out
}
