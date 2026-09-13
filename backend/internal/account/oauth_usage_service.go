// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	errors "errors"
	fmt "fmt"
	errgroup "golang.org/x/sync/errgroup"
	sync "sync"
	time "time"
)

type OAuthUsageReader interface {
	GetByID(context.Context, int64) (*Record, error)
	GetByIDs(context.Context, []int64) ([]*Record, error)
}

// OAuthUsageOptions 提供当前平台观测能力；具体供应商 HTTP、身份和响应解析不进入核心。
type OAuthUsageOptions struct {
	Now              func() time.Time
	Jitter           func(int64) int64
	Log              func(string, ...any)
	Warn             func(string, ...any)
	OpenAI           OpenAIUsageOptions
	Gemini           GeminiUsageOptions
	Antigravity      AntigravityUsageOptions
	Grok             GrokUsageOptions
	Qoder            QoderUsageOptions
	Anthropic        func(context.Context, *Record) (*ClaudeUsageResponse, error)
	OpenAIQuotaPause func(context.Context, *Record, *UsageInfo)
}

// OAuthUsageService 拥有入口、并发批量、Anthropic 主被动查询、统一恢复与受控查询生命周期。
type OAuthUsageService struct {
	accountRepo OAuthUsageReader
	cache       *OAuthUsageCache
	stats       *LocalUsageStatistics
	options     OAuthUsageOptions
	activity    operationActivity
}

var ErrOAuthUsageStopped = errors.New("oauth account usage is stopped")

func NewOAuthUsageService(reader OAuthUsageReader, cache *OAuthUsageCache, stats *LocalUsageStatistics, options OAuthUsageOptions) *OAuthUsageService {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Jitter == nil {
		options.Jitter = func(int64) int64 { return 0 }
	}
	if options.Log == nil {
		options.Log = func(string, ...any) {}
	}
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	if cache == nil {
		cache = NewOAuthUsageCache()
	}
	return &OAuthUsageService{accountRepo: reader, cache: cache, stats: stats, options: options}
}
func (s *OAuthUsageService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.activity.stop(ctx, "OAuth account usage")
}

// BeginDetached 保留等待方独立取消，在资源 Stop 时仍取消并等待原本脱离调用方的查询。
func (s *OAuthUsageService) BeginDetached(parent context.Context, budget time.Duration) (context.Context, func(), error) {
	ctx, finish, err := s.activity.begin(context.WithoutCancel(parent), ErrOAuthUsageStopped)
	// 派生工作属于已接纳查询；停止与派生竞争时按取消结束，不重新解释为新入口拒绝。
	if err == ErrOAuthUsageStopped {
		return nil, nil, context.Canceled
	}
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	return ctx, func() { cancel(); finish() }, nil
}

func SupportsAnthropicPassiveUsage(account *Record) bool {
	return account != nil && account.IsAnthropicOAuthOrSetupToken()
}

func batchUsageErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *OAuthUsageService) GetUsageForAccount(ctx context.Context, account *Record, forceProbe bool) (*UsageInfo, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, err
	}
	defer finish()

	if account == nil {
		return nil, fmt.Errorf("account is required")
	}
	accountID := account.ID
	identity := UsageCacheIdentity(account)

	if account.Platform == PlatformOpenAI && account.Type == AccountTypeOAuth {
		usage, err := s.GetOpenAIUsage(ctx, account, forceProbe)
		if err == nil {
			s.options.OpenAIQuotaPause(ctx, account, usage)
			s.recoverAccountError(ctx, account)
		}
		return usage, err
	}

	if account.Platform == PlatformGemini {
		usage, err := s.GetGeminiUsage(ctx, account)
		if err == nil {
			s.recoverAccountError(ctx, account)
		}
		return usage, err
	}

	// Antigravity 平台：使用 AntigravityQuotaFetcher 获取额度
	if account.Platform == PlatformAntigravity {
		usage, err := s.GetAntigravityUsage(ctx, account)
		if err == nil {
			s.recoverAccountError(ctx, account)
		}
		return usage, err
	}

	if account.Platform == PlatformGrok {
		usage, err := s.GetGrokUsage(ctx, account, forceProbe)
		if err == nil && usage != nil && usage.Error == "" {
			s.recoverAccountError(ctx, account)
		}
		return usage, err
	}

	if account.Platform == PlatformQoder {
		usage, err := s.GetQoderUsage(ctx, account, forceProbe)
		if err == nil && (usage == nil || (usage.Error == "" && usage.ErrorCode == "")) {
			s.recoverAccountError(ctx, account)
		}
		return usage, err
	}

	// 只有oauth类型账号可以通过API获取usage（有profile scope）
	if account.CanGetUsage() {
		var apiResp *ClaudeUsageResponse

		// 1. 检查缓存（成功响应 3 分钟 / 错误响应 1 分钟）
		if cached, ok := s.cache.LoadAPI(accountID); ok {
			if cache, ok := cached.(*OAuthAPIUsageCache); ok && cache != nil && cache.Identity == identity {
				age := s.options.Now().Sub(cache.Timestamp)
				if cache.Err != nil && age < OAuthUsageAPIErrorCacheTTL {
					// 负缓存命中：返回缓存的错误，避免重试风暴
					return nil, cache.Err
				}
				if cache.Response != nil && age < OAuthUsageAPICacheTTL {
					apiResp = cache.Response
				}
			}
		}

		// 2. 如果没有有效缓存，通过 singleflight 从 API 获取（防止并发击穿）
		if apiResp == nil {
			// 随机延迟：打散多账号并发请求，避免同一时刻大量相同 TLS 指纹请求
			// 触发上游反滥用检测。延迟范围 0~800ms，仅在缓存未命中时生效。
			jitter := time.Duration(s.options.Jitter(int64(OAuthUsageAPIQueryMaxJitter)))
			select {
			case <-time.After(jitter):
			case <-ctx.Done():
				return nil, ctx.Err()
			}

			flightKey := fmt.Sprintf("usage:%d", accountID)
			result, flightErr, _ := s.cache.DoAPI(flightKey, func() (any, error) {
				// 再次检查缓存（可能在等待 singleflight 期间被其他请求填充）
				if cached, ok := s.cache.LoadAPI(accountID); ok {
					if cache, ok := cached.(*OAuthAPIUsageCache); ok && cache != nil && cache.Identity == identity {
						age := s.options.Now().Sub(cache.Timestamp)
						if cache.Err != nil && age < OAuthUsageAPIErrorCacheTTL {
							return oauthAPIFlightResult{Err: cache.Err, Identity: identity}, nil
						}
						if cache.Response != nil && age < OAuthUsageAPICacheTTL {
							return oauthAPIFlightResult{Response: cache.Response, Identity: identity}, nil
						}
					}
				}
				resp, fetchErr := s.options.Anthropic(ctx, account)
				if fetchErr != nil {
					// 负缓存：缓存错误响应，防止后续请求重复触发 429
					s.cache.StoreAPI(accountID, &OAuthAPIUsageCache{Identity: identity,
						Err:       fetchErr,
						Timestamp: s.options.Now(),
					})
					return oauthAPIFlightResult{Err: fetchErr, Identity: identity}, nil
				}
				// 缓存成功响应
				s.cache.StoreAPI(accountID, &OAuthAPIUsageCache{Identity: identity,
					Response:  resp,
					Timestamp: s.options.Now(),
				})
				return oauthAPIFlightResult{Response: clonePointer(resp), Identity: identity}, nil
			})
			if flightErr != nil {
				return nil, flightErr
			}
			completed, ok := result.(oauthAPIFlightResult)
			if !ok || completed.Identity != identity {
				return nil, ErrUsageObservationChanged
			}
			if completed.Err != nil {
				return nil, completed.Err
			}
			apiResp = completed.Response
		}

		// 3. 构建 UsageInfo（每次都重新计算 RemainingSeconds）
		now := s.options.Now()
		usage := BuildUsageInfo(apiResp, &now, s.options.Now, s.options.Log)

		// 4. 添加窗口统计（有独立缓存，1 分钟）
		s.stats.AddWindowStats(ctx, account, usage)

		// 5. 将主动查询结果同步到被动缓存，下次 passive 加载即为最新值
		s.SyncActiveToPassive(ctx, account, usage)

		// 6. 上游 usage API 目前不一定下发 Fable 7d 窗口；缺失时回填被动采样
		// （7d_oi 响应头）的数据，避免主动查询后 7d F 进度条丢失。
		if usage.SevenDayFable == nil {
			usage.SevenDayFable = BuildPassiveUsageWindow(account.Extra, "passive_usage_7d_oi_utilization", "passive_usage_7d_oi_reset", s.options.Now)
		}

		s.recoverAccountError(ctx, account)
		return usage, nil
	}

	// Setup Token账号：根据session_window推算（没有profile scope，无法调用usage API）
	if account.Type == AccountTypeSetupToken {
		usage := EstimateSetupTokenUsage(account, s.options.Now)
		// 添加窗口统计
		s.stats.AddWindowStats(ctx, account, usage)
		return usage, nil
	}

	// API Key账号不支持usage查询
	return nil, fmt.Errorf("account type %s does not support usage query", account.Type)
}

// GetUsage 获取账号使用量
// OAuth账号: 调用Anthropic API获取真实数据（需要profile scope），API响应缓存10分钟，窗口统计缓存1分钟
// Setup Token账号: 根据session_window推算5h窗口，7d数据不可用（没有profile scope）
// API Key账号: 不支持usage查询
func (s *OAuthUsageService) GetUsage(ctx context.Context, accountID int64, force ...bool) (*UsageInfo, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, err
	}
	defer finish()

	forceProbe := len(force) > 0 && force[0]

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("get account failed: %w", err)
	}

	return s.GetUsageForAccount(ctx, account, forceProbe)
}

// GetUsageBatch 批量获取账号使用量。
// Anthropic OAuth/SetupToken 统一走 passive 链路，其他账号复用现有主动查询逻辑。
// 单个账号失败不会中断整批请求，错误会按账号返回。
func (s *OAuthUsageService) GetUsageBatch(ctx context.Context, accountIDs []int64, force bool) (map[int64]*UsageInfo, map[int64]string, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, nil, err
	}
	defer finish()

	uniqueIDs := make([]int64, 0, len(accountIDs))
	seen := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		if _, ok := seen[accountID]; ok {
			continue
		}
		seen[accountID] = struct{}{}
		uniqueIDs = append(uniqueIDs, accountID)
	}

	usageByAccount := make(map[int64]*UsageInfo, len(uniqueIDs))
	errorsByAccount := make(map[int64]string)
	if len(uniqueIDs) == 0 {
		return usageByAccount, errorsByAccount, nil
	}

	accounts, err := s.accountRepo.GetByIDs(ctx, uniqueIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("get accounts failed: %w", err)
	}

	accountsByID := make(map[int64]*Record, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		accountsByID[account.ID] = account
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(6)

	for _, accountID := range uniqueIDs {
		id := accountID
		account := accountsByID[id]
		if account == nil {
			mu.Lock()
			errorsByAccount[id] = ErrAccountNotFound.Error()
			mu.Unlock()
			continue
		}

		g.Go(func() error {
			var usage *UsageInfo
			var usageErr error
			if SupportsAnthropicPassiveUsage(account) {
				usage, usageErr = s.GetPassiveUsageForAccount(gctx, account)
			} else {
				usage, usageErr = s.GetUsageForAccount(gctx, account, force)
			}

			mu.Lock()
			defer mu.Unlock()
			if usageErr != nil {
				errorsByAccount[id] = batchUsageErrorMessage(usageErr)
				return nil
			}
			usageByAccount[id] = usage
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, nil, err
	}

	return usageByAccount, errorsByAccount, nil
}

// GetPassiveUsage 从 Record.Extra 中的被动采样数据构建 UsageInfo，不调用外部 API。
// 仅适用于 Anthropic OAuth / SetupToken 账号。
func (s *OAuthUsageService) GetPassiveUsage(ctx context.Context, accountID int64) (*UsageInfo, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, err
	}
	defer finish()

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("get account failed: %w", err)
	}

	return s.GetPassiveUsageForAccount(ctx, account)
}

func (s *OAuthUsageService) GetPassiveUsageForAccount(ctx context.Context, account *Record) (*UsageInfo, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, err
	}
	defer finish()

	if !SupportsAnthropicPassiveUsage(account) {
		return nil, fmt.Errorf("passive usage only supported for Anthropic OAuth/SetupToken accounts")
	}

	// 复用 estimateSetupTokenUsage 构建 5h 窗口（OAuth 和 SetupToken 逻辑一致）
	info := EstimateSetupTokenUsage(account, s.options.Now)
	info.Source = "passive"

	// 设置采样时间
	if raw, ok := account.Extra["passive_usage_sampled_at"]; ok {
		if str, ok := raw.(string); ok {
			if t, err := time.Parse(time.RFC3339, str); err == nil {
				info.UpdatedAt = &t
			}
		}
	}

	// 构建 7d 窗口（从被动采样数据）
	info.SevenDay = BuildPassiveUsageWindow(account.Extra, "passive_usage_7d_utilization", "passive_usage_7d_reset", s.options.Now)

	// 构建 7d Fable 窗口（从被动采样的 7d_oi 响应头数据）
	info.SevenDayFable = BuildPassiveUsageWindow(account.Extra, "passive_usage_7d_oi_utilization", "passive_usage_7d_oi_reset", s.options.Now)

	// 添加窗口统计
	s.stats.AddWindowStats(ctx, account, info)

	return info, nil
}

// SyncActiveToPassive 将主动查询的最新数据回写到 Extra 被动缓存，
// 这样下次被动加载时能看到最新值。
func (s *OAuthUsageService) SyncActiveToPassive(ctx context.Context, observed *Record, usage *UsageInfo) {
	if observed == nil || usage == nil {
		return
	}
	accountID := observed.ID
	version := ObserveUsageVersion(observed)
	extraUpdates := make(map[string]any, 4)

	if usage.FiveHour != nil {
		extraUpdates["session_window_utilization"] = usage.FiveHour.Utilization / 100
	}
	if usage.SevenDay != nil {
		extraUpdates["passive_usage_7d_utilization"] = usage.SevenDay.Utilization / 100
		if usage.SevenDay.ResetsAt != nil {
			extraUpdates["passive_usage_7d_reset"] = usage.SevenDay.ResetsAt.Unix()
		}
	}
	if usage.SevenDayFable != nil {
		extraUpdates["passive_usage_7d_oi_utilization"] = usage.SevenDayFable.Utilization / 100
		if usage.SevenDayFable.ResetsAt != nil {
			extraUpdates["passive_usage_7d_oi_reset"] = usage.SevenDayFable.ResetsAt.Unix()
		}
	}

	if len(extraUpdates) > 0 {
		extraUpdates["passive_usage_sampled_at"] = s.options.Now().UTC().Format(time.RFC3339)
		writer, ok := s.accountRepo.(UsageExtraWriter)
		if !ok {
			s.options.Warn("sync_active_to_passive_failed", "account_id", accountID, "error", ErrUsageObservationWriterMissing)
			return
		}
		applied, err := writer.UpdateUsageExtraIfUnchanged(ctx, version, extraUpdates)
		if err != nil {
			s.options.Warn("sync_active_to_passive_failed", "account_id", accountID, "error", err)
		} else if !applied {
			return
		}
	}

	// 5h ResetsAt 必须回写到 SessionWindowEnd column，estimateSetupTokenUsage
	// 读这个字段作为窗口结束时间；只塞 Extra 会让 UI 一直拿到上个窗口的过期时间。
	if usage.FiveHour != nil && usage.FiveHour.ResetsAt != nil {
		writer, ok := s.accountRepo.(UsageSessionWindowWriter)
		if !ok {
			s.options.Warn("sync_active_to_passive_session_window_end_failed", "account_id", accountID, "error", ErrUsageObservationWriterMissing)
			return
		}
		if _, err := writer.UpdateUsageSessionWindowEndIfUnchanged(ctx, version, observed.SessionWindowEnd, *usage.FiveHour.ResetsAt); err != nil {
			s.options.Warn("sync_active_to_passive_session_window_end_failed", "account_id", accountID, "error", err)
		}
	}
}

// recoverAccountError 仅适配本轮观察值与新条件恢复能力。
func (s *OAuthUsageService) recoverAccountError(ctx context.Context, value *Record) {
	writer, _ := s.accountRepo.(UsageRecoveryWriter)
	applied, err := RecoverUsageAccountError(ctx, value, writer)
	if err != nil {
		s.options.Log("[usage] failed to clear recoverable account error for account %d: %v", value.ID, err)
		return
	}
	if applied {
		value.Status = StatusActive
		value.ErrorMessage = ""
	}
}

// oauthAPIFlightResult 连同负缓存保存来源，原账号 key 的等待者不能消费另一身份结果。
type oauthAPIFlightResult struct {
	Response *ClaudeUsageResponse
	Err      error
	Identity string
}

// UsageSessionWindowWriter 保留窗口列的独立提交，并比较查询前身份及原窗口。
type UsageSessionWindowWriter interface {
	UpdateUsageSessionWindowEndIfUnchanged(context.Context, UsageObservationVersion, *time.Time, time.Time) (bool, error)
}
