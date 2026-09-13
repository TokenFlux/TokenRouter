// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	proxydto "github.com/TokenFlux/TokenRouter/internal/egress/httpapi/dto"
)

type Proxy = proxydto.Proxy

type ProxyWithAccountCount = proxydto.ProxyWithAccountCount

type AdminProxy = proxydto.AdminProxy

type AdminProxyWithAccountCount = proxydto.AdminProxyWithAccountCount

type ProxyAccountSummary = proxydto.ProxyAccountSummary
