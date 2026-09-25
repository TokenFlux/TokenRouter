// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	"context"
)

// RequestMetadata 由网关入口投影，保留值缺失与显式空值的区别。
type RequestMetadata struct {
	ForcePlatform      string
	ForcePlatformSet   bool
	InboundEndpoint    string
	InboundEndpointSet bool
}
type requestMetadataKey struct{}

func WithRequestMetadata(ctx context.Context, value RequestMetadata) context.Context {
	return context.WithValue(ctx, requestMetadataKey{}, value)
}

// WithForcePlatform 从已有投影派生新值，保留显式空平台与尚未设置的区别。
func WithForcePlatform(ctx context.Context, platform string) context.Context {
	value := RequestMetadataFromContext(ctx)
	value.ForcePlatform, value.ForcePlatformSet = platform, true
	return WithRequestMetadata(ctx, value)
}

// WithInboundEndpoint 只记录已规范化入口，不读取请求体或重新执行鉴权。
func WithInboundEndpoint(ctx context.Context, endpoint string) context.Context {
	value := RequestMetadataFromContext(ctx)
	value.InboundEndpoint, value.InboundEndpointSet = endpoint, true
	return WithRequestMetadata(ctx, value)
}
func RequestMetadataFromContext(ctx context.Context) RequestMetadata {
	if ctx == nil {
		return RequestMetadata{}
	}
	v, _ := ctx.Value(requestMetadataKey{}).(RequestMetadata)
	return v
}
func ForcePlatformFromContext(ctx context.Context) (string, bool) {
	v := RequestMetadataFromContext(ctx)
	return v.ForcePlatform, v.ForcePlatformSet
}
func InboundEndpointFromContext(ctx context.Context) (string, bool) {
	v := RequestMetadataFromContext(ctx)
	return v.InboundEndpoint, v.InboundEndpointSet
}
