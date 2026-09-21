//go:build wireinject

package app

import (
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	opshttp "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	"github.com/google/wire"
)

// nativeHTTPProviders 只组合原生 HTTP 构造器，各用例保持原唯一实例。
var nativeHTTPProviders = wire.NewSet(
	billinghttp.NewPlanHandler,
	billinghttp.NewRedeemHandler,
	billinghttp.NewSubscriptionHandler,
	billinghttp.NewAdminRedeemHandler,
	billinghttp.NewAdminSubscriptionHandler,
	sitehttp.NewAnnouncementHandler,
	sitehttp.NewAdminAnnouncementHandler,
	opshttp.NewOpsHandler,
)
