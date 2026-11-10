// recentSearches.ts stores the keys of search results a visitor recently opened, kept
// separately for each account in browser storage. Only keys are stored, not titles or
// prices, so the palette always shows current names and drops entries the account can
// no longer see. Storage can be unavailable (private browsing, blocked site data) or
// contain stale or foreign values; reads then return an empty list and writes are
// skipped, so the palette works the same without history.

// MAX_RECENT_SEARCHES limits stored history to the five rows the palette shows.
export const MAX_RECENT_SEARCHES = 5

// STORAGE_PREFIX namespaces the history key; the owner ID or "guest" is appended.
const STORAGE_PREFIX = 'global_search_recent_'

// storageKey returns the history key for one account, or for guests when owner is null.
function storageKey(owner: number | null): string {
  return `${STORAGE_PREFIX}${owner ?? 'guest'}`
}

// readRecentSearches returns the owner's stored result keys, most recent first. Missing,
// unreadable, or malformed history returns an empty list; non-string entries are dropped
// and the list is cut to MAX_RECENT_SEARCHES.
export function readRecentSearches(owner: number | null): string[] {
  try {
    const raw = localStorage.getItem(storageKey(owner))
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed
      .filter((value): value is string => typeof value === 'string')
      .slice(0, MAX_RECENT_SEARCHES)
  } catch {
    // Blocked storage and corrupted JSON both mean no usable history; the palette then
    // shows suggestions only instead of failing to open.
    return []
  }
}

// recordRecentSearch moves key to the front of the owner's history and returns the new
// list. The returned list reflects the visit even when storage rejects the write, so the
// open palette stays consistent for the rest of the session.
export function recordRecentSearch(owner: number | null, key: string): string[] {
  const next = [key, ...readRecentSearches(owner).filter((value) => value !== key)].slice(
    0,
    MAX_RECENT_SEARCHES,
  )
  try {
    localStorage.setItem(storageKey(owner), JSON.stringify(next))
  } catch {
    // Quota or privacy restrictions only cost persistence across reloads; the caller
    // still receives the updated list.
  }
  return next
}
