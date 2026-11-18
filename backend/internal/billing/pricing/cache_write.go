package pricing

// cache_write.go selects optional GPT cache write rates and validates usage
// subdivisions for both user and provider-cost calculation. A missing rate
// inherits the selected aggregate amount. An explicit zero remains separate
// from absence at each level of the pricing card.
//
// The incoming counter already contributes to the context threshold as a total.
// When its detail is complete, calculation divides that total between rates
// without adding more input tokens. Price overrides are applied before display
// so a configured uniform rate and a zero lifetime rate agree in both consumers.

// openAIWriteBreakdown returns the two reported write buckets when they
// partition the aggregate. Incomplete, negative, or contradictory usage
// remains eligible for the aggregate rate, without adding inferred tokens.
func openAIWriteBreakdown(tokens UsageTokens) (int, int, bool) {
	if tokens.CacheCreation30mTokens == nil || tokens.CacheCreation12hTokens == nil {
		return 0, 0, false
	}
	short, long := *tokens.CacheCreation30mTokens, *tokens.CacheCreation12hTokens
	return short, long, short >= 0 && long >= 0 && short <= tokens.CacheCreationTokens && long == tokens.CacheCreationTokens-short
}

// cacheWriteRate selects an explicit optional amount or its aggregate fallback.
func cacheWriteRate(rate *float64, fallback float64) float64 {
	if rate == nil {
		return fallback
	}
	return *rate
}

// applyCacheWriteTTLPrices overrides supplied durations at one pricing level.
// A same-level uniform override replaces inherited split amounts. Missing
// bucket pointers otherwise retain the enclosing level's selected rates and
// existing nil values retain their aggregate fallback behavior.
func applyCacheWriteTTLPrices(price *ModelPricing, aggregate, short, long *float64) {
	if aggregate != nil && price.CacheCreation30mPrice != nil {
		price.CacheCreation30mPrice = copyOptional(aggregate)
	}
	if aggregate != nil && price.CacheCreation12hPrice != nil {
		price.CacheCreation12hPrice = copyOptional(aggregate)
	}
	if short != nil {
		price.CacheCreation30mPrice = copyOptional(short)
	}
	if long != nil {
		price.CacheCreation12hPrice = copyOptional(long)
	}
}

// displayOpenAIWriteRates includes effective amounts only for prices that
// expose the GPT split. An absent sibling uses the same uniform fallback as
// settlement, and both pointers preserve an effective zero for presentation.
func displayOpenAIWriteRates(pricing *ModelPricing, multiplier float64) (*float64, *float64) {
	if pricing.CacheCreation30mPrice == nil && pricing.CacheCreation12hPrice == nil {
		return nil, nil
	}
	return new(cacheWriteRate(pricing.CacheCreation30mPrice, pricing.CacheCreationPricePerToken) * multiplier),
		new(cacheWriteRate(pricing.CacheCreation12hPrice, pricing.CacheCreationPricePerToken) * multiplier)
}
