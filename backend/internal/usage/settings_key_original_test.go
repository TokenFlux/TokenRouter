package usage_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/usage"
)

func TestSettingKeyAllowUserViewErrorRequests_Constant(t *testing.T) {
	if usage.SettingKeyAllowUserViewErrorRequests != "allow_user_view_error_requests" {
		t.Fatalf("unexpected key: %s", usage.SettingKeyAllowUserViewErrorRequests)
	}
}
