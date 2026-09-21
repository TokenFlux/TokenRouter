// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	fmt "fmt"
	reflect "reflect"
	strconv "strconv"
	strings "strings"
	time "time"

	transfer "github.com/TokenFlux/TokenRouter/internal/account/transfer"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
)

// CRSAccountStore 保留原同步配置、影子关系及凭据专用写入能力。
type CRSAccountStore interface {
	ShadowProxyStore
	Create(context.Context, *Record) error
	GetByCRSAccountID(context.Context, string) (*Record, error)
	ListCRSAccountIDs(context.Context) (map[string]int64, error)
}
type CRSProxyStore interface {
	ListActive(context.Context) ([]egress.Proxy, error)
	Create(context.Context, *egress.Proxy) error
}
type CRSExporter interface {
	Fetch(context.Context, string, string, string) (*transfer.CRSExportResponse, error)
}
type CRSOptions struct {
	Now     func() time.Time
	Warn    func(string, ...any)
	Refresh func(context.Context, *Record) error
}

// CRSSync 拥有六类来源的逐项同步与预览，网络登录和供应商交换经端口提供。
type CRSSync struct {
	accountRepo CRSAccountStore
	proxyRepo   CRSProxyStore
	exporter    CRSExporter
	options     CRSOptions
}

func NewCRSSync(accounts CRSAccountStore, proxies CRSProxyStore, exporter CRSExporter, options CRSOptions) *CRSSync {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	return &CRSSync{accounts, proxies, exporter, options}
}

// 导入已提交后才尝试刷新，失败保留原逐项成功计数。
func (s *CRSSync) refreshOAuthToken(ctx context.Context, value *Record) {
	if s.options.Refresh != nil {
		_ = s.options.Refresh(ctx, value)
	}
}

// GuardCRSShadowParentInvariant 守住「有 spark 影子的母账号」不变量(与 AdminService.UpdateAccount 一致):
// 影子读透母账号凭据,母账号必须**始终是 OpenAI OAuth**。CRS 同步按全局 crs_account_id 匹配既有账号
// (GetByCRSAccountID 已排除影子、但能命中母账号),各平台分支会重写 Platform/Type;若 CRS ID 跨 kind/平台
// 碰撞,非 OpenAI 分支会把母账号改成 Anthropic/Gemini 或 api_key→影子 resolveCredentialAccount 必崩(外审第9轮,
// 收紧第8轮仅查 Type 的版本:Claude OAuth 把 Type 保持 OAuth 但 Platform 改成 Anthropic 能绕过旧守卫)。
// 故任何会把母账号目标结果改离 OpenAI OAuth 的 CRS 更新,在其有影子时一律拒绝(须先删影子);返回非 nil
// 表示该账号更新应被跳过(调用方标记 failed)。
func GuardCRSShadowParentInvariant(ctx context.Context, repo ShadowProxyStore, existing *Record, newPlatform, newType string) error {
	if existing == nil {
		return nil
	}
	// 目标仍是合法影子父(OpenAI OAuth)→ 放行(常见:OpenAI OAuth 分支重新同步母账号),免去一次查询。
	if newPlatform == PlatformOpenAI && newType == AccountTypeOAuth {
		return nil
	}
	shadows, err := repo.ListShadowsByParent(ctx, existing.ID)
	if err != nil {
		return fmt.Errorf("check spark shadows for crs update: %w", err)
	}
	if len(shadows) > 0 {
		return fmt.Errorf("cannot change a spark-shadow parent account to %s/%s; it must stay OpenAI OAuth (delete the shadow first)", newPlatform, newType)
	}
	return nil
}

type SyncFromCRSInput struct {
	BaseURL            string
	Username           string
	Password           string
	SyncProxies        bool
	SelectedAccountIDs []string // nil 创建全部；显式空只更新已存在账号
}

type SyncFromCRSItemResult struct {
	CRSAccountID string `json:"crs_account_id"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Action       string `json:"action"` // 创建、更新、失败或跳过
	Error        string `json:"error,omitempty"`
}

type SyncFromCRSResult struct {
	Created int                     `json:"created"`
	Updated int                     `json:"updated"`
	Skipped int                     `json:"skipped"`
	Failed  int                     `json:"failed"`
	Items   []SyncFromCRSItemResult `json:"items"`
}

func (s *CRSSync) SyncFromCRS(ctx context.Context, input SyncFromCRSInput) (*SyncFromCRSResult, error) {
	exported, err := s.exporter.Fetch(ctx, input.BaseURL, input.Username, input.Password)
	if err != nil {
		return nil, err
	}

	now := s.options.Now().UTC().Format(time.RFC3339)

	result := &SyncFromCRSResult{
		Items: make(
			[]SyncFromCRSItemResult,
			0,
			len(exported.Data.ClaudeAccounts)+len(exported.Data.ClaudeConsoleAccounts)+len(exported.Data.OpenAIOAuthAccounts)+len(exported.Data.OpenAIResponsesAccounts)+len(exported.Data.GeminiOAuthAccounts)+len(exported.Data.GeminiAPIKeyAccounts),
		),
	}

	selectedSet := CRSBuildSelectedSet(input.SelectedAccountIDs)

	var proxies []egress.Proxy
	if input.SyncProxies {
		proxies, _ = s.proxyRepo.ListActive(ctx)
	}

	// Claude 来源转换为 Anthropic OAuth 或 Setup Token。
	for _, src := range exported.Data.ClaudeAccounts {
		item := SyncFromCRSItemResult{
			CRSAccountID: src.ID,
			Kind:         src.Kind,
			Name:         src.Name,
		}

		targetType := strings.TrimSpace(src.AuthType)
		if targetType == "" {
			targetType = "oauth"
		}
		if targetType != AccountTypeOAuth && targetType != AccountTypeSetupToken {
			item.Action = "skipped"
			item.Error = "unsupported authType: " + targetType
			result.Skipped++
			result.Items = append(result.Items, item)
			continue
		}

		accessToken, _ := src.Credentials["access_token"].(string)
		if strings.TrimSpace(accessToken) == "" {
			item.Action = "failed"
			item.Error = "missing access_token"
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		proxyID, err := egress.MatchOrCreateCRSProxy(ctx, s.proxyRepo, input.SyncProxies, &proxies, src.Proxy, fmt.Sprintf("crs-%s", src.Name))
		if err != nil {
			item.Action = "failed"
			item.Error = "proxy sync failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		credentials := CRSSanitizeCredentialsMap(src.Credentials)
		// Claude 基地址去除末尾 /v1。
		CRSCleanBaseURL(credentials, "/v1")
		// 将 ISO 到期值转换为 Unix 时间戳。
		if expiresAtStr, ok := credentials["expires_at"].(string); ok && expiresAtStr != "" {
			if t, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
				credentials["expires_at"] = t.Unix()
			}
		}
		// 未提供预热拦截开关时保持默认 false。
		if _, exists := credentials["intercept_warmup_requests"]; !exists {
			credentials["intercept_warmup_requests"] = false
		}
		priority := CRSClampPriority(src.Priority)
		concurrency := 3
		status := MapCRSStatus(src.IsActive, src.Status)

		// 保留来源扩展字段并补入同步元数据。
		extra := make(map[string]any)
		if src.Extra != nil {
			for k, v := range src.Extra {
				extra[k] = v
			}
		}
		extra["crs_account_id"] = src.ID
		extra["crs_kind"] = src.Kind
		extra["crs_synced_at"] = now
		// 从凭据投影组织与账号 ID 到扩展信息。
		if orgUUID, ok := src.Credentials["org_uuid"]; ok {
			extra["org_uuid"] = orgUUID
		}
		if accountUUID, ok := src.Credentials["account_uuid"]; ok {
			extra["account_uuid"] = accountUUID
		}

		existing, err := s.accountRepo.GetByCRSAccountID(ctx, src.ID)
		if err != nil {
			item.Action = "failed"
			item.Error = "db lookup failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		if existing != nil {
			extra = CRSMergeMap(existing.Extra, extra)
			credentials = CRSMergeMap(existing.Credentials, credentials)
		}
		ReconcileCRSOllamaCloudUsageExtra(existing, PlatformAnthropic, targetType, credentials, extra)

		if existing == nil {
			if !CRSShouldCreateAccount(src.ID, selectedSet) {
				item.Action = "skipped"
				item.Error = "not selected"
				result.Skipped++
				result.Items = append(result.Items, item)
				continue
			}
			account := &Record{
				Name:        CRSDefaultName(src.Name, src.ID),
				Platform:    PlatformAnthropic,
				Type:        targetType,
				Credentials: credentials,
				Extra:       CloneValues(extra),
				ProxyID:     proxyID,
				Concurrency: concurrency,
				Priority:    priority,
				Status:      status,
				Schedulable: src.Schedulable,
			}
			if err := s.accountRepo.Create(ctx, account); err != nil {
				item.Action = "failed"
				item.Error = "create failed: " + err.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				continue
			}
			// 创建成功后尽力刷新令牌。
			if targetType == AccountTypeOAuth {
				s.refreshOAuthToken(ctx, account)
			}
			item.Action = "created"
			result.Created++
			result.Items = append(result.Items, item)
			continue
		}

		// 母账号守卫(外审第9轮):CRS ID 跨平台碰撞时,本(Anthropic OAuth)分支不得改坏有 spark 影子的 OpenAI 母账号。
		if gerr := GuardCRSShadowParentInvariant(ctx, s.accountRepo, existing, PlatformAnthropic, targetType); gerr != nil {
			item.Action = "failed"
			item.Error = gerr.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		// 更新现有账号配置。
		existing.Extra = CloneValues(extra)
		existing.Name = CRSDefaultName(src.Name, src.ID)
		existing.Platform = PlatformAnthropic
		existing.Type = targetType
		existing.Credentials = credentials
		if proxyID != nil {
			existing.ProxyID = proxyID
		}
		existing.Concurrency = concurrency
		existing.Priority = priority
		existing.Status = status
		existing.Schedulable = src.Schedulable

		if err := WriteConfiguration(ctx, s.accountRepo, existing, CRSSyncConfiguration(proxyID != nil)); err != nil {
			item.Action = "failed"
			item.Error = "update failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		// 配置更新成功后尽力刷新令牌。
		if targetType == AccountTypeOAuth {
			s.refreshOAuthToken(ctx, existing)
		}

		item.Action = "updated"
		result.Updated++
		result.Items = append(result.Items, item)
	}

	// Claude Console 来源转换为 Anthropic API Key。
	for _, src := range exported.Data.ClaudeConsoleAccounts {
		item := SyncFromCRSItemResult{
			CRSAccountID: src.ID,
			Kind:         src.Kind,
			Name:         src.Name,
		}

		apiKey, _ := src.Credentials["api_key"].(string)
		if strings.TrimSpace(apiKey) == "" {
			item.Action = "failed"
			item.Error = "missing api_key"
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		proxyID, err := egress.MatchOrCreateCRSProxy(ctx, s.proxyRepo, input.SyncProxies, &proxies, src.Proxy, fmt.Sprintf("crs-%s", src.Name))
		if err != nil {
			item.Action = "failed"
			item.Error = "proxy sync failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		credentials := CRSSanitizeCredentialsMap(src.Credentials)
		priority := CRSClampPriority(src.Priority)
		concurrency := 3
		if src.MaxConcurrentTasks > 0 {
			concurrency = src.MaxConcurrentTasks
		}
		status := MapCRSStatus(src.IsActive, src.Status)

		extra := map[string]any{
			"crs_account_id": src.ID,
			"crs_kind":       src.Kind,
			"crs_synced_at":  now,
		}

		existing, err := s.accountRepo.GetByCRSAccountID(ctx, src.ID)
		if err != nil {
			item.Action = "failed"
			item.Error = "db lookup failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		if existing != nil {
			extra = CRSMergeMap(existing.Extra, extra)
			credentials = CRSMergeMap(existing.Credentials, credentials)
		}
		ReconcileCRSOllamaCloudUsageExtra(existing, PlatformAnthropic, AccountTypeAPIKey, credentials, extra)

		if existing == nil {
			if !CRSShouldCreateAccount(src.ID, selectedSet) {
				item.Action = "skipped"
				item.Error = "not selected"
				result.Skipped++
				result.Items = append(result.Items, item)
				continue
			}
			account := &Record{
				Name:        CRSDefaultName(src.Name, src.ID),
				Platform:    PlatformAnthropic,
				Type:        AccountTypeAPIKey,
				Credentials: credentials,
				Extra:       CloneValues(extra),
				ProxyID:     proxyID,
				Concurrency: concurrency,
				Priority:    priority,
				Status:      status,
				Schedulable: src.Schedulable,
			}
			if err := s.accountRepo.Create(ctx, account); err != nil {
				item.Action = "failed"
				item.Error = "create failed: " + err.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				continue
			}
			item.Action = "created"
			result.Created++
			result.Items = append(result.Items, item)
			continue
		}

		// 母账号守卫(外审第9轮):CRS ID 跨平台碰撞时,本(Anthropic APIKey)分支不得改坏有 spark 影子的 OpenAI 母账号。
		if gerr := GuardCRSShadowParentInvariant(ctx, s.accountRepo, existing, PlatformAnthropic, AccountTypeAPIKey); gerr != nil {
			item.Action = "failed"
			item.Error = gerr.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		existing.Extra = CloneValues(extra)
		existing.Name = CRSDefaultName(src.Name, src.ID)
		existing.Platform = PlatformAnthropic
		existing.Type = AccountTypeAPIKey
		existing.Credentials = credentials
		if proxyID != nil {
			existing.ProxyID = proxyID
		}
		existing.Concurrency = concurrency
		existing.Priority = priority
		existing.Status = status
		existing.Schedulable = src.Schedulable

		if err := WriteConfiguration(ctx, s.accountRepo, existing, CRSSyncConfiguration(proxyID != nil)); err != nil {
			item.Action = "failed"
			item.Error = "update failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		item.Action = "updated"
		result.Updated++
		result.Items = append(result.Items, item)
	}

	// OpenAI OAuth 来源保留 OAuth 类型。
	for _, src := range exported.Data.OpenAIOAuthAccounts {
		item := SyncFromCRSItemResult{
			CRSAccountID: src.ID,
			Kind:         src.Kind,
			Name:         src.Name,
		}

		accessToken, _ := src.Credentials["access_token"].(string)
		if strings.TrimSpace(accessToken) == "" {
			item.Action = "failed"
			item.Error = "missing access_token"
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		proxyID, err := egress.MatchOrCreateCRSProxy(ctx, s.proxyRepo,
			input.SyncProxies,
			&proxies,
			src.Proxy,
			fmt.Sprintf("crs-%s", src.Name),
		)
		if err != nil {
			item.Action = "failed"
			item.Error = "proxy sync failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		credentials := CRSSanitizeCredentialsMap(src.Credentials)
		// 规范化令牌类型。
		if v, ok := credentials["token_type"].(string); !ok || strings.TrimSpace(v) == "" {
			credentials["token_type"] = "Bearer"
		}
		// 将 ISO 到期值转换为 Unix 时间戳。
		if expiresAtStr, ok := credentials["expires_at"].(string); ok && expiresAtStr != "" {
			if t, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
				credentials["expires_at"] = t.Unix()
			}
		}
		priority := CRSClampPriority(src.Priority)
		concurrency := 3
		status := MapCRSStatus(src.IsActive, src.Status)

		// 保留来源扩展字段并补入同步元数据。
		extra := make(map[string]any)
		if src.Extra != nil {
			for k, v := range src.Extra {
				extra[k] = v
			}
		}
		extra["crs_account_id"] = src.ID
		extra["crs_kind"] = src.Kind
		extra["crs_synced_at"] = now
		// 将来源 crs_email 投影为 email。
		if crsEmail, ok := src.Extra["crs_email"]; ok {
			extra["email"] = crsEmail
		}

		existing, err := s.accountRepo.GetByCRSAccountID(ctx, src.ID)
		if err != nil {
			item.Action = "failed"
			item.Error = "db lookup failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		var existingExtra map[string]any
		if existing != nil {
			existingExtra = existing.Extra
		}
		extra = CRSMergeMap(existingExtra, extra)
		DiscardDeprecatedExtra(extra)
		if existing != nil {
			credentials = CRSMergeMap(existing.Credentials, credentials)
		}
		ReconcileCRSOllamaCloudUsageExtra(existing, PlatformOpenAI, AccountTypeOAuth, credentials, extra)

		if existing == nil {
			if !CRSShouldCreateAccount(src.ID, selectedSet) {
				item.Action = "skipped"
				item.Error = "not selected"
				result.Skipped++
				result.Items = append(result.Items, item)
				continue
			}
			account := &Record{
				Name:        CRSDefaultName(src.Name, src.ID),
				Platform:    PlatformOpenAI,
				Type:        AccountTypeOAuth,
				Credentials: credentials,
				Extra:       CloneValues(extra),
				ProxyID:     proxyID,
				Concurrency: concurrency,
				Priority:    priority,
				Status:      status,
				Schedulable: src.Schedulable,
			}
			if err := s.accountRepo.Create(ctx, account); err != nil {
				item.Action = "failed"
				item.Error = "create failed: " + err.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				continue
			}
			// 创建成功后尽力刷新令牌。
			s.refreshOAuthToken(ctx, account)
			item.Action = "created"
			result.Created++
			result.Items = append(result.Items, item)
			continue
		}

		existing.Extra = CloneValues(extra)
		existing.Name = CRSDefaultName(src.Name, src.ID)
		existing.Platform = PlatformOpenAI
		existing.Type = AccountTypeOAuth
		existing.Credentials = credentials
		if proxyID != nil {
			existing.ProxyID = proxyID
		}
		existing.Concurrency = concurrency
		existing.Priority = priority
		existing.Status = status
		existing.Schedulable = src.Schedulable

		if err := WriteConfiguration(ctx, s.accountRepo, existing, CRSSyncConfiguration(proxyID != nil)); err != nil {
			item.Action = "failed"
			item.Error = "update failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		// 配置更新成功后尽力刷新令牌。
		s.refreshOAuthToken(ctx, existing)

		// 母账号 proxy 经 CRS 改动后同步到其 spark 影子,避免影子保留旧 proxy 出现出站漂移(外审第8轮)。
		// 影子 proxy 恒继承母账号(创建即继承、AdminService 编辑也传播)。best-effort:母账号本身已成功
		// 更新,影子传播失败仅记录告警,不回退该条目状态。
		if perr := PropagateAccountProxyToShadows(ctx, s.accountRepo, existing.ID, existing.ProxyID); perr != nil {
			s.options.Warn("crs_sync_propagate_proxy_to_shadows_failed", "account_id", existing.ID, "error", perr)
		}

		item.Action = "updated"
		result.Updated++
		result.Items = append(result.Items, item)
	}

	// OpenAI Responses 来源转换为 API Key。
	for _, src := range exported.Data.OpenAIResponsesAccounts {
		item := SyncFromCRSItemResult{
			CRSAccountID: src.ID,
			Kind:         src.Kind,
			Name:         src.Name,
		}

		apiKey, _ := src.Credentials["api_key"].(string)
		if strings.TrimSpace(apiKey) == "" {
			item.Action = "failed"
			item.Error = "missing api_key"
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		if baseURL, ok := src.Credentials["base_url"].(string); !ok || strings.TrimSpace(baseURL) == "" {
			src.Credentials["base_url"] = "https://api.openai.com"
		}
		// OpenAI 基地址去除末尾 /v1。
		CRSCleanBaseURL(src.Credentials, "/v1")

		proxyID, err := egress.MatchOrCreateCRSProxy(ctx, s.proxyRepo,
			input.SyncProxies,
			&proxies,
			src.Proxy,
			fmt.Sprintf("crs-%s", src.Name),
		)
		if err != nil {
			item.Action = "failed"
			item.Error = "proxy sync failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		credentials := CRSSanitizeCredentialsMap(src.Credentials)
		priority := CRSClampPriority(src.Priority)
		concurrency := 3
		status := MapCRSStatus(src.IsActive, src.Status)

		extra := make(map[string]any, len(src.Extra)+3)
		for key, value := range src.Extra {
			extra[key] = value
		}
		extra["crs_account_id"] = src.ID
		extra["crs_kind"] = src.Kind
		extra["crs_synced_at"] = now

		existing, err := s.accountRepo.GetByCRSAccountID(ctx, src.ID)
		if err != nil {
			item.Action = "failed"
			item.Error = "db lookup failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		var existingExtra map[string]any
		if existing != nil {
			existingExtra = existing.Extra
		}
		extra = CRSMergeMap(existingExtra, extra)
		DiscardDeprecatedExtra(extra)
		if existing != nil {
			credentials = CRSMergeMap(existing.Credentials, credentials)
		}
		ReconcileCRSOllamaCloudUsageExtra(existing, PlatformOpenAI, AccountTypeAPIKey, credentials, extra)

		if existing == nil {
			if !CRSShouldCreateAccount(src.ID, selectedSet) {
				item.Action = "skipped"
				item.Error = "not selected"
				result.Skipped++
				result.Items = append(result.Items, item)
				continue
			}
			account := &Record{
				Name:        CRSDefaultName(src.Name, src.ID),
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Credentials: credentials,
				Extra:       CloneValues(extra),
				ProxyID:     proxyID,
				Concurrency: concurrency,
				Priority:    priority,
				Status:      status,
				Schedulable: src.Schedulable,
			}
			if err := s.accountRepo.Create(ctx, account); err != nil {
				item.Action = "failed"
				item.Error = "create failed: " + err.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				continue
			}
			item.Action = "created"
			result.Created++
			result.Items = append(result.Items, item)
			continue
		}

		// 母账号守卫(外审第8/9轮):CRS 不得把有 spark 影子的母账号改离 OpenAI OAuth(此处会翻成 api_key),
		// 否则影子读透母凭据失败、resolveCredentialAccount 必报错、spark 调度与用量刷新全崩。须先删影子再改。
		if gerr := GuardCRSShadowParentInvariant(ctx, s.accountRepo, existing, PlatformOpenAI, AccountTypeAPIKey); gerr != nil {
			item.Action = "failed"
			item.Error = gerr.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		existing.Extra = CloneValues(extra)
		existing.Name = CRSDefaultName(src.Name, src.ID)
		existing.Platform = PlatformOpenAI
		existing.Type = AccountTypeAPIKey
		existing.Credentials = credentials
		if proxyID != nil {
			existing.ProxyID = proxyID
		}
		existing.Concurrency = concurrency
		existing.Priority = priority
		existing.Status = status
		existing.Schedulable = src.Schedulable

		if err := WriteConfiguration(ctx, s.accountRepo, existing, CRSSyncConfiguration(proxyID != nil)); err != nil {
			item.Action = "failed"
			item.Error = "update failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		item.Action = "updated"
		result.Updated++
		result.Items = append(result.Items, item)
	}

	// Gemini OAuth 来源保留 OAuth 类型。
	for _, src := range exported.Data.GeminiOAuthAccounts {
		item := SyncFromCRSItemResult{
			CRSAccountID: src.ID,
			Kind:         src.Kind,
			Name:         src.Name,
		}

		refreshToken, _ := src.Credentials["refresh_token"].(string)
		if strings.TrimSpace(refreshToken) == "" {
			item.Action = "failed"
			item.Error = "missing refresh_token"
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		proxyID, err := egress.MatchOrCreateCRSProxy(ctx, s.proxyRepo, input.SyncProxies, &proxies, src.Proxy, fmt.Sprintf("crs-%s", src.Name))
		if err != nil {
			item.Action = "failed"
			item.Error = "proxy sync failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		credentials := CRSSanitizeCredentialsMap(src.Credentials)
		if v, ok := credentials["token_type"].(string); !ok || strings.TrimSpace(v) == "" {
			credentials["token_type"] = "Bearer"
		}
		// 到期值按原 Gemini 契约转换为 Unix 秒字符串。
		if expiresAtStr, ok := credentials["expires_at"].(string); ok && strings.TrimSpace(expiresAtStr) != "" {
			if t, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
				credentials["expires_at"] = strconv.FormatInt(t.Unix(), 10)
			}
		}

		extra := make(map[string]any)
		if src.Extra != nil {
			for k, v := range src.Extra {
				extra[k] = v
			}
		}
		extra["crs_account_id"] = src.ID
		extra["crs_kind"] = src.Kind
		extra["crs_synced_at"] = now

		existing, err := s.accountRepo.GetByCRSAccountID(ctx, src.ID)
		if err != nil {
			item.Action = "failed"
			item.Error = "db lookup failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		if existing != nil {
			extra = CRSMergeMap(existing.Extra, extra)
			credentials = CRSMergeMap(existing.Credentials, credentials)
		}
		ReconcileCRSOllamaCloudUsageExtra(existing, PlatformGemini, AccountTypeOAuth, credentials, extra)

		if existing == nil {
			if !CRSShouldCreateAccount(src.ID, selectedSet) {
				item.Action = "skipped"
				item.Error = "not selected"
				result.Skipped++
				result.Items = append(result.Items, item)
				continue
			}
			account := &Record{
				Name:        CRSDefaultName(src.Name, src.ID),
				Platform:    PlatformGemini,
				Type:        AccountTypeOAuth,
				Credentials: credentials,
				Extra:       CloneValues(extra),
				ProxyID:     proxyID,
				Concurrency: 3,
				Priority:    CRSClampPriority(src.Priority),
				Status:      MapCRSStatus(src.IsActive, src.Status),
				Schedulable: src.Schedulable,
			}
			if err := s.accountRepo.Create(ctx, account); err != nil {
				item.Action = "failed"
				item.Error = "create failed: " + err.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				continue
			}
			s.refreshOAuthToken(ctx, account)
			item.Action = "created"
			result.Created++
			result.Items = append(result.Items, item)
			continue
		}

		// 母账号守卫(外审第9轮):CRS ID 跨平台碰撞时,本(Gemini OAuth)分支不得改坏有 spark 影子的 OpenAI 母账号。
		if gerr := GuardCRSShadowParentInvariant(ctx, s.accountRepo, existing, PlatformGemini, AccountTypeOAuth); gerr != nil {
			item.Action = "failed"
			item.Error = gerr.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		existing.Extra = CloneValues(extra)
		existing.Name = CRSDefaultName(src.Name, src.ID)
		existing.Platform = PlatformGemini
		existing.Type = AccountTypeOAuth
		existing.Credentials = credentials
		if proxyID != nil {
			existing.ProxyID = proxyID
		}
		existing.Concurrency = 3
		existing.Priority = CRSClampPriority(src.Priority)
		existing.Status = MapCRSStatus(src.IsActive, src.Status)
		existing.Schedulable = src.Schedulable

		if err := WriteConfiguration(ctx, s.accountRepo, existing, CRSSyncConfiguration(proxyID != nil)); err != nil {
			item.Action = "failed"
			item.Error = "update failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		s.refreshOAuthToken(ctx, existing)

		item.Action = "updated"
		result.Updated++
		result.Items = append(result.Items, item)
	}

	// Gemini API Key 来源保留 API Key 类型。
	for _, src := range exported.Data.GeminiAPIKeyAccounts {
		item := SyncFromCRSItemResult{
			CRSAccountID: src.ID,
			Kind:         src.Kind,
			Name:         src.Name,
		}

		apiKey, _ := src.Credentials["api_key"].(string)
		if strings.TrimSpace(apiKey) == "" {
			item.Action = "failed"
			item.Error = "missing api_key"
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		proxyID, err := egress.MatchOrCreateCRSProxy(ctx, s.proxyRepo, input.SyncProxies, &proxies, src.Proxy, fmt.Sprintf("crs-%s", src.Name))
		if err != nil {
			item.Action = "failed"
			item.Error = "proxy sync failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		credentials := CRSSanitizeCredentialsMap(src.Credentials)
		if baseURL, ok := credentials["base_url"].(string); !ok || strings.TrimSpace(baseURL) == "" {
			credentials["base_url"] = "https://generativelanguage.googleapis.com"
		}

		extra := make(map[string]any)
		if src.Extra != nil {
			for k, v := range src.Extra {
				extra[k] = v
			}
		}
		extra["crs_account_id"] = src.ID
		extra["crs_kind"] = src.Kind
		extra["crs_synced_at"] = now

		existing, err := s.accountRepo.GetByCRSAccountID(ctx, src.ID)
		if err != nil {
			item.Action = "failed"
			item.Error = "db lookup failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		if existing != nil {
			extra = CRSMergeMap(existing.Extra, extra)
			credentials = CRSMergeMap(existing.Credentials, credentials)
		}
		ReconcileCRSOllamaCloudUsageExtra(existing, PlatformGemini, AccountTypeAPIKey, credentials, extra)

		if existing == nil {
			if !CRSShouldCreateAccount(src.ID, selectedSet) {
				item.Action = "skipped"
				item.Error = "not selected"
				result.Skipped++
				result.Items = append(result.Items, item)
				continue
			}
			account := &Record{
				Name:        CRSDefaultName(src.Name, src.ID),
				Platform:    PlatformGemini,
				Type:        AccountTypeAPIKey,
				Credentials: credentials,
				Extra:       CloneValues(extra),
				ProxyID:     proxyID,
				Concurrency: 3,
				Priority:    CRSClampPriority(src.Priority),
				Status:      MapCRSStatus(src.IsActive, src.Status),
				Schedulable: src.Schedulable,
			}
			if err := s.accountRepo.Create(ctx, account); err != nil {
				item.Action = "failed"
				item.Error = "create failed: " + err.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				continue
			}
			item.Action = "created"
			result.Created++
			result.Items = append(result.Items, item)
			continue
		}

		// 母账号守卫(外审第9轮):CRS ID 跨平台碰撞时,本(Gemini APIKey)分支不得改坏有 spark 影子的 OpenAI 母账号。
		if gerr := GuardCRSShadowParentInvariant(ctx, s.accountRepo, existing, PlatformGemini, AccountTypeAPIKey); gerr != nil {
			item.Action = "failed"
			item.Error = gerr.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		existing.Extra = CloneValues(extra)
		existing.Name = CRSDefaultName(src.Name, src.ID)
		existing.Platform = PlatformGemini
		existing.Type = AccountTypeAPIKey
		existing.Credentials = credentials
		if proxyID != nil {
			existing.ProxyID = proxyID
		}
		existing.Concurrency = 3
		existing.Priority = CRSClampPriority(src.Priority)
		existing.Status = MapCRSStatus(src.IsActive, src.Status)
		existing.Schedulable = src.Schedulable

		if err := WriteConfiguration(ctx, s.accountRepo, existing, CRSSyncConfiguration(proxyID != nil)); err != nil {
			item.Action = "failed"
			item.Error = "update failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}

		item.Action = "updated"
		result.Updated++
		result.Items = append(result.Items, item)
	}

	return result, nil
}

func CRSMergeMap(existing map[string]any, updates map[string]any) map[string]any {
	out := make(map[string]any, len(existing)+len(updates))
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range updates {
		out[k] = v
	}
	return CloneValues(out)
}

func ReconcileCRSOllamaCloudUsageExtra(
	existing *Record,
	targetPlatform, targetType string,
	targetCredentials map[string]any,
	extra map[string]any,
) {
	DiscardDeprecatedExtra(extra)
	for _, key := range []string{
		OllamaCloudUsageSessionExtraKey,
		OllamaCloudUsageAutoRefreshExtraKey,
		OllamaCloudUsageSnapshotExtraKey,
	} {
		delete(extra, key)
	}
	if existing == nil {
		return
	}
	target := &Record{Platform: targetPlatform, Type: targetType, Credentials: targetCredentials}
	if IsOllamaCloudUsageAccount(existing) && IsOllamaCloudUsageAccount(target) &&
		reflect.DeepEqual(OllamaCloudUsageIdentity(existing), OllamaCloudUsageIdentity(target)) {
		if session, ok := existing.Extra[OllamaCloudUsageSessionExtraKey]; ok {
			extra[OllamaCloudUsageSessionExtraKey] = session
		}
		if enabled, ok := existing.Extra[OllamaCloudUsageAutoRefreshExtraKey]; ok {
			extra[OllamaCloudUsageAutoRefreshExtraKey] = enabled
		}
		if snapshot, ok := existing.Extra[OllamaCloudUsageSnapshotExtraKey]; ok {
			extra[OllamaCloudUsageSnapshotExtraKey] = snapshot
		}
	}
}

// CRSDefaultProxyName 为旧辅助入口委托代理所属规则。
func CRSDefaultProxyName(base, protocol, host string, port int) string {
	return egress.CRSDefaultProxyName(base, protocol, host, port)
}

func CRSDefaultName(name, id string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return "CRS " + id
}

func CRSClampPriority(priority int) int {
	if priority < 1 || priority > 100 {
		return 50
	}
	return priority
}

func CRSSanitizeCredentialsMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for k, v := range input {
		// 按原约定省略 nil 凭据键。
		if v != nil {
			out[k] = v
		}
	}
	return CloneValues(out)
}

func MapCRSStatus(isActive bool, status string) string {
	if !isActive {
		return "inactive"
	}
	if strings.EqualFold(strings.TrimSpace(status), "error") {
		return "error"
	}
	return "active"
}

// CRSCleanBaseURL 仅删除 Claude/OpenAI 基地址的既定末尾段。
func CRSCleanBaseURL(credentials map[string]any, suffixToRemove string) {
	if baseURL, ok := credentials["base_url"].(string); ok && baseURL != "" {
		trimmed := strings.TrimSpace(baseURL)
		if strings.HasSuffix(trimmed, suffixToRemove) {
			credentials["base_url"] = strings.TrimSuffix(trimmed, suffixToRemove)
		}
	}
}

// CRSBuildSelectedSet 区分未发送与显式空集合：前者创建全部，后者不新建。
func CRSBuildSelectedSet(ids []string) map[string]struct{} {
	if ids == nil {
		return nil
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// CRSShouldCreateAccount 仅决定是否创建，未选中不阻止更新已存在账号。
func CRSShouldCreateAccount(crsID string, selectedSet map[string]struct{}) bool {
	if selectedSet == nil {
		return true
	}
	_, ok := selectedSet[crsID]
	return ok
}

// PreviewFromCRSResult 保持原同步预览计数与逐项列表。
type PreviewFromCRSResult struct {
	NewAccounts      []CRSPreviewAccount `json:"new_accounts"`
	ExistingAccounts []CRSPreviewAccount `json:"existing_accounts"`
}

// CRSPreviewAccount 只包含预览展示字段。
type CRSPreviewAccount struct {
	CRSAccountID string `json:"crs_account_id"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Platform     string `json:"platform"`
	Type         string `json:"type"`
}

// PreviewFromCRS 先获取导出，再一次查询本地来源 ID，保持原分类顺序。
func (s *CRSSync) PreviewFromCRS(ctx context.Context, input SyncFromCRSInput) (*PreviewFromCRSResult, error) {
	exported, err := s.exporter.Fetch(ctx, input.BaseURL, input.Username, input.Password)
	if err != nil {
		return nil, err
	}

	// 一次读取已存在的来源 ID。
	existingCRSIDs, err := s.accountRepo.ListCRSAccountIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing CRS accounts: %w", err)
	}

	result := &PreviewFromCRSResult{
		NewAccounts:      make([]CRSPreviewAccount, 0),
		ExistingAccounts: make([]CRSPreviewAccount, 0),
	}

	classify := func(crsID, kind, name, platform, accountType string) {
		preview := CRSPreviewAccount{
			CRSAccountID: crsID,
			Kind:         kind,
			Name:         CRSDefaultName(name, crsID),
			Platform:     platform,
			Type:         accountType,
		}
		if _, exists := existingCRSIDs[crsID]; exists {
			result.ExistingAccounts = append(result.ExistingAccounts, preview)
		} else {
			result.NewAccounts = append(result.NewAccounts, preview)
		}
	}

	for _, src := range exported.Data.ClaudeAccounts {
		authType := strings.TrimSpace(src.AuthType)
		if authType == "" {
			authType = AccountTypeOAuth
		}
		classify(src.ID, src.Kind, src.Name, PlatformAnthropic, authType)
	}
	for _, src := range exported.Data.ClaudeConsoleAccounts {
		classify(src.ID, src.Kind, src.Name, PlatformAnthropic, AccountTypeAPIKey)
	}
	for _, src := range exported.Data.OpenAIOAuthAccounts {
		classify(src.ID, src.Kind, src.Name, PlatformOpenAI, AccountTypeOAuth)
	}
	for _, src := range exported.Data.OpenAIResponsesAccounts {
		classify(src.ID, src.Kind, src.Name, PlatformOpenAI, AccountTypeAPIKey)
	}
	for _, src := range exported.Data.GeminiOAuthAccounts {
		classify(src.ID, src.Kind, src.Name, PlatformGemini, AccountTypeOAuth)
	}
	for _, src := range exported.Data.GeminiAPIKeyAccounts {
		classify(src.ID, src.Kind, src.Name, PlatformGemini, AccountTypeAPIKey)
	}

	return result, nil
}
