// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package domain

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
)

type SubscriptionPlanSnapshot = billing.SubscriptionPlanSnapshot
type BillingAllocationType = billing.BillingAllocationType

const BillingAllocationTypeSubscription = billing.BillingAllocationTypeSubscription
const BillingAllocationTypeBalance = billing.BillingAllocationTypeBalance

type BillingAllocation = billing.BillingAllocation
