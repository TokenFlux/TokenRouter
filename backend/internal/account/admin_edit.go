// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func (s *Admin) UpdateAccount(ctx context.Context, id int64, input *UpdateAccountInput) (*Record, error) {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	var expected *CredentialVersion
	if input.ExpectedCredentials != nil {
		frozen := CloneCredentialVersion(*input.ExpectedCredentials)
		expected = &frozen
		if !MatchesCredentialVersion(account, frozen) {
			return nil, ErrRefreshAccountStateChanged
		}
	}
	credentialInput := input.Credentials
	if input.PatchCredentials {
		credentialInput = CloneValues(input.Credentials)
		copy := *input
		copy.Credentials = MergeCredentials(account.Credentials, CloneValues(credentialInput))
		input = &copy
	}
	var extraPatch map[string]any
	if input.PatchExtra {
		extraPatch = CloneValues(input.Extra)
		copy := *input
		copy.Extra = CloneValues(account.Extra)
		if copy.Extra == nil {
			copy.Extra = map[string]any{}
		}
		for key, value := range extraPatch {
			copy.Extra[key] = value
		}
		input = &copy
	}
	var computeResetAt, normalizeWindowAt *time.Time
	previousCNUsageIdentity := CNUsageMonitorIdentityFingerprint(account)
	originalQoderSite, originalQoderSiteErr := s.options.Credentials.Site(account)
	originalQoderPAT := strings.TrimSpace(account.GetCredential("pat"))
	normalizedExtra, shouldReplaceExtra := NormalizeDeprecatedAccountExtraUpdate(input.Extra)
	if shouldReplaceExtra {
		if IsOpenAIAPIKeyAccount(account) && HasOpenAIConfigurationPatch(nil, normalizedExtra) {
			if err := NormalizeOpenAIAPIKeyConfigurationPatch(nil, normalizedExtra); err != nil {
				return nil, err
			}
		}
		normalizedExtra, err = NormalizeGrokMediaEligibilityUpdateExtra(account, input, normalizedExtra)
		if err != nil {
			return nil, err
		}
		if err := ValidateUpstreamRequestIDHeaderExtra(normalizedExtra); err != nil {
			return nil, err
		}
	}
	previousOllamaUsageIdentity := OllamaCloudUsageIdentity(account)
	// 安全/身份不变量(影子账号):通用更新路径被 edit/re-auth/refresh/batch 共用,
	// 必须在此守住,否则仅在创建时的保证可被这些路径绕过。
	if account.IsCredentialShadow() {
		// 影子绝不持有凭据(凭据只在母账号)——外审 F5。
		if !IsAllowedSparkShadowCredentialsUpdate(input.Credentials) {
			return nil, infraerrors.Newf(infraerrors.CategoryBadRequest, "SPARK_SHADOW_NO_CREDENTIALS",
				"spark shadow accounts do not hold auth credentials; only model mapping can be configured on the shadow account")
		}
		// 影子 type 不可变——很多上游逻辑按 account.Type 分支(OAuth transform / ChatGPT
		// header 注入 / WS OAuth 决策),改成 apikey 会让 spark 影子被选中后按错误协议转发(外审 G7)。
		if input.Type != "" && input.Type != account.Type {
			return nil, infraerrors.Newf(infraerrors.CategoryBadRequest, "SPARK_SHADOW_IMMUTABLE_TYPE",
				"spark shadow account type cannot be changed; it must remain an OpenAI OAuth shadow")
		}
	} else if input.Type != "" && input.Type != account.Type && input.Type != AccountTypeOAuth {
		// 母账号守卫(外审 D/P1):有 spark 影子的账号不能把 type 改出 OpenAI OAuth——影子读透母
		// 凭据,母变成 apikey/setup_token 会让影子被调度后按错协议失败(resolveCredentialAccount
		// 必报错)。须先删影子再改 type。
		shadows, serr := s.accountRepo.ListShadowsByParent(ctx, id)
		if serr != nil {
			return nil, serr
		}
		if len(shadows) > 0 {
			return nil, infraerrors.New(infraerrors.CategoryBadRequest, "SPARK_SHADOW_PARENT_IMMUTABLE_TYPE",
				"cannot change account type while it has a spark shadow; delete the shadow first")
		}
	}
	wasOveragesEnabled := account.IsOveragesEnabled()

	if input.Name != "" {
		account.Name = input.Name
	}
	if input.Type != "" {
		account.Type = input.Type
	}
	if input.Notes != nil {
		account.Notes = NormalizeAccountNotes(input.Notes)
	}
	if account.IsCredentialShadow() && input.Credentials != nil {
		account.Credentials = SanitizeSparkShadowCredentials(input.Credentials)
	} else if len(input.Credentials) > 0 {
		incomingCredentials := PreserveProtocolCredentials(account.Credentials, input.Credentials)
		if IsOpenAIAPIKeyAccount(account) {
			// 先规范化本次增量，确保旧客户端提交的别名能覆盖账号中已有的新键。
			if err := NormalizeOpenAIAPIKeyConfigurationPatch(incomingCredentials, nil); err != nil {
				return nil, err
			}
		}
		// 敏感子键采用"incoming 没提供就保留"的合并语义：前端响应已脱敏，
		// 全对象 PUT 编辑时不会再带回 token，避免覆盖时清空已有凭证。
		account.Credentials = MergePreservingSensitiveCreds(account.Credentials, incomingCredentials)
		// 校验并规范化请求头覆写配置（header 名小写化、格式检查）
		if err := egress.NormalizeHeaderOverrideCredentials(account.Credentials); err != nil {
			return nil, err
		}
		// 移除不得与 OAuth token 一同保存的 SSO 和密码残留。
		account.Credentials = SanitizeStoredCredentials(account.Platform, account.Credentials)
		if err := ValidateGeminiThirdPartyBaseURL(account); err != nil {
			return nil, err
		}
	}
	// Extra 使用 map：需要区分“未提供(nil)”与“显式清空({})”。
	// 关闭配额限制时前端会删除 quota_* 键并提交 extra:{}，此时也必须落库；只有废弃键时则不替换。
	if shouldReplaceExtra {
		DiscardDeprecatedAccountExtra(normalizedExtra)
		if err := NormalizeUpstreamUsageExtra(normalizedExtra); err != nil {
			return nil, err
		}
		// 旧版编辑器可能未携带该键；整份 Extra 替换时仍保留已有查询配置。
		if _, provided := input.Extra[UpstreamUsageQueryExtraKey]; !provided {
			if value, exists := account.Extra[UpstreamUsageQueryExtraKey]; exists {
				if normalized, ok := NormalizedUpstreamUsageConfigValue(value); ok {
					normalizedExtra[UpstreamUsageQueryExtraKey] = normalized
				}
			}
		}
		delete(normalizedExtra, OllamaCloudUsageSessionExtraKey)
		delete(normalizedExtra, OllamaCloudUsageAutoRefreshExtraKey)
		delete(normalizedExtra, OllamaCloudUsageSnapshotExtraKey)
		delete(normalizedExtra, CNUsageMonitorSnapshotExtraKey)
		// 保留配额用量和专用服务受管字段，防止普通账号编辑意外覆盖。
		for _, key := range []string{
			"quota_used",
			"quota_daily_used",
			"quota_daily_start",
			"quota_weekly_used",
			"quota_weekly_start",
			"grok_billing_snapshot",
			OllamaCloudUsageSessionExtraKey,
			OllamaCloudUsageAutoRefreshExtraKey,
			OllamaCloudUsageSnapshotExtraKey,
			CNUsageMonitorSnapshotExtraKey,
		} {
			if v, ok := account.Extra[key]; ok {
				normalizedExtra[key] = v
			}
		}
		if IsOpenAIAPIKeyAccount(account) {
			// 新增能力字段对旧版编辑器保持兼容；未回传时保留已有管理员设置。
			_, continuationProvided := input.Extra[ExtraKeyResponsesContinuationSupported]
			if !continuationProvided {
				if value, ok := account.Extra[ExtraKeyResponsesContinuationSupported]; ok {
					normalizedExtra[ExtraKeyResponsesContinuationSupported] = value
				}
			}
		}
		normalizedExtra = PrepareCodexFingerprintExtraForUpdate(account, normalizedExtra, s.options.Creation.NewSeed)
		account.Extra = normalizedExtra
		if account.Platform == PlatformAntigravity && wasOveragesEnabled && !account.IsOveragesEnabled() {
			delete(account.Extra, "antigravity_credits_overages") // 清理旧版 overages 运行态
			// 清除 AICredits 限流 key
			if rawLimits, ok := account.Extra["model_rate_limits"].(map[string]any); ok {
				delete(rawLimits, "AICredits")
			}
		}
		if account.Platform == PlatformAntigravity && !wasOveragesEnabled && account.IsOveragesEnabled() {
			delete(account.Extra, "model_rate_limits")
			delete(account.Extra, "antigravity_credits_overages") // 清理旧版 overages 运行态
		}
		// 校验并预计算固定时间重置的下次重置时间
		if err := ValidateQuotaResetConfig(account.Extra, s.options.Creation.LoadLocation); err != nil {
			return nil, err
		}
		resetNow := s.options.Creation.Now()
		computeResetAt = &resetNow
		ComputeQuotaResetAt(account.Extra, resetNow, s.options.Creation.LoadLocation)
		windowNow := s.options.Creation.Now()
		normalizeWindowAt = &windowNow
		NormalizeFixedQuotaWindows(account.Extra, windowNow, s.options.Creation.LoadLocation)
	}
	if input.Extra == nil {
		account.Extra = PrepareCodexFingerprintExtraForUpdate(account, account.Extra, s.options.Creation.NewSeed)
	}
	// 影子代理恒继承母账号(由 propagateProxyToShadows 同步),不接受独立编辑——外审 B/P1;
	// 否则要等母账号下次改 proxy 才被覆盖,期间影子会出现"有时继承、有时独立"的漂移。
	if input.ProxyID != nil && !account.IsCredentialShadow() {
		// 0 表示清除代理（前端发送 0 而不是 null 来表达清除意图）
		if *input.ProxyID == 0 {
			account.ProxyID = nil
		} else {
			account.ProxyID = input.ProxyID
		}
		account.Proxy = nil // 清除关联对象，防止 GORM Save 时根据 Proxy.ID 覆盖 ProxyID
	}
	DiscardDeprecatedAccountExtra(account.Extra)
	ApplyLegacyProtocolPatch(account, input.Credentials, input.Extra)
	if err := NormalizeCNProviderCredentials(account, false); err != nil {
		return nil, err
	}
	if err := NormalizeOpenAIAPIKeyConfiguration(account); err != nil {
		return nil, err
	}
	if err := NormalizeAccountProtocols(account); err != nil {
		return nil, err
	}
	if account.Extra != nil {
		if !IsOllamaCloudUsageAccount(account) {
			delete(account.Extra, OllamaCloudUsageSessionExtraKey)
			delete(account.Extra, OllamaCloudUsageAutoRefreshExtraKey)
			delete(account.Extra, OllamaCloudUsageSnapshotExtraKey)
		} else if !reflect.DeepEqual(previousOllamaUsageIdentity, OllamaCloudUsageIdentity(account)) {
			delete(account.Extra, OllamaCloudUsageSessionExtraKey)
			delete(account.Extra, OllamaCloudUsageAutoRefreshExtraKey)
			delete(account.Extra, OllamaCloudUsageSnapshotExtraKey)
		}
	}
	// 只在指针非 nil 时更新 Concurrency（支持设置为 0）
	if input.Concurrency != nil {
		account.Concurrency = NormalizeAccountConcurrency(account.Platform, account.Type, *input.Concurrency)
	}
	// 只在指针非 nil 时更新 Priority（支持设置为 0）
	if input.Priority != nil {
		account.Priority = *input.Priority
	}
	if input.RateMultiplier != nil {
		if *input.RateMultiplier < 0 {
			return nil, errors.New("rate_multiplier must be >= 0")
		}
		account.RateMultiplier = input.RateMultiplier
	}
	if input.LoadFactor != nil {
		if *input.LoadFactor <= 0 {
			account.LoadFactor = nil // 0 或负数表示清除
		} else if *input.LoadFactor > 10000 {
			return nil, errors.New("load_factor must be <= 10000")
		} else {
			account.LoadFactor = input.LoadFactor
		}
	}
	if input.Status != "" {
		account.Status = input.Status
	}
	if input.ExpiresAt != nil {
		if *input.ExpiresAt <= 0 {
			account.ExpiresAt = nil
		} else {
			expiresAt := time.Unix(*input.ExpiresAt, 0)
			account.ExpiresAt = &expiresAt
		}
	}
	if input.AutoPauseOnExpired != nil {
		account.AutoPauseOnExpired = *input.AutoPauseOnExpired
	}

	// 先验证分组是否存在（在任何写操作之前）
	if input.GroupIDs != nil {
		if err := s.ValidateGroupIDs(ctx, *input.GroupIDs); err != nil {
			return nil, err
		}

		// 检查混合渠道风险（除非用户已确认）
		if !input.SkipMixedChannelCheck {
			if err := s.checkMixedChannelRisk(ctx, account.ID, account.Platform, *input.GroupIDs); err != nil {
				return nil, err
			}
		}
	}

	deferQoderPATValidation := false
	if account.IsQoder() && originalQoderSiteErr == nil && originalQoderPAT != "" && originalQoderPAT == strings.TrimSpace(account.GetCredential("pat")) {
		if currentSite, siteErr := s.options.Credentials.Site(account); siteErr == nil {
			deferQoderPATValidation = currentSite != originalQoderSite
		}
	}
	s.attachProxyForValidation(ctx, account)
	if previousCNUsageIdentity != CNUsageMonitorIdentityFingerprint(account) && account.Extra != nil {
		delete(account.Extra, CNUsageMonitorSnapshotExtraKey)
	}
	if err := s.options.Credentials.ValidateEdit(ctx, account, deferQoderPATValidation); err != nil {
		return nil, err
	}
	change := ConfigurationChange{ExtraPatch: extraPatch, ExpectedCredentials: expected, NormalizeProtocols: true, PreserveSensitive: !account.IsCredentialShadow(), CredentialInput: credentialInput, ProtocolExtra: input.Extra, ComputeResetAt: computeResetAt, NormalizeWindowAt: normalizeWindowAt}
	if input.PatchCredentials {
		change.CredentialPatch = CloneValues(credentialInput)
	}
	if input.Name != "" {
		change.Fields |= ConfigName
	}
	if input.Type != "" {
		change.Fields |= ConfigType
	}
	if input.Status != "" {
		change.Fields |= ConfigStatus
	}
	if input.Notes != nil {
		change.Fields |= ConfigNotes
	}
	if input.Concurrency != nil {
		change.Fields |= ConfigConcurrency
	}
	if input.Priority != nil {
		change.Fields |= ConfigPriority
	}
	if input.RateMultiplier != nil {
		change.Fields |= ConfigRateMultiplier
	}
	if input.LoadFactor != nil {
		change.Fields |= ConfigLoadFactor
	}
	if input.ExpiresAt != nil {
		change.Fields |= ConfigExpiresAt
	}
	if input.AutoPauseOnExpired != nil {
		change.Fields |= ConfigAutoPauseOnExpired
	}
	if (account.IsCredentialShadow() && input.Credentials != nil) || len(input.Credentials) > 0 {
		change.Fields |= ConfigCredentials
	}
	if input.ProxyID != nil && !account.IsCredentialShadow() {
		change.Fields |= ConfigProxyID
	}
	if shouldReplaceExtra {
		change.Fields |= ConfigExtra
		if _, provided := input.Extra[UpstreamUsageQueryExtraKey]; !provided {
			change.PreserveExtraKeys = append(change.PreserveExtraKeys, UpstreamUsageQueryExtraKey)
		}
		if _, provided := input.Extra[ExtraKeyResponsesContinuationSupported]; !provided && IsOpenAIAPIKeyAccount(account) {
			change.PreserveExtraKeys = append(change.PreserveExtraKeys, ExtraKeyResponsesContinuationSupported)
		}
	}
	if err := WriteConfiguration(ctx, s.accountRepo, account, change); err != nil {
		return nil, err
	}

	// 将 proxy 变更传播到 spark 影子账号（同步；Update 内部已触发调度快照）。
	// 影子自身 proxy 不可独立编辑(见上),故对影子的更新不触发传播。
	if input.ProxyID != nil && !account.IsCredentialShadow() {
		if err := s.propagateProxyToShadows(ctx, id, account.ProxyID); err != nil {
			return nil, err
		}
	}

	// 绑定分组
	if input.GroupIDs != nil {
		if err := s.accountRepo.BindGroups(ctx, account.ID, *input.GroupIDs); err != nil {
			return nil, err
		}
	}

	// 重新查询以确保返回完整数据（包括正确的 Proxy 关联对象）
	updated, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// UpdateAccountExtra 仅对账号 Extra JSONB 做 key 级合并，避免覆盖运行态或持久化配置键。
func (s *Admin) UpdateAccountExtra(ctx context.Context, id int64, updates map[string]any) error {
	updates = SanitizedCodexFingerprintExtraUpdates(updates)
	DiscardDeprecatedAccountExtra(updates)
	if err := NormalizeUpstreamUsageExtra(updates); err != nil {
		return err
	}
	delete(updates, OllamaCloudUsageSessionExtraKey)
	delete(updates, OllamaCloudUsageAutoRefreshExtraKey)
	delete(updates, OllamaCloudUsageSnapshotExtraKey)
	delete(updates, CNUsageMonitorSnapshotExtraKey)
	if HasOpenAIConfigurationPatch(nil, updates) {
		account, err := s.accountRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}
		if !IsOpenAIAPIKeyAccount(account) {
			return infraerrors.BadRequest(
				"OPENAI_CONFIGURATION_TARGET_INVALID",
				"OpenAI text protocol and continuation configuration only applies to OpenAI API Key accounts",
			)
		}
		if err := NormalizeOpenAIAPIKeyConfigurationPatch(nil, updates); err != nil {
			return err
		}
	}
	if len(updates) == 0 {
		return nil
	}
	return s.accountRepo.UpdateExtra(ctx, id, updates)
}

// BulkUpdateAccounts 在单次请求中更新多个账号。
// 凭据和 extra 使用键级合并，不覆盖整个对象。
func (s *Admin) BulkUpdateAccounts(ctx context.Context, input *BulkUpdateAccountsInput) (*BulkUpdateAccountsResult, error) {
	// 受管会话状态只能通过专用类型接口更新，废弃账号扩展字段直接丢弃。
	input.Extra = SanitizedCodexFingerprintExtraUpdates(input.Extra)
	DiscardDeprecatedAccountExtra(input.Extra)
	if err := NormalizeUpstreamUsageExtra(input.Extra); err != nil {
		return nil, err
	}
	delete(input.Extra, OllamaCloudUsageSessionExtraKey)
	delete(input.Extra, OllamaCloudUsageAutoRefreshExtraKey)
	delete(input.Extra, OllamaCloudUsageSnapshotExtraKey)
	delete(input.Extra, CNUsageMonitorSnapshotExtraKey)

	if len(input.AccountIDs) == 0 && input.Filters != nil {
		accountIDs, err := s.resolveBulkUpdateTargetIDs(ctx, input.Filters)
		if err != nil {
			return nil, err
		}
		input.AccountIDs = accountIDs
	}

	result := &BulkUpdateAccountsResult{
		SuccessIDs: make([]int64, 0, len(input.AccountIDs)),
		FailedIDs:  make([]int64, 0, len(input.AccountIDs)),
		Results:    make([]BulkUpdateAccountResult, 0, len(input.AccountIDs)),
	}

	if len(input.AccountIDs) == 0 {
		return result, nil
	}
	if input.GroupIDs != nil {
		if err := s.ValidateGroupIDs(ctx, *input.GroupIDs); err != nil {
			return nil, err
		}
	}
	openAISettings, err := normalizeBulkOpenAISettings(input)
	if err != nil {
		return nil, err
	}

	needMixedChannelCheck := input.GroupIDs != nil && !input.SkipMixedChannelCheck

	// 预取所有目标账号，供凭据守卫/代理守卫/混合渠道检查共用，避免多次 DB 查询。
	var cachedTargets []*Record
	hasOpenAIConfigPatch := HasOpenAIConfigurationPatch(input.Credentials, input.Extra)
	if len(input.Credentials) > 0 || input.ProxyID != nil || needMixedChannelCheck || hasOpenAIConfigPatch {
		loaded, err := s.accountRepo.GetByIDs(ctx, input.AccountIDs)
		if err != nil {
			return nil, err
		}
		cachedTargets = loaded
	}
	if hasOpenAIConfigPatch {
		targetsByID := make(map[int64]*Record, len(cachedTargets))
		for _, account := range cachedTargets {
			if account != nil {
				targetsByID[account.ID] = account
			}
		}
		for _, accountID := range input.AccountIDs {
			account, ok := targetsByID[accountID]
			if !ok || account == nil {
				return nil, invalidBulkOpenAITarget(accountID, "account does not exist")
			}
			if !IsOpenAIAPIKeyAccount(account) {
				return nil, infraerrors.BadRequest(
					"OPENAI_CONFIGURATION_TARGET_INVALID",
					"OpenAI text protocol and continuation configuration can only be bulk-updated on OpenAI API Key accounts",
				)
			}
		}
		if err := validateBulkOpenAISettingsTargets(input, openAISettings, targetsByID); err != nil {
			return nil, err
		}
		if err := NormalizeOpenAIAPIKeyConfigurationPatch(input.Credentials, input.Extra); err != nil {
			return nil, err
		}
	}
	// 影子账号绝不持有凭据:批量更新携带凭据时,目标中不得含影子(外审 G5,与单账号
	// UpdateAccount 守卫对齐)。覆盖显式 IDs 与 filter 解析出的 IDs(此处 AccountIDs 已解析完成)。
	if len(input.Credentials) > 0 {
		for _, acc := range cachedTargets {
			if acc != nil && acc.IsCredentialShadow() {
				return nil, infraerrors.Newf(infraerrors.CategoryBadRequest, "SPARK_SHADOW_NO_CREDENTIALS",
					"spark shadow account %d cannot hold credentials; manage credentials on the parent account", acc.ID)
			}
		}
	}

	// 影子账号 proxy 恒继承母账号(与单账号 UpdateAccount 守卫对齐——外审第4轮 P1):批量携带 proxy
	// 时目标不得含影子,否则影子会获得独立 proxy、破坏继承不变量(网关按所选影子自身 proxy 出站,
	// 要等母账号下次改 proxy 才覆盖→漂移)。含影子即整体拒绝,提示从选择中剔除影子。
	if input.ProxyID != nil {
		for _, acc := range cachedTargets {
			if acc != nil && acc.IsCredentialShadow() {
				return nil, infraerrors.Newf(infraerrors.CategoryBadRequest, "SPARK_SHADOW_PROXY_INHERITED",
					"spark shadow account %d proxy is inherited from its parent and cannot be set in bulk; manage it on the parent account", acc.ID)
			}
		}
	}

	// 预加载账号平台信息（混合渠道检查需要）。
	platformByID := map[int64]string{}
	if needMixedChannelCheck {
		for _, account := range cachedTargets {
			if account != nil {
				platformByID[account.ID] = account.Platform
			}
		}
	}

	// 预检查混合渠道风险：在任何写操作之前，若发现风险立即返回错误。
	if needMixedChannelCheck {
		for _, accountID := range input.AccountIDs {
			platform := platformByID[accountID]
			if platform == "" {
				continue
			}
			if err := s.checkMixedChannelRisk(ctx, accountID, platform, *input.GroupIDs); err != nil {
				return nil, err
			}
		}
	}

	if input.RateMultiplier != nil {
		if *input.RateMultiplier < 0 {
			return nil, errors.New("rate_multiplier must be >= 0")
		}
	}

	// 校验并规范化请求头覆写配置（批量路径为 JSONB 顶层 key 合并，直接校验增量即可）
	if err := egress.NormalizeHeaderOverrideCredentials(input.Credentials); err != nil {
		return nil, err
	}
	// 批量更新可能混合平台，因此始终移除临时 SSO、密码和 cookie 字段。
	if input.Credentials != nil {
		input.Credentials = SanitizeStoredCredentials("", input.Credentials)
	}
	protocolUpdates := map[int64]map[string]any{}
	if len(input.Credentials) > 0 || hasOpenAIConfigPatch {
		for _, account := range cachedTargets {
			if account == nil {
				continue
			}
			prospective := *account
			prospective.Credentials = maps.Clone(account.Credentials)
			if prospective.Credentials == nil {
				prospective.Credentials = make(map[string]any, len(input.Credentials))
			}
			for key, value := range input.Credentials {
				prospective.Credentials[key] = value
			}
			if err := NormalizeCNProviderCredentials(&prospective, false); err != nil {
				return nil, err
			}
			prospective.Extra = maps.Clone(account.Extra)
			if prospective.Extra == nil {
				prospective.Extra = map[string]any{}
			}
			for key, value := range input.Extra {
				prospective.Extra[key] = value
			}
			ApplyLegacyProtocolPatch(&prospective, input.Credentials, input.Extra)
			if err := NormalizeAccountProtocols(&prospective); err != nil {
				return nil, err
			}
			protocolUpdates[account.ID] = map[string]any{UpstreamProtocolsKey: prospective.Credentials[UpstreamProtocolsKey]}
			if urls, exists := prospective.Credentials["api_base_urls"]; exists {
				protocolUpdates[account.ID]["api_base_urls"] = urls
			}
			if err := ValidateGeminiThirdPartyBaseURL(&prospective); err != nil {
				return nil, err
			}
		}
	}

	// Prepare bulk updates for columns and JSONB fields.
	repoUpdates := AccountBulkUpdate{
		ProtocolUpdates:            protocolUpdates,
		Credentials:                input.Credentials,
		Extra:                      input.Extra,
		EnsureCodexFingerprintSeed: ShouldEnsureCodexFingerprintSeedForExtraUpdates(input.Extra),
	}
	if input.Name != "" {
		repoUpdates.Name = &input.Name
	}
	if input.ProxyID != nil {
		repoUpdates.ProxyID = input.ProxyID
	}
	if input.Concurrency != nil {
		repoUpdates.Concurrency = input.Concurrency
	}
	if input.Priority != nil {
		repoUpdates.Priority = input.Priority
	}
	if input.RateMultiplier != nil {
		repoUpdates.RateMultiplier = input.RateMultiplier
	}
	if input.LoadFactor != nil {
		if *input.LoadFactor <= 0 {
			repoUpdates.LoadFactor = nil // 0 或负数表示清除
		} else if *input.LoadFactor > 10000 {
			return nil, errors.New("load_factor must be <= 10000")
		} else {
			repoUpdates.LoadFactor = input.LoadFactor
		}
	}
	if input.Status != "" {
		repoUpdates.Status = &input.Status
	}
	if input.Schedulable != nil {
		repoUpdates.Schedulable = input.Schedulable
	}

	// Run bulk update for column/jsonb fields first.
	if _, err := s.accountRepo.BulkUpdate(ctx, input.AccountIDs, repoUpdates); err != nil {
		return nil, err
	}

	// 将 proxy 变更传播到每个目标账号的 spark 影子账号
	if repoUpdates.ProxyID != nil {
		var effectiveProxyID *int64
		if *repoUpdates.ProxyID != 0 {
			effectiveProxyID = repoUpdates.ProxyID
		}
		for _, accountID := range input.AccountIDs {
			if err := s.propagateProxyToShadows(ctx, accountID, effectiveProxyID); err != nil {
				return nil, err
			}
		}
	}

	// Handle group bindings per account (requires individual operations).
	for _, accountID := range input.AccountIDs {
		entry := BulkUpdateAccountResult{AccountID: accountID}

		if input.GroupIDs != nil {
			if err := s.accountRepo.BindGroups(ctx, accountID, *input.GroupIDs); err != nil {
				entry.Success = false
				entry.Error = err.Error()
				result.Failed++
				result.FailedIDs = append(result.FailedIDs, accountID)
				result.Results = append(result.Results, entry)
				continue
			}
		}

		entry.Success = true
		result.Success++
		result.SuccessIDs = append(result.SuccessIDs, accountID)
		result.Results = append(result.Results, entry)
	}

	return result, nil
}

func (s *Admin) resolveBulkUpdateTargetIDs(ctx context.Context, filters *BulkUpdateAccountFilters) ([]int64, error) {
	if filters == nil {
		return nil, nil
	}

	groupID := int64(0)
	switch strings.TrimSpace(filters.Group) {
	case "":
	case "ungrouped":
		groupID = AccountListGroupUngrouped
	default:
		parsedGroupID, err := strconv.ParseInt(strings.TrimSpace(filters.Group), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid group filter: %w", err)
		}
		groupID = parsedGroupID
	}

	const pageSize = 500
	page := 1
	accountIDs := make([]int64, 0, pageSize)

	for {
		accounts, total, err := s.ListAccounts(
			ctx,
			page,
			pageSize,
			filters.Platform,
			filters.Type,
			filters.Status,
			filters.Search,
			groupID,
			filters.PrivacyMode,
			"",
			"",
		)
		if err != nil {
			return nil, err
		}
		for _, account := range accounts {
			accountIDs = append(accountIDs, account.ID)
		}
		if int64(len(accountIDs)) >= total || len(accounts) == 0 {
			return accountIDs, nil
		}
		page++
	}
}
