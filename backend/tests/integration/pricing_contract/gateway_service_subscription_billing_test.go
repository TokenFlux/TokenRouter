//go:build unit

package pricingcontract

import (
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// TestBuildUsageBillingCommand_BillableAmountTracksActualCost locks in the fix
// that usage billing always uses ActualCost as the user-facing billable amount.
func TestBuildUsageBillingCommand_BillableAmountTracksActualCost(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	subID := int64(42)

	tests := []struct {
		name         string
		totalCost    float64
		actualCost   float64
		wantBillable float64
	}{
		{
			name:         "subscription with 2x multiplier consumes 2x quota",
			totalCost:    1.0,
			actualCost:   2.0,
			wantBillable: 2.0,
		},
		{
			name:         "subscription with 0.5x multiplier consumes 0.5x quota",
			totalCost:    1.0,
			actualCost:   0.5,
			wantBillable: 0.5,
		},
		{
			name:         "free subscription (multiplier 0) consumes no quota",
			totalCost:    1.0,
			actualCost:   0,
			wantBillable: 0,
		},
		{
			name:         "balance billing keeps using ActualCost (regression)",
			totalCost:    1.0,
			actualCost:   2.0,
			wantBillable: 2.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &contractSettlementInput{
				Cost:         &pricing.CostBreakdown{TotalCost: tt.totalCost, ActualCost: tt.actualCost},
				User:         &identity.User{ID: 1},
				APIKey:       &apikey.APIKey{ID: 2, GroupID: &groupID},
				Account:      &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3}},
				Subscription: &billing.UserSubscription{ID: subID},
			}

			cmd := buildContractBillingCommand("req-1", nil, p)
			if cmd == nil {
				t.Fatal("buildContractBillingCommand returned nil")
			}
			if cmd.BillableAmountUSD != tt.wantBillable {
				t.Errorf("BillableAmountUSD = %v, want %v", cmd.BillableAmountUSD, tt.wantBillable)
			}
		})
	}
}

func TestBuildUsageBillingCommand_AccountQuotaUsesAccountStatsCost(t *testing.T) {
	t.Parallel()

	customCost := 2.0
	zeroCost := 0.0
	tests := []struct {
		name                  string
		accountStatsCost      *float64
		totalCost             float64
		actualCost            float64
		accountRateMultiplier float64
		wantAccountQuota      float64
	}{
		{
			name:                  "自定义账号成本乘账号倍率",
			accountStatsCost:      &customCost,
			totalCost:             5,
			actualCost:            7,
			accountRateMultiplier: 1.5,
			wantAccountQuota:      3,
		},
		{
			name:                  "空账号成本回退总成本",
			totalCost:             4,
			actualCost:            1.25,
			accountRateMultiplier: 2,
			wantAccountQuota:      8,
		},
		{
			name:                  "显式零账号成本不累计额度",
			accountStatsCost:      &zeroCost,
			totalCost:             4,
			actualCost:            1.25,
			accountRateMultiplier: 3,
			wantAccountQuota:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			usageLog := &usage.UsageLog{AccountStatsCost: tt.accountStatsCost}
			p := &contractSettlementInput{
				Cost: &pricing.CostBreakdown{
					TotalCost:  tt.totalCost,
					ActualCost: tt.actualCost,
				},
				User:                  &identity.User{ID: 1},
				APIKey:                &apikey.APIKey{ID: 2},
				Account:               &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3, Type: capability.AccountTypeAPIKey, Extra: map[string]any{"quota_limit": 100}}},
				AccountRateMultiplier: tt.accountRateMultiplier,
			}

			cmd := buildContractBillingCommand("req-account-quota", usageLog, p)

			if cmd == nil {
				t.Fatal("buildContractBillingCommand returned nil")
			}
			if cmd.AccountQuotaCost != tt.wantAccountQuota {
				t.Errorf("AccountQuotaCost = %v, want %v", cmd.AccountQuotaCost, tt.wantAccountQuota)
			}
			// 用户余额、订阅和 API Key 配额仍必须使用 ActualCost，不能被账号成本口径影响。
			if cmd.BillableAmountUSD != tt.actualCost {
				t.Errorf("BillableAmountUSD = %v, want %v", cmd.BillableAmountUSD, tt.actualCost)
			}
		})
	}
}

func TestBuildUsageBillingCommand_IncludesRequestGroupID(t *testing.T) {
	groupID := int64(42)
	p := &contractSettlementInput{
		Cost: &pricing.CostBreakdown{ActualCost: 1.25},
		User: &identity.User{ID: 10},
		APIKey: &apikey.APIKey{
			ID:      20,
			GroupID: &groupID,
		},
		Account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 30, Type: capability.AccountTypeAPIKey}},
	}

	cmd := buildContractBillingCommand("req-group", nil, p)

	if cmd == nil {
		t.Fatal("buildContractBillingCommand returned nil")
	}
	if cmd.GroupID == nil {
		t.Fatal("GroupID is nil")
	}
	if *cmd.GroupID != groupID {
		t.Fatalf("GroupID = %d, want %d", *cmd.GroupID, groupID)
	}
}

func TestBuildUsageBillingCommand_NonTokenModesKeepAllocationRates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mode       routing.BillingMode
		totalCost  float64
		actualCost float64
		wantRate   float64
	}{
		{name: "image rate", mode: routing.BillingModeImage, totalCost: 0.2, actualCost: 0.2, wantRate: 0.15},
		{name: "video rate", mode: routing.BillingModeVideo, totalCost: 0.08, actualCost: 0.02, wantRate: 0.15},
		{name: "per request rate", mode: routing.BillingModePerRequest, totalCost: 0.4, actualCost: 0.1, wantRate: 0.15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &contractSettlementInput{
				Cost: &pricing.CostBreakdown{
					TotalCost:   tt.totalCost,
					ActualCost:  tt.actualCost,
					BillingMode: string(tt.mode),
				},
				User:                            &identity.User{ID: 1},
				APIKey:                          &apikey.APIKey{ID: 2},
				Account:                         &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3}},
				SubscriptionRateMultiplier:      0.15,
				SubscriptionRateMultiplierScale: 1,
				BalanceRateMultiplier:           2,
			}

			cmd := buildContractBillingCommand("req-non-token", nil, p)

			if cmd == nil {
				t.Fatal("buildContractBillingCommand returned nil")
			}
			if cmd.SubscriptionRateMultiplier != tt.wantRate {
				t.Errorf("SubscriptionRateMultiplier = %v, want %v", cmd.SubscriptionRateMultiplier, tt.wantRate)
			}
			if cmd.SubscriptionRateMultiplierScale != 1 {
				t.Errorf("SubscriptionRateMultiplierScale = %v, want 1", cmd.SubscriptionRateMultiplierScale)
			}
			if cmd.BalanceRateMultiplier != 2 {
				t.Errorf("BalanceRateMultiplier = %v, want %v", cmd.BalanceRateMultiplier, 2.0)
			}
		})
	}
}

func TestBuildUsageBillingCommand_TokenModeKeepsAllocationRates(t *testing.T) {
	t.Parallel()

	p := &contractSettlementInput{
		Cost: &pricing.CostBreakdown{
			TotalCost:   1,
			ActualCost:  0.5,
			BillingMode: string(routing.BillingModeToken),
		},
		User:                            &identity.User{ID: 1},
		APIKey:                          &apikey.APIKey{ID: 2},
		Account:                         &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3}},
		SubscriptionRateMultiplier:      0.8,
		SubscriptionRateMultiplierScale: 1.5,
		BalanceRateMultiplier:           0.3,
	}

	cmd := buildContractBillingCommand("req-token", nil, p)

	if cmd == nil {
		t.Fatal("buildContractBillingCommand returned nil")
	}
	if cmd.SubscriptionRateMultiplier != 0.8 {
		t.Errorf("SubscriptionRateMultiplier = %v, want 0.8", cmd.SubscriptionRateMultiplier)
	}
	if cmd.SubscriptionRateMultiplierScale != 1.5 {
		t.Errorf("SubscriptionRateMultiplierScale = %v, want 1.5", cmd.SubscriptionRateMultiplierScale)
	}
	if cmd.BalanceRateMultiplier != 0.3 {
		t.Errorf("BalanceRateMultiplier = %v, want 0.3", cmd.BalanceRateMultiplier)
	}
}

// TestBuildUsageBillingCommand_UsesOverrideBaseAmountForFreeFast 验证免费 Fast
// 可以替换用户资金分配的基础价，同时保留账号统计成本对应的额度口径。
func TestBuildUsageBillingCommand_UsesOverrideBaseAmountForFreeFast(t *testing.T) {
	standardBase := 0.4
	fastTotal := 1.2
	standardActual := 0.2
	accountStatsCost := fastTotal
	groupID := int64(88)
	accountRate := 1.5

	cmd := buildContractBillingCommand("req-free-fast-base", &usage.UsageLog{AccountStatsCost: &accountStatsCost}, &contractSettlementInput{
		Cost: &pricing.CostBreakdown{
			TotalCost:  fastTotal,
			ActualCost: standardActual,
		},
		BillingBaseAmountUSD:  &standardBase,
		User:                  &identity.User{ID: 1},
		APIKey:                &apikey.APIKey{ID: 2, GroupID: &groupID},
		Account:               &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3, Type: capability.AccountTypeAPIKey, Extra: map[string]any{"quota_limit": 100}}},
		AccountRateMultiplier: accountRate,
	})

	if cmd == nil {
		t.Fatal("buildContractBillingCommand returned nil")
	}
	if cmd.BaseAmountUSD != standardBase {
		t.Fatalf("BaseAmountUSD = %v, want %v", cmd.BaseAmountUSD, standardBase)
	}
	if cmd.BillableAmountUSD != standardActual {
		t.Fatalf("BillableAmountUSD = %v, want %v", cmd.BillableAmountUSD, standardActual)
	}
	if diff := cmd.AccountQuotaCost - fastTotal*accountRate; diff > 1e-12 || diff < -1e-12 {
		t.Fatalf("AccountQuotaCost = %v, want %v", cmd.AccountQuotaCost, fastTotal*accountRate)
	}
}
