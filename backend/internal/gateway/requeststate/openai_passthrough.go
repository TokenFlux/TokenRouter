package requeststate

import "context"

type openAIHTTPPassthroughRoutingKey struct{}

// WithOpenAIHTTPPassthroughRouting 保留 nil context 的后台缺省与本次请求的透传意图。
func WithOpenAIHTTPPassthroughRouting(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIHTTPPassthroughRoutingKey{}, true)
}
func OpenAIHTTPPassthroughRoutingFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(openAIHTTPPassthroughRoutingKey{}).(bool)
	return value
}
