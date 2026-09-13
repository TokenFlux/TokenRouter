// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	time "time"
)

type ProxyHandler = egresshttp.ProxyHandler

// NewProxyHandler 保留独立旧调用者，生产路由直接注入新 egress HTTP 实例。
func NewProxyHandler(admin egress.ProxyAdministrator) *ProxyHandler {
	return egresshttp.NewProxyHandler(admin, egress.NewProxyTransfer(admin, legacyProxyTransferTasks{}, time.Now))
}

type legacyProxyTransferTasks struct{}

func (legacyProxyTransferTasks) Go(name string, run func()) bool {
	return service.RunBackgroundTask(name, service.BackgroundCall0(run))
}

type CreateProxyRequest = egresshttp.CreateProxyRequest
type UpdateProxyRequest = egresshttp.UpdateProxyRequest
type BatchCreateProxyItem = egresshttp.BatchCreateProxyItem
type BatchCreateRequest = egresshttp.BatchCreateRequest
