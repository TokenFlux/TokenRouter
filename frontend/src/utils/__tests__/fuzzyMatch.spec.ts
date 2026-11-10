import { describe, expect, it } from 'vitest'
import { MATCH_TIER, bestMatch, fuzzyMatch, highlightSegments } from '../fuzzyMatch'

// fuzzyMatch.spec.ts checks how search queries are scored against display text and how
// matched characters map back to the original text for highlighting. Fixtures use model
// identifiers with mixed separators and camelCase labels, which are the inputs the global
// search palette ranks.

describe('fuzzyMatch', () => {
  // Each tier is exercised with one query against different texts. An exact match must
  // outrank a prefix, which must outrank a word-start, substring, and subsequence match.
  it('orders match kinds from exact to subsequence', () => {
    const scores = [
      fuzzyMatch('gpt4o', 'gpt-4o'),
      fuzzyMatch('gpt4o', 'gpt-4o-mini'),
      fuzzyMatch('gpt4o', 'openai/gpt-4o'),
      fuzzyMatch('gpt4o', 'chatgpt-4o-latest'),
      fuzzyMatch('gpt4o', 'gpt-4-turbo-o'),
    ].map((match) => match?.score ?? -1)
    expect(scores[0]).toBe(MATCH_TIER.exact)
    expect(scores[1]).toBe(MATCH_TIER.prefix)
    for (let index = 1; index < scores.length; index++) {
      expect(scores[index]).toBeLessThan(scores[index - 1])
    }
    expect(scores[4]).toBeGreaterThan(0)
  })

  // Users type spaces where identifiers use hyphens or dots. Normalization ignores those
  // separators on both sides, and case never affects the match.
  it('ignores case and separators', () => {
    expect(fuzzyMatch('Claude Opus 4.5', 'claude-opus-4-5')?.score).toBe(MATCH_TIER.exact)
    expect(fuzzyMatch('gpt 4o', 'GPT_4O')?.score).toBe(MATCH_TIER.exact)
  })

  // Highlight ranges must point into the original text even though separators were
  // skipped while matching, producing one run per contiguous group of source characters.
  it('maps ranges back to the original text', () => {
    const match = fuzzyMatch('opus45', 'claude-opus-4-5')
    expect(match?.score).toBeLessThanOrEqual(MATCH_TIER.wordStart)
    expect(match?.ranges).toEqual([
      { start: 7, end: 11 },
      { start: 12, end: 13 },
      { start: 14, end: 15 },
    ])
  })

  // camelCase and slash-separated words start new words, so a query matching there ranks
  // as a word start instead of a plain substring.
  it('detects camelCase and slash word starts', () => {
    expect(fuzzyMatch('keys', 'apiKeys')?.score).toBeGreaterThan(MATCH_TIER.substring)
    expect(fuzzyMatch('models', '/admin/models')?.score).toBeGreaterThan(MATCH_TIER.substring)
    expect(fuzzyMatch('eys', 'apiKeys')?.score).toBeLessThanOrEqual(MATCH_TIER.substring)
  })

  // Queries that are empty, contain only separators, or have characters out of order
  // never match, so callers can rely on null meaning "do not list".
  it('rejects empty and unmatched queries', () => {
    expect(fuzzyMatch('', 'gpt-4o')).toBeNull()
    expect(fuzzyMatch(' - ', 'gpt-4o')).toBeNull()
    expect(fuzzyMatch('o4tpg', 'gpt-4o')).toBeNull()
    expect(fuzzyMatch('gpt', '')).toBeNull()
  })
})

describe('bestMatch', () => {
  // A candidate matches through its title, ID, or alias. The strongest field wins, and an
  // equal score keeps the earlier field so the visible title is highlighted first.
  it('returns the strongest field and prefers earlier fields on ties', () => {
    expect(bestMatch('key', ['API Keys', '/keys', 'token'])).toMatchObject({ field: 1 })
    expect(bestMatch('token', ['API Keys', '/keys', 'token'])).toMatchObject({ field: 2 })
    expect(bestMatch('gpt', ['gpt', 'gpt'])).toMatchObject({ field: 0 })
    expect(bestMatch('zzz', ['API Keys'])).toBeNull()
  })
})

describe('highlightSegments', () => {
  // Ranges split text into alternating plain and matched runs. Ranges past the end are
  // clipped, and no ranges return the whole text as one plain run.
  it('splits text into plain and matched runs', () => {
    expect(highlightSegments('gpt-4o', [{ start: 4, end: 9 }])).toEqual([
      { text: 'gpt-', matched: false },
      { text: '4o', matched: true },
    ])
    expect(highlightSegments('gpt', [])).toEqual([{ text: 'gpt', matched: false }])
  })
})
