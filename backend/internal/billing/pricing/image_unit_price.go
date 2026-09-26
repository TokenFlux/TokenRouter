package pricing

import (
	"fmt"
	"math"
	"strings"
)

// ConfiguredImageUnitPrice 保持显式零价与未定价之间的区别。
func ConfiguredImageUnitPrice(resolved *ResolvedPricing, size string) (float64, bool) {
	var price float64
	var found bool
	if resolved != nil && (resolved.Mode == BillingModeImage || resolved.Mode == BillingModePerRequest) {
		price, found = GetRequestTierPriceValue(resolved, strings.TrimSpace(size))
		if !found && resolved.ConfigPricing != nil && resolved.ConfigPricing.PerRequestPrice != nil {
			price, found = resolved.DefaultPerRequestPrice, true
		}
	}

	return price, found
}

// ValidateImageUnitPrice 保留旧固定单张价的失败语义。
func ValidateImageUnitPrice(price float64) (float64, error) {
	if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
		return 0, fmt.Errorf("invalid image unit price: %w", ErrModelPricingUnavailable)
	}
	return price, nil
}
