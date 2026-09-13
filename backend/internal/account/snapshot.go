package account

import (
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// AccountSnapshot 是候选判断需要的身份、资格与运行投影，不携带凭据或管理 Extra。
// 模型重写及平台凭据读取仍由各自显式入口提供，不允许从快照反查完整记录。
type AccountSnapshot struct {
	// ModelPolicy 仅在实际模型匹配点装配，不随协议预检提前读取动态默认值。
	ModelPolicy      ModelRoutingSnapshot `json:"-"`
	ID               int64
	ParentAccountID  *int64
	Platform         string
	Type             string
	AuthMode         string
	EnabledProtocols []capability.ProtocolID
	Status           string
	Schedulable      bool
	Concurrency      int
	Priority         int
	ExpiresAt        *time.Time
}

// RoutingSnapshot 返回请求独立副本；协议缺省解析仍由账号配置规则唯一负责。
func (r *Record) RoutingSnapshot() AccountSnapshot {
	if r == nil {
		return AccountSnapshot{}
	}
	snapshot := AccountSnapshot{ID: r.ID, Platform: r.Platform, Type: r.Type, AuthMode: ProtocolAuthMode(r),
		EnabledProtocols: slices.Clone(r.UpstreamProtocols()), Status: r.Status, Schedulable: r.Schedulable,
		Concurrency: r.Concurrency, Priority: r.Priority}
	if r.ParentAccountID != nil {
		id := *r.ParentAccountID
		snapshot.ParentAccountID = &id
	}
	if r.ExpiresAt != nil {
		at := *r.ExpiresAt
		snapshot.ExpiresAt = &at
	}
	return snapshot
}

// Protocols 仅把已经解析好的能力传给纯目录，不暴露账号存储结构。
func (s AccountSnapshot) Protocols() capability.AccountProtocols {
	return capability.AccountProtocols{Platform: s.Platform, Type: s.Type, AuthMode: s.AuthMode, Enabled: slices.Clone(s.EnabledProtocols)}
}
