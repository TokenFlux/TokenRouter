package apikey

import "context"

type accessSnapshotContextKey struct{}

// WithAccessSnapshot 由认证成功的入口调用；失败诊断仍走独立的观测投影。
// 保存结构体副本，后续 Fast 策略覆盖不会修改认证结果或其它请求。
func WithAccessSnapshot(ctx context.Context, access AccessSnapshot) context.Context {
	access.TeamID = clonePointer(access.TeamID)
	return context.WithValue(ctx, accessSnapshotContextKey{}, access)
}

func AccessSnapshotFromContext(ctx context.Context) (AccessSnapshot, bool) {
	if ctx == nil {
		return AccessSnapshot{}, false
	}
	access, ok := ctx.Value(accessSnapshotContextKey{}).(AccessSnapshot)
	access.TeamID = clonePointer(access.TeamID)
	return access, ok
}

// WithFastModePolicy 只派生当前请求策略，不改变 L1/L2 中的 Key。
func WithFastModePolicy(ctx context.Context, policy string) context.Context {
	access, _ := AccessSnapshotFromContext(ctx)
	access.fastModePolicy = policy
	return WithAccessSnapshot(ctx, access)
}

func (a AccessSnapshot) FastModePolicy() string { return a.fastModePolicy }
