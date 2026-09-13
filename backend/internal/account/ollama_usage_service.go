// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	errors "errors"
	fmt "fmt"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	errgroup "golang.org/x/sync/errgroup"
	singleflight "golang.org/x/sync/singleflight"
	maps "maps"
	strconv "strconv"
	time "time"
)

type OllamaAccountReader interface {
	GetByID(context.Context, int64) (*Record, error)
}
type OllamaUsageSettingsStore interface {
	GetOllamaCloudUsageSettings(context.Context) (*OllamaCloudUsageSettings, error)
	SetOllamaCloudUsageSettings(context.Context, *OllamaCloudUsageSettings) error
}
type OllamaSessionCipher interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}
type OllamaUsageOptions struct {
	EncryptionKeyConfigured bool
	Now                     func() time.Time
	Jitter                  func(int64) int64
	InstanceID              string
	Fetch                   func(context.Context, OllamaUsageFetchInput) (*OllamaUsageObservation, error)
	Lease                   func(context.Context, string, string, time.Duration) (func(), bool)
	Log                     func(string, ...any)
}

// OllamaCloudUsageService 拥有分组会话、合并查询、手动/周期资格、失败快照和生命周期。
type OllamaCloudUsageService struct {
	accountRepo             OllamaAccountReader
	settingService          OllamaUsageSettingsStore
	encryptor               OllamaSessionCipher
	encryptionKeyConfigured bool
	runtime                 *OllamaUsageRuntime
	cycleMu                 *RefreshLock
	refreshGroup            singleflight.Group
	refreshSlots            chan struct{}
	now                     func() time.Time
	options                 OllamaUsageOptions
}

func NewOllamaCloudUsageService(repo OllamaAccountReader, settings OllamaUsageSettingsStore, cipher OllamaSessionCipher, options OllamaUsageOptions) *OllamaCloudUsageService {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Log == nil {
		options.Log = func(string, ...any) {}
	}
	s := &OllamaCloudUsageService{accountRepo: repo, settingService: settings, encryptor: cipher, encryptionKeyConfigured: options.EncryptionKeyConfigured, cycleMu: NewRefreshLock(), refreshSlots: make(chan struct{}, 4), now: options.Now, options: options}
	s.runtime = NewOllamaUsageRuntime(s.RunDue, options.Log)
	return s
}

type OllamaUsageRepository interface {
	ListOllamaCloudUsageGroupAccounts(context.Context, []*Record) ([]Record, error)
	SaveOllamaCloudUsageSession(context.Context, *Record, string, bool) error
	DeleteOllamaCloudUsageSession(context.Context, *Record) error
	SetOllamaCloudUsageAutoRefresh(context.Context, *Record, bool) error
	UpdateOllamaCloudUsageSnapshot(context.Context, *Record, *OllamaCloudUsageSnapshot) error
	DisableOllamaCloudUsageAutoRefresh(context.Context, *Record) error
	ListDueOllamaCloudUsageAccounts(context.Context, time.Time, time.Duration, time.Duration, int) ([]Record, error)
}

const (
	ollamaCloudUsageManualRefreshInterval = 30 * time.Second
	ollamaCloudUsageMaxPerCycle           = 20
	ollamaCloudUsageLeaderLockKey         = "ollama:cloud:usage:leader"
	ollamaCloudUsageLeaderLockTTL         = 2 * time.Minute
)

// StartContext 在组合根绑定后启动；旧入口复用相同运行拥有者。
func (s *OllamaCloudUsageService) Start() { _ = s.StartContext(context.Background()) }

func (s *OllamaCloudUsageService) StartContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.runtime.StartContext(ctx)
}

// StopContext 将手动及周期在途工作纳入调用方总预算，重复调用保留首次结果。
func (s *OllamaCloudUsageService) Stop() { _ = s.StopContext(context.Background()) }

func (s *OllamaCloudUsageService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.runtime.StopContext(ctx)
}

func (s *OllamaCloudUsageService) GetSettings(ctx context.Context) (*OllamaCloudUsageSettings, error) {
	if s == nil || s.settingService == nil {
		return DefaultOllamaCloudUsageSettings(), nil
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
	if err := s.ResolveAccounts(ctx, []*Record{account}); err != nil {
		return nil, err
	}
	state := OllamaCloudUsageStateFromAccount(account)
	s.EnrichState(state)
	return state, nil
}

// ResolveAccounts 将共享身份组管理的状态覆盖到给定账号对象上。
// 仓储通过一次有界查询解析所有匹配账号，避免账号列表逐行查询。
func (s *OllamaCloudUsageService) ResolveAccounts(ctx context.Context, accounts []*Record) error {
	if s == nil || s.accountRepo == nil || len(accounts) == 0 {
		return nil
	}
	writer, ok := s.accountRepo.(OllamaUsageRepository)
	if !ok {
		return nil
	}
	eligible := make([]*Record, 0, len(accounts))
	for _, account := range accounts {
		if _, ok := OllamaCloudUsageGroupFingerprint(account); ok {
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
	sources := make(map[string]*Record)
	for index := range siblings {
		candidate := &siblings[index]
		fingerprint, valid := OllamaCloudUsageGroupFingerprint(candidate)
		if !valid || !OllamaCloudUsageConfigured(candidate) {
			continue
		}
		current := sources[fingerprint]
		if current == nil || candidate.UpdatedAt.After(current.UpdatedAt) ||
			(candidate.UpdatedAt.Equal(current.UpdatedAt) && candidate.ID < current.ID) {
			sources[fingerprint] = candidate
		}
	}
	resolvedSources := make(map[string]*Record, len(sources))
	for fingerprint, source := range sources {
		clone := *source
		clone.Extra = make(map[string]any, len(source.Extra))
		maps.Copy(clone.Extra, source.Extra)
		resolvedSources[fingerprint] = &clone
	}
	for index := range siblings {
		candidate := &siblings[index]
		fingerprint, valid := OllamaCloudUsageGroupFingerprint(candidate)
		source := resolvedSources[fingerprint]
		if !valid || source == nil || !sameOllamaCloudUsageSession(source, candidate) {
			continue
		}
		candidateSnapshot := DecodeOllamaCloudUsageSnapshot(candidate.Extra)
		currentSnapshot := DecodeOllamaCloudUsageSnapshot(source.Extra)
		if candidateSnapshot != nil && (currentSnapshot == nil || candidateSnapshot.LastAttemptAt.After(currentSnapshot.LastAttemptAt)) {
			source.Extra[OllamaCloudUsageSnapshotExtraKey] = candidate.Extra[OllamaCloudUsageSnapshotExtraKey]
		}
	}
	for _, account := range eligible {
		fingerprint, _ := OllamaCloudUsageGroupFingerprint(account)
		applyOllamaCloudUsageManagedExtra(account, resolvedSources[fingerprint])
	}
	return nil
}

func (s *OllamaCloudUsageService) SaveSession(ctx context.Context, accountID int64, session string) (*OllamaCloudUsageState, error) {
	if s == nil || s.accountRepo == nil || s.encryptor == nil {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	if !s.encryptionKeyConfigured {
		return nil, ErrOllamaCloudUsageEncryptionKey
	}
	normalized, err := egress.NormalizeOllamaCloudUsageCookie(session)
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
	if err := s.ResolveAccounts(ctx, []*Record{account}); err != nil {
		return nil, err
	}
	ciphertext, err := s.encryptor.Encrypt(normalized)
	if err != nil {
		return nil, fmt.Errorf("encrypt Ollama web session: %w", err)
	}
	writer, ok := s.accountRepo.(OllamaUsageRepository)
	if !ok {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	preserveAutoRefresh := OllamaCloudUsageConfigured(account) && OllamaCloudUsageAutoRefreshEnabled(account)
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
	if err := s.ResolveAccounts(ctx, []*Record{account}); err != nil {
		return nil, err
	}
	writer, ok := s.accountRepo.(OllamaUsageRepository)
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
	if err := s.ResolveAccounts(ctx, []*Record{account}); err != nil {
		return nil, err
	}
	if enabled && !OllamaCloudUsageConfigured(account) {
		return nil, ErrOllamaCloudUsageSessionRequired
	}
	writer, ok := s.accountRepo.(OllamaUsageRepository)
	if !ok {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	if err := writer.SetOllamaCloudUsageAutoRefresh(ctx, account, enabled); err != nil {
		return nil, err
	}
	return s.GetState(ctx, accountID)
}

func (s *OllamaCloudUsageService) Refresh(ctx context.Context, accountID int64) (*OllamaCloudUsageState, error) {
	if s == nil {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	ctx, finish, err := s.runtime.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()

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
	ctx, finish, err := s.runtime.Begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if err := s.cycleMu.Lock(ctx); err != nil {
		return err
	}
	defer s.cycleMu.Unlock()
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	release, acquired := s.acquireLease(ctx)
	if !acquired {
		return nil
	}
	defer release()

	writer, ok := s.accountRepo.(OllamaUsageRepository)
	if !ok {
		return ErrOllamaCloudUsageUnavailable
	}
	now := s.currentTime()
	debounce, maxWait := OllamaCloudUsageDurations(settings)
	accounts, err := writer.ListDueOllamaCloudUsageAccounts(ctx, now, debounce, maxWait, ollamaCloudUsageMaxPerCycle)
	if err != nil {
		return fmt.Errorf("list due Ollama Cloud usage accounts: %w", err)
	}
	var group errgroup.Group
	seenGroups := make(map[string]struct{}, len(accounts))
	for index := range accounts {
		account := accounts[index]
		fingerprint, valid := OllamaCloudUsageGroupFingerprint(&account)
		if !valid || !account.IsActive() || !OllamaCloudUsageConfigured(&account) || !OllamaCloudUsageAutoRefreshEnabled(&account) {
			continue
		}
		if _, duplicate := seenGroups[fingerprint]; duplicate {
			continue
		}
		seenGroups[fingerprint] = struct{}{}
		snapshot := DecodeOllamaCloudUsageSnapshot(account.Extra)
		// ListDue 已将 api_key 分组的 MAX(last_used_at) 写入 Record.LastUsedAt。
		if !OllamaCloudUsageIsAutoRefreshDue(snapshot, account.LastUsedAt, now, debounce, maxWait) {
			continue
		}
		accountID := account.ID
		expected := account
		group.Go(func() error {
			if _, refreshErr := s.refreshAccount(ctx, accountID, settings, true); refreshErr != nil {
				if errors.Is(refreshErr, ErrOllamaCloudUsageIdentityChanged) {
					if disableErr := writer.DisableOllamaCloudUsageAutoRefresh(ctx, &expected); disableErr != nil {
						s.options.Log("disable_auto_refresh_failed: account_id=%d err=%v", accountID, disableErr)
					}
					return nil
				}
				s.options.Log("refresh_due_failed: account_id=%d err=%v", accountID, refreshErr)
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
		settings = DefaultOllamaCloudUsageSettings()
	}
	intervalMinutes := settings.IntervalMinutes
	debounce, maxWait := OllamaCloudUsageDurations(settings)
	anchor, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	key, valid := OllamaCloudUsageGroupFingerprint(anchor)
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
		currentKey, currentValid := OllamaCloudUsageGroupFingerprint(account)
		if !currentValid {
			return nil, ErrOllamaCloudUsageAccountInvalid
		}
		if currentKey != key {
			return nil, ErrOllamaCloudUsageIdentityChanged
		}
		if err := s.ResolveAccounts(ctx, []*Record{account}); err != nil {
			return nil, err
		}
		if !OllamaCloudUsageConfigured(account) {
			return nil, ErrOllamaCloudUsageSessionRequired
		}
		if !requireEnabled {
			if snapshot := DecodeOllamaCloudUsageSnapshot(account.Extra); snapshot != nil && !snapshot.LastAttemptAt.IsZero() {
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
			if !account.IsActive() || !OllamaCloudUsageAutoRefreshEnabled(account) {
				return nil, nil
			}
			groupLastUsed := account.LastUsedAt
			if writer, ok := s.accountRepo.(OllamaUsageRepository); ok {
				siblings, listErr := writer.ListOllamaCloudUsageGroupAccounts(ctx, []*Record{account})
				if listErr != nil {
					// 回退到当前账号自身的 last_used_at。它比分组最大值的活动信号更窄，
					// 到期检查可能因此跳过原本应执行的刷新，所以记录错误而不是静默改变语义。
					s.options.Log(
						"group_last_used_lookup_failed: account_id=%d err=%v", account.ID, listErr)
				} else {
					groupLastUsed = MaxOllamaCloudUsageGroupLastUsed(siblings)
				}
			}
			if !OllamaCloudUsageIsAutoRefreshDue(DecodeOllamaCloudUsageSnapshot(account.Extra), groupLastUsed, s.currentTime(), debounce, maxWait) {
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

func (s *OllamaCloudUsageService) refreshLoadedAccount(ctx context.Context, account *Record, intervalMinutes int) (*OllamaCloudUsageSnapshot, error) {
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
	cookie, err = egress.NormalizeOllamaCloudUsageCookie(cookie)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("OLLAMA_CLOUD_USAGE_SESSION_INVALID", "stored Ollama web session is invalid")
	}
	if s.options.Fetch == nil {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
			return nil, ErrOllamaCloudUsageIdentityChanged
		}
		proxyURL = account.Proxy.URL()
	}
	observation, err := s.options.Fetch(ctx, OllamaUsageFetchInput{AccountID: account.ID, Concurrency: account.Concurrency, ProxyURL: proxyURL, Cookie: cookie, ObservedAt: now})
	if err != nil {
		return nil, err
	}
	if observation.Failure != "" {
		return s.persistFailure(ctx, account, intervalMinutes, now, observation.HTTPStatus, observation.Failure, observation.RetryAfter, observation.Unauthorized)
	}
	snapshot := &OllamaCloudUsageSnapshot{
		Status:        OllamaCloudUsageStatusOK,
		Data:          observation.Data,
		FetchedAt:     &now,
		LastAttemptAt: now,
		NextRefreshAt: now.Add(NextOllamaCloudUsageDelay(intervalMinutes, 0, 0, s.options.Jitter)),
		HTTPStatus:    observation.HTTPStatus,
	}
	if err := s.updateSnapshot(ctx, account, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *OllamaCloudUsageService) persistFailure(
	ctx context.Context,
	account *Record,
	intervalMinutes int,
	now time.Time,
	httpStatus int,
	reason string,
	retryAfterDuration time.Duration,
	unauthorized bool,
) (*OllamaCloudUsageSnapshot, error) {
	previous := DecodeOllamaCloudUsageSnapshot(account.Extra)
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
		NextRefreshAt: now.Add(NextOllamaCloudUsageDelay(intervalMinutes, failureCount, retryAfterDuration, s.options.Jitter)),
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

func (s *OllamaCloudUsageService) updateSnapshot(ctx context.Context, account *Record, snapshot *OllamaCloudUsageSnapshot) error {
	// 停止或调用方取消后不得把迟到观测写入数据库。
	if err := ctx.Err(); err != nil {
		return err
	}
	writer, ok := s.accountRepo.(OllamaUsageRepository)
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

func (s *OllamaCloudUsageService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func sameOllamaCloudUsageSession(left, right *Record) bool {
	if left == nil || right == nil || left.Extra == nil || right.Extra == nil {
		return false
	}
	leftSession, leftOK := left.Extra[OllamaCloudUsageSessionExtraKey].(string)
	rightSession, rightOK := right.Extra[OllamaCloudUsageSessionExtraKey].(string)
	return leftOK && rightOK && leftSession != "" && leftSession == rightSession
}

func applyOllamaCloudUsageManagedExtra(target, source *Record) {
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
func (s *OllamaCloudUsageService) acquireLease(ctx context.Context) (func(), bool) {
	if s.options.Lease == nil {
		return func() {}, true
	}
	return s.options.Lease(ctx, ollamaCloudUsageLeaderLockKey, s.options.InstanceID, ollamaCloudUsageLeaderLockTTL)
}
