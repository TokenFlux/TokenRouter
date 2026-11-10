import { describe, expect, it } from 'vitest'
import type { MarketplaceGroup, MarketplaceModel } from '@/api/marketplace.types'
import { buildModelIndex, compareMarketplaceGroups } from '../marketplaceCatalog'

// marketplaceCatalog.spec.ts checks how marketplace groups become one search entry per
// model. Fixtures offer the same model through several groups with different quotes,
// billing kinds, and display order, which is how the global search palette receives them.

// group builds a public group fixture with the supplied ordering fields and models.
function group(
  id: number,
  models: MarketplaceModel[],
  overrides: Partial<MarketplaceGroup> = {},
): MarketplaceGroup {
  return {
    id,
    name: `group-${id}`,
    description: '',
    display_brand: '',
    sort_order: 0,
    rate_multiplier: 1,
    models,
    ...overrides,
  } as MarketplaceGroup
}

// tokenModel builds a token-priced model fixture quoting per-token input and output.
function tokenModel(id: string, input: number, output: number, name = id): MarketplaceModel {
  return {
    id,
    display_name: name,
    pricing: {
      pricing_mode: 'token',
      price_status: 'priced',
      input_price_per_token: input,
      output_price_per_token: output,
    },
  }
}

describe('compareMarketplaceGroups', () => {
  // Exclusive groups come first, then server sort order, then ID. This is the order the
  // marketplace page renders and the index uses to pick display names.
  it('orders exclusive groups first, then sort order and ID', () => {
    const groups = [
      group(3, [], { sort_order: 1 }),
      group(2, [], { sort_order: 1 }),
      group(9, [], { sort_order: 5, is_exclusive: true }),
      group(1, [], { sort_order: 2 }),
    ]
    expect([...groups].sort(compareMarketplaceGroups).map((item) => item.id)).toEqual([9, 2, 3, 1])
  })
})

describe('buildModelIndex', () => {
  // One model offered by two groups at different prices becomes a single entry with both
  // brands, the cheaper quote, and a flag saying the price varies between groups.
  it('merges a model across groups and keeps the cheapest quote', () => {
    const index = buildModelIndex([
      group(1, [tokenModel('gpt-4o', 0.000005, 0.00002, 'GPT-4o')], { display_brand: 'OpenAI' }),
      group(2, [tokenModel('gpt-4o', 0.0000025, 0.00001)], { display_brand: 'Azure' }),
    ])
    expect(index).toHaveLength(1)
    expect(index[0]).toMatchObject({
      id: 'gpt-4o',
      displayName: 'GPT-4o',
      groupCount: 2,
      price: { kind: 'token', input: 0.0000025, output: 0.00001, varies: true },
    })
    expect(index[0].brands).toHaveLength(2)
  })

  // Identical quotes in every group are a single price, so the row should not say "from".
  it('does not mark equal quotes as varying', () => {
    const index = buildModelIndex([
      group(1, [tokenModel('m', 0.000001, 0.000002)]),
      group(2, [tokenModel('m', 0.000001, 0.000002)]),
    ])
    expect(index[0].price).toMatchObject({ kind: 'token', varies: false })
  })

  // A model without a public quote in any group stays searchable with an unpriced summary;
  // a priced group elsewhere supplies the quote even when it is listed second.
  it('summarizes unpriced and image quotes', () => {
    const unpriced: MarketplaceModel = {
      id: 'draft',
      display_name: '',
      pricing: { pricing_mode: 'token', price_status: 'unpriced' },
    }
    const image: MarketplaceModel = {
      id: 'img',
      display_name: 'Image',
      pricing: {
        pricing_mode: 'image',
        price_status: 'priced',
        image_price_1k: 0.04,
        image_price_2k: 0.08,
      },
    }
    const index = buildModelIndex([
      group(1, [unpriced, { ...image, pricing: unpriced.pricing }]),
      group(2, [image]),
    ])
    expect(index.find((entry) => entry.id === 'draft')).toMatchObject({
      displayName: 'draft',
      price: { kind: 'unpriced' },
    })
    expect(index.find((entry) => entry.id === 'img')?.price).toEqual({
      kind: 'image',
      amount: 0.04,
      varies: false,
    })
  })

  // Attribute search terms from every group are unioned without duplicates, so a term
  // configured on one route still finds the merged entry.
  it('unions attribute search terms', () => {
    const withTerms = (terms: string[]): MarketplaceModel => ({
      ...tokenModel('m', 0, 0),
      attributes: { search_terms: terms } as MarketplaceModel['attributes'],
    })
    const index = buildModelIndex([
      group(1, [withTerms(['vision', 'fast'])]),
      group(2, [withTerms(['fast', 'cheap'])]),
    ])
    expect(index[0].searchTerms).toEqual(['vision', 'fast', 'cheap'])
  })
})
