package service

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// upstreamErrorAccount 只提取请求观测字段，不传递凭据。
func upstreamErrorAccount(value *gatewayprovider.ExecutionAccount) *gatewayhttp.UpstreamErrorAccount {
	if value == nil {
		return nil
	}
	return &gatewayhttp.UpstreamErrorAccount{ID: value.Record.ID, Name: value.Record.Name, Platform: value.Record.Platform}
}
