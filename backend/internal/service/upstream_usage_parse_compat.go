// 旧通用查询 helper 只委托原生实现，后续消费者清零后删除。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/upstream/usageprovider"
)

func parseSub2APIUsage(body []byte) (*UpstreamUsageInfo, error) {
	return native.ParseSub2APIUsage(body)
}

func parseZivvUsage(body []byte) (*UpstreamUsageInfo, error) { return native.ParseZivvUsage(body) }

type newAPITokenUsageResponse = native.NewAPITokenUsageResponse

type newAPIUsageDisplaySettings = native.NewAPIUsageDisplaySettings

func parseNewAPIWalletBalance(body []byte, settings newAPIUsageDisplaySettings) (*UpstreamUsageInfo, error) {
	return native.ParseNewAPIWalletBalance(body, settings)
}
func parseNewAPIUserSelfWallet(body []byte, settings newAPIUsageDisplaySettings, expectedUserID string) (*UpstreamUsageInfo, error) {
	return native.ParseNewAPIUserSelfWallet(body, settings, expectedUserID)
}

func parseNewAPITokenUsage(body []byte) (*newAPITokenUsageResponse, error) {
	return native.ParseNewAPITokenUsage(body)
}
func normalizeNewAPITokenUsage(response *newAPITokenUsageResponse, settings newAPIUsageDisplaySettings) (*UpstreamUsageInfo, error) {
	return native.NormalizeNewAPITokenUsage(response, settings)
}
func normalizeNewAPIQuota(value float64, settings newAPIUsageDisplaySettings) float64 {
	return native.NormalizeNewAPIQuota(value, settings)
}
