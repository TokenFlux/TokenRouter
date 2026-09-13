// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"slices"
	time "time"
)

// AccountRecordView 把旧兼容实体投影为账号记录，保留执行副本的私有状态在旧入口。
func AccountRecordView(value *Account) *accountcore.Record {
	return accountcore.CloneRecord(accountRecordView(value, make(map[*Account]*accountcore.Record)))
}
func accountRecordView(value *Account, seen map[*Account]*accountcore.Record) *accountcore.Record {
	if value == nil {
		return nil
	}
	if previous, ok := seen[value]; ok {
		return previous
	}
	out := &accountcore.Record{Now: time.Now, LoadLocation: time.LoadLocation,
		ID:                      value.ID,
		Name:                    value.Name,
		Notes:                   value.Notes,
		Platform:                value.Platform,
		Type:                    value.Type,
		Credentials:             value.Credentials,
		Extra:                   value.Extra,
		ProxyID:                 value.ProxyID,
		ProxyFallbackOriginID:   value.ProxyFallbackOriginID,
		ProxyFallbackOriginName: value.ProxyFallbackOriginName,
		Concurrency:             value.Concurrency,
		Priority:                value.Priority,
		RateMultiplier:          value.RateMultiplier,
		LoadFactor:              value.LoadFactor,
		Status:                  value.Status,
		ErrorMessage:            value.ErrorMessage,
		LastUsedAt:              value.LastUsedAt,
		ExpiresAt:               value.ExpiresAt,
		AutoPauseOnExpired:      value.AutoPauseOnExpired,
		CreatedAt:               value.CreatedAt,
		UpdatedAt:               value.UpdatedAt,
		Schedulable:             value.Schedulable,
		RateLimitedAt:           value.RateLimitedAt,
		RateLimitResetAt:        value.RateLimitResetAt,
		OverloadUntil:           value.OverloadUntil,
		TempUnschedulableUntil:  value.TempUnschedulableUntil,
		TempUnschedulableReason: value.TempUnschedulableReason,
		QuotaAutoPaused:         value.QuotaAutoPaused,
		SessionWindowStart:      value.SessionWindowStart,
		SessionWindowEnd:        value.SessionWindowEnd,
		SessionWindowStatus:     value.SessionWindowStatus,
		ParentAccountID:         value.ParentAccountID,
		QuotaDimension:          value.QuotaDimension,
		GroupIDs:                value.GroupIDs,
	}
	seen[value] = out
	out.Proxy = value.Proxy
	if value.Groups != nil {
		out.Groups = make([]*accessview.GroupConfig, len(value.Groups))
		for i, g := range value.Groups {
			out.Groups[i] = (*accessview.GroupConfig)(RoutingGroupView(g))
		}
	}
	if value.AccountGroups != nil {
		out.AccountGroups = make([]accountcore.GroupMembership, len(value.AccountGroups))
		for i, g := range value.AccountGroups {
			out.AccountGroups[i] = accountcore.GroupMembership{AccountID: g.AccountID, GroupID: g.GroupID, CreatedAt: g.CreatedAt, Group: (*accessview.GroupConfig)(RoutingGroupView(g.Group)), Account: accountRecordView(g.Account, seen)}
		}
	}
	return out
}
func AccountFromRecord(value *accountcore.Record) *Account {
	return accountFromRecord(accountcore.CloneRecord(value), make(map[*accountcore.Record]*Account))
}
func accountFromRecord(value *accountcore.Record, seen map[*accountcore.Record]*Account) *Account {
	if value == nil {
		return nil
	}
	if previous, ok := seen[value]; ok {
		return previous
	}
	out := &Account{}
	seen[value] = out
	fillAccountRecord(out, value, seen)
	return out
}
func fillAccountRecord(out *Account, value *accountcore.Record, seen map[*accountcore.Record]*Account) {
	out.ID = value.ID
	out.Name = value.Name
	out.Notes = value.Notes
	out.Platform = value.Platform
	out.Type = value.Type
	out.Credentials = value.Credentials
	out.Extra = value.Extra
	out.ProxyID = value.ProxyID
	out.ProxyFallbackOriginID = value.ProxyFallbackOriginID
	out.ProxyFallbackOriginName = value.ProxyFallbackOriginName
	out.Concurrency = value.Concurrency
	out.Priority = value.Priority
	out.RateMultiplier = value.RateMultiplier
	out.LoadFactor = value.LoadFactor
	out.Status = value.Status
	out.ErrorMessage = value.ErrorMessage
	out.LastUsedAt = value.LastUsedAt
	out.ExpiresAt = value.ExpiresAt
	out.AutoPauseOnExpired = value.AutoPauseOnExpired
	out.CreatedAt = value.CreatedAt
	out.UpdatedAt = value.UpdatedAt
	out.Schedulable = value.Schedulable
	out.RateLimitedAt = value.RateLimitedAt
	out.RateLimitResetAt = value.RateLimitResetAt
	out.OverloadUntil = value.OverloadUntil
	out.TempUnschedulableUntil = value.TempUnschedulableUntil
	out.TempUnschedulableReason = value.TempUnschedulableReason
	out.QuotaAutoPaused = value.QuotaAutoPaused
	out.SessionWindowStart = value.SessionWindowStart
	out.SessionWindowEnd = value.SessionWindowEnd
	out.SessionWindowStatus = value.SessionWindowStatus
	out.ParentAccountID = value.ParentAccountID
	out.QuotaDimension = value.QuotaDimension
	out.GroupIDs = value.GroupIDs

	out.Proxy = value.Proxy
	if value.Groups != nil {
		out.Groups = make([]*Group, len(value.Groups))
		for i, g := range value.Groups {
			out.Groups[i] = GroupFromRouting((*routing.Group)(g))
		}
	}
	if value.AccountGroups != nil {
		out.AccountGroups = make([]AccountGroup, len(value.AccountGroups))
		for i, g := range value.AccountGroups {
			out.AccountGroups[i] = AccountGroup{AccountID: g.AccountID, GroupID: g.GroupID, CreatedAt: g.CreatedAt, Group: GroupFromRouting((*routing.Group)(g.Group)), Account: accountFromRecord(g.Account, seen)}
		}
	}
}

// ApplyAccountRecord 同步原调用者的持久字段，不改变其转发协议和热路径缓存句柄。
func ApplyAccountRecord(out *Account, value *accountcore.Record) {
	if out == nil || value == nil {
		return
	}
	cloned := accountcore.CloneRecord(value)
	fillAccountRecord(out, cloned, map[*accountcore.Record]*Account{cloned: out})
}

func AccountsFromRecords(values []accountcore.Record) []Account {
	if values == nil {
		return nil
	}
	out := make([]Account, len(values))
	for i := range values {
		out[i] = *AccountFromRecord(&values[i])
	}
	return out
}

// AccountMembershipsForRecord 保留传入关联的展示值，并将自关联绑定到同一新记录。
func AccountMembershipsForRecord(groups []AccountGroup, owner *Account, record *accountcore.Record) []accountcore.GroupMembership {
	if groups == nil {
		return nil
	}
	seen := map[*Account]*accountcore.Record{owner: record}
	out := make([]accountcore.GroupMembership, len(groups))
	for i, g := range groups {
		out[i] = accountcore.GroupMembership{AccountID: g.AccountID, GroupID: g.GroupID, CreatedAt: g.CreatedAt, Account: accountRecordView(g.Account, seen), Group: (*accessview.GroupConfig)(RoutingGroupView(g.Group))}
	}
	return out
}

func AccountRecordsView(values []Account) []accountcore.Record {
	if values == nil {
		return nil
	}
	out := make([]accountcore.Record, len(values))
	for i := range values {
		out[i] = *AccountRecordView(&values[i])
	}
	return out
}

// AccountSnapshotView 只输出旧执行入口实际使用的候选值，保留旧请求协议覆盖的解析。
func AccountSnapshotView(value *Account) accountcore.AccountSnapshot {
	if value == nil {
		return accountcore.AccountSnapshot{}
	}
	v := protocolRecord(value)
	v.ID = value.ID
	v.Status = value.Status
	v.Schedulable = value.Schedulable
	v.Concurrency = value.Concurrency
	v.Priority = value.Priority
	v.ExpiresAt = value.ExpiresAt
	snapshot := v.RoutingSnapshot()
	snapshot.EnabledProtocols = slices.Clone(value.UpstreamProtocols())
	return snapshot
}
