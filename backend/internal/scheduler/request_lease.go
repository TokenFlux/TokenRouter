package scheduler

import "context"

// requestLeaseKey 只传递当前请求资源拥有者，不涉及存储事务或认证主体。
type requestLeaseKey struct{}

// WithRequestLease 将已经取得的用户租约传递给后续账号尝试，执行层继续决定释放时机。
func WithRequestLease(ctx context.Context, lease *Lease) context.Context {
	return context.WithValue(ctx, requestLeaseKey{}, lease)
}
func RequestLease(ctx context.Context) *Lease {
	if ctx == nil {
		return nil
	}
	lease, _ := ctx.Value(requestLeaseKey{}).(*Lease)
	return lease
}

// ownRequestResource 让请求异常返回也能清理；显式 attempt 完成与请求清理共享幂等释放。
func ownRequestResource(ctx context.Context, release func()) {
	if owner := RequestLease(ctx); owner != nil {
		owner.Own(release)
	}
}
