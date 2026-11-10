import { afterEach, describe, expect, it, vi } from 'vitest'
import { MAX_RECENT_SEARCHES, readRecentSearches, recordRecentSearch } from '../recentSearches'

// recentSearches.spec.ts checks the per-account history of opened search results. It uses
// jsdom's real localStorage and replaces it with failing or malformed storage to cover
// browsers that block or corrupt site data.

afterEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
})

describe('recent searches', () => {
  // Recording an existing key moves it to the front, and the list never grows beyond the
  // number of rows the palette shows.
  it('keeps the latest keys first without duplicates', () => {
    for (let index = 0; index < MAX_RECENT_SEARCHES + 2; index++) {
      recordRecentSearch(7, `page:/p${index}`)
    }
    const list = recordRecentSearch(7, 'page:/p3')
    expect(list[0]).toBe('page:/p3')
    expect(list).toHaveLength(MAX_RECENT_SEARCHES)
    expect(new Set(list).size).toBe(list.length)
    expect(readRecentSearches(7)).toEqual(list)
  })

  // Each account and guests keep separate histories, so switching accounts never shows
  // another account's visits.
  it('separates accounts and guests', () => {
    recordRecentSearch(1, 'model:a')
    recordRecentSearch(null, 'model:b')
    expect(readRecentSearches(1)).toEqual(['model:a'])
    expect(readRecentSearches(2)).toEqual([])
    expect(readRecentSearches(null)).toEqual(['model:b'])
  })

  // Corrupted JSON, non-array values, and non-string entries are ignored instead of
  // breaking the palette.
  it('ignores malformed stored values', () => {
    localStorage.setItem('global_search_recent_3', '{bad json')
    expect(readRecentSearches(3)).toEqual([])
    localStorage.setItem('global_search_recent_3', JSON.stringify({ key: 'x' }))
    expect(readRecentSearches(3)).toEqual([])
    localStorage.setItem('global_search_recent_3', JSON.stringify(['model:a', 4, null]))
    expect(readRecentSearches(3)).toEqual(['model:a'])
  })

  // Blocked storage throws on access. Reads return no history and a write still returns
  // the updated list for the open palette.
  it('works without storage access', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    expect(readRecentSearches(1)).toEqual([])
    expect(recordRecentSearch(1, 'model:a')).toEqual(['model:a'])
  })
})
