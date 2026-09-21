package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

func TestIsQoderAuthenticationError(t *testing.T) {
	for _, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		err := fmt.Errorf("wrapped: %w", &qoder.APIError{StatusCode: statusCode})
		if !isQoderAuthenticationError(err) {
			t.Fatalf("status %d should be treated as authentication failure", statusCode)
		}
	}
	if isQoderAuthenticationError(&qoder.APIError{StatusCode: http.StatusInternalServerError}) {
		t.Fatal("status 500 should not trigger PAT session rebuild")
	}
}

func TestQoderQuotaInfoFromResponseInfersAddOnQuotaRemainingFromCap(t *testing.T) {
	t.Parallel()

	quota := qoderQuotaInfoFromResponse(&qoder.QuotaUsageResponse{
		UserType:        "teams",
		UsageType:       "credits",
		IsQuotaExceeded: true,
		ExpiresAt:       qoder.FlexibleInt64(time.Now().Add(time.Hour).UnixMilli()),
		UserQuota:       &qoder.QuotaProgress{Total: 100, Used: 100, Remaining: 0, Unit: "credits"},
		AddOnQuota: &qoder.QuotaProgress{
			Cap:  50,
			Used: 10,
			Unit: "credits",
		},
	}, time.Now(), false)

	if quota.AddOnQuota == nil {
		t.Fatalf("expected add-on quota: %#v", quota)
	}
	if quota.AddOnQuota.Total != 50 {
		t.Fatalf("add-on total = %v, want 50", quota.AddOnQuota.Total)
	}
	if quota.AddOnQuota.Remaining != 40 {
		t.Fatalf("add-on remaining = %v, want 40", quota.AddOnQuota.Remaining)
	}
	if _, limited := accountcore.QoderQuotaRateLimitResetAt(quota, time.Now()); limited {
		t.Fatalf("add-on cap-derived remaining credits should prevent quota rate limit")
	}
}

func TestQoderQuotaInfoFromResponseInfersOrgResourcePackageTotalFromUsedRemaining(t *testing.T) {
	t.Parallel()

	quota := qoderQuotaInfoFromResponse(&qoder.QuotaUsageResponse{
		UserType:        "teams",
		UsageType:       "credits",
		IsQuotaExceeded: true,
		ExpiresAt:       qoder.FlexibleInt64(time.Now().Add(time.Hour).UnixMilli()),
		UserQuota:       &qoder.QuotaProgress{Total: 100, Used: 100, Remaining: 0, Unit: "credits"},
		OrgResourcePackage: &qoder.QuotaProgress{
			Used:      25,
			Remaining: 75,
			Unit:      "credits",
		},
	}, time.Now(), false)

	if quota.OrgResourcePackage == nil {
		t.Fatalf("expected org resource package quota: %#v", quota)
	}
	if quota.OrgResourcePackage.Total != 100 {
		t.Fatalf("org resource total = %v, want 100", quota.OrgResourcePackage.Total)
	}
	if capacity := accountcore.QoderQuotaTotalCapacity(quota); capacity != 200 {
		t.Fatalf("total capacity = %v, want 200", capacity)
	}
	if remaining, ok := accountcore.QoderQuotaTotalRemaining(quota); !ok || remaining != 75 {
		t.Fatalf("total remaining = (%v, %v), want (75, true)", remaining, ok)
	}
	if _, limited := accountcore.QoderQuotaRateLimitResetAt(quota, time.Now()); limited {
		t.Fatalf("org resource remaining credits should prevent quota rate limit")
	}
}

func TestQoderQuotaProgressFromJSONInfersOnlyMissingRemaining(t *testing.T) {
	t.Parallel()

	var explicit qoder.QuotaUsageResponse
	if err := json.Unmarshal([]byte(`{
		"userType":"teams",
		"isQuotaExceeded":true,
		"expiresAt":4102444800000,
		"userQuota":{"total":100,"used":50,"remaining":0,"percentage":0,"unit":"credits"}
	}`), &explicit); err != nil {
		t.Fatalf("unmarshal explicit quota response: %v", err)
	}
	quota := qoderQuotaInfoFromResponse(&explicit, time.Now(), false)
	if quota == nil || quota.UserQuota == nil {
		t.Fatalf("expected user quota: %#v", quota)
	}
	if quota.UserQuota.Remaining != 0 {
		t.Fatalf("explicit remaining=0 must not be inferred to positive balance, got %v", quota.UserQuota.Remaining)
	}
	if quota.UserQuota.Percentage != 0 {
		t.Fatalf("explicit percentage=0 must be preserved, got %v", quota.UserQuota.Percentage)
	}
	if _, limited := accountcore.QoderQuotaRateLimitResetAt(quota, time.Now()); !limited {
		t.Fatalf("explicit zero remaining with exceeded quota should set quota rate limit")
	}

	var missing qoder.QuotaUsageResponse
	if err := json.Unmarshal([]byte(`{
		"userType":"teams",
		"isQuotaExceeded":false,
		"expiresAt":4102444800000,
		"userQuota":{"total":100,"used":40,"unit":"credits"}
	}`), &missing); err != nil {
		t.Fatalf("unmarshal missing quota response: %v", err)
	}
	missingQuota := qoderQuotaInfoFromResponse(&missing, time.Now(), false)
	if missingQuota == nil || missingQuota.UserQuota == nil {
		t.Fatalf("expected missing user quota: %#v", missingQuota)
	}
	if missingQuota.UserQuota.Remaining != 60 {
		t.Fatalf("missing remaining should be inferred from total-used, got %v", missingQuota.UserQuota.Remaining)
	}
	if missingQuota.UserQuota.Percentage != 40 {
		t.Fatalf("missing percentage should be inferred from used/total, got %v", missingQuota.UserQuota.Percentage)
	}
}
