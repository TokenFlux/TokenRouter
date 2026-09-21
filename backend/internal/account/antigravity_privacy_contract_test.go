//go:build unit

package account_test

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func applyAntigravitySubscriptionResult(account *accountcore.Record, result accountcore.AntigravitySubscriptionResult) (map[string]any, map[string]any) {
	credentials := make(map[string]any)
	for k, v := range account.Credentials {
		credentials[k] = v
	}
	credentials["plan_type"] = result.PlanType

	extra := make(map[string]any)
	for k, v := range account.Extra {
		extra[k] = v
	}
	if result.SubscriptionStatus != "" {
		extra["subscription_status"] = result.SubscriptionStatus
	} else {
		delete(extra, "subscription_status")
	}
	if result.SubscriptionError != "" {
		extra["subscription_error"] = result.SubscriptionError
	} else {
		delete(extra, "subscription_error")
	}
	return credentials, extra
}

func TestApplyAntigravityPrivacyMode_SetsInMemoryExtra(t *testing.T) {
	account := &accountcore.Record{}

	accountcore.ApplyAntigravityPrivacyMode(account, accountcore.AntigravityPrivacySet)

	if account.Extra == nil {
		t.Fatal("expected account.Extra to be initialized")
	}
	if got := account.Extra["privacy_mode"]; got != accountcore.AntigravityPrivacySet {
		t.Fatalf("expected privacy_mode %q, got %v", accountcore.AntigravityPrivacySet, got)
	}
}

func TestApplyAntigravityPrivacyMode_PreservedBySubscriptionResult(t *testing.T) {
	account := &accountcore.Record{
		Credentials: map[string]any{
			"access_token": "token",
		},
		Extra: map[string]any{
			"existing": "value",
		},
	}
	accountcore.ApplyAntigravityPrivacyMode(account, accountcore.AntigravityPrivacySet)

	_, extra := applyAntigravitySubscriptionResult(account, accountcore.AntigravitySubscriptionResult{
		PlanType: "Pro",
	})

	if got := extra["privacy_mode"]; got != accountcore.AntigravityPrivacySet {
		t.Fatalf("expected subscription writeback to keep privacy_mode %q, got %v", accountcore.AntigravityPrivacySet, got)
	}
	if got := extra["existing"]; got != "value" {
		t.Fatalf("expected existing extra fields to be preserved, got %v", got)
	}
}
