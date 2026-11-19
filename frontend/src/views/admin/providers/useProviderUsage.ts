import { ref, onUnmounted, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { useDesktopTableViewport } from '@/composables/useDesktopTableViewport'
import { enqueueUsageRequest } from '@/utils/usageLoadQueue'
import type { Provider, ProviderUsageInfo, WindowStats } from '@/api/admin/providers.types'

// useProviderUsage.ts coordinates daily statistics and desktop usage requests
// for the provider table. Visible rows determine the daily-statistics request,
// while usage cells enqueue provider IDs for a shared batch on the next timer
// turn. Cached usage observations expire after five minutes, and forced
// requests discard the matching cached value before entering the queue.
//
// Daily statistics use a request sequence for the whole visible page. Usage
// batches instead capture a token for each provider, allowing later requests
// for one provider to supersede that result independently. The composable
// registers the desktop media listener and releases its listener and queued
// timer when the page unmounts.

// useProviderUsage owns daily statistics, queued usage batches, and manual
// usage-refresh signals for the provider page. Pass live rows, selected IDs,
// and the shared hidden-column set. Daily statistics and usage batches retain
// separate request ownership because one covers a visible page and the other
// covers individual providers.
//
// The composable tracks the desktop media query at mount, batches usage
// requests on a timer, and releases that timer and media listener on unmount.
// Selected providers outside the page are resolved before bulk active queries
// are queued.
export function useProviderUsage(
  providers: Ref<Provider[]>,
  selIds: Readonly<Ref<number[]>>,
  hiddenColumns: ReadonlySet<string>,
) {
  // The translator and application store supply messages for this controller
  // while its request and selection state belongs to this mounted page.
  const { t } = useI18n()
  const appStore = useAppStore()
  // bulkUsageLoading prevents duplicate active-usage actions while selected
  // provider metadata and queued requests are being resolved.
  const bulkUsageLoading = ref(false)

  // todayStatsByProviderId, todayStatsLoading, todayStatsError, and
  // todayStatsReqSeq describe one visible-page statistics request and prevent
  // older pages from replacing its result.
  const todayStatsByProviderId = ref<Record<string, WindowStats>>({})
  const todayStatsLoading = ref(false)
  const todayStatsError = ref<string | null>(null)
  const todayStatsReqSeq = ref(0)

  // usageManualRefreshToken advances after successful manual queries so mounted
  // usage cells can refresh their own displayed observations.
  const usageManualRefreshToken = ref(0)

  // isDesktopViewport gates cell batching through the shared viewport listener.
  const isDesktopViewport = useDesktopTableViewport()

  // usageBatchByProviderId, usageBatchErrorByProviderId, and
  // usageBatchLoadingByProviderId publish independent provider cells;
  // usageBatchRequestTokenByProviderId identifies the request allowed to update
  // each cell.
  const usageBatchByProviderId = ref<Record<string, ProviderUsageInfo | null>>({})
  const usageBatchErrorByProviderId = ref<Record<string, string | null>>({})
  const usageBatchLoadingByProviderId = ref<Record<string, boolean>>({})
  const usageBatchRequestTokenByProviderId = ref<Record<string, number>>({})

  // usageBatchCache stores successful observations with millisecond timestamps;
  // the TTL limits reuse to five minutes before another cell request reaches the
  // server.
  const usageBatchCache = new Map<
    number,
    {
      // data and ts belong to one provider entry owned by this composable; a
      // successful batch replaces both, and ts controls reuse of that data by TTL.
      // The accepted data object is also shared with the displayed usage map.
      data: ProviderUsageInfo // Accepted usage observation.
      ts: number // Epoch milliseconds for TTL.
    }
  >()
  const USAGE_BATCH_CACHE_TTL = 5 * 60 * 1000

  // pendingUsageBatchIds combines same-turn admissions, usageBatchFlushTimer
  // schedules one flush, and queuedUsageBatchForce records whether any admitted
  // provider requires a forced query.
  const pendingUsageBatchIds = new Set<number>()
  let usageBatchFlushTimer: ReturnType<typeof setTimeout> | null = null
  let queuedUsageBatchForce = false

  // usageBatchRequestToken increases for each admitted provider request and is
  // copied into that provider ownership entry before batching.
  let usageBatchRequestToken = 0

  // buildDefaultTodayStats creates a zero-valued statistics record for missing
  // provider results.
  const buildDefaultTodayStats = (): WindowStats => ({
    requests: 0,
    tokens: 0,
    cost: 0,
    standard_cost: 0,
    user_cost: 0,
  })

  // providerSupportsBatchUsage reports whether a provider supports the batched
  // usage endpoint. OAuth and setup-token eligibility depends on platform; Gemini
  // excludes API-key providers configured as third-party accounts.
  const providerSupportsBatchUsage = (provider: Provider) => {
    if (provider.platform === 'anthropic') {
      return provider.type === 'oauth' || provider.type === 'setup-token'
    }
    if (provider.platform === 'gemini') {
      const credentials = provider.credentials as Record<string, unknown> | undefined
      return !(provider.type === 'apikey' && credentials?.provider_type === 'third_party')
    }
    if (provider.platform === 'antigravity') return provider.type === 'oauth'
    if (provider.platform === 'openai') return provider.type === 'oauth'
    if (provider.platform === 'grok') return provider.type === 'oauth'
    return false
  }

  // setUsageBatchLoading publishes the loading flag for one provider's usage
  // cell.
  const setUsageBatchLoading = (providerID: number, loadingState: boolean) => {
    usageBatchLoadingByProviderId.value = {
      ...usageBatchLoadingByProviderId.value,
      [String(providerID)]: loadingState,
    }
  }

  // setUsageBatchState publishes one provider's usage result and error together.
  const setUsageBatchState = (
    providerID: number,
    usage: ProviderUsageInfo | null,
    error: string | null,
  ) => {
    const key = String(providerID)
    usageBatchByProviderId.value = {
      ...usageBatchByProviderId.value,
      [key]: usage,
    }
    usageBatchErrorByProviderId.value = {
      ...usageBatchErrorByProviderId.value,
      [key]: error,
    }
  }

  // handleProviderUsageLoaded accepts a usage observation returned by a child
  // cell. Reusing the identical stored object requires no update; another object
  // replaces its provider entry and clears its error.
  const handleProviderUsageLoaded = (providerID: number, usage: ProviderUsageInfo) => {
    if (usageBatchByProviderId.value[String(providerID)] === usage) return
    setUsageBatchState(providerID, usage, null)
  }

  // flushQueuedUsageBatch requests the currently queued provider IDs with the
  // accumulated force flag. It releases the queue and timer reference before
  // awaiting the request, allowing later cell requests to form another batch.
  // Captured per-provider tokens decide which returned entries still own their
  // display state.
  //
  // Accepted results update the value, error, loading maps, and cache together.
  // Missing usage removes that provider's cache entry. A transport failure
  // changes errors and loading flags only for providers whose tokens still match,
  // retaining newer observations.
  const flushQueuedUsageBatch = async () => {
    // Release the queued batch before transport starts so cell requests
    // arriving during this await can schedule their own flush.
    usageBatchFlushTimer = null
    const providerIDs = Array.from(pendingUsageBatchIds)
    const force = queuedUsageBatchForce
    pendingUsageBatchIds.clear()
    queuedUsageBatchForce = false

    if (providerIDs.length === 0) return

    const requestTokensByProvider = providerIDs.reduce<Record<string, number>>(
      (acc, providerID) => {
        acc[String(providerID)] = usageBatchRequestTokenByProviderId.value[String(providerID)] ?? 0
        return acc
      },
      {},
    )

    try {
      const result = await adminAPI.providers.getBatchUsage(providerIDs, force)

      const usageMap = result.usage ?? {}
      const errorMap = result.errors ?? {}
      const now = Date.now()
      const nextUsage = { ...usageBatchByProviderId.value }
      const nextErrors = { ...usageBatchErrorByProviderId.value }
      const nextLoading = { ...usageBatchLoadingByProviderId.value }

      for (const providerID of providerIDs) {
        const key = String(providerID)
        // Skip superseded provider entries before updating maps or cache,
        // retaining the result and loading state of their newer request.
        if ((usageBatchRequestTokenByProviderId.value[key] ?? 0) !== requestTokensByProvider[key]) {
          continue
        }
        const usage = usageMap[key] ?? null
        nextUsage[key] = usage
        nextErrors[key] = errorMap[key] ?? null
        nextLoading[key] = false
        if (usage) {
          usageBatchCache.set(providerID, { data: usage, ts: now })
        } else {
          usageBatchCache.delete(providerID)
        }
      }

      usageBatchByProviderId.value = nextUsage
      usageBatchErrorByProviderId.value = nextErrors
      usageBatchLoadingByProviderId.value = nextLoading
    } catch (error) {
      const nextErrors = { ...usageBatchErrorByProviderId.value }
      const nextLoading = { ...usageBatchLoadingByProviderId.value }
      for (const providerID of providerIDs) {
        const key = String(providerID)
        if ((usageBatchRequestTokenByProviderId.value[key] ?? 0) !== requestTokensByProvider[key]) {
          continue
        }
        nextErrors[key] = 'Failed'
        nextLoading[key] = false
      }
      usageBatchErrorByProviderId.value = nextErrors
      usageBatchLoadingByProviderId.value = nextLoading
      console.error('Failed to load provider usage batch:', error)
    }
  }

  // queueBatchedUsage admits eligible desktop provider cells to a shared
  // next-turn usage batch. A forced request removes its cache entry; otherwise a
  // fresh cached observation is published immediately. Admitted requests assign a
  // new provider token and mark the cell loading before adding its ID to the
  // queue.
  //
  // A set combines duplicate provider IDs, and any forced admission makes the
  // batch forced. One timer flushes the accumulated IDs, while token comparisons
  // protect providers that receive newer requests before an earlier batch
  // finishes.
  const queueBatchedUsage = (
    provider: Provider,
    options?: {
      // force defaults to false; true deletes this provider's cached observation
      // before queueing and forces the resulting shared batch request.
      force?: boolean
    },
  ) => {
    if (!isDesktopViewport.value) return
    if (!providerSupportsBatchUsage(provider)) return

    const force = options?.force === true
    const cacheKey = provider.id
    const key = String(cacheKey)

    if (force) {
      // A forced admission invalidates the cached observation before scheduling
      // transport, so another cell cannot reuse it during this request.
      usageBatchCache.delete(cacheKey)
    } else {
      const cached = usageBatchCache.get(cacheKey)
      if (cached && Date.now() - cached.ts < USAGE_BATCH_CACHE_TTL) {
        setUsageBatchState(cacheKey, cached.data, null)
        setUsageBatchLoading(cacheKey, false)
        return
      }
    }

    usageBatchErrorByProviderId.value = {
      ...usageBatchErrorByProviderId.value,
      [key]: null,
    }
    usageBatchRequestTokenByProviderId.value = {
      ...usageBatchRequestTokenByProviderId.value,
      [key]: ++usageBatchRequestToken,
    }
    setUsageBatchLoading(cacheKey, true)
    pendingUsageBatchIds.add(cacheKey)
    queuedUsageBatchForce = queuedUsageBatchForce || force

    // Coalesce same-turn cell admissions into one batch; the scheduled callback
    // will consume the shared ID set and force flag.
    if (usageBatchFlushTimer !== null) return
    usageBatchFlushTimer = setTimeout(() => {
      void flushQueuedUsageBatch()
    }, 0)
  }

  // refreshTodayStatsBatch fetches daily metrics for the visible provider IDs
  // when either statistics column needs them. Empty rows clear the result, and
  // hidden statistics and usage columns suppress the request. Each admitted
  // request receives a sequence number before loading state changes.
  //
  // Completion publishes results and clears loading only when that sequence
  // remains current. Missing provider entries become zero-valued records, while
  // transport failures retain the current data and expose an error. This keeps an
  // earlier page request from replacing a later page's metrics.
  const refreshTodayStatsBatch = async () => {
    // Why this checks both columns:
    // - today_stats column shows dedicated today's metrics.
    // - usage column also embeds today's stats for Key/Bedrock rows.
    // So we only skip fetching when BOTH columns are hidden.
    if (hiddenColumns.has('today_stats') && hiddenColumns.has('usage')) {
      todayStatsLoading.value = false
      todayStatsError.value = null
      return
    }

    const providerIDs = providers.value.map((provider) => provider.id)
    const reqSeq = ++todayStatsReqSeq.value
    if (providerIDs.length === 0) {
      todayStatsByProviderId.value = {}
      todayStatsError.value = null
      todayStatsLoading.value = false
      return
    }

    todayStatsLoading.value = true
    todayStatsError.value = null

    try {
      const result = await adminAPI.providers.getBatchTodayStats(providerIDs)
      // A later page request owns the statistics maps; discard this completed
      // page before allocating replacement entries for its providers.
      if (reqSeq !== todayStatsReqSeq.value) return
      const serverStats = result.stats ?? {}
      const nextStats: Record<string, WindowStats> = {}
      for (const providerID of providerIDs) {
        const key = String(providerID)
        nextStats[key] = serverStats[key] ?? buildDefaultTodayStats()
      }
      todayStatsByProviderId.value = nextStats
    } catch (error) {
      if (reqSeq !== todayStatsReqSeq.value) return
      todayStatsError.value = 'Failed'
      console.error('Failed to load provider today stats:', error)
    } finally {
      if (reqSeq === todayStatsReqSeq.value) {
        todayStatsLoading.value = false
      }
    }
  }

  // canQueryProviderUsage reports whether a provider supports an active usage
  // query. It follows the bulk action's platform and credential-type rules,
  // including Qoder COSY accounts and Gemini credentials other than API keys.
  const canQueryProviderUsage = (provider: Provider) => {
    // Eligibility follows the active usage endpoint, which differs from the platform rules used for automatic desktop batches.
    return (
      (provider.platform === 'anthropic' &&
        (provider.type === 'oauth' || provider.type === 'setup-token')) ||
      (provider.platform === 'openai' && provider.type === 'oauth') ||
      (provider.platform === 'gemini' && provider.type !== 'apikey') ||
      (provider.platform === 'antigravity' && provider.type === 'oauth') ||
      (provider.platform === 'qoder' && provider.type === 'cosy')
    )
  }

  // handleBulkQueryUsage actively refreshes usage for the current selected
  // providers. It prevents duplicate starts, captures selected IDs, and resolves
  // details for off-page providers before filtering supported credentials. Each
  // supported request enters the shared usage queue, preserving its existing
  // admission policy.
  //
  // Metadata failures and request failures contribute to the displayed failure
  // count. Any success advances the manual-refresh token so visible usage cells
  // request fresh data. The bulk loading flag is released for unsupported
  // selections, partial failure, and successful completion.
  const handleBulkQueryUsage = async () => {
    if (bulkUsageLoading.value) return
    const providerIds = [...selIds.value]
    if (providerIds.length === 0) return

    bulkUsageLoading.value = true
    try {
      const currentProvidersById = new Map(
        providers.value.map((provider) => [provider.id, provider]),
      )
      // Resolve off-page selections before checking eligibility so the action covers selected IDs beyond the currently rendered table rows.
      const selectedProviderResults = await Promise.allSettled(
        providerIds.map((providerId) => {
          const provider = currentProvidersById.get(providerId)
          return provider ? Promise.resolve(provider) : adminAPI.providers.getById(providerId)
        }),
      )
      const selectedProviders = selectedProviderResults
        .filter(
          (result): result is PromiseFulfilledResult<Provider> => result.status === 'fulfilled',
        )
        .map((result) => result.value)
      const metadataFailed = selectedProviderResults.length - selectedProviders.length
      const queryableProviders = selectedProviders.filter(canQueryProviderUsage)
      if (queryableProviders.length === 0) {
        appStore.showWarning(t('admin.providers.bulkActions.queryUsageNoSupported'))
        return
      }

      const results = await Promise.allSettled(
        queryableProviders.map((provider) =>
          enqueueUsageRequest(provider, () =>
            adminAPI.providers.getUsage(provider.id, 'active', true),
          ),
        ),
      )
      const success = results.filter((result) => result.status === 'fulfilled').length
      const failed = metadataFailed + results.length - success
      if (success > 0) {
        usageManualRefreshToken.value += 1
      }
      if (failed > 0) {
        appStore.showError(
          t('admin.providers.bulkActions.queryUsagePartialSuccess', { success, failed }),
        )
        return
      }
      appStore.showSuccess(t('admin.providers.bulkActions.queryUsageSuccess', { count: success }))
    } catch (error) {
      console.error('Failed to bulk query usage:', error)
      appStore.showError(String(error))
    } finally {
      bulkUsageLoading.value = false
    }
  }
  onUnmounted(() => {
    // Cancel queued work when the provider page unmounts so pending batches
    // cannot start after teardown.
    if (usageBatchFlushTimer !== null) {
      clearTimeout(usageBatchFlushTimer)
      usageBatchFlushTimer = null
    }
    pendingUsageBatchIds.clear()
  })
  return {
    todayStatsByProviderId,
    todayStatsLoading,
    todayStatsError,
    usageManualRefreshToken,
    isDesktopViewport,
    usageBatchByProviderId,
    usageBatchErrorByProviderId,
    usageBatchLoadingByProviderId,
    handleProviderUsageLoaded,
    queueBatchedUsage,
    refreshTodayStatsBatch,
    bulkUsageLoading,
    handleBulkQueryUsage,
  }
}
