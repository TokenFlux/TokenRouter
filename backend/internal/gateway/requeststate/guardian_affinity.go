package requeststate

import (
	"context"
	"strings"
)

type guardianParentAffinityKey struct{}

// GuardianParentAffinity 只含请求内会话散列，不含可公开或反向读取的账号 ID。
type GuardianParentAffinity struct{ CurrentSessionHash, LegacySessionHash string }

func WithGuardianParentAffinity(ctx context.Context, value GuardianParentAffinity) context.Context {
	return context.WithValue(ctx, guardianParentAffinityKey{}, value)
}
func GuardianParentAffinityFromContext(ctx context.Context) (GuardianParentAffinity, bool) {
	if ctx == nil {
		return GuardianParentAffinity{}, false
	}
	value, ok := ctx.Value(guardianParentAffinityKey{}).(GuardianParentAffinity)
	return value, ok && value.CurrentSessionHash != ""
}

// PreserveGuardianParentBinding 保留父线程的当前和旧散列绑定，不允许子请求覆盖。
func PreserveGuardianParentBinding(ctx context.Context, hash string) bool {
	value, ok := GuardianParentAffinityFromContext(ctx)
	if !ok {
		return false
	}
	hash = strings.TrimSpace(hash)
	return hash != "" && (hash == value.CurrentSessionHash || hash == value.LegacySessionHash)
}
