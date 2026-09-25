//go:build unit

package identity_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
)

func TestSettingKeyAuthSourcePlatformQuotas(t *testing.T) {
	if got := identity.SettingKeyAuthSourcePlatformQuotas("email"); got != "auth_source_default_email_platform_quotas" {
		t.Fatalf("got %q, want %q", got, "auth_source_default_email_platform_quotas")
	}
	if got := identity.SettingKeyAuthSourcePlatformQuotas("dingtalk"); got != "auth_source_default_dingtalk_platform_quotas" {
		t.Fatalf("got %q, want %q", got, "auth_source_default_dingtalk_platform_quotas")
	}
}
