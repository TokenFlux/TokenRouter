// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"
	"errors"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	dbaccount "github.com/TokenFlux/TokenRouter/ent/account"
	dbaccountgroup "github.com/TokenFlux/TokenRouter/ent/accountgroup"
	dbgroup "github.com/TokenFlux/TokenRouter/ent/group"
	dbproxy "github.com/TokenFlux/TokenRouter/ent/proxy"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

func (r *AccountStore) Create(ctx context.Context, record *account.Record) error {
	if err := CreateRecord(ctx, r.client, record); err != nil {
		return err
	}
	if err := r.publish(ctx, r.sql, record.ID, record.GroupIDs); err != nil {
		r.observe("[SchedulerOutbox] enqueue account create failed: account=%d err=%v", record.ID, err)
	}
	return nil
}

func CreateRecord(ctx context.Context, client *dbent.Client, record *account.Record) error {
	if record == nil {
		return account.ErrAccountNilInput
	}
	account.DiscardDeprecatedExtra(record.Extra)

	builder := client.Account.Create().
		SetName(record.Name).
		SetNillableNotes(record.Notes).
		SetPlatform(record.Platform).
		SetType(record.Type).
		SetCredentials(normalizeJSONMap(record.Credentials)).
		SetExtra(normalizeJSONMap(record.Extra)).
		SetConcurrency(record.Concurrency).
		SetPriority(record.Priority).
		SetStatus(record.Status).
		SetErrorMessage(record.ErrorMessage).
		SetSchedulable(record.Schedulable).
		SetAutoPauseOnExpired(record.AutoPauseOnExpired)

	if record.RateMultiplier != nil {
		builder.SetRateMultiplier(*record.RateMultiplier)
	}
	if record.LoadFactor != nil {
		builder.SetLoadFactor(*record.LoadFactor)
	}

	if record.ProxyID != nil {
		builder.SetProxyID(*record.ProxyID)
	}
	if record.LastUsedAt != nil {
		builder.SetLastUsedAt(*record.LastUsedAt)
	}
	if record.ExpiresAt != nil {
		builder.SetExpiresAt(*record.ExpiresAt)
	}
	if record.RateLimitedAt != nil {
		builder.SetRateLimitedAt(*record.RateLimitedAt)
	}
	if record.RateLimitResetAt != nil {
		builder.SetRateLimitResetAt(*record.RateLimitResetAt)
	}
	if record.OverloadUntil != nil {
		builder.SetOverloadUntil(*record.OverloadUntil)
	}
	if record.SessionWindowStart != nil {
		builder.SetSessionWindowStart(*record.SessionWindowStart)
	}
	if record.SessionWindowEnd != nil {
		builder.SetSessionWindowEnd(*record.SessionWindowEnd)
	}
	if record.SessionWindowStatus != "" {
		builder.SetSessionWindowStatus(record.SessionWindowStatus)
	}

	builder.SetQuotaDimension(dbaccount.QuotaDimension(record.QuotaDimensionOrDefault()))
	if record.ParentAccountID != nil {
		builder.SetParentAccountID(*record.ParentAccountID)
	}

	created, err := builder.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, account.ErrAccountNotFound, nil)
	}

	record.ID = created.ID
	record.CreatedAt = created.CreatedAt
	record.UpdatedAt = created.UpdatedAt
	return nil
}

// CreateWithAccountGroups 在同一事务中持久化账号、分组绑定，
// 以及用于发布新路由快照的调度 outbox 事件。
func (r *AccountStore) CreateWithAccountGroups(ctx context.Context, record *account.Record, groups []account.GroupMembership) error {
	if record == nil {
		return account.ErrAccountNilInput
	}
	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return err
	}

	var txClient *dbent.Client
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		txClient = tx.Client()
	} else {
		// 仓储已处于事务中时，复用调用方持有的事务。
		txClient = r.client
	}

	if err := CreateRecord(ctx, txClient, record); err != nil {
		return err
	}
	groupIDs := make([]int64, 0, len(groups))
	if len(groups) > 0 {
		builders := make([]*dbent.AccountGroupCreate, 0, len(groups))
		for i := range groups {
			groups[i].AccountID = record.ID
			groupIDs = append(groupIDs, groups[i].GroupID)
			builders = append(builders, txClient.AccountGroup.Create().
				SetAccountID(record.ID).
				SetGroupID(groups[i].GroupID),
			)
		}
		if _, err := txClient.AccountGroup.CreateBulk(builders...).Save(ctx); err != nil {
			return err
		}
	}
	record.GroupIDs = groupIDs
	record.AccountGroups = append([]account.GroupMembership(nil), groups...)
	if err := r.publish(ctx, txClient, record.ID, groupIDs); err != nil {
		return err
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (r *AccountStore) GetByID(ctx context.Context, id int64) (*account.Record, error) {
	m, err := r.client.Account.Query().Where(dbaccount.IDEQ(id)).Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, account.ErrAccountNotFound, nil)
	}

	accounts, err := r.RecordsFromEntities(ctx, []*dbent.Account{m})
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, account.ErrAccountNotFound
	}
	return &accounts[0], nil
}

func (r *AccountStore) GetByIDs(ctx context.Context, ids []int64) ([]*account.Record, error) {
	if len(ids) == 0 {
		return []*account.Record{}, nil
	}

	// De-duplicate while preserving order of first occurrence.
	uniqueIDs := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return []*account.Record{}, nil
	}

	entAccounts, err := r.client.Account.
		Query().
		Where(dbaccount.IDIn(uniqueIDs...)).
		WithProxy().
		All(ctx)
	if err != nil {
		return nil, err
	}
	if len(entAccounts) == 0 {
		return []*account.Record{}, nil
	}

	accountIDs := make([]int64, 0, len(entAccounts))
	entByID := make(map[int64]*dbent.Account, len(entAccounts))
	for _, acc := range entAccounts {
		entByID[acc.ID] = acc
		accountIDs = append(accountIDs, acc.ID)
	}

	groupsByAccount, groupIDsByAccount, accountGroupsByAccount, err := r.LoadAccountGroups(ctx, accountIDs)
	if err != nil {
		return nil, err
	}

	outByID := make(map[int64]*account.Record, len(entAccounts))
	for _, entAcc := range entAccounts {
		out := r.recordFromEntity(entAcc)
		if out == nil {
			continue
		}

		// Prefer the preloaded proxy edge when available.
		if entAcc.Edges.Proxy != nil {
			out.Proxy = r.options.Proxy(entAcc.Edges.Proxy)
		}

		if groups, ok := groupsByAccount[entAcc.ID]; ok {
			out.Groups = groups
		}
		if groupIDs, ok := groupIDsByAccount[entAcc.ID]; ok {
			out.GroupIDs = groupIDs
		}
		if ags, ok := accountGroupsByAccount[entAcc.ID]; ok {
			out.AccountGroups = ags
		}
		outByID[entAcc.ID] = out
	}

	// Preserve input order (first occurrence), and ignore missing IDs.
	out := make([]*account.Record, 0, len(uniqueIDs))
	for _, id := range uniqueIDs {
		if _, ok := entByID[id]; !ok {
			continue
		}
		if acc, ok := outByID[id]; ok && acc != nil {
			out = append(out, account.CloneRecord(acc))
		}
	}

	return out, nil
}

func (r *AccountStore) RecordsFromEntities(ctx context.Context, accounts []*dbent.Account) ([]account.Record, error) {
	if len(accounts) == 0 {
		return []account.Record{}, nil
	}

	accountIDs := make([]int64, 0, len(accounts))
	proxyIDs := make([]int64, 0, len(accounts))
	for _, acc := range accounts {
		accountIDs = append(accountIDs, acc.ID)
		if acc.ProxyID != nil {
			proxyIDs = append(proxyIDs, *acc.ProxyID)
		}
		if acc.ProxyFallbackOriginID != nil {
			proxyIDs = append(proxyIDs, *acc.ProxyFallbackOriginID)
		}
	}

	proxyMap, err := r.LoadProxies(ctx, proxyIDs)
	if err != nil {
		return nil, err
	}
	groupsByAccount, groupIDsByAccount, accountGroupsByAccount, err := r.LoadAccountGroups(ctx, accountIDs)
	if err != nil {
		return nil, err
	}

	outAccounts := make([]account.Record, 0, len(accounts))
	for _, acc := range accounts {
		out := r.recordFromEntity(acc)
		if out == nil {
			continue
		}
		if acc.ProxyID != nil {
			if proxy, ok := proxyMap[*acc.ProxyID]; ok {
				out.Proxy = proxy
			}
		}
		out.ProxyFallbackOriginID = acc.ProxyFallbackOriginID
		if acc.ProxyFallbackOriginID != nil {
			if op, ok := proxyMap[*acc.ProxyFallbackOriginID]; ok && op != nil {
				n := op.Name
				out.ProxyFallbackOriginName = &n
			}
		}
		if groups, ok := groupsByAccount[acc.ID]; ok {
			out.Groups = groups
		}
		if groupIDs, ok := groupIDsByAccount[acc.ID]; ok {
			out.GroupIDs = groupIDs
		}
		if ags, ok := accountGroupsByAccount[acc.ID]; ok {
			out.AccountGroups = ags
		}
		outAccounts = append(outAccounts, *account.CloneRecord(out))
	}

	return outAccounts, nil
}

func (r *AccountStore) LoadProxies(ctx context.Context, proxyIDs []int64) (map[int64]*egress.Proxy, error) {
	proxyMap := make(map[int64]*egress.Proxy)
	proxyIDs = UniquePositiveInt64s(proxyIDs)
	if len(proxyIDs) == 0 {
		return proxyMap, nil
	}

	for start := 0; start < len(proxyIDs); start += postgresParameterBatchSize {
		end := start + postgresParameterBatchSize
		if end > len(proxyIDs) {
			end = len(proxyIDs)
		}
		proxies, err := r.client.Proxy.Query().Where(dbproxy.IDIn(proxyIDs[start:end]...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range proxies {
			proxyMap[p.ID] = r.options.Proxy(p)
		}
	}
	return proxyMap, nil
}

func (r *AccountStore) LoadAccountGroups(ctx context.Context, accountIDs []int64) (map[int64][]*accessview.GroupConfig, map[int64][]int64, map[int64][]account.GroupMembership, error) {
	groupsByAccount := make(map[int64][]*accessview.GroupConfig)
	groupIDsByAccount := make(map[int64][]int64)
	accountGroupsByAccount := make(map[int64][]account.GroupMembership)

	accountIDs = UniquePositiveInt64s(accountIDs)
	if len(accountIDs) == 0 {
		return groupsByAccount, groupIDsByAccount, accountGroupsByAccount, nil
	}

	for start := 0; start < len(accountIDs); start += postgresParameterBatchSize {
		end := start + postgresParameterBatchSize
		if end > len(accountIDs) {
			end = len(accountIDs)
		}
		entries, err := r.client.AccountGroup.Query().
			Where(dbaccountgroup.AccountIDIn(accountIDs[start:end]...)).
			Order(dbaccountgroup.ByAccountID(), dbaccountgroup.ByGroupID()).
			All(ctx)
		if err != nil {
			return nil, nil, nil, err
		}

		groupIDs := make([]int64, 0, len(entries))
		for _, ag := range entries {
			groupIDs = append(groupIDs, ag.GroupID)
		}
		groupMap, err := r.LoadGroups(ctx, groupIDs)
		if err != nil {
			return nil, nil, nil, err
		}

		for _, ag := range entries {
			groupSvc := groupMap[ag.GroupID]
			agSvc := account.GroupMembership{
				AccountID: ag.AccountID,
				GroupID:   ag.GroupID,
				CreatedAt: ag.CreatedAt,
				Group:     groupSvc,
			}
			accountGroupsByAccount[ag.AccountID] = append(accountGroupsByAccount[ag.AccountID], agSvc)
			groupIDsByAccount[ag.AccountID] = append(groupIDsByAccount[ag.AccountID], ag.GroupID)
			if groupSvc != nil {
				groupsByAccount[ag.AccountID] = append(groupsByAccount[ag.AccountID], groupSvc)
			}
		}
	}

	return groupsByAccount, groupIDsByAccount, accountGroupsByAccount, nil
}

func (r *AccountStore) LoadGroups(ctx context.Context, groupIDs []int64) (map[int64]*accessview.GroupConfig, error) {
	groupMap := make(map[int64]*accessview.GroupConfig)
	groupIDs = UniquePositiveInt64s(groupIDs)
	if len(groupIDs) == 0 {
		return groupMap, nil
	}

	for start := 0; start < len(groupIDs); start += postgresParameterBatchSize {
		end := start + postgresParameterBatchSize
		if end > len(groupIDs) {
			end = len(groupIDs)
		}
		groups, err := r.client.Group.Query().Where(dbgroup.IDIn(groupIDs[start:end]...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			groupMap[g.ID] = r.options.Group(g)
		}
	}
	return groupMap, nil
}

func UniquePositiveInt64s(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func RecordFromEntity(m *dbent.Account) *account.Record {
	if m == nil {
		return nil
	}

	rateMultiplier := m.RateMultiplier

	return &account.Record{
		ID:                      m.ID,
		Name:                    m.Name,
		Notes:                   m.Notes,
		Platform:                m.Platform,
		Type:                    m.Type,
		Credentials:             account.CloneValues(m.Credentials),
		Extra:                   account.CloneValues(m.Extra),
		ProxyID:                 m.ProxyID,
		ProxyFallbackOriginID:   m.ProxyFallbackOriginID,
		Concurrency:             m.Concurrency,
		Priority:                m.Priority,
		RateMultiplier:          &rateMultiplier,
		LoadFactor:              m.LoadFactor,
		Status:                  m.Status,
		ErrorMessage:            derefString(m.ErrorMessage),
		LastUsedAt:              m.LastUsedAt,
		ExpiresAt:               m.ExpiresAt,
		AutoPauseOnExpired:      m.AutoPauseOnExpired,
		CreatedAt:               m.CreatedAt,
		UpdatedAt:               m.UpdatedAt,
		Schedulable:             m.Schedulable,
		RateLimitedAt:           m.RateLimitedAt,
		RateLimitResetAt:        m.RateLimitResetAt,
		OverloadUntil:           m.OverloadUntil,
		TempUnschedulableUntil:  m.TempUnschedulableUntil,
		TempUnschedulableReason: derefString(m.TempUnschedulableReason),
		SessionWindowStart:      m.SessionWindowStart,
		SessionWindowEnd:        m.SessionWindowEnd,
		SessionWindowStatus:     derefString(m.SessionWindowStatus),
		ParentAccountID:         m.ParentAccountID,
		QuotaDimension:          string(m.QuotaDimension),
	}
}
