// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

var ErrSessionBindingMismatch = identity.ErrSessionBindingMismatch

type SessionBinding = identity.SessionBinding

// WithSessionBinding 委托所属模块的唯一实现。
func WithSessionBinding(ctx context.Context, binding *SessionBinding) context.Context {
	return identity.WithSessionBinding(ctx, binding)
}

// SessionBindingFromContext 委托所属模块的唯一实现。
func SessionBindingFromContext(ctx context.Context) *SessionBinding {
	return identity.SessionBindingFromContext(ctx)
}
