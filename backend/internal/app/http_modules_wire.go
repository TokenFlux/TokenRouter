//go:build wireinject

package app

import (
	"github.com/google/wire"

	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	opshttp "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
)

// http_modules_wire.go registers constructors for billing, announcement, and
// Ops HTTP handlers with Wire. Announcement handlers consume
// AnnouncementService, and subscription and redeem handlers consume their
// billing use cases. These HTTP constructors provide concrete handler types to
// application route mounting, alongside the Ops handler constructed from
// OpsService for operational queries.

// nativeHTTPProviders supplies billing, announcement, and Ops HTTP handlers
// whose use cases are resolved by other application sets.
var nativeHTTPProviders = wire.NewSet(
	billinghttp.NewPlanHandler,
	billinghttp.NewRedeemHandler,
	billinghttp.NewSubscriptionHandler,
	billinghttp.NewAdminRedeemHandler,
	billinghttp.NewAdminSubscriptionHandler,
	sitehttp.NewAnnouncementHandler,
	sitehttp.NewAdminAnnouncementHandler,
	provideOpsWSOptions,
	opshttp.NewOpsHandler,
)
