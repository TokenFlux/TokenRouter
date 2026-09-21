//go:build unit

package provider

import (
	"net/http"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

func TestGrokMediaGenerationEligibility(t *testing.T) {
	weeklyUsagePercent := 12.5
	forbiddenBilling := &xai.BillingSummary{
		StatusCode:        http.StatusForbidden,
		WeeklyStatusCode:  http.StatusForbidden,
		MonthlyStatusCode: http.StatusForbidden,
	}
	weeklyAllowance := &xai.BillingSummary{
		PeriodType:       "weekly",
		UsagePercent:     &weeklyUsagePercent,
		StatusCode:       http.StatusOK,
		WeeklyStatusCode: http.StatusOK,
	}
	freeBilling := &xai.BillingSummary{
		PeriodType:        "monthly",
		StatusCode:        http.StatusOK,
		WeeklyStatusCode:  http.StatusOK,
		MonthlyStatusCode: http.StatusOK,
		MonthlyUpdatedAt:  "2026-07-17T00:00:00Z",
	}
	inconclusiveBilling := &xai.BillingSummary{
		StatusCode:        http.StatusOK,
		WeeklyStatusCode:  http.StatusOK,
		MonthlyStatusCode: http.StatusBadGateway,
		Partial:           true,
		FailedWindows:     []string{"monthly"},
	}
	weeklyForbidden := &xai.BillingSummary{
		StatusCode:        http.StatusOK,
		WeeklyStatusCode:  http.StatusForbidden,
		MonthlyStatusCode: http.StatusOK,
	}
	monthlyForbidden := &xai.BillingSummary{
		StatusCode:        http.StatusOK,
		WeeklyStatusCode:  http.StatusOK,
		MonthlyStatusCode: http.StatusForbidden,
	}

	tests := []struct {
		name       string
		account    *accountcore.Record
		want       bool
		wantReason string
	}{
		{name: "nil account", account: nil, want: false, wantReason: "not_grok"},
		{name: "non grok account", account: &accountcore.Record{Platform: capability.PlatformOpenAI}, want: false, wantReason: "not_grok"},
		{name: "non oauth grok account stays eligible", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey}, want: true, wantReason: "non_oauth"},
		{name: "unobserved oauth fails closed", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}, want: false, wantReason: "billing_unobserved"},
		{name: "weekly paid usage is eligible without inferring from period type", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Extra: map[string]any{accountcore.GrokUsageBillingExtraKey: weeklyAllowance}}, want: true, wantReason: "eligible"},
		{name: "observed free account is rejected", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Extra: map[string]any{accountcore.GrokUsageBillingExtraKey: freeBilling}}, want: false, wantReason: "billing_free_tier"},
		{name: "inconclusive billing fails closed", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Extra: map[string]any{accountcore.GrokUsageBillingExtraKey: inconclusiveBilling}}, want: false, wantReason: "billing_inconclusive"},
		{name: "billing forbidden is rejected", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Extra: map[string]any{accountcore.GrokUsageBillingExtraKey: forbiddenBilling}}, want: false, wantReason: "billing_forbidden"},
		{name: "weekly billing forbidden is rejected after partial success", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Extra: map[string]any{accountcore.GrokUsageBillingExtraKey: weeklyForbidden}}, want: false, wantReason: "billing_forbidden"},
		{name: "monthly billing forbidden is rejected after partial success", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Extra: map[string]any{accountcore.GrokUsageBillingExtraKey: monthlyForbidden}}, want: false, wantReason: "billing_forbidden"},
		{name: "malformed billing observation fails closed", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Extra: map[string]any{accountcore.GrokUsageBillingExtraKey: make(chan int)}}, want: false, wantReason: "billing_unobserved"},
		{name: "malformed override falls back to observations", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Extra: map[string]any{accountcore.GrokMediaEligibleExtraKey: "false", accountcore.GrokUsageBillingExtraKey: weeklyAllowance}}, want: true, wantReason: "eligible"},
		{name: "explicit disable wins", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Extra: map[string]any{accountcore.GrokMediaEligibleExtraKey: false}}, want: false, wantReason: "override_disabled"},
		{name: "explicit enable wins over forbidden probe", account: &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Extra: map[string]any{accountcore.GrokMediaEligibleExtraKey: true, accountcore.GrokUsageBillingExtraKey: forbiddenBilling}}, want: true, wantReason: "override_enabled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := accountcore.GrokMediaGenerationEligibility(tt.account, GrokTierRules())
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.wantReason, reason)
		})
	}
}
