// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	slog "log/slog"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

func legacyCatalogueDefaults() routing.CatalogueDefaults {
	return gatewayprovider.CatalogueDefaults()
}
func (s *GatewayService) requestableModelResolver() routing.RequestableResolver {
	var channels routing.CatalogueChannels
	if s != nil && s.channelService != nil {
		channels = s.channelService
	}
	return routing.RequestableResolver{Channels: channels, Defaults: legacyCatalogueDefaults(), Warn: slog.Warn}
}

func legacyCatalogueAccounts(gateway *GatewayService, values []Account) []routing.CatalogueAccount {
	if values == nil {
		return nil
	}
	out := make([]routing.CatalogueAccount, len(values))
	for i := range values {
		out[i] = gatewayprovider.CatalogueAccount(AccountRecordView(&values[i]), values[i].attemptRoute)
	}
	return out
}
