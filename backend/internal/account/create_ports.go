// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
)

type GroupReference struct {
	Platform         string
	RequireOAuthOnly bool
	ID               int64
	Name             string
}
type AdminGroups interface {
	ActiveGroups(context.Context, string) ([]GroupReference, error)
	ValidateGroups(context.Context, []int64) error
	DefaultGroup(context.Context, string) (*GroupReference, error)
	GetGroup(context.Context, int64) (*GroupReference, error)
}
type CreateCredentialHooks struct {
	// 平台端口保留 Qoder 站点切换与 PAT 校验的旧时序。
	Site         func(*Record) (string, error)
	ValidateEdit func(context.Context, *Record, bool) error
	Prepare      func(*Record)
	Validate     func(context.Context, *Record) error
}

// DuplicateStore 拥有原账号与关联一次提交，不执行平台交换。
type DuplicateStore interface {
	CreateWithAccountGroups(context.Context, *Record, []GroupMembership) error
}

// ShadowProxyStore 保留原同步传播顺序，配置字段写权限继续收口。
type ShadowProxyStore interface {
	ListShadowsByParent(context.Context, int64) ([]*Record, error)
	Update(context.Context, *Record) error
}
