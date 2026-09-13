// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
)

var ErrProxyNotFound = egress.ErrProxyNotFound

var ErrProxyInUse = egress.ErrProxyInUse

type ProxyRepository = egress.ProxyRepository

type CreateProxyRequest = egress.CreateProxyRequest

type UpdateProxyRequest = egress.UpdateProxyRequest

type ProxyService = egress.ProxyService

// NewProxyService 委托所属模块的唯一实现。
func NewProxyService(proxyRepo ProxyRepository) *ProxyService {
	return egress.NewProxyService(proxyRepo)
}
