// 原传输上下文入口委托同一技术 key，不改变已设置 context 的识别。
package service

import (
	"context"

	native "github.com/TokenFlux/TokenRouter/internal/upstream"
)

type HTTPUpstreamProfile = native.HTTPUpstreamProfile

const HTTPUpstreamProfileDefault = native.HTTPUpstreamProfileDefault
const HTTPUpstreamProfileOpenAI = native.HTTPUpstreamProfileOpenAI
const HTTPUpstreamProfileGrok = native.HTTPUpstreamProfileGrok

func WithHTTPUpstreamProfile(ctx context.Context, profile HTTPUpstreamProfile) context.Context {
	return native.WithHTTPUpstreamProfile(ctx, profile)
}
func HTTPUpstreamProfileFromContext(ctx context.Context) HTTPUpstreamProfile {
	return native.HTTPUpstreamProfileFromContext(ctx)
}
func WithHTTPUpstreamRedirectsDisabled(ctx context.Context) context.Context {
	return native.WithHTTPUpstreamRedirectsDisabled(ctx)
}
func HTTPUpstreamRedirectsDisabled(ctx context.Context) bool {
	return native.HTTPUpstreamRedirectsDisabled(ctx)
}
func WithHTTPUpstreamPublicHostsOnly(ctx context.Context) context.Context {
	return native.WithHTTPUpstreamPublicHostsOnly(ctx)
}
func HTTPUpstreamPublicHostsOnly(ctx context.Context) bool {
	return native.HTTPUpstreamPublicHostsOnly(ctx)
}
