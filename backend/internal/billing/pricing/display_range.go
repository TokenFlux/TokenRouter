package pricing

// ResolvedTokenPriceRange 是正上下文范围的实际价格；nil 表示该范围缺价。
type ResolvedTokenPriceRange struct {
	minTokens int
	maxTokens *int
	pricing   *ModelPricing
}
