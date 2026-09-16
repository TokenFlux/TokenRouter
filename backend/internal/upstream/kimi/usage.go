// 固定供应商余额/周期协议保持原请求顺序和归一化，不写账号或调度。
package kimi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/upstream/internal/usageclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usagecontract"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
	"github.com/tidwall/gjson"
)

// ParseKimiUsageTiers 解析 Kimi For Coding 的 /usages 响应。
//
//   - limits[].detail.{limit,remaining,resetTime} → 5h 窗口（取首个 detail）
//   - usage.{limit,remaining,resetTime} → 周窗口
//
// utilization = (limit-remaining)/limit*100。
func ParseKimiUsageTiers(body []byte) []usageview.CNQuotaTier {
	var tiers []usageview.CNQuotaTier

	if limits := gjson.GetBytes(body, "limits"); limits.IsArray() {
		limits.ForEach(func(_, item gjson.Result) bool {
			detail := item.Get("detail")
			if !detail.Exists() {
				return true
			}
			limit, _ := usageclient.CnParseF64(detail.Get("limit").Value())
			remaining, _ := usageclient.CnParseF64(detail.Get("remaining").Value())
			used := limit - remaining
			if used < 0 {
				used = 0
			}
			var util float64
			if limit > 0 {
				util = used / limit * 100
			}
			tiers = append(tiers, usageview.CNQuotaTier{
				Window:      "5h",
				UsedPercent: util,
				ResetAt:     usageclient.CnNormalizeResetTime(detail.Get("resetTime").Value()),
			})
			return false // 取首个 detail 作为 5h 窗口
		})
	}

	if usage := gjson.GetBytes(body, "usage"); usage.Exists() {
		limit, _ := usageclient.CnParseF64(usage.Get("limit").Value())
		remaining, _ := usageclient.CnParseF64(usage.Get("remaining").Value())
		used := limit - remaining
		if used < 0 {
			used = 0
		}
		var util float64
		if limit > 0 {
			util = used / limit * 100
		}
		tiers = append(tiers, usageview.CNQuotaTier{
			Window:      "weekly",
			UsedPercent: util,
			ResetAt:     usageclient.CnNormalizeResetTime(usage.Get("resetTime").Value()),
		})
	}

	return tiers
}

type KimiCodingUsageAdapter struct{}

func (*KimiCodingUsageAdapter) Name() string { return usageview.UpstreamUsageAdapterKimiCoding }
func (*KimiCodingUsageAdapter) Query(ctx context.Context, input *usagecontract.Request) (*usageview.UpstreamUsageInfo, error) {
	client := usageclient.New(input)
	endpoint, err := usageclient.CnUsageEndpoint(client.BaseURL, "/v1/usages", true)
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
	tiers := ParseKimiUsageTiers(body)
	return usageclient.CnUsageLimits("kimi", tiers)
}

type KimiBalanceUsageAdapter struct{}

func (*KimiBalanceUsageAdapter) Name() string { return usageview.UpstreamUsageAdapterKimiBalance }
func (*KimiBalanceUsageAdapter) Query(ctx context.Context, input *usagecontract.Request) (*usageview.UpstreamUsageInfo, error) {
	client := usageclient.New(input)
	endpoint, err := usageclient.CnUsageEndpoint(client.BaseURL, "/v1/users/me/balance", false)
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
	if code := gjson.GetBytes(body, "code"); code.Exists() && code.Int() != 0 {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	value, ok := usageclient.CnParseF64(gjson.GetBytes(body, "data.available_balance").Value())
	if !ok || !usageview.ValidFiniteNumber(value) {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	return &usageview.UpstreamUsageInfo{
		Provider: "kimi",
		Mode:     "balance",
		Unit:     "CNY",
		Balance:  &usageview.UpstreamUsageAmount{Remaining: &value},
	}, nil
}
