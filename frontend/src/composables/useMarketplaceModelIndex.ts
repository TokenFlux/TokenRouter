import { computed, readonly, ref, watch, type Ref } from 'vue'
import { getAvailableMarketplaceModels, getMarketplaceModels } from '@/api/marketplace'
import { useLocaleRefresh } from '@/composables/useLocaleRefresh'
import { useAuthStore } from '@/stores'
import { buildModelIndex, type ModelSearchEntry } from '@/utils/marketplaceCatalog'

// useMarketplaceModelIndex.ts loads the marketplace catalog on demand and exposes it as
// a model-level search index for the global search palette. Guests read the public
// catalog; signed-in accounts read the catalog that includes their exclusive groups.
//
// The palette can stay mounted while the account changes, the locale changes, or a
// slow request is still running. Every request therefore captures a generation number
// and the account identity it was made for. A completion publishes entries or an error
// only while both still match, so a late response for a previous account can never
// show that account's exclusive models to the next one. An identity change also clears
// the published entries immediately and returns the index to idle, so the next open
// starts a fresh request instead of reusing the old catalog.

// ModelIndexStatus describes the catalog lifecycle shown by the palette: idle before
// the first load, loading while a request is current, ready with entries, or error.
export type ModelIndexStatus = 'idle' | 'loading' | 'ready' | 'error'

// MarketplaceModelIndex exposes the published entries and the controls that load them.
export interface MarketplaceModelIndex {
  entries: Readonly<Ref<readonly ModelSearchEntry[]>> // Published model entries.
  status: Readonly<Ref<ModelIndexStatus>> // Current lifecycle state.
  ensureLoaded: () => Promise<void> // Loads once per account.
  retry: () => Promise<void> // Forces a new request.
}

// useMarketplaceModelIndex creates an on-demand model index bound to the current
// account. Call it during component setup; the locale listener and identity watcher
// are released with the calling scope. Nothing is requested until ensureLoaded runs.
export function useMarketplaceModelIndex(): MarketplaceModelIndex {
  const authStore = useAuthStore()
  const entries = ref<ModelSearchEntry[]>([])
  const status = ref<ModelIndexStatus>('idle')

  // identity names the catalog owner, so requests and published entries can be checked
  // against the account that is current when they complete.
  const identity = computed(() =>
    authStore.isAuthenticated ? `user:${authStore.user?.id ?? ''}` : 'guest',
  )

  // generation increases with every request and invalidation; requestedIdentity records
  // which account the latest load or loaded catalog belongs to.
  let generation = 0
  let requestedIdentity: string | null = null

  // load requests the catalog for the current account and publishes it when the
  // request is still current. The generation is captured before awaiting, and the
  // completion is discarded when a newer load, a retry, or an account change has
  // advanced it, which keeps late results for a previous account out of the index.
  //
  // Failures publish the error status without clearing entries from the same account,
  // so a failed locale refresh keeps the earlier names visible. Errors are logged here
  // because the palette only shows a generic retry message.
  async function load(): Promise<void> {
    const current = ++generation
    const owner = identity.value
    requestedIdentity = owner
    status.value = 'loading'
    try {
      const groups = await (authStore.isAuthenticated
        ? getAvailableMarketplaceModels()
        : getMarketplaceModels())
      if (current !== generation || owner !== identity.value) return
      entries.value = buildModelIndex(groups)
      status.value = 'ready'
    } catch (error) {
      if (current !== generation || owner !== identity.value) return
      console.error('Failed to load the model search index:', error)
      status.value = 'error'
    }
  }

  // ensureLoaded starts a request only when this account has no loaded, pending, or
  // failed catalog. A failed load stays failed until retry, so reopening the palette
  // does not repeat a request that is likely to fail again.
  async function ensureLoaded(): Promise<void> {
    if (requestedIdentity === identity.value && status.value !== 'idle') return
    await load()
  }

  // Account changes clear the previous catalog synchronously and invalidate pending
  // requests, so the next render cannot show the previous account's exclusive models.
  watch(
    identity,
    () => {
      generation++
      requestedIdentity = null
      entries.value = []
      status.value = 'idle'
    },
    { flush: 'sync' },
  )

  // Localized display names change with the locale; reload only a catalog that has
  // already been requested so an unopened palette stays idle.
  useLocaleRefresh(() => (status.value === 'idle' ? undefined : load()))

  return {
    entries: readonly(entries) as Readonly<Ref<readonly ModelSearchEntry[]>>,
    status: readonly(status),
    ensureLoaded,
    retry: load,
  }
}
