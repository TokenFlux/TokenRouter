package completion

import (
	"maps"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// snapshot.go copies mutable completion inputs before asynchronous processing.
// A queued completion can outlive the mutable result, key, and provider records
// used by the request. Capture therefore separates pointer values and
// collection storage before workers normalize usage or resolve settlement
// settings. The subscription snapshot intentionally keeps its ID and plan group
// rates, because the funds transaction reloads eligibility and allocations from
// persistent state.
//
// SnapshotResult separates usage metadata, and SnapshotKey copies mutable price
// pointers while retaining the immutable time.Location reference. Notification
// summaries copy the slices and thresholds used by post-settlement checks. These
// helpers read caller-owned data synchronously; callers serialize capture with
// writers and submit the returned object after capture is complete.

// Snapshot captures the data completion can normalize after the request returns.
// Nil input returns nil. Callers serialize reads with mutations of the supplied
// objects, then pass the returned snapshot to the worker.
//
// Result, key, payer, and provider values use their dedicated copy helpers. A
// subscription retains only its ID and plan group membership/rates, allowing
// multiplier selection from captured data while billing reloads actual eligibility
// inside its transaction. The key's time.Location remains shared and is treated
// as immutable; mutable collections copied here have separate storage.
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
	out.Provider = SnapshotProvider(in.Provider)
	// Keep the pricing evidence available to the worker while leaving live status,
	// windows, and funding allocation to the transaction that reloads the subscription.
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

// SnapshotResult copies observed usage and separates its mutable metadata.
// Nil returns nil. Pointer scalars, output-size slices, size-breakdown maps, and
// audio units receive new storage so worker normalization cannot alter the source.
func SnapshotResult(in *Result) *Result {
	if in == nil {
		return nil
	}
	out := *in
	if in.Usage.CacheCreation30mTokens != nil {
		out.Usage.CacheCreation30mTokens = new(*in.Usage.CacheCreation30mTokens)
	}
	if in.Usage.CacheCreation12hTokens != nil {
		out.Usage.CacheCreation12hTokens = new(*in.Usage.CacheCreation12hTokens)
	}
	out.UpstreamRequestID = cloneString(in.UpstreamRequestID)
	out.ServiceTier = cloneString(in.ServiceTier)
	out.ReasoningEffort = cloneString(in.ReasoningEffort)
	out.RequestedReasoningEffort = cloneString(in.RequestedReasoningEffort)
	out.Timings = in.Timings.Clone()
	out.ImageOutputSizes = slices.Clone(in.ImageOutputSizes)
	out.ImageSizeBreakdown = maps.Clone(in.ImageSizeBreakdown)
	if in.AudioUsage != nil {
		v := *in.AudioUsage
		out.AudioUsage = &v
	}
	return &out
}

// SnapshotProvider separates provider quota-notification and credential-owner
// metadata for completion. Nil returns nil; the parent ID and notification's
// Dimensions slice receive independent storage while scalar observations are copied.
func SnapshotProvider(in *ProviderSnapshot) *ProviderSnapshot {
	if in == nil {
		return nil
	}
	out := *in
	out.CredentialProviderID = cloneInt64(in.CredentialProviderID)
	if in.Notification != nil {
		v := *in.Notification
		v.Dimensions = slices.Clone(v.Dimensions)
		out.Notification = &v
	}
	return &out
}

// SnapshotKey copies key identity and mutable group pricing values for completion.
// Nil returns nil. Group, optional IDs, search prices, and audio prices are copied;
// the group's time.Location remains shared as immutable timezone data.
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

// cloneNotifyUser captures the payer fields used by balance notification checks.
// Nil returns nil. The threshold pointer and extra-email slice receive separate
// storage so later request-side changes cannot alter the captured notification inputs.
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

// cloneString copies an optional string value into separate pointer storage.
// Nil input remains nil, allowing callers to retain the distinction between an
// absent observation and a present zero value during asynchronous capture.
func cloneString(v *string) *string {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// cloneInt64 copies an optional identifier value into separate pointer storage.
// Nil input remains nil, allowing callers to retain the distinction between an
// absent observation and a present zero value during asynchronous capture.
func cloneInt64(v *int64) *int64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// cloneFloat copies an optional numeric value into separate pointer storage.
// Nil input remains nil, allowing callers to retain the distinction between an
// absent observation and a present zero value during asynchronous capture.
func cloneFloat(v *float64) *float64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
