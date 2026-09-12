// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	context "context"
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
