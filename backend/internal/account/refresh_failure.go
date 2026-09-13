package account

import (
	"context"
	"time"
)

// RefreshFailureVersion 限定旧失败能够修改的账号身份，不能覆盖管理员刚改变的调度开关。
type RefreshFailureVersion struct {
	CredentialVersion
	Schedulable bool
}
type RefreshFailureKind uint8

const (
	RefreshFailurePermanent RefreshFailureKind = iota
	RefreshFailureCooldown
)

type RefreshFailure struct {
	Kind    RefreshFailureKind
	Message string
	Until   time.Time
}

// RefreshFailureWriter 返回身份是否仍匹配；同身份已有更长 cooldown 时保持原值但仍算匹配。
// 未匹配不得触发内存阻断、成功失效或新的交换。
type RefreshFailureWriter interface {
	ApplyOAuthRefreshFailure(context.Context, RefreshFailureVersion, RefreshFailure) (bool, error)
}

func FailureVersion(value *Record) RefreshFailureVersion {
	if value == nil {
		return RefreshFailureVersion{}
	}
	credentials := CloneValues(value.Credentials)
	if credentials == nil {
		credentials = map[string]any{}
	}
	return RefreshFailureVersion{CredentialVersion: CredentialVersion{ID: value.ID, Platform: value.Platform, Type: value.Type, Status: value.Status, Credentials: credentials, ProxyID: clonePointer(value.ProxyID)}, Schedulable: value.Schedulable}
}
