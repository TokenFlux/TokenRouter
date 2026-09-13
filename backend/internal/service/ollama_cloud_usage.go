// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	sql "database/sql"
	errors "errors"
	fmt "fmt"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	uuid "github.com/google/uuid"
	rand "math/rand/v2"
	http "net/http"
	url "net/url"
	strconv "strconv"
	strings "strings"
	time "time"
)

type OllamaCloudUsageService struct {
	core         *acctcore.OllamaCloudUsageService
	httpUpstream HTTPUpstream
	now          func() time.Time
	lockCache    LeaderLockCache
	db           *sql.DB
	instanceID   string
}

func (s *OllamaCloudUsageService) Core() *acctcore.OllamaCloudUsageService {
	if s == nil {
		return nil
	}
	return s.core
}
func (s *OllamaCloudUsageService) BindCore(core *acctcore.OllamaCloudUsageService) { s.core = core }

// NewOllamaUsageExecution 只创建旧 HTTP 执行适配，不创建第二个缓存或后台任务。
func NewOllamaUsageExecution(upstream HTTPUpstream) *OllamaCloudUsageService {
	return &OllamaCloudUsageService{httpUpstream: upstream}
}

const (
	OllamaCloudUsageSessionExtraKey     = acctcore.OllamaCloudUsageSessionExtraKey
	OllamaCloudUsageAutoRefreshExtraKey = acctcore.OllamaCloudUsageAutoRefreshExtraKey
	OllamaCloudUsageSnapshotExtraKey    = acctcore.OllamaCloudUsageSnapshotExtraKey

	// OllamaCloudUsageMinFetchInterval 是同一分组两次成功抓取之间的硬下限，
	// 与 nextOllamaCloudUsageDelay 应用于 next_refresh_at 的下限一致。
	// 活动可以把刷新提前到该边界，但不能越过；导出该常量供仓储 SQL 到期筛选复用。
	OllamaCloudUsageMinFetchInterval = acctcore.OllamaCloudUsageMinFetchInterval

	ollamaCloudUsageSettingsURL            = "https://ollama.com/settings"
	ollamaCloudUsageDefaultIntervalMinutes = acctcore.OllamaCloudUsageDefaultIntervalMinutes
	ollamaCloudUsageMinIntervalMinutes     = acctcore.OllamaCloudUsageMinIntervalMinutes
	ollamaCloudUsageMaxIntervalMinutes     = acctcore.OllamaCloudUsageMaxIntervalMinutes
	ollamaCloudUsageDefaultDebounceMinutes = acctcore.OllamaCloudUsageDefaultDebounceMinutes
	ollamaCloudUsageMinDebounceMinutes     = acctcore.OllamaCloudUsageMinDebounceMinutes
	ollamaCloudUsageMaxDebounceMinutes     = acctcore.OllamaCloudUsageMaxDebounceMinutes
	ollamaCloudUsageCycleInterval          = time.Minute
	ollamaCloudUsageManualRefreshInterval  = 30 * time.Second
	ollamaCloudUsageRequestTimeout         = 15 * time.Second
	ollamaCloudUsageMaxBodyBytes           = 512 * 1024
	ollamaCloudUsageMaxSessionBytes        = 16 * 1024
	ollamaCloudUsageMaxPerCycle            = 20
	ollamaCloudUsageConcurrency            = 4
	ollamaCloudUsageMaxDelay               = acctcore.OllamaCloudUsageMaxDelay
	ollamaCloudUsageLeaderLockKey          = "ollama:cloud:usage:leader"
	ollamaCloudUsageLeaderLockTTL          = 2 * time.Minute
)

var (
	ErrOllamaCloudUsageUnavailable        = acctcore.ErrOllamaCloudUsageUnavailable
	ErrOllamaCloudUsageAccountInvalid     = acctcore.ErrOllamaCloudUsageAccountInvalid
	ErrOllamaCloudUsageSessionRequired    = acctcore.ErrOllamaCloudUsageSessionRequired
	ErrOllamaCloudUsageEncryptionKey      = acctcore.ErrOllamaCloudUsageEncryptionKey
	ErrOllamaCloudUsageIdentityChanged    = acctcore.ErrOllamaCloudUsageIdentityChanged
	ErrOllamaCloudUsageRefreshRateLimited = acctcore.ErrOllamaCloudUsageRefreshRateLimited
	errOllamaCloudUsageUnauthorizedHTML   = errors.New("settings HTML is a sign-in page")
)

const OllamaCloudUsageStatusOK = acctcore.OllamaCloudUsageStatusOK
const OllamaCloudUsageStatusUnauthorized = acctcore.OllamaCloudUsageStatusUnauthorized
const OllamaCloudUsageStatusFailed = acctcore.OllamaCloudUsageStatusFailed

type OllamaCloudUsageSettings = acctcore.OllamaCloudUsageSettings
type OllamaCloudUsageWindow = acctcore.OllamaCloudUsageWindow
type OllamaCloudUsageModelWindow = acctcore.OllamaCloudUsageModelWindow

const OllamaCloudUsageModelWindowFiveHour = acctcore.OllamaCloudUsageModelWindowFiveHour
const OllamaCloudUsageModelWindowSevenDay = acctcore.OllamaCloudUsageModelWindowSevenDay

type OllamaCloudUsageModel = acctcore.OllamaCloudUsageModel
type OllamaCloudUsageData = acctcore.OllamaCloudUsageData
type OllamaCloudUsageSnapshot = acctcore.OllamaCloudUsageSnapshot
type OllamaCloudUsageState = acctcore.OllamaCloudUsageState
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
	return acctcore.DecodeOllamaCloudUsageSettings(raw)
}
func (s *SettingService) SetOllamaCloudUsageSettings(ctx context.Context, settings *OllamaCloudUsageSettings) error {
	if s == nil || s.settingRepo == nil {
		return ErrOllamaCloudUsageUnavailable
	}
	data, err := acctcore.EncodeOllamaCloudUsageSettings(settings)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyOllamaCloudUsageSettings, data)
}
func defaultOllamaCloudUsageSettings() *OllamaCloudUsageSettings {
	return acctcore.DefaultOllamaCloudUsageSettings()
}

func ollamaCloudUsageIsAutoRefreshDue(
	snapshot *OllamaCloudUsageSnapshot,
	groupLastUsedAt *time.Time,
	now time.Time,
	debounce, maxWait time.Duration,
) bool {
	return acctcore.OllamaCloudUsageIsAutoRefreshDue(snapshot, groupLastUsedAt, now, debounce, maxWait)
}
func ollamaCloudUsageAutoRefreshDueAt(
	snapshot *OllamaCloudUsageSnapshot,
	groupLastUsedAt *time.Time,
	debounce, maxWait time.Duration,
) (time.Time, bool) {
	return acctcore.OllamaCloudUsageAutoRefreshDueAt(snapshot, groupLastUsedAt, debounce, maxWait)
}

// scheduleOllamaCloudUsageActivity 记录 Ollama Cloud API Key 账号实际尝试了上游模型请求，
// 包括 429、5xx 和传输错误。本地鉴权或校验失败不得调用；DeferredService 会合并写入。
func scheduleOllamaCloudUsageActivity(deferred *DeferredService, account *Account) {
	if deferred == nil || account == nil || !IsOllamaCloudUsageAccount(account) {
		return
	}
	deferred.ScheduleLastUsedUpdate(account.ID)
}

func OllamaCloudUsageStateFromAccount(account *Account) *OllamaCloudUsageState {
	return acctcore.OllamaCloudUsageStateFromAccount(AccountRecordView(account))
}
func IsOllamaCloudUsageAccount(account *Account) bool {
	return acctcore.IsOllamaCloudUsageAccount(AccountRecordView(account))
}
func isOllamaCloudBaseURL(raw string) bool { return egress.IsOllamaCloudBaseURL(raw) }
func ollamaCloudUsageGroupFingerprint(account *Account) (string, bool) {
	return acctcore.OllamaCloudUsageGroupFingerprint(AccountRecordView(account))
}
func isExactOllamaCloudSettingsURL(parsed *url.URL) bool {
	return parsed != nil && parsed.Scheme == "https" && parsed.Host == "ollama.com" && parsed.Path == "/settings" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.RawPath == ""
}
func normalizeOllamaCloudUsageCookie(raw string) (string, error) {
	return egress.NormalizeOllamaCloudUsageCookie(raw)
}

func decodeOllamaCloudUsageSnapshot(extra map[string]any) *OllamaCloudUsageSnapshot {
	return acctcore.DecodeOllamaCloudUsageSnapshot(extra)
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
	return acctcore.NextOllamaCloudUsageDelay(intervalMinutes, failureCount, retryAfterDuration, rand.Int64N)
}

// 独立旧构造仅适配现有能力；完整生产图由 app 直接注入新 Store。
func NewOllamaCloudUsageService(repo AccountRepository, upstream HTTPUpstream, settings *SettingService, cipher SecretEncryptor, keyConfigured bool) *OllamaCloudUsageService {
	s := NewOllamaUsageExecution(upstream)
	s.now = time.Now
	s.instanceID = uuid.NewString()
	options := acctcore.OllamaUsageOptions{EncryptionKeyConfigured: keyConfigured, Now: s.currentTime, Jitter: rand.Int64N, InstanceID: s.instanceID, Log: func(format string, args ...any) { logger.LegacyPrintf("service.ollama_cloud_usage", format, args...) }, Lease: func(ctx context.Context, key, owner string, ttl time.Duration) (func(), bool) {
		return tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, key, owner, ttl)
	}}
	if upstream != nil {
		options.Fetch = s.FetchOllamaCloudUsage
	}
	var settingsPort acctcore.OllamaUsageSettingsStore
	if settings != nil {
		settingsPort = settings
	}
	s.core = acctcore.NewOllamaCloudUsageService(legacyOllamaRepository(repo), settingsPort, cipher, options)
	return s
}

func (s *OllamaCloudUsageService) Start() { s.Core().Start() }
func (s *OllamaCloudUsageService) StartContext(ctx context.Context) error {
	return s.Core().StartContext(ctx)
}
func (s *OllamaCloudUsageService) Stop() { s.Core().Stop() }
func (s *OllamaCloudUsageService) StopContext(ctx context.Context) error {
	return s.Core().StopContext(ctx)
}
func (s *OllamaCloudUsageService) GetSettings(ctx context.Context) (*OllamaCloudUsageSettings, error) {
	return s.Core().GetSettings(ctx)
}
func (s *OllamaCloudUsageService) UpdateSettings(ctx context.Context, settings *OllamaCloudUsageSettings) error {
	return s.Core().UpdateSettings(ctx, settings)
}
func (s *OllamaCloudUsageService) GetState(ctx context.Context, accountID int64) (*OllamaCloudUsageState, error) {
	return s.Core().GetState(ctx, accountID)
}
func (s *OllamaCloudUsageService) ResolveAccounts(ctx context.Context, values []*Account) error {
	if s == nil || len(values) == 0 {
		return nil
	}
	records := make([]*acctcore.Record, len(values))
	for i, v := range values {
		records[i] = AccountRecordView(v)
	}
	err := s.core.ResolveAccounts(ctx, records)
	if err != nil {
		return err
	}
	for i, v := range values {
		if v != nil && records[i] != nil {
			v.Extra = records[i].Extra
		}
	}
	return nil
}

func (s *OllamaCloudUsageService) SaveSession(ctx context.Context, accountID int64, session string) (*OllamaCloudUsageState, error) {
	return s.Core().SaveSession(ctx, accountID, session)
}
func (s *OllamaCloudUsageService) DeleteSession(ctx context.Context, accountID int64) (*OllamaCloudUsageState, error) {
	return s.Core().DeleteSession(ctx, accountID)
}
func (s *OllamaCloudUsageService) SetAutoRefresh(ctx context.Context, accountID int64, enabled bool) (*OllamaCloudUsageState, error) {
	return s.Core().SetAutoRefresh(ctx, accountID, enabled)
}
func (s *OllamaCloudUsageService) Refresh(ctx context.Context, accountID int64) (*OllamaCloudUsageState, error) {
	return s.Core().Refresh(ctx, accountID)
}
func (s *OllamaCloudUsageService) RunDue(ctx context.Context) error { return s.Core().RunDue(ctx) }
func (s *OllamaCloudUsageService) EnrichState(state *OllamaCloudUsageState) {
	s.Core().EnrichState(state)
}
func (s *OllamaCloudUsageService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}
