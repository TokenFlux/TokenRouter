//go:build unit

package pricingcontract

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	completiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
)

func newOpenAIRecordUsageServiceWithBillingRepoForTest(logs usagecore.UsageLogRepository, funds completion.Store, _ identity.UserRepository, _ billing.UserSubscriptionRepository, rates billing.UserGroupRateRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, funds, rates, false)
}
