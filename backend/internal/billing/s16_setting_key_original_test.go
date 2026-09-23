//go:build unit

package billing_test

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"testing"
)

func TestSettingKeyDefaultPlatformQuotas(t *testing.T) {
	if billing.SettingKeyDefaultPlatformQuotas != "default_platform_quotas" {
		t.Errorf("SettingKeyDefaultPlatformQuotas = %q, want %q",
			billing.SettingKeyDefaultPlatformQuotas, "default_platform_quotas")
	}
}
