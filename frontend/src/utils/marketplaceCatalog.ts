import type {
  MarketplaceGroup,
  MarketplaceModel,
  MarketplaceModelPricing,
} from '@/api/marketplace.types'
import { pricingKind, videoPricingEntries } from '@/utils/marketplacePricing'
import { providerBrandDisplayName } from '@/utils/providerBrand'

// marketplaceCatalog.ts derives display order, brand identity, and a model-level search
// index from marketplace group responses. The marketplace page and the global search
// palette share these rules so both list groups in the same order and label brands the
// same way.
//
// The marketplace API nests models inside routing groups, and one model can be offered
// by several groups at different group-adjusted prices. Search needs one row per model,
// so buildModelIndex merges a model's appearances into a single entry: names and search
// terms are unioned, brands are collected in group order, and the price summary shows
// the cheapest quote of the first priced billing kind. When the other groups quote a
// different amount, the summary records that the price varies so the row can say
// "from" instead of implying a single price.

// groupBrandSource selects the group's source text for brand labels and artwork.
// A nonblank display_brand wins after trimming; otherwise the group name is
// returned as supplied, including its surrounding whitespace.
export function groupBrandSource(group: Pick<MarketplaceGroup, 'display_brand' | 'name'>): string {
  return group.display_brand?.trim() || group.name
}

// compareMarketplaceGroups orders exclusive groups before public groups, then follows
// the server's sort_order and finally the group ID, matching the marketplace display.
export function compareMarketplaceGroups(left: MarketplaceGroup, right: MarketplaceGroup): number {
  const exclusiveDiff = Number(Boolean(right.is_exclusive)) - Number(Boolean(left.is_exclusive))
  if (exclusiveDiff !== 0) {
    return exclusiveDiff
  }
  const sortDiff = (left.sort_order ?? 0) - (right.sort_order ?? 0)
  if (sortDiff !== 0) {
    return sortDiff
  }
  return left.id - right.id
}

// ModelPriceSummary condenses a model's cheapest public quote for one-line display.
// Amounts are group-adjusted balance units: token prices per token, image prices per
// image, and video prices per second. A token side is null when no group quotes it.
// varies reports that another group quotes a different amount for the same kind.
export type ModelPriceSummary =
  | { kind: 'token'; input: number | null; output: number | null; varies: boolean }
  | { kind: 'image' | 'video'; amount: number; varies: boolean }
  | { kind: 'unpriced' }

// ModelSearchEntry is one searchable model merged across every group that offers it.
export interface ModelSearchEntry {
  id: string // Requestable model identifier.
  displayName: string // Name from the first group in display order.
  brands: string[] // Distinct brand labels in group order.
  searchTerms: string[] // Distinct attribute search terms.
  groupCount: number // Groups offering this model.
  price: ModelPriceSummary // Cheapest quote summary.
}

// PricedQuote is one group's quote reduced to a comparable primary amount.
type PricedQuote =
  | { kind: 'token'; input: number | null; output: number | null; primary: number }
  | { kind: 'image' | 'video'; amount: number; primary: number }

// isQuoteAmount accepts finite nonnegative amounts, keeping explicit zero as free.
function isQuoteAmount(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

// tokenQuote reads input and output token prices. Context-interval quotes carry their
// amounts per interval, so the first listed interval supplies them when the top-level
// fields are absent. The primary amount prefers input, then output, so a quote without
// either amount sorts last.
function tokenQuote(pricing: MarketplaceModelPricing): PricedQuote {
  const source =
    isQuoteAmount(pricing.input_price_per_token) || isQuoteAmount(pricing.output_price_per_token)
      ? pricing
      : (pricing.context_intervals?.[0] ?? pricing)
  const input = isQuoteAmount(source.input_price_per_token) ? source.input_price_per_token : null
  const output = isQuoteAmount(source.output_price_per_token) ? source.output_price_per_token : null
  return { kind: 'token', input, output, primary: input ?? output ?? Number.POSITIVE_INFINITY }
}

// quoteFor reduces one group's pricing to a comparable quote, or null when the group
// has no usable public price. Image quotes use the cheapest listed resolution and video
// quotes the cheapest per-second entry, which is the "from" price a row displays.
function quoteFor(pricing: MarketplaceModelPricing): PricedQuote | null {
  const kind = pricingKind(pricing)
  if (kind === 'token') {
    return tokenQuote(pricing)
  }
  if (kind === 'image') {
    const amounts = [pricing.image_price_1k, pricing.image_price_2k, pricing.image_price_4k].filter(
      isQuoteAmount,
    )
    const amount = Math.min(...amounts)
    return { kind, amount, primary: amount }
  }
  if (kind === 'video') {
    const amount = Math.min(...videoPricingEntries(pricing).map((entry) => entry.price))
    return { kind, amount, primary: amount }
  }
  return null
}

// summarizePrices picks the cheapest quote of the first priced kind in group order.
// Mixed billing kinds across groups are rare; following group order keeps the summary
// consistent with the first card a user sees in the marketplace.
function summarizePrices(quotes: PricedQuote[]): ModelPriceSummary {
  if (quotes.length === 0) {
    return { kind: 'unpriced' }
  }
  const kind = quotes[0].kind
  const sameKind = quotes.filter((quote) => quote.kind === kind)
  const cheapest = sameKind.reduce((best, quote) => (quote.primary < best.primary ? quote : best))
  const varies = sameKind.some((quote) => quote.primary !== cheapest.primary)
  if (cheapest.kind === 'token') {
    return { kind: 'token', input: cheapest.input, output: cheapest.output, varies }
  }
  return { kind: cheapest.kind, amount: cheapest.amount, varies }
}

// ModelAccumulator gathers one model's appearances before the entry is finalized.
interface ModelAccumulator {
  model: MarketplaceModel // First appearance in group order.
  brands: Set<string> // Brand labels in insertion order.
  searchTerms: Set<string> // Attribute terms in insertion order.
  groupCount: number // Appearances counted so far.
  quotes: PricedQuote[] // Priced appearances in group order.
}

// buildModelIndex merges marketplace groups into one search entry per model ID. Groups
// are visited in marketplace display order, so the first group supplies the display
// name and the first priced kind. The input array is not modified, and entries keep the
// order in which models first appear.
export function buildModelIndex(groups: readonly MarketplaceGroup[]): ModelSearchEntry[] {
  const byId = new Map<string, ModelAccumulator>()
  for (const group of [...groups].sort(compareMarketplaceGroups)) {
    const brand = providerBrandDisplayName(groupBrandSource(group))
    for (const model of group.models) {
      let entry = byId.get(model.id)
      if (!entry) {
        entry = { model, brands: new Set(), searchTerms: new Set(), groupCount: 0, quotes: [] }
        byId.set(model.id, entry)
      }
      entry.brands.add(brand)
      for (const term of model.attributes?.search_terms ?? []) {
        entry.searchTerms.add(term)
      }
      entry.groupCount++
      const quote = quoteFor(model.pricing)
      if (quote) {
        entry.quotes.push(quote)
      }
    }
  }
  return [...byId.values()].map((entry) => ({
    id: entry.model.id,
    displayName: entry.model.display_name || entry.model.id,
    brands: [...entry.brands],
    searchTerms: [...entry.searchTerms],
    groupCount: entry.groupCount,
    price: summarizePrices(entry.quotes),
  }))
}
