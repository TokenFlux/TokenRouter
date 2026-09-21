package service

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

func setAnthropicAPIKeyAuthHeader(header http.Header, value *Account, token string) {
	claude.SetAPIKeyAuthHeader(header, value.GetAnthropicAPIKeyAuthScheme() == account.AnthropicAPIKeyAuthSchemeAuthorizationBearer, token)
}

// 旧实体只投影认证配置，不保留认证头规则副本。
func (a *Account) GetAnthropicAPIKeyAuthScheme() string {
	return protocolRecord(a).GetAnthropicAPIKeyAuthScheme()
}
