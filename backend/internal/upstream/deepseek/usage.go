// 固定供应商余额/周期协议保持原请求顺序和归一化，不写账号或调度。
package deepseek

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream/internal/usageclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usagecontract"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
	"github.com/tidwall/gjson"
)

type DeepseekBalanceUsageAdapter struct{}

func (*DeepseekBalanceUsageAdapter) Name() string {
	return usageview.UpstreamUsageAdapterDeepseekBalance
}
func (*DeepseekBalanceUsageAdapter) Query(ctx context.Context, input *usagecontract.Request) (*usageview.UpstreamUsageInfo, error) {
	client := usageclient.New(input)
	endpoint, err := usageclient.CnUsageEndpoint(client.BaseURL, "/user/balance", false)
	if err != nil {
		return nil, usageview.ErrUpstreamUsageConfigInvalid.WithCause(err)
	}
	body, status, err := client.GetURL(ctx, endpoint, true)
	if err != nil {
		return nil, err
	}
	if err := usageclient.ValidateCNUsageStatus(status); err != nil {
		return nil, err
	}
	var balances []usageview.UpstreamUsageBalanceEntry
	available := true
	if raw := gjson.GetBytes(body, "is_available"); raw.Exists() {
		available = raw.Bool()
	}
	balanceInfos := gjson.GetBytes(body, "balance_infos")
	if !balanceInfos.Exists() || !balanceInfos.IsArray() {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	balanceInfos.ForEach(func(_, item gjson.Result) bool {
		currency := strings.ToUpper(strings.TrimSpace(item.Get("currency").String()))
		totalBalance := item.Get("total_balance")
		value, ok := usageclient.CnParseF64(totalBalance.Value())
		if !totalBalance.Exists() || !ok || !usageview.ValidFiniteNumber(value) {
			return true
		}
		if currency == "" {
			currency = "CNY"
		}
		balances = append(balances, usageview.UpstreamUsageBalanceEntry{Currency: currency, Remaining: value})
		return true
	})
	if len(balances) == 0 {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	primary := balances[0].Remaining
	return &usageview.UpstreamUsageInfo{
		Provider:  "deepseek",
		Mode:      "balance",
		Unit:      balances[0].Currency,
		Balance:   &usageview.UpstreamUsageAmount{Remaining: &primary},
		Balances:  balances,
		Available: &available,
	}, nil
}
