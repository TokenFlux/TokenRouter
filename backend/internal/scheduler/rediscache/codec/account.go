// 本文件只定义 sched:v2 的历史存储形状；凭据不进入账号公开 JSON。
package codec

import (
	"encoding/json"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"time"
)

type accountWire struct {
	ID                      int64
	Name                    string
	Notes                   *string
	Platform                string
	Type                    string
	Credentials             map[string]any
	Extra                   map[string]any
	ProxyID                 *int64
	ProxyFallbackOriginID   *int64
	ProxyFallbackOriginName *string
	Concurrency             int
	Priority                int
	RateMultiplier          *float64
	LoadFactor              *int
	Status                  string
	ErrorMessage            string
	LastUsedAt              *time.Time
	ExpiresAt               *time.Time
	AutoPauseOnExpired      bool
	CreatedAt               time.Time
	UpdatedAt               time.Time
	Schedulable             bool
	RateLimitedAt           *time.Time
	RateLimitResetAt        *time.Time
	OverloadUntil           *time.Time
	TempUnschedulableUntil  *time.Time
	TempUnschedulableReason string
	QuotaAutoPaused         bool `json:"-"`
	SessionWindowStart      *time.Time
	SessionWindowEnd        *time.Time
	SessionWindowStatus     string
	ParentAccountID         *int64
	QuotaDimension          string
	Proxy                   *egress.Proxy
	AccountGroups           []accountGroupWire
	GroupIDs                []int64
	Groups                  []*groupWire
}

// 原分组记录不回载反向账号关系；旧字段仍写 null，避免改变现存缓存形状。
type groupWire struct {
	*accessview.GroupConfig
	AccountGroups []json.RawMessage
}
type accountGroupWire struct {
	AccountID int64
	GroupID   int64
	CreatedAt time.Time
	Account   *accountWire
	Group     *groupWire
}

func encodeGroup(group *accessview.GroupConfig) *groupWire {
	if group == nil {
		return nil
	}
	return &groupWire{GroupConfig: group}
}
func decodeGroup(group *groupWire) *accessview.GroupConfig {
	if group == nil {
		return nil
	}
	return group.GroupConfig
}

// MarshalAccountRecord 保留完整快照中的凭据、额外字段及 nil/空关系集合。
func MarshalAccountRecord(value *account.Record) ([]byte, error) {
	return json.Marshal(recordToWire(value, make(map[*account.Record]*accountWire)))
}
func recordToWire(value *account.Record, seen map[*account.Record]*accountWire) *accountWire {
	if value == nil {
		return nil
	}
	if previous, ok := seen[value]; ok {
		return previous
	}
	out := &accountWire{}
	seen[value] = out
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
	out.Proxy = value.Proxy
	out.GroupIDs = value.GroupIDs

	if value.Groups != nil {
		out.Groups = make([]*groupWire, len(value.Groups))
		for i, g := range value.Groups {
			out.Groups[i] = encodeGroup(g)
		}
	}
	if value.AccountGroups != nil {
		out.AccountGroups = make([]accountGroupWire, len(value.AccountGroups))
		for i, g := range value.AccountGroups {
			out.AccountGroups[i] = accountGroupWire{AccountID: g.AccountID, GroupID: g.GroupID, CreatedAt: g.CreatedAt, Account: recordToWire(g.Account, seen), Group: encodeGroup(g.Group)}
		}
	}
	return out
}

// UnmarshalAccountRecord 只恢复受控完整读取结果，不把凭据交给调度评分核心。
func UnmarshalAccountRecord(payload []byte) (*account.Record, error) {
	var wire accountWire
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, err
	}
	return recordFromWire(&wire), nil
}
func recordFromWire(value *accountWire) *account.Record {
	if value == nil {
		return nil
	}
	out := &account.Record{Now: time.Now, LoadLocation: time.LoadLocation}
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
	out.Proxy = value.Proxy
	out.GroupIDs = value.GroupIDs

	if value.Groups != nil {
		out.Groups = make([]*accessview.GroupConfig, len(value.Groups))
		for i, g := range value.Groups {
			out.Groups[i] = decodeGroup(g)
		}
	}
	if value.AccountGroups != nil {
		out.AccountGroups = make([]account.GroupMembership, len(value.AccountGroups))
		for i, g := range value.AccountGroups {
			out.AccountGroups[i] = account.GroupMembership{AccountID: g.AccountID, GroupID: g.GroupID, CreatedAt: g.CreatedAt, Account: recordFromWire(g.Account), Group: decodeGroup(g.Group)}
		}
	}
	return out
}
