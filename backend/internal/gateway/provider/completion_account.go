package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
)

func ProjectCompletionAccount(v *account.Record) *completion.AccountSnapshot {
	if v == nil {
		return nil
	}
	out := &completion.AccountSnapshot{
		ID:                         v.ID,
		CacheTTLOverrideEnabled:    v.IsCacheTTLOverrideEnabled(),
		CacheTTLOverrideTarget:     v.GetCacheTTLOverrideTarget(),
		AnthropicOAuthOrSetupToken: v.IsAnthropicOAuthOrSetupToken(),
		Type:                       v.Type,
		Platform:                   v.Platform,
		RateMultiplier:             v.BillingRateMultiplier(),
		OpenAI:                     v.IsOpenAI(),
		CNProvider:                 v.IsCNProvider(),
		OAuthLike:                  v.IsOpenAIOAuthLike(),
		QuotaEligible:              v.IsAPIKeyOrBedrock(),
		HasQuotaLimit:              v.HasAnyQuotaLimit(),
		CredentialAccountID:        v.ParentAccountID,
		Notification:               accountprovider.QuotaNotification(&account.Record{ID: v.ID, Name: v.Name, Platform: v.Platform, Type: v.Type, Extra: v.Extra}),
	}
	return completion.SnapshotAccount(out)
}
