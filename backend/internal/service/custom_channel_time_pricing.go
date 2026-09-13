package service

import (
	provider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	time "time"
)

// loadChannelTimePricingLocation 保留原时区缓存与错误文本的入口。
func loadChannelTimePricingLocation(name string) (*time.Location, error) {
	return provider.LoadPricingLocation(name)
}
