package service

import (
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
)

// validateChannelTimePricing 保持先校验时区、再校验时间区间的错误顺序。
func validateChannelTimePricing(config *ChannelTimePricing) error {
	if config == nil || len(config.Periods) == 0 {
		return nil
	}
	if _, err := loadChannelTimePricingLocation(config.Timezone); err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	return pricing.ValidateChannelTimePricing(config)
}

// loadChannelTimePricingLocation 保留原时区缓存与错误文本的入口。
func loadChannelTimePricingLocation(name string) (*time.Location, error) {
	return provider.LoadPricingLocation(name)
}
