//go:build unit

package pricingcontract

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	completiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

func newGatewayRecordUsageServiceWithBillingRepoForTest(logs usagecore.UsageLogRepository, funds completion.Store, _ identity.UserRepository, _ billing.UserSubscriptionRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, funds, nil, false)
}
