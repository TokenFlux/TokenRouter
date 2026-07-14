package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/dgraph-io/ristretto"
	"golang.org/x/sync/singleflight"
)

var (
	ErrAPIKeyNotFound              = infraerrors.NotFound("API_KEY_NOT_FOUND", "api key not found")
	ErrGroupNotAllowed             = infraerrors.Forbidden("GROUP_NOT_ALLOWED", "user is not allowed to bind this group")
	ErrGroupDisabledForUser        = infraerrors.Forbidden("GROUP_DISABLED_FOR_USER", "user is not allowed to use this public group")
	ErrAPIKeyExists                = infraerrors.Conflict("API_KEY_EXISTS", "api key already exists")
	ErrAPIKeyTooShort              = infraerrors.BadRequest("API_KEY_TOO_SHORT", "api key must be at least 16 characters")
	ErrAPIKeyInvalidChars          = infraerrors.BadRequest("API_KEY_INVALID_CHARS", "api key can only contain letters, numbers, underscores, and hyphens")
	ErrAPIKeyRateLimited           = infraerrors.TooManyRequests("API_KEY_RATE_LIMITED", "too many failed attempts, please try again later")
	ErrInvalidIPPattern            = infraerrors.BadRequest("INVALID_IP_PATTERN", "invalid IP or CIDR pattern")
	ErrInvalidAPIKeyFastModePolicy = infraerrors.BadRequest("INVALID_API_KEY_FAST_MODE_POLICY", "invalid API key fast mode policy")
	ErrDataSharingConsentRequired  = infraerrors.Forbidden("DATA_SHARING_CONSENT_REQUIRED", "switching to a data sharing group requires confirmation")
	// ErrAPIKeyExpired        = infraerrors.Forbidden("API_KEY_EXPIRED", "api key has expired")
	ErrAPIKeyExpired = infraerrors.Forbidden("API_KEY_EXPIRED", "api key 已过期")
	// ErrAPIKeyQuotaExhausted = infraerrors.TooManyRequests("API_KEY_QUOTA_EXHAUSTED", "api key quota exhausted")
	ErrAPIKeyQuotaExhausted = infraerrors.TooManyRequests("API_KEY_QUOTA_EXHAUSTED", "api key 额度已用完")

	// Rate limit errors
	ErrAPIKeyRateLimit5hExceeded = infraerrors.TooManyRequests("API_KEY_RATE_5H_EXCEEDED", "api key 5小时限额已用完")
	ErrAPIKeyRateLimit1dExceeded = infraerrors.TooManyRequests("API_KEY_RATE_1D_EXCEEDED", "api key 日限额已用完")
	ErrAPIKeyRateLimit7dExceeded = infraerrors.TooManyRequests("API_KEY_RATE_7D_EXCEEDED", "api key 7天限额已用完")
)

const (
	apiKeyMaxErrorsPerHour       = 20
	apiKeyLastUsedMinTouch       = 30 * time.Second
	apiKeySortCurrentConcurrency = "current_concurrency"
	// DB 写失败后的短退避，避免请求路径持续同步重试造成写风暴与高延迟。
	apiKeyLastUsedFailBackoff = 5 * time.Second
)

type APIKeyRepository interface {
	Create(ctx context.Context, key *APIKey) error
	GetByID(ctx context.Context, id int64) (*APIKey, error)
	// GetKeyAndOwnerID 仅获取 API Key 的 key 与所有者 ID，用于删除等轻量场景
	GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error)
	GetByKey(ctx context.Context, key string) (*APIKey, error)
	// GetByKeyForAuth 认证专用查询，返回最小字段集
	GetByKeyForAuth(ctx context.Context, key string) (*APIKey, error)
	Update(ctx context.Context, key *APIKey) error
	Delete(ctx context.Context, id int64) error
	// DeleteWithAudit 在同一事务内先写 deleted_api_key_audits 审计、再软删除该 key。
	DeleteWithAudit(ctx context.Context, id int64) error

	ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error)
	VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error)
	CountByUserID(ctx context.Context, userID int64) (int64, error)
	ExistsByKey(ctx context.Context, key string) (bool, error)
	ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error)
	SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error)
	ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error)
	// UpdateGroupIDByUserAndGroup 将用户下绑定 oldGroupID 的所有 Key 迁移到 newGroupID
	UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error)
	CountByGroupID(ctx context.Context, groupID int64) (int64, error)
	ListKeysByUserID(ctx context.Context, userID int64) ([]string, error)
	ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error)

	// Quota methods
	IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error)
	UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error

	// Rate limit methods
	IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error
	ResetRateLimitWindows(ctx context.Context, id int64) error
	GetRateLimitData(ctx context.Context, id int64) (*APIKeyRateLimitData, error)
}

type apiKeyAllByUserIDLister interface {
	ListAllByUserID(ctx context.Context, userID int64, filters APIKeyListFilters) ([]APIKey, error)
}

// APIKeyRateLimitData holds rate limit usage and window state for an API key.
type APIKeyRateLimitData struct {
	Usage5h       float64
	Usage1d       float64
	Usage7d       float64
	Window5hStart *time.Time
	Window1dStart *time.Time
	Window7dStart *time.Time
}

// EffectiveUsage5h returns the 5h window usage, or 0 if the window has expired.
func (d *APIKeyRateLimitData) EffectiveUsage5h() float64 {
	if IsWindowExpired(d.Window5hStart, RateLimitWindow5h) {
		return 0
	}
	return d.Usage5h
}

// EffectiveUsage1d returns the 1d window usage, or 0 if the window has expired.
func (d *APIKeyRateLimitData) EffectiveUsage1d() float64 {
	if IsWindowExpired(d.Window1dStart, RateLimitWindow1d) {
		return 0
	}
	return d.Usage1d
}

// EffectiveUsage7d returns the 7d window usage, or 0 if the window has expired.
func (d *APIKeyRateLimitData) EffectiveUsage7d() float64 {
	if IsWindowExpired(d.Window7dStart, RateLimitWindow7d) {
		return 0
	}
	return d.Usage7d
}

// APIKeyQuotaUsageState captures the latest quota fields after an atomic quota update.
// It is intentionally small so repositories can return it from a single SQL statement.
type APIKeyQuotaUsageState struct {
	QuotaUsed float64
	Quota     float64
	Key       string
	Status    string
}

// APIKeyCache defines cache operations for API key service
type APIKeyCache interface {
	GetCreateAttemptCount(ctx context.Context, userID int64) (int, error)
	IncrementCreateAttemptCount(ctx context.Context, userID int64) error
	DeleteCreateAttemptCount(ctx context.Context, userID int64) error

	IncrementDailyUsage(ctx context.Context, apiKey string) error
	SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error

	GetAuthCache(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error)
	SetAuthCache(ctx context.Context, key string, entry *APIKeyAuthCacheEntry, ttl time.Duration) error
	DeleteAuthCache(ctx context.Context, key string) error

	// Pub/Sub for L1 cache invalidation across instances
	PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error
	SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error
}

// APIKeyAuthCacheInvalidator 提供认证缓存失效能力
type APIKeyAuthCacheInvalidator interface {
	InvalidateAuthCacheByKey(ctx context.Context, key string)
	InvalidateAuthCacheByUserID(ctx context.Context, userID int64)
	InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64)
}

// CreateAPIKeyRequest 创建API Key请求
type CreateAPIKeyRequest struct {
	Name        string   `json:"name"`
	GroupID     *int64   `json:"group_id"`
	CustomKey   *string  `json:"custom_key"`   // 可选的自定义key
	IPWhitelist []string `json:"ip_whitelist"` // IP 白名单
	IPBlacklist []string `json:"ip_blacklist"` // IP 黑名单
	// FastModePolicy 为空时默认跟随下游请求。
	FastModePolicy string `json:"fast_mode_policy"`

	// Quota fields
	Quota         float64 `json:"quota"`           // Quota limit in USD (0 = unlimited)
	ExpiresInDays *int    `json:"expires_in_days"` // Days until expiry (nil = never expires)

	// Rate limit fields (0 = unlimited)
	RateLimit5h float64 `json:"rate_limit_5h"`
	RateLimit1d float64 `json:"rate_limit_1d"`
	RateLimit7d float64 `json:"rate_limit_7d"`

	// FallbackToDefaultGroupWhenUnavailable 表示绑定分组停用时是否允许回退到同平台默认分组，nil 时默认开启。
	FallbackToDefaultGroupWhenUnavailable *bool `json:"fallback_to_default_group_when_unavailable"`

	// 数据共享确认字段：创建时直接选择数据共享分组也必须确认。
	DataSharingConfirmed     bool `json:"data_sharing_confirmed"`
	DataSharingNoticeVersion int  `json:"data_sharing_notice_version"`
}

// UpdateAPIKeyRequest 更新API Key请求
type UpdateAPIKeyRequest struct {
	Name        *string  `json:"name"`
	GroupID     *int64   `json:"group_id"`
	Status      *string  `json:"status"`
	IPWhitelist []string `json:"ip_whitelist"` // IP 白名单（空数组清空）
	IPBlacklist []string `json:"ip_blacklist"` // IP 黑名单（空数组清空）
	// FastModePolicy 为 nil 时保持原值。
	FastModePolicy *string `json:"fast_mode_policy"`

	// Quota fields
	Quota           *float64   `json:"quota"`       // Quota limit in USD (nil = no change, 0 = unlimited)
	ExpiresAt       *time.Time `json:"expires_at"`  // Expiration time (nil = no change)
	ClearExpiration bool       `json:"-"`           // Clear expiration (internal use)
	ResetQuota      *bool      `json:"reset_quota"` // Reset quota_used to 0

	// Rate limit fields (nil = no change, 0 = unlimited)
	RateLimit5h         *float64 `json:"rate_limit_5h"`
	RateLimit1d         *float64 `json:"rate_limit_1d"`
	RateLimit7d         *float64 `json:"rate_limit_7d"`
	ResetRateLimitUsage *bool    `json:"reset_rate_limit_usage"` // Reset all usage counters to 0

	// FallbackToDefaultGroupWhenUnavailable 为 nil 时保持原值。
	FallbackToDefaultGroupWhenUnavailable *bool `json:"fallback_to_default_group_when_unavailable"`

	// 数据共享确认字段：用户切换到数据共享分组时必须由弹窗确认后提交。
	DataSharingConfirmed     bool `json:"data_sharing_confirmed"`
	DataSharingNoticeVersion int  `json:"data_sharing_notice_version"`
}

// APIKeyService API Key服务
// RateLimitCacheInvalidator invalidates rate limit cache entries on manual reset.
type RateLimitCacheInvalidator interface {
	InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error
}

// DataSharingNoticeReader 提供当前数据共享须知版本，用于 API Key 切组校验。
type DataSharingNoticeReader interface {
	GetNotice(ctx context.Context) (*DataShareNotice, error)
}

type APIKeyService struct {
	apiKeyRepo            APIKeyRepository
	userRepo              UserRepository
	groupRepo             GroupRepository
	userSubRepo           UserSubscriptionRepository
	userGroupRateRepo     UserGroupRateRepository
	cache                 APIKeyCache
	rateLimitCacheInvalid RateLimitCacheInvalidator // optional: invalidate Redis rate limit cache
	concurrencyService    *ConcurrencyService
	cfg                   *config.Config
	authCacheL1           *ristretto.Cache
	authCfg               apiKeyAuthCacheConfig
	authGroup             singleflight.Group
	lastUsedTouchL1       sync.Map // keyID -> nextAllowedAt(time.Time)
	lastUsedTouchSF       singleflight.Group
	dataSharingNotice     DataSharingNoticeReader
}

// NewAPIKeyService 创建API Key服务实例
func NewAPIKeyService(
	apiKeyRepo APIKeyRepository,
	userRepo UserRepository,
	groupRepo GroupRepository,
	userSubRepo UserSubscriptionRepository,
	userGroupRateRepo UserGroupRateRepository,
	cache APIKeyCache,
	cfg *config.Config,
) *APIKeyService {
	svc := &APIKeyService{
		apiKeyRepo:        apiKeyRepo,
		userRepo:          userRepo,
		groupRepo:         groupRepo,
		userSubRepo:       userSubRepo,
		userGroupRateRepo: userGroupRateRepo,
		cache:             cache,
		cfg:               cfg,
	}
	svc.initAuthCache(cfg)
	return svc
}

// SetRateLimitCacheInvalidator sets the optional rate limit cache invalidator.
// Called after construction (e.g. in wire) to avoid circular dependencies.
func (s *APIKeyService) SetRateLimitCacheInvalidator(inv RateLimitCacheInvalidator) {
	s.rateLimitCacheInvalid = inv
}

// SetDataSharingNoticeReader 注入数据共享须知读取器，避免 API Key 服务直接耦合 settings 存储细节。
func (s *APIKeyService) SetDataSharingNoticeReader(reader DataSharingNoticeReader) {
	s.dataSharingNotice = reader
}

// SetConcurrencyService 注入 API Key 实时并发统计服务。
func (s *APIKeyService) SetConcurrencyService(concurrencyService *ConcurrencyService) {
	s.concurrencyService = concurrencyService
}

func (s *APIKeyService) compileAPIKeyIPRules(apiKey *APIKey) {
	if apiKey == nil {
		return
	}
	apiKey.CompiledIPWhitelist = ip.CompileIPRules(apiKey.IPWhitelist)
	apiKey.CompiledIPBlacklist = ip.CompileIPRules(apiKey.IPBlacklist)
}

// GenerateKey 生成随机API Key
func (s *APIKeyService) GenerateKey() (string, error) {
	// 生成32字节随机数据
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}

	// 转换为十六进制字符串并添加前缀
	prefix := s.cfg.Default.APIKeyPrefix
	if prefix == "" {
		prefix = "sk-"
	}

	key := prefix + hex.EncodeToString(bytes)
	return key, nil
}

// ValidateCustomKey 验证自定义API Key格式
func (s *APIKeyService) ValidateCustomKey(key string) error {
	// 检查长度
	if len(key) < 16 {
		return ErrAPIKeyTooShort
	}

	// 检查字符：只允许字母、数字、下划线、连字符
	for _, c := range key {
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_' || c == '-' {
			continue
		}
		return ErrAPIKeyInvalidChars
	}

	return nil
}

// checkAPIKeyRateLimit 检查用户创建自定义Key的错误次数是否超限
func (s *APIKeyService) checkAPIKeyRateLimit(ctx context.Context, userID int64) error {
	if s.cache == nil {
		return nil
	}

	count, err := s.cache.GetCreateAttemptCount(ctx, userID)
	if err != nil {
		// Redis 出错时不阻止用户操作
		return nil
	}

	if count >= apiKeyMaxErrorsPerHour {
		return ErrAPIKeyRateLimited
	}

	return nil
}

// incrementAPIKeyErrorCount 增加用户创建自定义Key的错误计数
func (s *APIKeyService) incrementAPIKeyErrorCount(ctx context.Context, userID int64) {
	if s.cache == nil {
		return
	}

	_ = s.cache.IncrementCreateAttemptCount(ctx, userID)
}

// canUserBindGroup 检查用户是否可以绑定指定分组。
// group 仅控制路由/访问权限，不再承载订阅语义。
func (s *APIKeyService) canUserBindGroup(ctx context.Context, user *User, group *Group) bool {
	return user.CanBindGroup(group.ID, group.IsExclusive)
}

// canUserUseBoundGroup 校验已有 API Key 当前绑定的公开分组是否仍被该用户允许。
func (s *APIKeyService) canUserUseBoundGroup(ctx context.Context, apiKey *APIKey) bool {
	if apiKey == nil || apiKey.GroupID == nil || apiKey.Group == nil || apiKey.Group.IsExclusive {
		return true
	}
	user := apiKey.User
	if user == nil {
		return true
	}
	if user.GroupRestrictionsLoaded {
		return user.CanBindGroup(apiKey.Group.ID, apiKey.Group.IsExclusive)
	}
	if s.userRepo == nil {
		return true
	}
	loadedUser, err := s.userRepo.GetByID(ctx, apiKey.UserID)
	if err != nil {
		return true
	}
	return loadedUser.CanBindGroup(apiKey.Group.ID, apiKey.Group.IsExclusive)
}

// Create 创建API Key
func (s *APIKeyService) Create(ctx context.Context, userID int64, req CreateAPIKeyRequest) (*APIKey, error) {
	fastModePolicy, ok := NormalizeAPIKeyFastModePolicy(req.FastModePolicy)
	if !ok {
		return nil, ErrInvalidAPIKeyFastModePolicy
	}

	// 验证用户存在
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	// 验证 IP 白名单格式
	if len(req.IPWhitelist) > 0 {
		if invalid := ip.ValidateIPPatterns(req.IPWhitelist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}

	// 验证 IP 黑名单格式
	if len(req.IPBlacklist) > 0 {
		if invalid := ip.ValidateIPPatterns(req.IPBlacklist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}

	// 验证分组权限（如果指定了分组）
	var dataSharingNoticeVersion int
	var dataSharingConfirmedGroupID *int64
	var dataSharingConfirmedAt *time.Time
	if req.GroupID != nil {
		group, err := s.groupRepo.GetByID(ctx, *req.GroupID)
		if err != nil {
			return nil, fmt.Errorf("get group: %w", err)
		}

		// 检查用户是否可以绑定该分组
		if !s.canUserBindGroup(ctx, user, group) {
			return nil, ErrGroupNotAllowed
		}
		if group.DataSharingEnabled {
			version, confirmedAt, err := s.validateCurrentDataSharingConsent(ctx, group, req.DataSharingConfirmed, req.DataSharingNoticeVersion)
			if err != nil {
				return nil, err
			}
			dataSharingNoticeVersion = version
			dataSharingConfirmedGroupID = &group.ID
			dataSharingConfirmedAt = &confirmedAt
		}
	}

	var key string

	// 判断是否使用自定义Key
	if req.CustomKey != nil && *req.CustomKey != "" {
		// 检查限流（仅对自定义key进行限流）
		if err := s.checkAPIKeyRateLimit(ctx, userID); err != nil {
			return nil, err
		}

		// 验证自定义Key格式
		if err := s.ValidateCustomKey(*req.CustomKey); err != nil {
			return nil, err
		}

		// 检查Key是否已存在
		exists, err := s.apiKeyRepo.ExistsByKey(ctx, *req.CustomKey)
		if err != nil {
			return nil, fmt.Errorf("check key exists: %w", err)
		}
		if exists {
			// Key已存在，增加错误计数
			s.incrementAPIKeyErrorCount(ctx, userID)
			return nil, ErrAPIKeyExists
		}

		key = *req.CustomKey
	} else {
		// 生成随机API Key
		var err error
		key, err = s.GenerateKey()
		if err != nil {
			return nil, fmt.Errorf("generate key: %w", err)
		}
	}

	// 新建 API Key 默认开启分组不可用时的自动回退，仍允许调用方显式传 false 关闭。
	fallbackToDefaultGroupWhenUnavailable := true
	if req.FallbackToDefaultGroupWhenUnavailable != nil {
		fallbackToDefaultGroupWhenUnavailable = *req.FallbackToDefaultGroupWhenUnavailable
	}

	// 创建API Key记录
	apiKey := &APIKey{
		UserID:                                userID,
		Key:                                   key,
		Name:                                  html.EscapeString(req.Name),
		GroupID:                               req.GroupID,
		Status:                                StatusActive,
		FastModePolicy:                        fastModePolicy,
		IPWhitelist:                           req.IPWhitelist,
		IPBlacklist:                           req.IPBlacklist,
		Quota:                                 req.Quota,
		QuotaUsed:                             0,
		RateLimit5h:                           req.RateLimit5h,
		RateLimit1d:                           req.RateLimit1d,
		RateLimit7d:                           req.RateLimit7d,
		FallbackToDefaultGroupWhenUnavailable: fallbackToDefaultGroupWhenUnavailable,
		DataSharingNoticeVersion:              dataSharingNoticeVersion,
		DataSharingConfirmedGroupID:           dataSharingConfirmedGroupID,
		DataSharingConfirmedAt:                dataSharingConfirmedAt,
	}

	// Set expiration time if specified
	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		expiresAt := time.Now().AddDate(0, 0, *req.ExpiresInDays)
		apiKey.ExpiresAt = &expiresAt
	}

	if err := s.apiKeyRepo.Create(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}

	s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	s.compileAPIKeyIPRules(apiKey)

	return apiKey, nil
}

// List 获取用户的API Key列表
func (s *APIKeyService) List(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	if normalizedAPIKeySortBy(params.SortBy) == apiKeySortCurrentConcurrency {
		return s.listByCurrentConcurrency(ctx, userID, params, filters)
	}

	keys, pagination, err := s.apiKeyRepo.ListByUserID(ctx, userID, params, filters)
	if err != nil {
		return nil, nil, fmt.Errorf("list api keys: %w", err)
	}
	s.fillCurrentConcurrency(ctx, keys)
	return keys, pagination, nil
}

func (s *APIKeyService) listByCurrentConcurrency(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	repo, ok := s.apiKeyRepo.(apiKeyAllByUserIDLister)
	if !ok {
		return nil, nil, fmt.Errorf("list api keys by current concurrency: repository does not support unpaginated API key listing")
	}

	keys, err := repo.ListAllByUserID(ctx, userID, filters)
	if err != nil {
		return nil, nil, fmt.Errorf("list api keys: %w", err)
	}
	s.fillCurrentConcurrency(ctx, keys)
	sortAPIKeysByCurrentConcurrency(keys, params.NormalizedSortOrder(pagination.SortOrderDesc))
	return paginateAPIKeys(keys, params), apiKeyPaginationResult(int64(len(keys)), params), nil
}

func normalizedAPIKeySortBy(sortBy string) string {
	return strings.ToLower(strings.TrimSpace(sortBy))
}

func sortAPIKeysByCurrentConcurrency(keys []APIKey, sortOrder string) {
	desc := sortOrder != pagination.SortOrderAsc
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].CurrentConcurrency == keys[j].CurrentConcurrency {
			if desc {
				return keys[i].ID > keys[j].ID
			}
			return keys[i].ID < keys[j].ID
		}
		if desc {
			return keys[i].CurrentConcurrency > keys[j].CurrentConcurrency
		}
		return keys[i].CurrentConcurrency < keys[j].CurrentConcurrency
	})
}

func paginateAPIKeys(keys []APIKey, params pagination.PaginationParams) []APIKey {
	if len(keys) == 0 {
		return []APIKey{}
	}
	limit := params.Limit()
	page := params.Page
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit
	if offset >= len(keys) {
		return []APIKey{}
	}
	end := offset + limit
	if end > len(keys) {
		end = len(keys)
	}
	return keys[offset:end]
}

func apiKeyPaginationResult(total int64, params pagination.PaginationParams) *pagination.PaginationResult {
	limit := params.Limit()
	pages := int(total) / limit
	if int(total)%limit > 0 {
		pages++
	}
	return &pagination.PaginationResult{
		Total:    total,
		Page:     params.Page,
		PageSize: limit,
		Pages:    pages,
	}
}

func (s *APIKeyService) fillCurrentConcurrency(ctx context.Context, keys []APIKey) {
	if s == nil || s.concurrencyService == nil || len(keys) == 0 {
		return
	}
	ids := make([]int64, 0, len(keys))
	for i := range keys {
		if keys[i].ID > 0 {
			ids = append(ids, keys[i].ID)
		}
	}
	counts, err := s.concurrencyService.GetAPIKeyConcurrencyBatch(ctx, ids)
	if err != nil {
		return
	}
	for i := range keys {
		keys[i].CurrentConcurrency = counts[keys[i].ID]
	}
}

func (s *APIKeyService) currentConcurrencyForAPIKey(ctx context.Context, apiKeyID int64) int {
	if s == nil || s.concurrencyService == nil || apiKeyID <= 0 {
		return 0
	}
	counts, err := s.concurrencyService.GetAPIKeyConcurrencyBatch(ctx, []int64{apiKeyID})
	if err != nil {
		return 0
	}
	return counts[apiKeyID]
}

func (s *APIKeyService) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	if len(apiKeyIDs) == 0 {
		return []int64{}, nil
	}

	validIDs, err := s.apiKeyRepo.VerifyOwnership(ctx, userID, apiKeyIDs)
	if err != nil {
		return nil, fmt.Errorf("verify api key ownership: %w", err)
	}
	return validIDs, nil
}

// GetByID 根据ID获取API Key
func (s *APIKeyService) GetByID(ctx context.Context, id int64) (*APIKey, error) {
	apiKey, err := s.apiKeyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}
	s.compileAPIKeyIPRules(apiKey)
	if apiKey != nil {
		apiKey.CurrentConcurrency = s.currentConcurrencyForAPIKey(ctx, apiKey.ID)
	}
	return apiKey, nil
}

// GetByKey 根据Key字符串获取API Key（用于认证）
func (s *APIKeyService) GetByKey(ctx context.Context, key string) (*APIKey, error) {
	cacheKey := s.authCacheKey(key)

	if entry, ok := s.getAuthCacheEntry(ctx, cacheKey); ok {
		if apiKey, used, err := s.applyAuthCacheEntry(key, entry); used {
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			apiKey = s.applyDefaultGroupFallback(ctx, apiKey)
			if !s.canUserUseBoundGroup(ctx, apiKey) {
				return nil, fmt.Errorf("get api key: %w", ErrGroupDisabledForUser)
			}
			s.compileAPIKeyIPRules(apiKey)
			return apiKey, nil
		}
	}

	if s.authCfg.singleflight {
		value, err, _ := s.authGroup.Do(cacheKey, func() (any, error) {
			return s.loadAuthCacheEntry(ctx, key, cacheKey)
		})
		if err != nil {
			return nil, err
		}
		entry, _ := value.(*APIKeyAuthCacheEntry)
		if apiKey, used, err := s.applyAuthCacheEntry(key, entry); used {
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			apiKey = s.applyDefaultGroupFallback(ctx, apiKey)
			if !s.canUserUseBoundGroup(ctx, apiKey) {
				return nil, fmt.Errorf("get api key: %w", ErrGroupDisabledForUser)
			}
			s.compileAPIKeyIPRules(apiKey)
			return apiKey, nil
		}
	} else {
		entry, err := s.loadAuthCacheEntry(ctx, key, cacheKey)
		if err != nil {
			return nil, err
		}
		if apiKey, used, err := s.applyAuthCacheEntry(key, entry); used {
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			apiKey = s.applyDefaultGroupFallback(ctx, apiKey)
			if !s.canUserUseBoundGroup(ctx, apiKey) {
				return nil, fmt.Errorf("get api key: %w", ErrGroupDisabledForUser)
			}
			s.compileAPIKeyIPRules(apiKey)
			return apiKey, nil
		}
	}

	apiKey, err := s.apiKeyRepo.GetByKeyForAuth(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}
	apiKey.Key = key
	apiKey = s.applyDefaultGroupFallback(ctx, apiKey)
	if !s.canUserUseBoundGroup(ctx, apiKey) {
		return nil, fmt.Errorf("get api key: %w", ErrGroupDisabledForUser)
	}
	s.compileAPIKeyIPRules(apiKey)
	return apiKey, nil
}

// applyDefaultGroupFallback 为未绑定有效分组或绑定停用分组的 API Key 计算请求级默认分组。
// 这里只修正当前请求中的对象，不回写数据库，也不写入认证缓存，避免不同端点之间互相污染。
func (s *APIKeyService) applyDefaultGroupFallback(ctx context.Context, apiKey *APIKey) *APIKey {
	if apiKey == nil || s.groupRepo == nil {
		return apiKey
	}
	if apiKey.Group != nil {
		if apiKey.GroupID == nil {
			gid := apiKey.Group.ID
			apiKey.GroupID = &gid
		}
		if strings.EqualFold(apiKey.Group.Status, "deleted") {
			return apiKey
		}
		if fallbackPlatform, ok := fallbackPlatformFromBoundGroup(apiKey.Group); ok {
			if !apiKey.FallbackToDefaultGroupWhenUnavailable {
				return apiKey
			}
			if fallbackID := apiKey.Group.UnavailableFallbackGroupID; fallbackID != nil && *fallbackID > 0 {
				if fallbackKey := s.applyUnavailableFallbackGroup(ctx, apiKey, fallbackPlatform, *fallbackID); fallbackKey != nil {
					return fallbackKey
				}
			}
			return s.applyDefaultGroupByPlatform(ctx, apiKey, fallbackPlatform)
		}
		if apiKey.GroupID != nil && apiKey.Group.ID == *apiKey.GroupID {
			return apiKey
		}
	} else if apiKey.GroupID != nil {
		// Key 明确绑定了分组但查询不到实体时，保持 GROUP_DELETED 语义，不使用入口默认分组兜底。
		return apiKey
	}

	platform, ok := resolveAPIKeyFallbackPlatform(ctx)
	if !ok {
		return apiKey
	}

	return s.applyDefaultGroupByPlatform(ctx, apiKey, platform)
}

// applyUnavailableFallbackGroup 将停用分组的请求优先切到管理员指定的回退分组。
// 若目标分组不存在、停用或平台不匹配，返回 nil 交给默认分组兜底逻辑继续处理。
func (s *APIKeyService) applyUnavailableFallbackGroup(ctx context.Context, apiKey *APIKey, platform string, fallbackGroupID int64) *APIKey {
	if apiKey == nil || s.groupRepo == nil || fallbackGroupID <= 0 {
		return nil
	}
	group, err := s.groupRepo.GetByIDLite(ctx, fallbackGroupID)
	if err != nil || group == nil {
		return nil
	}
	if !group.IsActive() || group.Platform != platform {
		return nil
	}
	gid := group.ID
	apiKey.GroupID = &gid
	apiKey.Group = group
	s.refreshFallbackUserGroupRPMOverride(ctx, apiKey, gid)
	return apiKey
}

// fallbackPlatformFromBoundGroup 返回停用绑定分组所属平台。
// deleted/缺失分组不兜底，保留调用方现有的不可用分组报错语义。
func fallbackPlatformFromBoundGroup(group *Group) (string, bool) {
	if group == nil {
		return "", false
	}
	if group.Status == StatusActive || strings.EqualFold(group.Status, "deleted") {
		return "", false
	}
	platform := strings.TrimSpace(group.Platform)
	if platform == "" {
		return "", false
	}
	return platform, true
}

// applyDefaultGroupByPlatform 将当前请求中的 API Key 切到指定平台的默认分组。
func (s *APIKeyService) applyDefaultGroupByPlatform(ctx context.Context, apiKey *APIKey, platform string) *APIKey {
	if apiKey == nil || s.groupRepo == nil {
		return apiKey
	}
	group, err := findPlatformDefaultGroup(ctx, s.groupRepo, platform)
	if err != nil || group == nil {
		return apiKey
	}

	gid := group.ID
	apiKey.GroupID = &gid
	apiKey.Group = group
	s.refreshFallbackUserGroupRPMOverride(ctx, apiKey, gid)
	return apiKey
}

// refreshFallbackUserGroupRPMOverride 重新绑定默认分组后刷新用户专属 RPM。
// 认证缓存里的 override 属于原分组，不能沿用到 fallback 分组。
func (s *APIKeyService) refreshFallbackUserGroupRPMOverride(ctx context.Context, apiKey *APIKey, groupID int64) {
	if apiKey == nil || apiKey.User == nil {
		return
	}
	apiKey.User.UserGroupRPMOverride = nil
	if s.userGroupRateRepo == nil || apiKey.UserID <= 0 || groupID <= 0 {
		return
	}
	override, err := s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, apiKey.UserID, groupID)
	if err == nil {
		apiKey.User.UserGroupRPMOverride = override
	}
}

// resolveAPIKeyFallbackPlatform 根据当前请求上下文推断默认分组所属平台。
// 仅在明确处于网关请求链路时返回 true，避免普通内部调用被意外改写为默认分组。
func resolveAPIKeyFallbackPlatform(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}

	if forcePlatform, ok := ctx.Value(ctxkey.ForcePlatform).(string); ok {
		forcePlatform = strings.TrimSpace(forcePlatform)
		if forcePlatform != "" {
			return forcePlatform, true
		}
	}

	inboundEndpoint, ok := ctx.Value(ctxkey.InboundEndpoint).(string)
	if !ok {
		return "", false
	}
	switch strings.TrimSpace(inboundEndpoint) {
	case "/v1beta/models":
		return PlatformGemini, true
	case "/v1/images/generations", "/v1/images/edits":
		return PlatformOpenAI, true
	case "/v1/messages", "/v1/chat/completions", "/v1/responses":
		// 通用 /v1 入口保持现有语义：未指定平台时默认走 anthropic。
		return PlatformAnthropic, true
	default:
		return "", false
	}
}

// Update 更新API Key
func (s *APIKeyService) Update(ctx context.Context, id int64, userID int64, req UpdateAPIKeyRequest) (*APIKey, error) {
	apiKey, err := s.apiKeyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}

	// 验证所有权
	if apiKey.UserID != userID {
		return nil, ErrInsufficientPerms
	}

	// 验证 IP 白名单格式
	if len(req.IPWhitelist) > 0 {
		if invalid := ip.ValidateIPPatterns(req.IPWhitelist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}

	// 验证 IP 黑名单格式
	if len(req.IPBlacklist) > 0 {
		if invalid := ip.ValidateIPPatterns(req.IPBlacklist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}

	// 更新字段
	if req.Name != nil {
		apiKey.Name = html.EscapeString(*req.Name)
	}
	if req.FastModePolicy != nil {
		fastModePolicy, ok := NormalizeAPIKeyFastModePolicy(*req.FastModePolicy)
		if !ok {
			return nil, ErrInvalidAPIKeyFastModePolicy
		}
		apiKey.FastModePolicy = fastModePolicy
	}

	if req.GroupID != nil {
		// 验证分组权限
		user, err := s.userRepo.GetByID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("get user: %w", err)
		}

		group, err := s.groupRepo.GetByID(ctx, *req.GroupID)
		if err != nil {
			return nil, fmt.Errorf("get group: %w", err)
		}

		if !s.canUserBindGroup(ctx, user, group) {
			return nil, ErrGroupNotAllowed
		}

		changingGroup := apiKey.GroupID == nil || *apiKey.GroupID != group.ID
		if err := s.validateDataSharingConsent(ctx, apiKey, group, req, changingGroup); err != nil {
			return nil, err
		}
		apiKey.GroupID = req.GroupID
		apiKey.Group = group
		apiKey.User = user
	}
	if !s.canUserUseBoundGroup(ctx, apiKey) {
		return nil, ErrGroupDisabledForUser
	}

	if req.Status != nil {
		apiKey.Status = *req.Status
		// 如果状态改变，清除Redis缓存
		if s.cache != nil {
			_ = s.cache.DeleteCreateAttemptCount(ctx, apiKey.UserID)
		}
	}

	// Update quota fields
	if req.Quota != nil {
		apiKey.Quota = *req.Quota
		// 额度仍有剩余，或改为 0（不限额）时，恢复已因额度耗尽停用的 Key。
		if apiKey.Status == StatusAPIKeyQuotaExhausted && (*req.Quota <= 0 || *req.Quota > apiKey.QuotaUsed) {
			apiKey.Status = StatusActive
		}
	}
	if req.ResetQuota != nil && *req.ResetQuota {
		apiKey.QuotaUsed = 0
		// If resetting quota and status was quota_exhausted, reactivate
		if apiKey.Status == StatusAPIKeyQuotaExhausted {
			apiKey.Status = StatusActive
		}
	}
	if req.ClearExpiration {
		apiKey.ExpiresAt = nil
		// If clearing expiry and status was expired, reactivate
		if apiKey.Status == StatusAPIKeyExpired {
			apiKey.Status = StatusActive
		}
	} else if req.ExpiresAt != nil {
		apiKey.ExpiresAt = req.ExpiresAt
		// If extending expiry and status was expired, reactivate
		if apiKey.Status == StatusAPIKeyExpired && time.Now().Before(*req.ExpiresAt) {
			apiKey.Status = StatusActive
		}
	}

	// 更新 IP 限制（空数组会清空设置）
	apiKey.IPWhitelist = req.IPWhitelist
	apiKey.IPBlacklist = req.IPBlacklist

	// Update rate limit configuration
	if req.RateLimit5h != nil {
		apiKey.RateLimit5h = *req.RateLimit5h
	}
	if req.RateLimit1d != nil {
		apiKey.RateLimit1d = *req.RateLimit1d
	}
	if req.RateLimit7d != nil {
		apiKey.RateLimit7d = *req.RateLimit7d
	}
	if req.FallbackToDefaultGroupWhenUnavailable != nil {
		apiKey.FallbackToDefaultGroupWhenUnavailable = *req.FallbackToDefaultGroupWhenUnavailable
	}
	resetRateLimit := req.ResetRateLimitUsage != nil && *req.ResetRateLimitUsage
	if resetRateLimit {
		apiKey.Usage5h = 0
		apiKey.Usage1d = 0
		apiKey.Usage7d = 0
		apiKey.Window5hStart = nil
		apiKey.Window1dStart = nil
		apiKey.Window7dStart = nil
	}

	if err := s.apiKeyRepo.Update(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("update api key: %w", err)
	}

	s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	s.compileAPIKeyIPRules(apiKey)

	// Invalidate Redis rate limit cache so reset takes effect immediately
	if resetRateLimit && s.rateLimitCacheInvalid != nil {
		_ = s.rateLimitCacheInvalid.InvalidateAPIKeyRateLimit(ctx, apiKey.ID)
	}

	return apiKey, nil
}

// validateDataSharingConsent 校验用户是否已在当前切组请求中确认数据共享须知。
func (s *APIKeyService) validateDataSharingConsent(ctx context.Context, apiKey *APIKey, group *Group, req UpdateAPIKeyRequest, changingGroup bool) error {
	if group == nil || !group.DataSharingEnabled || !changingGroup {
		return nil
	}
	version, confirmedAt, err := s.validateCurrentDataSharingConsent(ctx, group, req.DataSharingConfirmed, req.DataSharingNoticeVersion)
	if err != nil {
		return err
	}
	gid := group.ID
	apiKey.DataSharingNoticeVersion = version
	apiKey.DataSharingConfirmedGroupID = &gid
	apiKey.DataSharingConfirmedAt = &confirmedAt
	return nil
}

// validateCurrentDataSharingConsent 要求确认信息必须随当前请求提交，防止复用历史确认绕过弹窗。
func (s *APIKeyService) validateCurrentDataSharingConsent(ctx context.Context, group *Group, confirmed bool, version int) (int, time.Time, error) {
	if group == nil || !group.DataSharingEnabled {
		return 0, time.Time{}, nil
	}
	if !confirmed || s.dataSharingNotice == nil {
		return 0, time.Time{}, ErrDataSharingConsentRequired
	}
	notice, err := s.dataSharingNotice.GetNotice(ctx)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("get data sharing notice: %w", err)
	}
	if version <= 0 || version != notice.Version {
		return 0, time.Time{}, ErrDataSharingConsentRequired
	}
	return version, time.Now(), nil
}

// Delete 删除API Key
func (s *APIKeyService) Delete(ctx context.Context, id int64, userID int64) error {
	key, ownerID, err := s.apiKeyRepo.GetKeyAndOwnerID(ctx, id)
	if err != nil {
		return fmt.Errorf("get api key: %w", err)
	}

	// 验证当前用户是否为该 API Key 的所有者
	if ownerID != userID {
		return ErrInsufficientPerms
	}

	// 事务内:写审计 + 软删除(tombstone)。
	if err := s.apiKeyRepo.DeleteWithAudit(ctx, id); err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}

	// 删除成功后再清理缓存,避免"缓存已清但删除失败"的竞态。
	if s.cache != nil {
		_ = s.cache.DeleteCreateAttemptCount(ctx, userID)
	}
	s.InvalidateAuthCacheByKey(ctx, key)
	s.lastUsedTouchL1.Delete(id)

	return nil
}

// ValidateKey 验证API Key是否有效（用于认证中间件）
func (s *APIKeyService) ValidateKey(ctx context.Context, key string) (*APIKey, *User, error) {
	// 获取API Key
	apiKey, err := s.GetByKey(ctx, key)
	if err != nil {
		return nil, nil, err
	}

	// 检查API Key状态
	if !apiKey.IsActive() {
		return nil, nil, infraerrors.Unauthorized("API_KEY_INACTIVE", "api key is not active")
	}

	// 获取用户信息
	user, err := s.userRepo.GetByID(ctx, apiKey.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("get user: %w", err)
	}

	// 检查用户状态
	if !user.IsActive() {
		return nil, nil, ErrUserNotActive
	}

	return apiKey, user, nil
}

// TouchLastUsed 通过防抖更新 api_keys.last_used_at，减少高频写放大。
// 该操作为尽力而为，不应阻塞主请求链路。
func (s *APIKeyService) TouchLastUsed(ctx context.Context, keyID int64) error {
	if keyID <= 0 {
		return nil
	}

	now := time.Now()
	if v, ok := s.lastUsedTouchL1.Load(keyID); ok {
		if nextAllowedAt, ok := v.(time.Time); ok && now.Before(nextAllowedAt) {
			return nil
		}
	}

	_, err, _ := s.lastUsedTouchSF.Do(strconv.FormatInt(keyID, 10), func() (any, error) {
		latest := time.Now()
		if v, ok := s.lastUsedTouchL1.Load(keyID); ok {
			if nextAllowedAt, ok := v.(time.Time); ok && latest.Before(nextAllowedAt) {
				return nil, nil
			}
		}

		if err := s.apiKeyRepo.UpdateLastUsed(ctx, keyID, latest); err != nil {
			s.lastUsedTouchL1.Store(keyID, latest.Add(apiKeyLastUsedFailBackoff))
			return nil, fmt.Errorf("touch api key last used: %w", err)
		}
		s.lastUsedTouchL1.Store(keyID, latest.Add(apiKeyLastUsedMinTouch))
		return nil, nil
	})
	return err
}

// IncrementUsage 增加API Key使用次数（可选：用于统计）
func (s *APIKeyService) IncrementUsage(ctx context.Context, keyID int64) error {
	// 使用Redis计数器
	if s.cache != nil {
		cacheKey := fmt.Sprintf("apikey:usage:%d:%s", keyID, timezone.Now().Format("2006-01-02"))
		if err := s.cache.IncrementDailyUsage(ctx, cacheKey); err != nil {
			return fmt.Errorf("increment usage: %w", err)
		}
		// 设置24小时过期
		_ = s.cache.SetDailyUsageExpiry(ctx, cacheKey, 24*time.Hour)
	}
	return nil
}

// GetAvailableGroups 获取用户有权限绑定的分组列表。
// group 仅负责路由/账号集合，不再承载订阅语义。
func (s *APIKeyService) GetAvailableGroups(ctx context.Context, userID int64) ([]Group, error) {
	// 获取用户信息
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	// 获取所有活跃分组
	allGroups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	// 过滤出用户有权限的分组
	availableGroups := make([]Group, 0)
	for _, group := range allGroups {
		if s.canUserBindGroupInternal(user, &group) {
			availableGroups = append(availableGroups, group)
		}
	}

	return availableGroups, nil
}

// canUserBindGroupInternal 内部方法，检查用户是否可以绑定分组。
func (s *APIKeyService) canUserBindGroupInternal(user *User, group *Group) bool {
	return user.CanBindGroup(group.ID, group.IsExclusive)
}

func (s *APIKeyService) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error) {
	keys, err := s.apiKeyRepo.SearchAPIKeys(ctx, userID, keyword, limit)
	if err != nil {
		return nil, fmt.Errorf("search api keys: %w", err)
	}
	return keys, nil
}

// GetUserGroupRates 获取用户的专属分组倍率配置
// 返回 map[groupID]rateMultiplier
func (s *APIKeyService) GetUserGroupRates(ctx context.Context, userID int64) (map[int64]float64, error) {
	if s.userGroupRateRepo == nil {
		return nil, nil
	}
	rates, err := s.userGroupRateRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user group rates: %w", err)
	}
	return rates, nil
}

// CheckAPIKeyQuotaAndExpiry checks if the API key is valid for use (not expired, quota not exhausted)
// Returns nil if valid, error if invalid
func (s *APIKeyService) CheckAPIKeyQuotaAndExpiry(apiKey *APIKey) error {
	// Check expiration
	if apiKey.IsExpired() {
		return ErrAPIKeyExpired
	}

	// Check quota
	if apiKey.IsQuotaExhausted() {
		return ErrAPIKeyQuotaExhausted
	}

	return nil
}

// UpdateQuotaUsed updates the quota_used field after a request
// Also checks if quota is exhausted and updates status accordingly
func (s *APIKeyService) UpdateQuotaUsed(ctx context.Context, apiKeyID int64, cost float64) error {
	if cost <= 0 {
		return nil
	}

	type quotaStateReader interface {
		IncrementQuotaUsedAndGetState(ctx context.Context, id int64, amount float64) (*APIKeyQuotaUsageState, error)
	}

	if repo, ok := s.apiKeyRepo.(quotaStateReader); ok {
		state, err := repo.IncrementQuotaUsedAndGetState(ctx, apiKeyID, cost)
		if err != nil {
			return fmt.Errorf("increment quota used: %w", err)
		}
		if state != nil && state.Status == StatusAPIKeyQuotaExhausted && strings.TrimSpace(state.Key) != "" {
			s.InvalidateAuthCacheByKey(ctx, state.Key)
		}
		return nil
	}

	// Use repository to atomically increment quota_used
	newQuotaUsed, err := s.apiKeyRepo.IncrementQuotaUsed(ctx, apiKeyID, cost)
	if err != nil {
		return fmt.Errorf("increment quota used: %w", err)
	}

	// Check if quota is now exhausted and update status if needed
	apiKey, err := s.apiKeyRepo.GetByID(ctx, apiKeyID)
	if err != nil {
		return nil // Don't fail the request, just log
	}

	// If quota is set and now exhausted, update status
	if apiKey.Quota > 0 && newQuotaUsed >= apiKey.Quota {
		apiKey.Status = StatusAPIKeyQuotaExhausted
		if err := s.apiKeyRepo.Update(ctx, apiKey); err != nil {
			return nil // Don't fail the request
		}
		// Invalidate cache so next request sees the new status
		s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	}

	return nil
}

// GetRateLimitData returns rate limit usage and window state for an API key.
func (s *APIKeyService) GetRateLimitData(ctx context.Context, id int64) (*APIKeyRateLimitData, error) {
	return s.apiKeyRepo.GetRateLimitData(ctx, id)
}

// UpdateRateLimitUsage atomically increments rate limit usage counters in the DB.
func (s *APIKeyService) UpdateRateLimitUsage(ctx context.Context, apiKeyID int64, cost float64) error {
	if cost <= 0 {
		return nil
	}
	return s.apiKeyRepo.IncrementRateLimitUsage(ctx, apiKeyID, cost)
}
