package service

import gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

// accountModelPolicy 只把剩余执行入口的账号值与当前路线分别传给原生模型规则。
func accountModelPolicy(value *Account) gatewayprovider.ModelPolicy {
	policy := gatewayprovider.ModelPolicy{Record: AccountRecordView(value)}
	if value != nil {
		policy.Route = value.attemptRoute
	}
	return policy
}
