// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	time "time"

	transfer "github.com/TokenFlux/TokenRouter/internal/account/transfer"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
)

// ArchiveAccounts 只提供文件导入/导出所需的管理操作，不暴露仓储或完整旧聚合服务。
type ArchiveAccounts interface {
	ListAccounts(context.Context, int, int, string, string, string, string, int64, string, string, string) ([]Record, int64, error)
	GetAccountsByIDs(context.Context, []int64) ([]*Record, error)
	CreateAccount(context.Context, *CreateAccountInput) (*Record, error)
}
type ArchiveProxies interface {
	GetProxiesByIDs(context.Context, []int64) ([]egress.Proxy, error)
	ImportForAccountBinding(context.Context, []egress.TransferProxy) (map[string]int64, egress.ProxyImportResult, error)
}
type ArchiveExportQuery struct {
	IDs                                                            []int64
	Platform, Type, Status, Search, PrivacyMode, SortBy, SortOrder string
	GroupID                                                        int64
	// 保留读账号/排除影子后才验证 include_proxies 的历史顺序。
	IncludeProxies func() (bool, error)
}
type ArchiveInputError struct{ Err error }

func (e *ArchiveInputError) Error() string { return e.Err.Error() }
func (e *ArchiveInputError) Unwrap() error { return e.Err }

type ArchiveOptions struct {
	Now                func() time.Time
	Defaults           func(context.Context) (*transfer.OpenAIOAuthImportDefaults, error)
	DecodeIDToken      func(string) (*ArchiveIdentityHints, error)
	Probe              func(AccountSnapshot)
	ForcePrivacy       func(context.Context, *Record) string
	Background         func(string, func()) bool
	Info, Error, Debug func(string, ...any)
}

// Archive 拥有备份查询、逐项创建和原后置行为，实际平台交换通过窄端口执行。
type Archive struct {
	accounts ArchiveAccounts
	proxies  ArchiveProxies
	options  ArchiveOptions
}

func NewArchive(accounts ArchiveAccounts, proxies ArchiveProxies, options ArchiveOptions) *Archive {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Info == nil {
		options.Info = func(string, ...any) {}
	}
	if options.Error == nil {
		options.Error = func(string, ...any) {}
	}
	if options.Debug == nil {
		options.Debug = func(string, ...any) {}
	}
	return &Archive{accounts: accounts, proxies: proxies, options: options}
}

func (h *Archive) Export(ctx context.Context, query ArchiveExportQuery) (transfer.DataPayload, error) {
	accounts, err := h.resolveExportAccounts(ctx, query)
	if err != nil {
		return transfer.DataPayload{}, err
	}

	// 排除 spark 影子账号:影子不持凭据,通用凭据型导出无法表达父子链接、导入侧又强制 credentials
	// 非空——若混入会产出无法还原的坏备份(导入即失败)。影子的独立调度配置(priority/并发/分组/
	// status,管理员可单独调)随之不进备份,还原后需在重建的影子上重新调优;前端按 skipped_shadows
	// 提示用户(外审第5轮发现、第6轮裁决:保持排除 + 警告,不做完整往返)。
	skippedShadows := 0
	exportable := make([]Record, 0, len(accounts))
	for i := range accounts {
		if accounts[i].IsCredentialShadow() {
			skippedShadows++
			continue
		}
		exportable = append(exportable, accounts[i])
	}
	accounts = exportable
	if skippedShadows > 0 {
		h.options.Info("export_skipped_spark_shadows", "count", skippedShadows)
	}

	includeProxies := true
	if query.IncludeProxies != nil {
		includeProxies, err = query.IncludeProxies()
	}
	if err != nil {
		return transfer.DataPayload{}, &ArchiveInputError{Err: err}
	}

	var proxies []egress.Proxy
	if includeProxies {
		proxies, err = h.resolveExportProxies(ctx, accounts)
		if err != nil {
			return transfer.DataPayload{}, err
		}
	} else {
		proxies = []egress.Proxy{}
	}

	// 构建 id→name 映射，用于导出备用代理 name
	proxyNameByID := make(map[int64]string, len(proxies))
	for i := range proxies {
		proxyNameByID[proxies[i].ID] = proxies[i].Name
	}

	proxyKeyByID := make(map[int64]string, len(proxies))
	dataProxies := make([]transfer.DataProxy, 0, len(proxies))
	for i := range proxies {
		p := proxies[i]
		key := egress.BuildTransferProxyKey(p.Protocol, p.Host, p.Port, p.Username, p.Password)
		proxyKeyByID[p.ID] = key

		var expiresAt *int64
		if p.ExpiresAt != nil {
			v := p.ExpiresAt.Unix()
			expiresAt = &v
		}
		var backupProxyName string
		if p.BackupProxyID != nil {
			backupProxyName = proxyNameByID[*p.BackupProxyID]
		}
		dataProxies = append(dataProxies, transfer.DataProxy{
			ProxyKey:        key,
			Name:            p.Name,
			Protocol:        p.Protocol,
			Host:            p.Host,
			Port:            p.Port,
			Username:        p.Username,
			Password:        p.Password,
			Status:          p.Status,
			ExpiresAt:       expiresAt,
			FallbackMode:    p.FallbackMode,
			BackupProxyName: backupProxyName,
			ExpiryWarnDays:  p.ExpiryWarnDays,
		})
	}

	dataAccounts := make([]transfer.DataAccount, 0, len(accounts))
	for i := range accounts {
		acc := *CloneRecord(&accounts[i])
		var proxyKey *string
		if acc.ProxyID != nil {
			if key, ok := proxyKeyByID[*acc.ProxyID]; ok {
				proxyKey = &key
			}
		}
		var expiresAt *int64
		if acc.ExpiresAt != nil {
			v := acc.ExpiresAt.Unix()
			expiresAt = &v
		}
		dataAccounts = append(dataAccounts, transfer.DataAccount{
			Name:               acc.Name,
			Notes:              acc.Notes,
			Platform:           acc.Platform,
			Type:               acc.Type,
			Credentials:        acc.Credentials,
			Extra:              acc.Extra,
			ProxyKey:           proxyKey,
			Concurrency:        archiveIntPointer(acc.Concurrency),
			Priority:           archiveIntPointer(acc.Priority),
			RateMultiplier:     acc.RateMultiplier,
			ExpiresAt:          expiresAt,
			AutoPauseOnExpired: &acc.AutoPauseOnExpired,
		})
	}

	payload := transfer.DataPayload{
		ExportedAt:     h.options.Now().UTC().Format(time.RFC3339),
		Proxies:        dataProxies,
		Accounts:       dataAccounts,
		SkippedShadows: skippedShadows,
	}

	return payload, nil
}

func (h *Archive) Import(ctx context.Context, req transfer.DataImportRequest) (transfer.DataImportResult, error) {
	skipDefaultGroupBind := true
	if req.SkipDefaultGroupBind != nil {
		skipDefaultGroupBind = *req.SkipDefaultGroupBind
	}

	dataPayload := req.Data
	result := transfer.DataImportResult{}

	proxyKeyToID, imported, err := h.proxies.ImportForAccountBinding(ctx, dataPayload.Proxies)
	result.ProxyCreated = imported.ProxyCreated
	result.ProxyReused = imported.ProxyReused
	result.ProxyFailed = imported.ProxyFailed
	result.Errors = imported.Errors
	if err != nil {
		return result, err
	}

	// 收集需要异步设置隐私的 Antigravity OAuth 账号
	var privacyAccounts []*Record
	var openAIOAuthImportDefaults *transfer.OpenAIOAuthImportDefaults
	if h.options.Defaults != nil {
		openAIOAuthImportDefaults, err = h.options.Defaults(ctx)
		if err != nil {
			return result, err
		}
	}

	for i := range dataPayload.Accounts {
		item := cloneArchiveItem(dataPayload.Accounts[i])
		if err := ValidateArchiveAccount(item); err != nil {
			result.AccountFailed++
			result.Errors = append(result.Errors, transfer.DataImportError{
				Kind:    "account",
				Name:    item.Name,
				Message: err.Error(),
			})
			continue
		}

		var proxyID *int64
		if item.ProxyKey != nil && *item.ProxyKey != "" {
			if id, ok := proxyKeyToID[*item.ProxyKey]; ok {
				proxyID = &id
			} else {
				result.AccountFailed++
				result.Errors = append(result.Errors, transfer.DataImportError{
					Kind:     "account",
					Name:     item.Name,
					ProxyKey: *item.ProxyKey,
					Message:  "proxy_key not found",
				})
				continue
			}
		}

		ApplyArchiveDefaults(&item, openAIOAuthImportDefaults)
		h.enrichIdentity(&item)

		accountInput := &CreateAccountInput{
			Name:                 item.Name,
			Notes:                item.Notes,
			Platform:             item.Platform,
			Type:                 item.Type,
			Credentials:          item.Credentials,
			Extra:                item.Extra,
			ProxyID:              proxyID,
			Concurrency:          archiveIntValue(item.Concurrency),
			Priority:             archiveIntValue(item.Priority),
			RateMultiplier:       item.RateMultiplier,
			GroupIDs:             nil,
			ExpiresAt:            item.ExpiresAt,
			AutoPauseOnExpired:   item.AutoPauseOnExpired,
			SkipDefaultGroupBind: skipDefaultGroupBind,
		}

		created, err := h.accounts.CreateAccount(ctx, accountInput)
		if err != nil {
			result.AccountFailed++
			result.Errors = append(result.Errors, transfer.DataImportError{
				Kind:    "account",
				Name:    item.Name,
				Message: err.Error(),
			})
			continue
		}
		// 收集 Antigravity OAuth 账号，稍后异步设置隐私
		if created.Platform == PlatformAntigravity && created.Type == AccountTypeOAuth {
			privacyAccounts = append(privacyAccounts, CloneRecord(created))
		}
		if h.options.Probe != nil {
			h.options.Probe(created.RoutingSnapshot())
		}
		result.AccountCreated++
	}

	// 异步设置 Antigravity 隐私，避免大量导入时阻塞请求
	if len(privacyAccounts) > 0 && h.options.Background != nil && h.options.ForcePrivacy != nil {
		h.options.Background("handler/admin/account_data.go:importData", func() {
			defer func() {
				if r := recover(); r != nil {
					h.options.Error("import_antigravity_privacy_panic", "recover", r)
				}
			}()
			bgCtx := context.Background()
			for _, acc := range privacyAccounts {
				h.options.ForcePrivacy(bgCtx, acc)
			}
			h.options.Info("import_antigravity_privacy_done", "count", len(privacyAccounts))
		})
	}

	return result, nil
}

func (h *Archive) listAccountsFiltered(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode, sortBy, sortOrder string) ([]Record, error) {
	page := 1
	pageSize := 1000
	var out []Record
	for {
		items, total, err := h.accounts.ListAccounts(ctx, page, pageSize, platform, accountType, status, search, groupID, privacyMode, sortBy, sortOrder)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if len(out) >= int(total) || len(items) == 0 {
			break
		}
		page++
	}
	return out, nil
}

func (h *Archive) resolveExportAccounts(ctx context.Context, query ArchiveExportQuery) ([]Record, error) {
	if len(query.IDs) > 0 {
		accounts, err := h.accounts.GetAccountsByIDs(ctx, query.IDs)
		if err != nil {
			return nil, err
		}
		out := make([]Record, 0, len(accounts))
		for _, acc := range accounts {
			if acc == nil {
				continue
			}
			out = append(out, *acc)
		}
		return out, nil
	}

	return h.listAccountsFiltered(ctx, query.Platform, query.Type, query.Status, query.Search, query.GroupID, query.PrivacyMode, query.SortBy, query.SortOrder)
}

func (h *Archive) resolveExportProxies(ctx context.Context, accounts []Record) ([]egress.Proxy, error) {
	if len(accounts) == 0 {
		return []egress.Proxy{}, nil
	}

	seen := make(map[int64]struct{})
	ids := make([]int64, 0)
	for i := range accounts {
		if accounts[i].ProxyID == nil {
			continue
		}
		id := *accounts[i].ProxyID
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return []egress.Proxy{}, nil
	}

	return h.proxies.GetProxiesByIDs(ctx, ids)
}
func (h *Archive) enrichIdentity(item *transfer.DataAccount) {
	token := ArchiveIDToken(item)
	if token == "" || h.options.DecodeIDToken == nil {
		return
	}
	hints, err := h.options.DecodeIDToken(token)
	if err != nil {
		h.options.Debug("import_enrich_id_token_decode_failed", "account", item.Name, "error", err)
		return
	}
	FillArchiveIdentity(item, hints)
}
func cloneArchiveItem(value transfer.DataAccount) transfer.DataAccount {
	value.Credentials = CloneValues(value.Credentials)
	value.Extra = CloneValues(value.Extra)
	value.Notes = clonePointer(value.Notes)
	value.ProxyKey = clonePointer(value.ProxyKey)
	value.Concurrency = clonePointer(value.Concurrency)
	value.Priority = clonePointer(value.Priority)
	value.RateMultiplier = clonePointer(value.RateMultiplier)
	value.ExpiresAt = clonePointer(value.ExpiresAt)
	value.AutoPauseOnExpired = clonePointer(value.AutoPauseOnExpired)
	return value
}
func archiveIntPointer(value int) *int { return &value }
func archiveIntValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

// ListAccountsForImport 保留 Codex 文件匹配的全量分页，后续导入入口复用同一账号读取逻辑。
func (h *Archive) ListAccountsForImport(ctx context.Context, platform, kind, status, search string, groupID int64, privacy, sortBy, sortOrder string) ([]Record, error) {
	return h.listAccountsFiltered(ctx, platform, kind, status, search, groupID, privacy, sortBy, sortOrder)
}
