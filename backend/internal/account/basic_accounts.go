// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	fmt "fmt"
	time "time"

	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// CreateAccountRequest 创建账号请求
type CreateAccountRequest struct {
	Name               string         `json:"name"`
	Notes              *string        `json:"notes"`
	Platform           string         `json:"platform"`
	Type               string         `json:"type"`
	Credentials        map[string]any `json:"credentials"`
	Extra              map[string]any `json:"extra"`
	ProxyID            *int64         `json:"proxy_id"`
	Concurrency        int            `json:"concurrency"`
	Priority           int            `json:"priority"`
	GroupIDs           []int64        `json:"group_ids"`
	ExpiresAt          *time.Time     `json:"expires_at"`
	AutoPauseOnExpired *bool          `json:"auto_pause_on_expired"`
}

// UpdateAccountRequest 更新账号请求
type UpdateAccountRequest struct {
	Name               *string         `json:"name"`
	Notes              *string         `json:"notes"`
	Credentials        *map[string]any `json:"credentials"`
	Extra              *map[string]any `json:"extra"`
	ProxyID            *int64          `json:"proxy_id"`
	Concurrency        *int            `json:"concurrency"`
	Priority           *int            `json:"priority"`
	Status             *string         `json:"status"`
	GroupIDs           *[]int64        `json:"group_ids"`
	ExpiresAt          *time.Time      `json:"expires_at"`
	AutoPauseOnExpired *bool           `json:"auto_pause_on_expired"`
}

// Create 创建账号
func (s *BasicAccounts) Create(ctx context.Context, req CreateAccountRequest) (*Record, error) {
	// 验证分组是否存在（如果指定了分组）
	if len(req.GroupIDs) > 0 {
		if err := s.validateGroupIDsExist(ctx, req.GroupIDs); err != nil {
			return nil, err
		}
	}

	// 创建账号
	account := &Record{
		Name:        req.Name,
		Notes:       NormalizeAccountNotes(req.Notes),
		Platform:    req.Platform,
		Type:        req.Type,
		Credentials: SanitizeStoredCredentials(req.Platform, req.Credentials),
		Extra:       PrepareCodexFingerprintExtraForCreate(req.Platform, req.Type, req.Extra, s.newSeed),
		ProxyID:     req.ProxyID,
		Concurrency: req.Concurrency,
		Priority:    req.Priority,
		Status:      StatusActive,
		ExpiresAt:   req.ExpiresAt,
	}
	if req.AutoPauseOnExpired != nil {
		account.AutoPauseOnExpired = *req.AutoPauseOnExpired
	} else {
		account.AutoPauseOnExpired = true
	}
	if err := NormalizeUpstreamUsageExtra(account.Extra); err != nil {
		return nil, err
	}

	if err := NormalizeCNProviderCredentials(account, true); err != nil {
		return nil, err
	}
	if err := NormalizeOpenAIAPIKeyConfiguration(account); err != nil {
		return nil, err
	}
	if err := NormalizeAccountProtocols(account); err != nil {
		return nil, err
	}
	if err := s.accountRepo.Create(ctx, account); err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}

	// require_oauth_only 检查：apikey 类型账号不可加入限制分组
	if account.Type == AccountTypeAPIKey && len(req.GroupIDs) > 0 {
		for _, gid := range req.GroupIDs {
			g, err := s.groupRepo.GetGroup(ctx, gid)
			if err != nil {
				return nil, err
			}
			if g.RequireOAuthOnly && (g.Platform == PlatformOpenAI || g.Platform == PlatformAntigravity || g.Platform == PlatformAnthropic || g.Platform == PlatformGemini || g.Platform == PlatformGrok) {
				return nil, fmt.Errorf("分组 [%s] 仅允许 OAuth 账号，apikey 类型账号无法加入", g.Name)
			}
		}
	}

	// 绑定分组
	if len(req.GroupIDs) > 0 {
		if err := s.accountRepo.BindGroups(ctx, account.ID, req.GroupIDs); err != nil {
			return nil, fmt.Errorf("bind groups: %w", err)
		}
	}

	return account, nil
}

// GetByID 根据ID获取账号
func (s *BasicAccounts) GetByID(ctx context.Context, id int64) (*Record, error) {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	return account, nil
}

// List 获取账号列表
func (s *BasicAccounts) List(ctx context.Context, params pagination.PaginationParams) ([]Record, *pagination.PaginationResult, error) {
	accounts, pagination, err := s.accountRepo.List(ctx, params)
	if err != nil {
		return nil, nil, fmt.Errorf("list accounts: %w", err)
	}
	return accounts, pagination, nil
}

// ListByPlatform 根据平台获取账号列表
func (s *BasicAccounts) ListByPlatform(ctx context.Context, platform string) ([]Record, error) {
	accounts, err := s.accountRepo.ListByPlatform(ctx, platform)
	if err != nil {
		return nil, fmt.Errorf("list accounts by platform: %w", err)
	}
	return accounts, nil
}

// ListByGroup 根据分组获取账号列表
func (s *BasicAccounts) ListByGroup(ctx context.Context, groupID int64) ([]Record, error) {
	accounts, err := s.accountRepo.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("list accounts by group: %w", err)
	}
	return accounts, nil
}

// Update 更新账号
func (s *BasicAccounts) Update(ctx context.Context, id int64, req UpdateAccountRequest) (*Record, error) {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}

	// 更新字段
	if req.Name != nil {
		account.Name = *req.Name
	}
	if req.Notes != nil {
		account.Notes = NormalizeAccountNotes(req.Notes)
	}

	if req.Credentials != nil {
		account.Credentials = SanitizeStoredCredentials(account.Platform, PreserveProtocolCredentials(account.Credentials, *req.Credentials))
	}

	if req.Extra != nil {
		extra := make(map[string]any, len(*req.Extra))
		for key, value := range *req.Extra {
			extra[key] = value
		}
		delete(extra, OllamaCloudUsageSessionExtraKey)
		delete(extra, OllamaCloudUsageAutoRefreshExtraKey)
		delete(extra, OllamaCloudUsageSnapshotExtraKey)
		if err := NormalizeUpstreamUsageExtra(extra); err != nil {
			return nil, err
		}
		if _, provided := (*req.Extra)[UpstreamUsageQueryExtraKey]; !provided && account.Extra != nil {
			if value, exists := account.Extra[UpstreamUsageQueryExtraKey]; exists {
				if normalized, ok := NormalizedUpstreamUsageConfigValue(value); ok {
					extra[UpstreamUsageQueryExtraKey] = normalized
				}
			}
		}
		account.Extra = PrepareCodexFingerprintExtraForUpdate(account, extra, s.newSeed)
	} else {
		account.Extra = PrepareCodexFingerprintExtraForUpdate(account, account.Extra, s.newSeed)
	}

	if req.ProxyID != nil {
		account.ProxyID = req.ProxyID
	}

	if req.Concurrency != nil {
		account.Concurrency = *req.Concurrency
	}

	if req.Priority != nil {
		account.Priority = *req.Priority
	}

	if req.Status != nil {
		account.Status = *req.Status
	}
	if req.ExpiresAt != nil {
		account.ExpiresAt = req.ExpiresAt
	}
	if req.AutoPauseOnExpired != nil {
		account.AutoPauseOnExpired = *req.AutoPauseOnExpired
	}

	// 先验证分组是否存在（在任何写操作之前）
	if req.GroupIDs != nil {
		if err := s.validateGroupIDsExist(ctx, *req.GroupIDs); err != nil {
			return nil, err
		}
	}

	if err := NormalizeCNProviderCredentials(account, false); err != nil {
		return nil, err
	}
	if err := NormalizeOpenAIAPIKeyConfiguration(account); err != nil {
		return nil, err
	}
	var credentialPatch, extraPatch map[string]any
	if req.Credentials != nil {
		credentialPatch = *req.Credentials
	}
	if req.Extra != nil {
		extraPatch = *req.Extra
	}
	ApplyLegacyProtocolPatch(account, credentialPatch, extraPatch)
	if err := NormalizeAccountProtocols(account); err != nil {
		return nil, err
	}
	// 只传递本次配置字段，避免完整对象回写并发凭据及健康状态。
	change := ConfigurationChange{NormalizeProtocols: true, CredentialInput: credentialPatch, ProtocolExtra: extraPatch}
	if req.Extra != nil {
		if _, provided := (*req.Extra)[UpstreamUsageQueryExtraKey]; !provided {
			change.PreserveExtraKeys = []string{UpstreamUsageQueryExtraKey}
		}
	}
	if req.Name != nil {
		change.Fields |= ConfigName
	}
	if req.Notes != nil {
		change.Fields |= ConfigNotes
	}
	if req.Credentials != nil {
		change.Fields |= ConfigCredentials
	}
	if req.Extra != nil {
		change.Fields |= ConfigExtra
	}
	if req.ProxyID != nil {
		change.Fields |= ConfigProxyID
	}
	if req.Concurrency != nil {
		change.Fields |= ConfigConcurrency
	}
	if req.Priority != nil {
		change.Fields |= ConfigPriority
	}
	if req.Status != nil {
		change.Fields |= ConfigStatus
	}
	if req.ExpiresAt != nil {
		change.Fields |= ConfigExpiresAt
	}
	if req.AutoPauseOnExpired != nil {
		change.Fields |= ConfigAutoPauseOnExpired
	}
	if err := WriteConfiguration(ctx, s.accountRepo, account, change); err != nil {
		return nil, fmt.Errorf("update account: %w", err)
	}

	// require_oauth_only 检查
	if account.Type == AccountTypeAPIKey && req.GroupIDs != nil {
		for _, gid := range *req.GroupIDs {
			g, err := s.groupRepo.GetGroup(ctx, gid)
			if err != nil {
				return nil, err
			}
			if g.RequireOAuthOnly && (g.Platform == PlatformOpenAI || g.Platform == PlatformAntigravity || g.Platform == PlatformAnthropic || g.Platform == PlatformGemini || g.Platform == PlatformGrok) {
				return nil, fmt.Errorf("分组 [%s] 仅允许 OAuth 账号，apikey 类型账号无法加入", g.Name)
			}
		}
	}

	// 绑定分组
	if req.GroupIDs != nil {
		if err := s.accountRepo.BindGroups(ctx, account.ID, *req.GroupIDs); err != nil {
			return nil, fmt.Errorf("bind groups: %w", err)
		}
	}

	return account, nil
}

// Delete 删除账号
// 优化：使用 ExistsByID 替代 GetByID 进行存在性检查，
// 避免加载完整账号对象及其关联数据，提升删除操作的性能
func (s *BasicAccounts) Delete(ctx context.Context, id int64) error {
	// 使用轻量级的存在性检查，而非加载完整账号对象
	exists, err := s.accountRepo.ExistsByID(ctx, id)
	if err != nil {
		return fmt.Errorf("check account: %w", err)
	}
	// 明确返回账号不存在错误，便于调用方区分错误类型
	if !exists {
		return ErrAccountNotFound
	}

	// 注意:此处不级联删除 spark 影子账号。当前唯一的后台删除入口走 AdminService.DeleteAccount
	// (已 ListShadowsByParent 先删影子再删母)。本方法目前无删除调用方;若未来有调用方经此
	// 删除母账号,需在此补级联,否则会留下孤儿影子(外审第6轮 P3:当前不可达,记为残留)。
	if err := s.accountRepo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}

	return nil
}

func (s *BasicAccounts) validateGroupIDsExist(ctx context.Context, groupIDs []int64) error {
	return s.groupRepo.ValidateGroups(ctx, groupIDs)
}

// UpdateStatus 更新账号状态
func (s *BasicAccounts) UpdateStatus(ctx context.Context, id int64, status string, errorMessage string) error {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get account: %w", err)
	}

	account.Status = status
	account.ErrorMessage = errorMessage

	if err := WriteConfiguration(ctx, s.accountRepo, account, ConfigurationChange{Fields: ConfigStatus | ConfigErrorMessage}); err != nil {
		return fmt.Errorf("update account: %w", err)
	}

	return nil
}

// UpdateLastUsed 更新最后使用时间
func (s *BasicAccounts) UpdateLastUsed(ctx context.Context, id int64) error {
	if err := s.accountRepo.UpdateLastUsed(ctx, id); err != nil {
		return fmt.Errorf("update last used: %w", err)
	}
	return nil
}

// GetCredential 获取账号凭证（安全访问）
func (s *BasicAccounts) GetCredential(ctx context.Context, id int64, key string) (string, error) {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get account: %w", err)
	}

	return account.GetCredential(key), nil
}

// TestCredentials 测试账号凭证是否有效（需要实现具体平台的测试逻辑）
func (s *BasicAccounts) TestCredentials(ctx context.Context, id int64) error {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get account: %w", err)
	}

	// 根据平台执行不同的测试逻辑
	switch account.Platform {
	case PlatformAnthropic:
		// TODO: 测试Anthropic API凭证
		return nil
	case PlatformOpenAI:
		// TODO: 测试OpenAI API凭证
		return nil
	case PlatformGemini:
		// TODO: 测试Gemini API凭证
		return nil
	case PlatformGrok:
		// Grok OAuth 凭证通过 token 兑换、刷新和请求路径探测校验。
		return nil
	case PlatformKimi, PlatformZhipu, PlatformDeepseek:
		// 国产 OpenAI 兼容供应商：凭证为 API Key，实际可用性经余额/额度探测与转发路径验证。
		return nil
	default:
		return fmt.Errorf("unsupported platform: %s", account.Platform)
	}
}

// BasicAccounts 保留旧基础入口的查询顺序和错误包装，无缓存或后台状态。
// 生产管理链使用 Admin；此接口提供基础账号操作。
type BasicAccounts struct {
	accountRepo BasicAccountStore
	groupRepo   BasicAccountGroups
	newSeed     func() string
}
type BasicAccountGroups interface {
	GetGroup(context.Context, int64) (*GroupReference, error)
	ValidateGroups(context.Context, []int64) error
}
type BasicAccountStore interface {
	Create(context.Context, *Record) error
	GetByID(context.Context, int64) (*Record, error)
	List(context.Context, pagination.PaginationParams) ([]Record, *pagination.PaginationResult, error)
	ListByPlatform(context.Context, string) ([]Record, error)
	ListByGroup(context.Context, int64) ([]Record, error)
	Update(context.Context, *Record) error
	BindGroups(context.Context, int64, []int64) error
	ExistsByID(context.Context, int64) (bool, error)
	Delete(context.Context, int64) error
	UpdateLastUsed(context.Context, int64) error
}

func NewBasicAccounts(store BasicAccountStore, groups BasicAccountGroups, newSeed func() string) *BasicAccounts {
	return &BasicAccounts{accountRepo: store, groupRepo: groups, newSeed: newSeed}
}
