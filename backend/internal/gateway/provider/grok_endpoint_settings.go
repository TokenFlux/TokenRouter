package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// GrokBaseURLForMode 将已选设置映射到平台端点，不创建客户端或复制运行缓存。
func GrokBaseURLForMode(mode string) string {
	switch gateway.NormalizeGrokDefaultBaseURLMode(mode) {
	case "api":
		return grok.DefaultBaseURL
	case "us-east-1":
		return grok.DefaultUSEast1BaseURL
	case "us-west-2":
		return grok.DefaultUSWest2BaseURL
	case "eu-west-1":
		return grok.DefaultEUWest1BaseURL
	default:
		return grok.DefaultCLIBaseURL
	}
}

// GrokDefaultBaseURLReader 保留每次查询读取动态设置的原时点。
func GrokDefaultBaseURLReader(settings *gateway.RuntimeSettings) func(context.Context) string {
	return func(ctx context.Context) string { return GrokBaseURLForMode(settings.GetGrokDefaultBaseURLMode(ctx)) }
}
