package scheduler

import "context"

// 这些请求标记只控制选择副作用，不承载认证或存储事务。
type selectOnlyKey struct{}
type preservedStickyKey struct{}

func WithSelectOnly(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, selectOnlyKey{}, true)
}
func IsSelectOnly(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(selectOnlyKey{}).(bool)
	return v
}
func WithPreservedSticky(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, preservedStickyKey{}, true)
}
func PreserveStickyFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(preservedStickyKey{}).(bool)
	return v
}
