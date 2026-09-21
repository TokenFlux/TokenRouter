//go:build unit

package billing

// newOriginalEligibility 直接构造唯一资金缓存，保留原独立夹具的异步回填。
func newOriginalEligibility(cache BillingCache, users BalanceReader, keys APIKeyRateLimitLoader, quotas UserPlatformQuotaRepository, options *EligibilityOptions) *Eligibility {
	return NewEligibility(cache, users, keys, quotas, func() EligibilityOptions { return *options }, nil, NewQuotaCoordinator(), func(_ string, fn func()) { go fn() })
}

// updateOriginalEligibilityOptions 只更新夹具的静态配置，在执行检查前完成。
func updateOriginalEligibilityOptions(service *Eligibility, update func(*EligibilityOptions)) {
	options := service.options()
	update(&options)
	service.options = func() EligibilityOptions { return options }
}
