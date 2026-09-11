package provider

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

var channelTimePricingLocations sync.Map

// LoadPricingLocation 在技术边界加载并复用时区，纯定价只接收返回的 Location。
func LoadPricingLocation(name string) (*time.Location, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("timezone is required")
	}
	if name == "Local" {
		return nil, fmt.Errorf("local is not a supported timezone")
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
