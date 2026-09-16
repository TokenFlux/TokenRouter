// 固定供应商余额/周期协议保持原请求顺序和归一化，不写账号或调度。
package zhipu

import (
	"context"
	"net/url"
	"sort"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream/internal/usageclient" // CnZhipuWindow 标识智谱 TOKENS_LIMIT 条目所属窗口。
	"github.com/TokenFlux/TokenRouter/internal/upstream/usagecontract"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
	"github.com/tidwall/gjson"
)

type CnZhipuWindow int

const (
	CnZhipuWindowUnknown CnZhipuWindow = iota
	CnZhipuWindow5h
	CnZhipuWindowWeekly
)

// ClassifyZhipuWindowUnit 按 unit 字段判定窗口类型（3=5h，6=weekly）。
// unit 缺失或未识别时返回 Unknown，由调用方走 reset 时间启发式兜底。
func ClassifyZhipuWindowUnit(unit int64) CnZhipuWindow {
	switch unit {
	case 3:
		return CnZhipuWindow5h
	case 6:
		return CnZhipuWindowWeekly
	default:
		return CnZhipuWindowUnknown
	}
}

// ParseZhipuTokenTiers 解析智谱额度响应 data.limits 为 5h + weekly 两档。
//
// 分类优先级（对齐 cc-switch parse_zhipu_token_tiers，issue #3036）：
//  1. 显式 unit 字段（3=5h / 6=weekly）——不能用 reset 排序代替，周期末尾
//     周窗口会比 5h 更早重置，时间排序必然标反。
//  2. unit 缺失/未识别：无 nextResetTime 的条目优先归 5h（0% 状态下 5h 桶可能
//     没有 reset），其余按 reset 升序依次填入仍空缺的槽位。
//
// CREDIT_LIMIT（信用额度）与 TOKENS_LIMIT（token 窗口）度量不同：两者同时返回时
// 只让 TOKENS_LIMIT 参与 5h/weekly 槽位竞争，避免信用额度百分比污染阈值停调
// 快照；仅当无任何 TOKENS_LIMIT 条目时才降级用 CREDIT_LIMIT 展示。
// 老套餐只回 1 条 TOKENS_LIMIT，自然降级为仅 5h；新套餐回 2 条。
func ParseZhipuTokenTiers(data gjson.Result) []usageview.CNQuotaTier {
	type entry struct {
		resetMs    int64
		hasReset   bool
		percentage float64
		resetISO   string
	}
	var (
		fiveHour     entry
		fiveHourSet  bool
		weekly       entry
		weeklySet    bool
		unclassified []entry
	)

	classify := func(item gjson.Result, e entry) {
		switch ClassifyZhipuWindowUnit(item.Get("unit").Int()) {
		case CnZhipuWindow5h:
			if !fiveHourSet {
				fiveHour, fiveHourSet = e, true
			} else {
				unclassified = append(unclassified, e)
			}
		case CnZhipuWindowWeekly:
			if !weeklySet {
				weekly, weeklySet = e, true
			} else {
				unclassified = append(unclassified, e)
			}
		default:
			unclassified = append(unclassified, e)
		}
	}
	var creditFallback []entry
	hasTokensLimit := false

	data.Get("limits").ForEach(func(_, item gjson.Result) bool {
		limitType := strings.ToUpper(strings.TrimSpace(item.Get("type").String()))
		if limitType != "TOKENS_LIMIT" && limitType != "CREDIT_LIMIT" {
			return true
		}
		percentage := 0.0
		if p, ok := usageclient.CnParseF64(item.Get("percentage").Value()); ok {
			percentage = p
		}
		var (
			resetMs  int64
			hasReset bool
			resetISO string
		)
		if nr := item.Get("nextResetTime"); nr.Exists() {
			switch nr.Type {
			case gjson.Number:
				resetMs = nr.Int()
				hasReset = resetMs > 0
				resetISO = usageclient.CnMillisToRFC3339(resetMs)
			case gjson.String:
				resetISO = usageclient.CnNormalizeResetTime(nr.String())
				hasReset = resetISO != ""
			}
		}
		e := entry{resetMs: resetMs, hasReset: hasReset, percentage: percentage, resetISO: resetISO}
		if limitType == "TOKENS_LIMIT" {
			hasTokensLimit = true
			classify(item, e)
		} else {
			creditFallback = append(creditFallback, e)
		}
		return true
	})

	// 无任何 TOKENS_LIMIT 条目（部分套餐只报信用额度）：降级用 CREDIT_LIMIT 展示。
	if !hasTokensLimit {
		unclassified = append(unclassified, creditFallback...)
	}

	// 无 reset 的条目排前，再按 reset 升序，依次填入仍空缺的槽位。
	sort.SliceStable(unclassified, func(i, j int) bool {
		if unclassified[i].hasReset != unclassified[j].hasReset {
			return !unclassified[i].hasReset
		}
		return unclassified[i].resetMs < unclassified[j].resetMs
	})
	for _, e := range unclassified {
		switch {
		case !fiveHourSet:
			fiveHour, fiveHourSet = e, true
		case !weeklySet:
			weekly, weeklySet = e, true
		}
	}

	var tiers []usageview.CNQuotaTier
	if fiveHourSet {
		tiers = append(tiers, usageview.CNQuotaTier{Window: "5h", UsedPercent: fiveHour.percentage, ResetAt: fiveHour.resetISO})
	}
	if weeklySet {
		tiers = append(tiers, usageview.CNQuotaTier{Window: "weekly", UsedPercent: weekly.percentage, ResetAt: weekly.resetISO})
	}
	return tiers
}

type ZhipuCodingUsageAdapter struct{}

func (*ZhipuCodingUsageAdapter) Name() string { return usageview.UpstreamUsageAdapterZhipuCoding }
func (*ZhipuCodingUsageAdapter) Query(ctx context.Context, input *usagecontract.Request) (*usageview.UpstreamUsageInfo, error) {
	client := usageclient.New(input)
	endpoint, err := usageclient.CnUsageEndpoint(client.BaseURL, "/api/monitor/usage/quota/limit", false)
	if err != nil {
		return nil, usageview.ErrUpstreamUsageConfigInvalid.WithCause(err)
	}
	// 智谱 Coding Plan 额度接口使用裸 API key，不是 Bearer token。
	teamHeaders := map[string]string{"Authorization": client.APIKey}
	organization := strings.TrimSpace(client.ZhipuOrganization)
	if organization != "" {
		if strings.ContainsAny(organization, "\r\n") {
			return nil, usageview.ErrUpstreamUsageConfigInvalid
		}
		// 团队版接口通过 type=2 和组织请求头区分个人 Coding Plan。
		parsed, parseErr := url.Parse(endpoint)
		if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, usageview.ErrUpstreamUsageConfigInvalid
		}
		query := parsed.Query()
		query.Set("type", "2")
		parsed.RawQuery = query.Encode()
		endpoint = parsed.String()
		teamHeaders["bigmodel-organization"] = organization
		if project := strings.TrimSpace(client.ZhipuProject); project != "" {
			if strings.ContainsAny(project, "\r\n") {
				return nil, usageview.ErrUpstreamUsageConfigInvalid
			}
			teamHeaders["bigmodel-project"] = project
		}
	}
	body, status, err := client.GetURLWithHeaders(ctx, endpoint, teamHeaders)
	if err != nil {
		return nil, err
	}
	if err := usageclient.ValidateCNUsageStatus(status); err != nil {
		return nil, err
	}
	if success := gjson.GetBytes(body, "success"); success.Exists() && !success.Bool() {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	tiers := ParseZhipuTokenTiers(gjson.GetBytes(body, "data"))
	return usageclient.CnUsageLimits("zhipu", tiers)
}
