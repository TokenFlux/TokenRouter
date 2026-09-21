package service

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
)

func usageSubscriptionResolverFrom(repo completion.Store) completion.SubscriptionReader {
	resolver, _ := repo.(completion.SubscriptionReader)
	return resolver
}
