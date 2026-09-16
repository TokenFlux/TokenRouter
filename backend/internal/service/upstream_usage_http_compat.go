// 旧端点/错误 helper 委托同一技术实现，S15/S16 清理。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/upstream/usageprovider"
)

func upstreamUsageEndpoint(base, path string) (string, error) {
	return native.UpstreamUsageEndpoint(base, path, buildOpenAIEndpointURL)
}
func upstreamUsageStatusEndpoint(base string) (string, error) {
	return native.UpstreamUsageStatusEndpoint(base)
}
func upstreamUsageTokenEndpoint(base string) (string, error) {
	return native.UpstreamUsageTokenEndpoint(base)
}
func upstreamUsageWalletEndpoint(base string) (string, error) {
	return native.UpstreamUsageWalletEndpoint(base)
}
func upstreamUsageUserSelfEndpoint(base string) (string, error) {
	return native.UpstreamUsageUserSelfEndpoint(base)
}
func upstreamUsageRootEndpoint(base, path string) (string, error) {
	return native.UpstreamUsageRootEndpoint(base, path)
}
