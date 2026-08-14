package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	"github.com/google/uuid"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const (
	OllamaCloudUsageSessionExtraKey     = "ollama_cloud_usage_session"
	OllamaCloudUsageAutoRefreshExtraKey = "ollama_cloud_usage_auto_refresh"
	OllamaCloudUsageSnapshotExtraKey    = "ollama_cloud_usage_snapshot"

	// OllamaCloudUsageMinFetchInterval 是同一分组两次成功抓取之间的硬下限，
	// 与 nextOllamaCloudUsageDelay 应用于 next_refresh_at 的下限一致。
	// 活动可以把刷新提前到该边界，但不能越过；导出该常量供仓储 SQL 到期筛选复用。
	OllamaCloudUsageMinFetchInterval = ollamaCloudUsageMinIntervalMinutes * time.Minute

	ollamaCloudUsageSettingsURL            = "https://ollama.com/settings"
	ollamaCloudUsageDefaultIntervalMinutes = 60
	ollamaCloudUsageMinIntervalMinutes     = 15
	ollamaCloudUsageMaxIntervalMinutes     = 24 * 60
	ollamaCloudUsageDefaultDebounceMinutes = 1
	ollamaCloudUsageMinDebounceMinutes     = 1
	ollamaCloudUsageMaxDebounceMinutes     = 60
	ollamaCloudUsageCycleInterval          = time.Minute
	ollamaCloudUsageManualRefreshInterval  = 30 * time.Second
	ollamaCloudUsageRequestTimeout         = 15 * time.Second
	ollamaCloudUsageMaxBodyBytes           = 512 * 1024
	ollamaCloudUsageMaxSessionBytes        = 16 * 1024
	ollamaCloudUsageMaxPerCycle            = 20
	ollamaCloudUsageConcurrency            = 4
	ollamaCloudUsageMaxDelay               = 24 * time.Hour
	ollamaCloudUsageLeaderLockKey          = "ollama:cloud:usage:leader"
	ollamaCloudUsageLeaderLockTTL          = 2 * time.Minute
)

var (
	ErrOllamaCloudUsageUnavailable = infraerrors.ServiceUnavailable(
		"OLLAMA_CLOUD_USAGE_UNAVAILABLE", "Ollama Cloud usage is unavailable",
	)
	ErrOllamaCloudUsageAccountInvalid = infraerrors.BadRequest(
		"OLLAMA_CLOUD_USAGE_ACCOUNT_INVALID", "account must be an OpenAI or Anthropic API key account using https://ollama.com",
	)
	ErrOllamaCloudUsageSessionRequired = infraerrors.BadRequest(
		"OLLAMA_CLOUD_USAGE_SESSION_REQUIRED", "an Ollama web session must be configured first",
	)
	ErrOllamaCloudUsageEncryptionKey = infraerrors.BadRequest(
		"OLLAMA_CLOUD_USAGE_ENCRYPTION_KEY_NOT_CONFIGURED", "cannot store an Ollama web session without a fixed TOTP_ENCRYPTION_KEY",
	)
	ErrOllamaCloudUsageIdentityChanged = infraerrors.Conflict(
		"OLLAMA_CLOUD_USAGE_IDENTITY_CHANGED", "account identity or Ollama web session changed during refresh; retry",
	)
	ErrOllamaCloudUsageRefreshRateLimited = infraerrors.TooManyRequests(
		"OLLAMA_CLOUD_USAGE_REFRESH_RATE_LIMITED", "Ollama Cloud usage can be refreshed manually once every 30 seconds",
	)
	errOllamaCloudUsageUnauthorizedHTML = errors.New("settings HTML is a sign-in page")
)

const (
	OllamaCloudUsageStatusOK           = "ok"
	OllamaCloudUsageStatusUnauthorized = "unauthorized"
	OllamaCloudUsageStatusFailed       = "failed"
)

// OllamaCloudUsageSettings 控制可选的请求驱动刷新任务。
//
// IntervalMinutes 是最大等待上限：模型请求持续到达并不断推迟尾随防抖时，
// 超过该时长会强制刷新。DebounceMinutes 是分组最近一次请求后的静默期。
type OllamaCloudUsageSettings struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"` // 请求持续到达时的最大等待时间
	DebounceMinutes int  `json:"debounce_minutes"` // 最近一次请求后的尾随静默期
}

// OllamaCloudUsageWindow 是单个官方用量窗口的最小化脱敏视图。
type OllamaCloudUsageWindow struct {
	UsedPercent float64    `json:"used_percent"`
	ResetAt     *time.Time `json:"reset_at,omitempty"`
	ResetText   string     `json:"reset_text,omitempty"`
}

// OllamaCloudUsageModelWindow 标识模型请求数所属的官方用量窗口。
type OllamaCloudUsageModelWindow string

const (
	OllamaCloudUsageModelWindowFiveHour OllamaCloudUsageModelWindow = "five_hour"
	OllamaCloudUsageModelWindowSevenDay OllamaCloudUsageModelWindow = "seven_day"
)

// OllamaCloudUsageModel 保存 Ollama 用量页面按窗口展示的模型及请求数。
type OllamaCloudUsageModel struct {
	Model    string                      `json:"model"`
	Window   OllamaCloudUsageModelWindow `json:"window"`
	Requests int64                       `json:"requests"`
}

// OllamaCloudUsageData 明确排除原始 HTML 和浏览器会话数据。
type OllamaCloudUsageData struct {
	Plan     string                  `json:"plan,omitempty"`
	FiveHour *OllamaCloudUsageWindow `json:"five_hour,omitempty"`
	SevenDay *OllamaCloudUsageWindow `json:"seven_day,omitempty"`
	Balance  string                  `json:"balance,omitempty"`
	Models   []OllamaCloudUsageModel `json:"models,omitempty"`
}

// OllamaCloudUsageSnapshot 是账号 extra 中唯一持久化的用量观测数据。
//
// NextRefreshAt 作为兼容字段继续持久化。状态为 ok 时，它只标记最大等待边界；
// 成功后的自动刷新由模型请求活动（分组 last_used_at + 防抖/最大等待）驱动，
// 不会仅由该字段触发。状态为 failed/unauthorized 时，它表示失败后的最早重试时间
// （Retry-After 或指数退避），实际到期时间取 activityDue 与 NextRefreshAt 的较晚者。
type OllamaCloudUsageSnapshot struct {
	Status        string                `json:"status"`
	Data          *OllamaCloudUsageData `json:"data,omitempty"`
	FetchedAt     *time.Time            `json:"fetched_at,omitempty"`
	LastAttemptAt time.Time             `json:"last_attempt_at"`
	NextRefreshAt time.Time             `json:"next_refresh_at"`
	FailureCount  int                   `json:"failure_count,omitempty"`
	HTTPStatus    int                   `json:"http_status,omitempty"`
	LastError     string                `json:"last_error,omitempty"`
}

// OllamaCloudUsageState 是向管理员暴露的专用 DTO。
type OllamaCloudUsageState struct {
	AccountID               int64                     `json:"account_id"`
	Eligible                bool                      `json:"eligible"`
	Configured              bool                      `json:"configured"`
	AutoRefreshEnabled      bool                      `json:"auto_refresh_enabled"`
	EncryptionKeyConfigured bool                      `json:"encryption_key_configured"`
	Snapshot                *OllamaCloudUsageSnapshot `json:"snapshot,omitempty"`
}

type ollamaCloudUsageRepository interface {
	ListOllamaCloudUsageGroupAccounts(context.Context, []*Account) ([]Account, error)
	SaveOllamaCloudUsageSession(context.Context, *Account, string, bool) error
	DeleteOllamaCloudUsageSession(context.Context, *Account) error
	SetOllamaCloudUsageAutoRefresh(context.Context, *Account, bool) error
	UpdateOllamaCloudUsageSnapshot(context.Context, *Account, *OllamaCloudUsageSnapshot) error
	DisableOllamaCloudUsageAutoRefresh(context.Context, *Account) error
	ListDueOllamaCloudUsageAccounts(context.Context, time.Time, time.Duration, time.Duration, int) ([]Account, error)
}

// GetOllamaCloudUsageSettings 在设置缺失时返回默认关闭的安全配置。
func (s *SettingService) GetOllamaCloudUsageSettings(ctx context.Context) (*OllamaCloudUsageSettings, error) {
	defaults := defaultOllamaCloudUsageSettings()
	if s == nil || s.settingRepo == nil {
		return defaults, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOllamaCloudUsageSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return defaults, nil
		}
		return nil, fmt.Errorf("get Ollama Cloud usage settings: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return defaults, nil
	}
	settings := *defaults
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, fmt.Errorf("parse Ollama Cloud usage settings: %w", err)
	}
	if settings.IntervalMinutes == 0 {
		settings.IntervalMinutes = defaults.IntervalMinutes
	}
	if settings.DebounceMinutes == 0 {
		settings.DebounceMinutes = defaults.DebounceMinutes
	}
	normalizeOllamaCloudUsageSettings(&settings)
	return &settings, nil
}

func (s *SettingService) SetOllamaCloudUsageSettings(ctx context.Context, settings *OllamaCloudUsageSettings) error {
	if s == nil || s.settingRepo == nil {
		return ErrOllamaCloudUsageUnavailable
	}
	if settings == nil {
		return infraerrors.BadRequest("INVALID_OLLAMA_CLOUD_USAGE_SETTINGS", "settings cannot be nil")
	}
	if settings.DebounceMinutes == 0 {
		// 旧客户端省略 debounce_minutes 时沿用安全默认值。
		settings.DebounceMinutes = ollamaCloudUsageDefaultDebounceMinutes
	}
	if settings.IntervalMinutes < ollamaCloudUsageMinIntervalMinutes || settings.IntervalMinutes > ollamaCloudUsageMaxIntervalMinutes {
		return infraerrors.BadRequest(
			"INVALID_OLLAMA_CLOUD_USAGE_INTERVAL",
			fmt.Sprintf("interval_minutes must be between %d and %d", ollamaCloudUsageMinIntervalMinutes, ollamaCloudUsageMaxIntervalMinutes),
		)
	}
	if settings.DebounceMinutes < ollamaCloudUsageMinDebounceMinutes || settings.DebounceMinutes > ollamaCloudUsageMaxDebounceMinutes {
		return infraerrors.BadRequest(
			"INVALID_OLLAMA_CLOUD_USAGE_DEBOUNCE",
			fmt.Sprintf("debounce_minutes must be between %d and %d", ollamaCloudUsageMinDebounceMinutes, ollamaCloudUsageMaxDebounceMinutes),
		)
	}
	// 到期时间为 min(lastUsed+debounce, fetchedAt+maxWait)。当 debounce 达到 maxWait 时，
	// 防抖项永远无法生效，配置会被静默忽略，因此拒绝该组合。
	if settings.DebounceMinutes >= settings.IntervalMinutes {
		return infraerrors.BadRequest(
			"INVALID_OLLAMA_CLOUD_USAGE_DEBOUNCE",
			fmt.Sprintf("debounce_minutes (%d) must be less than interval_minutes (%d)", settings.DebounceMinutes, settings.IntervalMinutes),
		)
	}
	normalizeOllamaCloudUsageSettings(settings)
	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal Ollama Cloud usage settings: %w", err)
	}
	return s.settingRepo.Set(ctx, SettingKeyOllamaCloudUsageSettings, string(data))
}

func defaultOllamaCloudUsageSettings() *OllamaCloudUsageSettings {
	return &OllamaCloudUsageSettings{
		Enabled:         false,
		IntervalMinutes: ollamaCloudUsageDefaultIntervalMinutes,
		DebounceMinutes: ollamaCloudUsageDefaultDebounceMinutes,
	}
}

func normalizeOllamaCloudUsageSettings(settings *OllamaCloudUsageSettings) {
	if settings.IntervalMinutes < ollamaCloudUsageMinIntervalMinutes {
		settings.IntervalMinutes = ollamaCloudUsageMinIntervalMinutes
	}
	if settings.IntervalMinutes > ollamaCloudUsageMaxIntervalMinutes {
		settings.IntervalMinutes = ollamaCloudUsageMaxIntervalMinutes
	}
	if settings.DebounceMinutes <= 0 {
		settings.DebounceMinutes = ollamaCloudUsageDefaultDebounceMinutes
	}
	if settings.DebounceMinutes < ollamaCloudUsageMinDebounceMinutes {
		settings.DebounceMinutes = ollamaCloudUsageMinDebounceMinutes
	}
	if settings.DebounceMinutes > ollamaCloudUsageMaxDebounceMinutes {
		settings.DebounceMinutes = ollamaCloudUsageMaxDebounceMinutes
	}
}

func ollamaCloudUsageDurations(settings *OllamaCloudUsageSettings) (debounce, maxWait time.Duration) {
	normalized := defaultOllamaCloudUsageSettings()
	if settings != nil {
		*normalized = *settings
	}
	normalizeOllamaCloudUsageSettings(normalized)
	return time.Duration(normalized.DebounceMinutes) * time.Minute,
		time.Duration(normalized.IntervalMinutes) * time.Minute
}

// ollamaCloudUsageIsAutoRefreshDue 判断已启用自动刷新的分组现在是否应刷新。
// groupLastUsedAt 必须是精确 api_key 分组的 MAX(last_used_at)，避免共享身份的
// 多平台账号遗漏活动。
//
// 成功时，请求必须晚于 fetched_at，dueAt = min(lastUsed+debounce, fetchedAt+maxWait)。
// 失败时，请求必须晚于 last_attempt_at，活动到期时间使用相同的 min 公式；
// 随后 dueAt = max(activityDue, next_refresh_at)，确保 Retry-After 或指数退避优先。
// 快照缺失或无效时按首次刷新处理。
func ollamaCloudUsageIsAutoRefreshDue(
	snapshot *OllamaCloudUsageSnapshot,
	groupLastUsedAt *time.Time,
	now time.Time,
	debounce, maxWait time.Duration,
) bool {
	dueAt, ok := ollamaCloudUsageAutoRefreshDueAt(snapshot, groupLastUsedAt, debounce, maxWait)
	if !ok {
		return false
	}
	return !now.Before(dueAt)
}

func ollamaCloudUsageAutoRefreshDueAt(
	snapshot *OllamaCloudUsageSnapshot,
	groupLastUsedAt *time.Time,
	debounce, maxWait time.Duration,
) (time.Time, bool) {
	if debounce <= 0 {
		debounce = time.Duration(ollamaCloudUsageDefaultDebounceMinutes) * time.Minute
	}
	if maxWait <= 0 {
		maxWait = time.Duration(ollamaCloudUsageDefaultIntervalMinutes) * time.Minute
	}
	if snapshot == nil {
		return time.Time{}, true
	}
	switch snapshot.Status {
	case OllamaCloudUsageStatusOK:
		if snapshot.FetchedAt == nil || snapshot.FetchedAt.IsZero() {
			return time.Time{}, true
		}
		fetchedAt := snapshot.FetchedAt.UTC()
		if groupLastUsedAt == nil || !groupLastUsedAt.After(fetchedAt) {
			return time.Time{}, false
		}
		lastUsed := groupLastUsedAt.UTC()
		dueAt := minTime(lastUsed.Add(debounce), fetchedAt.Add(maxWait))
		// 保留两次成功抓取之间原有的硬下限。成功路径已不再读取 next_refresh_at，
		// 而 nextOllamaCloudUsageDelay 原本通过该字段应用 ollamaCloudUsageMinIntervalMinutes；
		// 若无此限制，间隔略大于防抖期的请求流量会让分组上游抓取频率远高于既有下限。
		if floor := fetchedAt.Add(OllamaCloudUsageMinFetchInterval); dueAt.Before(floor) {
			return floor, true
		}
		return dueAt, true
	case OllamaCloudUsageStatusFailed, OllamaCloudUsageStatusUnauthorized:
		if snapshot.LastAttemptAt.IsZero() {
			return time.Time{}, true
		}
		lastAttempt := snapshot.LastAttemptAt.UTC()
		if groupLastUsedAt == nil || !groupLastUsedAt.After(lastAttempt) {
			return time.Time{}, false
		}
		lastUsed := groupLastUsedAt.UTC()
		activityDue := minTime(lastUsed.Add(debounce), lastAttempt.Add(maxWait))
		if !snapshot.NextRefreshAt.IsZero() && snapshot.NextRefreshAt.UTC().After(activityDue) {
			return snapshot.NextRefreshAt.UTC(), true
		}
		return activityDue, true
	default:
		return time.Time{}, true
	}
}

// maxOllamaCloudUsageGroupLastUsed 返回分组成员中最新的 last_used_at。
func maxOllamaCloudUsageGroupLastUsed(accounts []Account) *time.Time {
	var latest *time.Time
	for i := range accounts {
		candidate := accounts[i].LastUsedAt
		if candidate == nil || candidate.IsZero() {
			continue
		}
		if latest == nil || candidate.After(*latest) {
			ts := candidate.UTC()
			latest = &ts
		}
	}
	return latest
}

// scheduleOllamaCloudUsageActivity 记录 Ollama Cloud API Key 账号实际尝试了上游模型请求，
// 包括 429、5xx 和传输错误。本地鉴权或校验失败不得调用；DeferredService 会合并写入。
func scheduleOllamaCloudUsageActivity(deferred *DeferredService, account *Account) {
	if deferred == nil || account == nil || !IsOllamaCloudUsageAccount(account) {
		return
	}
	deferred.ScheduleLastUsedUpdate(account.ID)
}

// OllamaCloudUsageService 刷新官方设置页 HTML，不影响账号调度状态。
type OllamaCloudUsageService struct {
	accountRepo             AccountRepository
	httpUpstream            HTTPUpstream
	settingService          *SettingService
	encryptor               SecretEncryptor
	encryptionKeyConfigured bool

	parentCtx    context.Context
	parentCancel context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.Mutex
	started      bool
	stopped      bool
	cycleMu      sync.Mutex
	refreshGroup singleflight.Group
	refreshSlots chan struct{}
	now          func() time.Time
	lockCache    LeaderLockCache
	db           *sql.DB
	instanceID   string
}

func NewOllamaCloudUsageService(
	accountRepo AccountRepository,
	httpUpstream HTTPUpstream,
	settingService *SettingService,
	encryptor SecretEncryptor,
	encryptionKeyConfigured bool,
) *OllamaCloudUsageService {
	ctx, cancel := context.WithCancel(context.Background())
	return &OllamaCloudUsageService{
		accountRepo:             accountRepo,
		httpUpstream:            httpUpstream,
		settingService:          settingService,
		encryptor:               encryptor,
		encryptionKeyConfigured: encryptionKeyConfigured,
		parentCtx:               ctx,
		parentCancel:            cancel,
		refreshSlots:            make(chan struct{}, ollamaCloudUsageConcurrency),
		now:                     time.Now,
		instanceID:              uuid.NewString(),
	}
}

func ProvideOllamaCloudUsageService(
	accountRepo AccountRepository,
	httpUpstream HTTPUpstream,
	settingService *SettingService,
	encryptor SecretEncryptor,
	cfg *config.Config,
	lockCache LeaderLockCache,
	db *sql.DB,
) *OllamaCloudUsageService {
	keyConfigured := cfg != nil && cfg.Totp.EncryptionKeyConfigured
	svc := NewOllamaCloudUsageService(accountRepo, httpUpstream, settingService, encryptor, keyConfigured)
	svc.lockCache = lockCache
	svc.db = db
	svc.Start()
	return svc
}

func (s *OllamaCloudUsageService) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.wg.Add(1)
	s.mu.Unlock()
	go s.runLoop()
}

func (s *OllamaCloudUsageService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.parentCancel()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *OllamaCloudUsageService) runLoop() {
	defer s.wg.Done()
	_ = s.RunDue(s.parentCtx)
	ticker := time.NewTicker(ollamaCloudUsageCycleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case <-ticker.C:
			if err := s.RunDue(s.parentCtx); err != nil {
				logger.LegacyPrintf("service.ollama_cloud_usage", "run_due_failed: err=%v", err)
			}
		}
	}
}

func (s *OllamaCloudUsageService) GetSettings(ctx context.Context) (*OllamaCloudUsageSettings, error) {
	if s == nil || s.settingService == nil {
		return defaultOllamaCloudUsageSettings(), nil
	}
	return s.settingService.GetOllamaCloudUsageSettings(ctx)
}

func (s *OllamaCloudUsageService) UpdateSettings(ctx context.Context, settings *OllamaCloudUsageSettings) error {
	if s == nil || s.settingService == nil {
		return ErrOllamaCloudUsageUnavailable
	}
	return s.settingService.SetOllamaCloudUsageSettings(ctx, settings)
}

func (s *OllamaCloudUsageService) GetState(ctx context.Context, accountID int64) (*OllamaCloudUsageState, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if err := s.ResolveAccounts(ctx, []*Account{account}); err != nil {
		return nil, err
	}
	state := OllamaCloudUsageStateFromAccount(account)
	s.EnrichState(state)
	return state, nil
}

// ResolveAccounts 将共享身份组管理的状态覆盖到给定账号对象上。
// 仓储通过一次有界查询解析所有匹配账号，避免账号列表逐行查询。
func (s *OllamaCloudUsageService) ResolveAccounts(ctx context.Context, accounts []*Account) error {
	if s == nil || s.accountRepo == nil || len(accounts) == 0 {
		return nil
	}
	writer, ok := s.accountRepo.(ollamaCloudUsageRepository)
	if !ok {
		return nil
	}
	eligible := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if _, ok := ollamaCloudUsageGroupFingerprint(account); ok {
			eligible = append(eligible, account)
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	siblings, err := writer.ListOllamaCloudUsageGroupAccounts(ctx, eligible)
	if err != nil {
		return fmt.Errorf("resolve Ollama Cloud usage groups: %w", err)
	}
	sources := make(map[string]*Account)
	for index := range siblings {
		candidate := &siblings[index]
		fingerprint, valid := ollamaCloudUsageGroupFingerprint(candidate)
		if !valid || !ollamaCloudUsageConfigured(candidate) {
			continue
		}
		current := sources[fingerprint]
		if current == nil || candidate.UpdatedAt.After(current.UpdatedAt) ||
			(candidate.UpdatedAt.Equal(current.UpdatedAt) && candidate.ID < current.ID) {
			sources[fingerprint] = candidate
		}
	}
	resolvedSources := make(map[string]*Account, len(sources))
	for fingerprint, source := range sources {
		clone := *source
		clone.Extra = make(map[string]any, len(source.Extra))
		maps.Copy(clone.Extra, source.Extra)
		resolvedSources[fingerprint] = &clone
	}
	for index := range siblings {
		candidate := &siblings[index]
		fingerprint, valid := ollamaCloudUsageGroupFingerprint(candidate)
		source := resolvedSources[fingerprint]
		if !valid || source == nil || !sameOllamaCloudUsageSession(source, candidate) {
			continue
		}
		candidateSnapshot := decodeOllamaCloudUsageSnapshot(candidate.Extra)
		currentSnapshot := decodeOllamaCloudUsageSnapshot(source.Extra)
		if candidateSnapshot != nil && (currentSnapshot == nil || candidateSnapshot.LastAttemptAt.After(currentSnapshot.LastAttemptAt)) {
			source.Extra[OllamaCloudUsageSnapshotExtraKey] = candidate.Extra[OllamaCloudUsageSnapshotExtraKey]
		}
	}
	for _, account := range eligible {
		fingerprint, _ := ollamaCloudUsageGroupFingerprint(account)
		applyOllamaCloudUsageManagedExtra(account, resolvedSources[fingerprint])
	}
	return nil
}

func sameOllamaCloudUsageSession(left, right *Account) bool {
	if left == nil || right == nil || left.Extra == nil || right.Extra == nil {
		return false
	}
	leftSession, leftOK := left.Extra[OllamaCloudUsageSessionExtraKey].(string)
	rightSession, rightOK := right.Extra[OllamaCloudUsageSessionExtraKey].(string)
	return leftOK && rightOK && leftSession != "" && leftSession == rightSession
}

func applyOllamaCloudUsageManagedExtra(target, source *Account) {
	if target == nil {
		return
	}
	if target.Extra == nil {
		target.Extra = make(map[string]any)
	}
	for _, key := range []string{
		OllamaCloudUsageSessionExtraKey,
		OllamaCloudUsageAutoRefreshExtraKey,
		OllamaCloudUsageSnapshotExtraKey,
	} {
		delete(target.Extra, key)
		if source != nil && source.Extra != nil {
			if value, ok := source.Extra[key]; ok {
				target.Extra[key] = value
			}
		}
	}
}

func (s *OllamaCloudUsageService) SaveSession(ctx context.Context, accountID int64, session string) (*OllamaCloudUsageState, error) {
	if s == nil || s.accountRepo == nil || s.encryptor == nil {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	if !s.encryptionKeyConfigured {
		return nil, ErrOllamaCloudUsageEncryptionKey
	}
	normalized, err := normalizeOllamaCloudUsageCookie(session)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_OLLAMA_CLOUD_USAGE_SESSION", err.Error())
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !IsOllamaCloudUsageAccount(account) {
		return nil, ErrOllamaCloudUsageAccountInvalid
	}
	if err := s.ResolveAccounts(ctx, []*Account{account}); err != nil {
		return nil, err
	}
	ciphertext, err := s.encryptor.Encrypt(normalized)
	if err != nil {
		return nil, fmt.Errorf("encrypt Ollama web session: %w", err)
	}
	writer, ok := s.accountRepo.(ollamaCloudUsageRepository)
	if !ok {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	preserveAutoRefresh := ollamaCloudUsageConfigured(account) && ollamaCloudUsageAutoRefreshEnabled(account)
	if err := writer.SaveOllamaCloudUsageSession(ctx, account, ciphertext, preserveAutoRefresh); err != nil {
		return nil, err
	}
	return s.GetState(ctx, accountID)
}

func (s *OllamaCloudUsageService) DeleteSession(ctx context.Context, accountID int64) (*OllamaCloudUsageState, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !IsOllamaCloudUsageAccount(account) {
		return nil, ErrOllamaCloudUsageAccountInvalid
	}
	if err := s.ResolveAccounts(ctx, []*Account{account}); err != nil {
		return nil, err
	}
	writer, ok := s.accountRepo.(ollamaCloudUsageRepository)
	if !ok {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	if err := writer.DeleteOllamaCloudUsageSession(ctx, account); err != nil {
		return nil, err
	}
	return s.GetState(ctx, accountID)
}

func (s *OllamaCloudUsageService) SetAutoRefresh(ctx context.Context, accountID int64, enabled bool) (*OllamaCloudUsageState, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !IsOllamaCloudUsageAccount(account) {
		return nil, ErrOllamaCloudUsageAccountInvalid
	}
	if err := s.ResolveAccounts(ctx, []*Account{account}); err != nil {
		return nil, err
	}
	if enabled && !ollamaCloudUsageConfigured(account) {
		return nil, ErrOllamaCloudUsageSessionRequired
	}
	writer, ok := s.accountRepo.(ollamaCloudUsageRepository)
	if !ok {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	if err := writer.SetOllamaCloudUsageAutoRefresh(ctx, account, enabled); err != nil {
		return nil, err
	}
	return s.GetState(ctx, accountID)
}

func (s *OllamaCloudUsageService) Refresh(ctx context.Context, accountID int64) (*OllamaCloudUsageState, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.refreshAccount(ctx, accountID, settings, false); err != nil {
		return nil, err
	}
	return s.GetState(ctx, accountID)
}

func (s *OllamaCloudUsageService) RunDue(ctx context.Context) error {
	if s == nil || s.accountRepo == nil {
		return nil
	}
	s.cycleMu.Lock()
	defer s.cycleMu.Unlock()
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	release, acquired := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, ollamaCloudUsageLeaderLockKey, s.instanceID, ollamaCloudUsageLeaderLockTTL)
	if !acquired {
		return nil
	}
	defer release()

	writer, ok := s.accountRepo.(ollamaCloudUsageRepository)
	if !ok {
		return ErrOllamaCloudUsageUnavailable
	}
	now := s.currentTime()
	debounce, maxWait := ollamaCloudUsageDurations(settings)
	accounts, err := writer.ListDueOllamaCloudUsageAccounts(ctx, now, debounce, maxWait, ollamaCloudUsageMaxPerCycle)
	if err != nil {
		return fmt.Errorf("list due Ollama Cloud usage accounts: %w", err)
	}
	var group errgroup.Group
	seenGroups := make(map[string]struct{}, len(accounts))
	for index := range accounts {
		account := accounts[index]
		fingerprint, valid := ollamaCloudUsageGroupFingerprint(&account)
		if !valid || !account.IsActive() || !ollamaCloudUsageConfigured(&account) || !ollamaCloudUsageAutoRefreshEnabled(&account) {
			continue
		}
		if _, duplicate := seenGroups[fingerprint]; duplicate {
			continue
		}
		seenGroups[fingerprint] = struct{}{}
		snapshot := decodeOllamaCloudUsageSnapshot(account.Extra)
		// ListDue 已将 api_key 分组的 MAX(last_used_at) 写入 Account.LastUsedAt。
		if !ollamaCloudUsageIsAutoRefreshDue(snapshot, account.LastUsedAt, now, debounce, maxWait) {
			continue
		}
		accountID := account.ID
		expected := account
		group.Go(func() error {
			if _, refreshErr := s.refreshAccount(ctx, accountID, settings, true); refreshErr != nil {
				if errors.Is(refreshErr, ErrOllamaCloudUsageIdentityChanged) {
					if disableErr := writer.DisableOllamaCloudUsageAutoRefresh(ctx, &expected); disableErr != nil {
						logger.LegacyPrintf("service.ollama_cloud_usage", "disable_auto_refresh_failed: account_id=%d err=%v", accountID, disableErr)
					}
					return nil
				}
				logger.LegacyPrintf("service.ollama_cloud_usage", "refresh_due_failed: account_id=%d err=%v", accountID, refreshErr)
			}
			return nil
		})
	}
	return group.Wait()
}

func (s *OllamaCloudUsageService) refreshAccount(ctx context.Context, accountID int64, settings *OllamaCloudUsageSettings, requireEnabled bool) (*OllamaCloudUsageSnapshot, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	if settings == nil {
		settings = defaultOllamaCloudUsageSettings()
	}
	intervalMinutes := settings.IntervalMinutes
	debounce, maxWait := ollamaCloudUsageDurations(settings)
	anchor, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	key, valid := ollamaCloudUsageGroupFingerprint(anchor)
	if !valid {
		return nil, ErrOllamaCloudUsageAccountInvalid
	}
	value, err, _ := s.refreshGroup.Do(key, func() (any, error) {
		select {
		case s.refreshSlots <- struct{}{}:
			defer func() { <-s.refreshSlots }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		account, loadErr := s.accountRepo.GetByID(ctx, accountID)
		if loadErr != nil {
			return nil, loadErr
		}
		currentKey, currentValid := ollamaCloudUsageGroupFingerprint(account)
		if !currentValid {
			return nil, ErrOllamaCloudUsageAccountInvalid
		}
		if currentKey != key {
			return nil, ErrOllamaCloudUsageIdentityChanged
		}
		if err := s.ResolveAccounts(ctx, []*Account{account}); err != nil {
			return nil, err
		}
		if !ollamaCloudUsageConfigured(account) {
			return nil, ErrOllamaCloudUsageSessionRequired
		}
		if !requireEnabled {
			if snapshot := decodeOllamaCloudUsageSnapshot(account.Extra); snapshot != nil && !snapshot.LastAttemptAt.IsZero() {
				retryAt := snapshot.LastAttemptAt.Add(ollamaCloudUsageManualRefreshInterval)
				if now := s.currentTime(); now.Before(retryAt) {
					remaining := retryAt.Sub(now)
					seconds := int((remaining + time.Second - 1) / time.Second)
					return nil, ErrOllamaCloudUsageRefreshRateLimited.WithMetadata(map[string]string{
						"retry_after_seconds": strconv.Itoa(seconds),
					})
				}
			}
		}
		if requireEnabled {
			if !account.IsActive() || !ollamaCloudUsageAutoRefreshEnabled(account) {
				return nil, nil
			}
			groupLastUsed := account.LastUsedAt
			if writer, ok := s.accountRepo.(ollamaCloudUsageRepository); ok {
				siblings, listErr := writer.ListOllamaCloudUsageGroupAccounts(ctx, []*Account{account})
				if listErr != nil {
					// 回退到当前账号自身的 last_used_at。它比分组最大值的活动信号更窄，
					// 到期检查可能因此跳过原本应执行的刷新，所以记录错误而不是静默改变语义。
					logger.LegacyPrintf("service.ollama_cloud_usage",
						"group_last_used_lookup_failed: account_id=%d err=%v", account.ID, listErr)
				} else {
					groupLastUsed = maxOllamaCloudUsageGroupLastUsed(siblings)
				}
			}
			if !ollamaCloudUsageIsAutoRefreshDue(decodeOllamaCloudUsageSnapshot(account.Extra), groupLastUsed, s.currentTime(), debounce, maxWait) {
				return nil, nil
			}
		}
		return s.refreshLoadedAccount(ctx, account, intervalMinutes)
	})
	if err != nil || value == nil {
		return nil, err
	}
	snapshot, ok := value.(*OllamaCloudUsageSnapshot)
	if !ok {
		return nil, fmt.Errorf("invalid Ollama Cloud usage refresh result")
	}
	return snapshot, nil
}

func (s *OllamaCloudUsageService) refreshLoadedAccount(ctx context.Context, account *Account, intervalMinutes int) (*OllamaCloudUsageSnapshot, error) {
	now := s.currentTime().UTC()
	ciphertext, _ := account.Extra[OllamaCloudUsageSessionExtraKey].(string)
	if ciphertext == "" {
		return nil, ErrOllamaCloudUsageSessionRequired
	}
	if !s.encryptionKeyConfigured || s.encryptor == nil {
		return nil, ErrOllamaCloudUsageEncryptionKey
	}
	cookie, err := s.encryptor.Decrypt(ciphertext)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("OLLAMA_CLOUD_USAGE_SESSION_DECRYPT_FAILED", "stored Ollama web session cannot be decrypted")
	}
	cookie, err = normalizeOllamaCloudUsageCookie(cookie)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("OLLAMA_CLOUD_USAGE_SESSION_INVALID", "stored Ollama web session is invalid")
	}
	if s.httpUpstream == nil {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
			return nil, ErrOllamaCloudUsageIdentityChanged
		}
		proxyURL = account.Proxy.URL()
	}
	requestCtx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), ollamaCloudUsageRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, ollamaCloudUsageSettingsURL, nil)
	if err != nil || !isExactOllamaCloudSettingsURL(req.URL) {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", "sub2api-ollama-usage/1")
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return s.persistFailure(ctx, account, intervalMinutes, now, 0, "request_failed", 0, false)
	}
	if resp == nil || resp.Body == nil {
		return s.persistFailure(ctx, account, intervalMinutes, now, 0, "empty_response", 0, false)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Request != nil && !isExactOllamaCloudSettingsURL(resp.Request.URL) {
		return s.persistFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "response_host_mismatch", 0, false)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return s.persistFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "redirect_blocked", ollamaCloudUsageRetryAfter(resp.Header, now), false)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return s.persistFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "unauthorized", ollamaCloudUsageRetryAfter(resp.Header, now), true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return s.persistFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "http_error", ollamaCloudUsageRetryAfter(resp.Header, now), false)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, ollamaCloudUsageMaxBodyBytes+1))
	if readErr != nil {
		return s.persistFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "response_read_failed", 0, false)
	}
	if len(body) > ollamaCloudUsageMaxBodyBytes {
		return s.persistFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "response_too_large", 0, false)
	}
	data, parseErr := parseOllamaCloudUsageHTML(body)
	if errors.Is(parseErr, errOllamaCloudUsageUnauthorizedHTML) {
		return s.persistFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "unauthorized", 0, true)
	}
	if parseErr != nil {
		return s.persistFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "invalid_html", 0, false)
	}
	snapshot := &OllamaCloudUsageSnapshot{
		Status:        OllamaCloudUsageStatusOK,
		Data:          data,
		FetchedAt:     &now,
		LastAttemptAt: now,
		NextRefreshAt: now.Add(nextOllamaCloudUsageDelay(intervalMinutes, 0, 0)),
		HTTPStatus:    resp.StatusCode,
	}
	if err := s.updateSnapshot(ctx, account, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *OllamaCloudUsageService) persistFailure(
	ctx context.Context,
	account *Account,
	intervalMinutes int,
	now time.Time,
	httpStatus int,
	reason string,
	retryAfterDuration time.Duration,
	unauthorized bool,
) (*OllamaCloudUsageSnapshot, error) {
	previous := decodeOllamaCloudUsageSnapshot(account.Extra)
	failureCount := 1
	if previous != nil {
		failureCount = previous.FailureCount + 1
	}
	status := OllamaCloudUsageStatusFailed
	if unauthorized {
		status = OllamaCloudUsageStatusUnauthorized
	}
	snapshot := &OllamaCloudUsageSnapshot{
		Status:        status,
		LastAttemptAt: now,
		NextRefreshAt: now.Add(nextOllamaCloudUsageDelay(intervalMinutes, failureCount, retryAfterDuration)),
		FailureCount:  failureCount,
		HTTPStatus:    httpStatus,
		LastError:     reason,
	}
	if previous != nil {
		snapshot.Data = previous.Data
		snapshot.FetchedAt = previous.FetchedAt
	}
	if err := s.updateSnapshot(ctx, account, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *OllamaCloudUsageService) updateSnapshot(ctx context.Context, account *Account, snapshot *OllamaCloudUsageSnapshot) error {
	writer, ok := s.accountRepo.(ollamaCloudUsageRepository)
	if !ok {
		return ErrOllamaCloudUsageUnavailable
	}
	return writer.UpdateOllamaCloudUsageSnapshot(ctx, account, snapshot)
}

// EnrichState 将服务持有的运行时配置补充到账号派生状态中。
func (s *OllamaCloudUsageService) EnrichState(state *OllamaCloudUsageState) {
	if state == nil {
		return
	}
	state.EncryptionKeyConfigured = s != nil && s.encryptionKeyConfigured
}

func OllamaCloudUsageStateFromAccount(account *Account) *OllamaCloudUsageState {
	state := &OllamaCloudUsageState{}
	if account == nil {
		return state
	}
	state.AccountID = account.ID
	state.Eligible = IsOllamaCloudUsageAccount(account)
	if !state.Eligible {
		return state
	}
	state.Configured = ollamaCloudUsageConfigured(account)
	state.AutoRefreshEnabled = state.Configured && ollamaCloudUsageAutoRefreshEnabled(account)
	state.Snapshot = decodeOllamaCloudUsageSnapshot(account.Extra)
	return state
}

func IsOllamaCloudUsageAccount(account *Account) bool {
	if account == nil || account.Type != AccountTypeAPIKey || (account.Platform != PlatformOpenAI && account.Platform != PlatformAnthropic) {
		return false
	}
	baseURL, _ := account.Credentials["base_url"].(string)
	return isOllamaCloudBaseURL(baseURL)
}

func isOllamaCloudBaseURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "?#") {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawFragment != "" {
		return false
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname != "ollama.com" && hostname != "www.ollama.com" {
		return false
	}
	authority := strings.ToLower(parsed.Host)
	if authority != hostname && authority != hostname+":443" {
		return false
	}
	if parsed.RawPath != "" {
		return false
	}
	return parsed.Path == "" || parsed.Path == "/v1"
}

func ollamaCloudUsageIdentity(account *Account) map[string]any {
	if !IsOllamaCloudUsageAccount(account) {
		return nil
	}
	apiKey, ok := account.Credentials["api_key"].(string)
	if !ok || apiKey == "" {
		return nil
	}
	return map[string]any{"host": "ollama.com", "api_key": apiKey}
}

func ollamaCloudUsageGroupFingerprint(account *Account) (string, bool) {
	identity := ollamaCloudUsageIdentity(account)
	if identity == nil {
		return "", false
	}
	apiKey, _ := identity["api_key"].(string)
	sum := sha256.Sum256([]byte("ollama.com\x00" + apiKey))
	return hex.EncodeToString(sum[:]), true
}

func isExactOllamaCloudSettingsURL(parsed *url.URL) bool {
	return parsed != nil && parsed.Scheme == "https" && parsed.Host == "ollama.com" && parsed.Path == "/settings" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.RawPath == ""
}

func normalizeOllamaCloudUsageCookie(raw string) (string, error) {
	if len(raw) > ollamaCloudUsageMaxSessionBytes {
		return "", errors.New("session is too large")
	}
	raw = strings.TrimSpace(raw)
	if strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("session contains invalid header characters")
	}
	if raw == "" {
		return "", errors.New("session cannot be empty")
	}
	if !httpguts.ValidHeaderFieldValue(raw) {
		return "", errors.New("session contains invalid header characters")
	}
	blockedAttributes := map[string]struct{}{
		"domain": {}, "path": {}, "expires": {}, "max-age": {}, "samesite": {}, "secure": {}, "httponly": {}, "partitioned": {},
	}
	parts := strings.Split(raw, ";")
	normalized := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		name, value, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || name == "" || value == "" || !httpguts.ValidHeaderFieldName(name) || strings.HasPrefix(name, "$") {
			return "", errors.New("session must be a Cookie header containing name=value pairs")
		}
		lowerName := strings.ToLower(name)
		if _, blocked := blockedAttributes[lowerName]; blocked {
			return "", errors.New("paste a Cookie header, not a Set-Cookie value with attributes")
		}
		if _, duplicate := seen[lowerName]; duplicate {
			return "", errors.New("session contains duplicate cookie names")
		}
		if strings.ContainsAny(value, ";\r\n") {
			return "", errors.New("session contains an invalid cookie value")
		}
		seen[lowerName] = struct{}{}
		if isAllowedOllamaCloudSessionCookie(name) {
			normalized = append(normalized, name+"="+value)
		}
	}
	if len(normalized) == 0 {
		return "", errors.New("session does not contain an allowed Ollama session cookie")
	}
	return strings.Join(normalized, "; "), nil
}

func isAllowedOllamaCloudSessionCookie(name string) bool {
	switch name {
	case "wos-session", "__Secure-session", "session", "ollama_session", "__Host-ollama_session":
		return true
	}
	for _, base := range []string{
		"next-auth.session-token",
		"__Secure-next-auth.session-token",
		"authjs.session-token",
		"__Secure-authjs.session-token",
	} {
		if name == base {
			return true
		}
		if suffix, ok := strings.CutPrefix(name, base+"."); ok && suffix != "" {
			validShard := true
			for _, char := range suffix {
				if char < '0' || char > '9' {
					validShard = false
					break
				}
			}
			if validShard {
				return true
			}
		}
	}
	return false
}

func ollamaCloudUsageConfigured(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	value, ok := account.Extra[OllamaCloudUsageSessionExtraKey].(string)
	return ok && strings.TrimSpace(value) != ""
}

func ollamaCloudUsageAutoRefreshEnabled(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	enabled, ok := account.Extra[OllamaCloudUsageAutoRefreshExtraKey].(bool)
	return ok && enabled
}

func decodeOllamaCloudUsageSnapshot(extra map[string]any) *OllamaCloudUsageSnapshot {
	if extra == nil {
		return nil
	}
	value, ok := extra[OllamaCloudUsageSnapshotExtraKey]
	if !ok || value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var snapshot OllamaCloudUsageSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil
	}
	if snapshot.Status != OllamaCloudUsageStatusOK && snapshot.Status != OllamaCloudUsageStatusUnauthorized && snapshot.Status != OllamaCloudUsageStatusFailed {
		return nil
	}
	return &snapshot
}

// ollamaCloudUsageRetryAfter 解析上游限流响应要求的最短重试间隔。
func ollamaCloudUsageRetryAfter(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := at.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}

func nextOllamaCloudUsageDelay(intervalMinutes, failureCount int, retryAfterDuration time.Duration) time.Duration {
	minimumDelay := retryAfterDuration
	base := time.Duration(intervalMinutes) * time.Minute
	if base < ollamaCloudUsageMinIntervalMinutes*time.Minute {
		base = ollamaCloudUsageMinIntervalMinutes * time.Minute
	}
	if failureCount > 0 {
		shift := min(failureCount-1, 6)
		base *= time.Duration(1 << shift)
	}
	if base > ollamaCloudUsageMaxDelay {
		base = ollamaCloudUsageMaxDelay
	}
	if retryAfterDuration > base {
		base = retryAfterDuration
	}
	jitterRange := base / 10
	if jitterRange > 5*time.Minute {
		jitterRange = 5 * time.Minute
	}
	if jitterRange > 0 {
		base += time.Duration(rand.Int64N(int64(jitterRange)*2+1)) - jitterRange
	}
	if base < minimumDelay {
		return minimumDelay
	}
	if base < time.Minute {
		return time.Minute
	}
	return base
}

func (s *OllamaCloudUsageService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}
