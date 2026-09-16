package completion

import (
	"maps"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// Snapshot 在队列提交之前复制所有完成处理会读取的可变字段。
// 订阅只保留身份和套餐倍率，事务内资格仍由 billing 重新读取并复核。
func Snapshot(in *Input) *Input {
	if in == nil {
		return nil
	}
	out := *in
	out.Result = SnapshotResult(in.Result)
	out.APIKey = SnapshotKey(in.APIKey)
	if in.User != nil {
		v := *in.User
		v.Notification = cloneNotifyUser(in.User.Notification)
		out.User = &v
	}
	out.Account = SnapshotAccount(in.Account)
	if in.Subscription != nil {
		v := &billing.UserSubscription{ID: in.Subscription.ID}
		if p := in.Subscription.Plan; p != nil {
			v.Plan = &billing.SubscriptionPlan{GroupIDs: slices.Clone(p.GroupIDs), GroupRateMultipliers: maps.Clone(p.GroupRateMultipliers)}
		}
		out.Subscription = v
	}
	out.RequestedReasoningEffort = cloneString(in.RequestedReasoningEffort)
	return &out
}

// SnapshotResult 不保留 HTTP 或供应商对象引用。
func SnapshotResult(in *Result) *Result {
	if in == nil {
		return nil
	}
	out := *in
	out.UpstreamRequestID = cloneString(in.UpstreamRequestID)
	out.ServiceTier = cloneString(in.ServiceTier)
	out.ReasoningEffort = cloneString(in.ReasoningEffort)
	out.RequestedReasoningEffort = cloneString(in.RequestedReasoningEffort)
	out.FirstTokenMs = cloneInt(in.FirstTokenMs)
	out.ImageOutputSizes = slices.Clone(in.ImageOutputSizes)
	out.ImageSizeBreakdown = maps.Clone(in.ImageSizeBreakdown)
	if in.AudioUsage != nil {
		v := *in.AudioUsage
		out.AudioUsage = &v
	}
	return &out
}
func SnapshotAccount(in *AccountSnapshot) *AccountSnapshot {
	if in == nil {
		return nil
	}
	out := *in
	out.CredentialAccountID = cloneInt64(in.CredentialAccountID)
	if in.Notification != nil {
		v := *in.Notification
		v.Dimensions = slices.Clone(v.Dimensions)
		out.Notification = &v
	}
	return &out
}
func SnapshotKey(in *KeySnapshot) *KeySnapshot {
	if in == nil {
		return nil
	}
	out := *in
	out.GroupID = cloneInt64(in.GroupID)
	out.TeamID = cloneInt64(in.TeamID)
	out.PreferredSubscriptionID = cloneInt64(in.PreferredSubscriptionID)
	if in.Group != nil {
		g := *in.Group
		if g.Price != nil {
			p := *g.Price
			p.ModelPricing = slices.Clone(p.ModelPricing)
			for i := range p.ModelPricing {
				p.ModelPricing[i] = p.ModelPricing[i].Clone()
			}
			g.Price = &p
		}
		g.WebSearchPricePerCall = cloneFloat(g.WebSearchPricePerCall)
		g.SearchPricePer1k = cloneFloat(g.SearchPricePer1k)
		if g.AudioPrice != nil {
			a := *g.AudioPrice
			a.RealtimePerMin = cloneFloat(a.RealtimePerMin)
			a.TTSPerMChars = cloneFloat(a.TTSPerMChars)
			a.STTPerHour = cloneFloat(a.STTPerHour)
			g.AudioPrice = &a
		}
		out.Group = &g
	}
	return &out
}
func cloneNotifyUser(in *billing.UserSummary) *billing.UserSummary {
	if in == nil {
		return nil
	}
	return &billing.UserSummary{
		ID:                         in.ID,
		Email:                      in.Email,
		Username:                   in.Username,
		Balance:                    in.Balance,
		BalanceNotifyEnabled:       in.BalanceNotifyEnabled,
		BalanceNotifyThresholdType: in.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:     cloneFloat(in.BalanceNotifyThreshold),
		BalanceNotifyExtraEmails:   slices.Clone(in.BalanceNotifyExtraEmails),
		TotalRecharged:             in.TotalRecharged,
	}
}
func cloneString(v *string) *string {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
func cloneInt64(v *int64) *int64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
func cloneInt(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
func cloneFloat(v *float64) *float64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
