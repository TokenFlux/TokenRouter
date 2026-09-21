package provider

import (
	"fmt"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

var channelTimePricingLocations sync.Map

// LoadPricingLocation 在技术边界加载并复用时区，纯定价只接收返回的 Location。
func LoadPricingLocation(name string) (*time.Location, error) {
	if err := pricing.ValidateTimezoneName(name); err != nil {
		return nil, err
	}
	if cached, ok := channelTimePricingLocations.Load(name); ok {
		location, valid := cached.(*time.Location)
		if valid && location != nil {
			return location, nil
		}
		channelTimePricingLocations.Delete(name)
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	actual, _ := channelTimePricingLocations.LoadOrStore(name, location)
	actualLocation, ok := actual.(*time.Location)
	if !ok || actualLocation == nil {
		return nil, fmt.Errorf("invalid cached timezone %q", name)
	}
	return actualLocation, nil
}
