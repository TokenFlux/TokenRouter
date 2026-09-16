// 旧路径护栏委托共享平台原语，不复制允许清单。
package service

import "github.com/TokenFlux/TokenRouter/internal/upstream"

const maxUpstreamPathSegmentLen = upstream.MaxUpstreamPathSegmentLen
const maxUpstreamPathSegments = upstream.MaxUpstreamPathSegments

func sanitizedUpstreamPathSuffix(raw string) (string, bool) {
	return upstream.SanitizedUpstreamPathSuffix(raw)
}
