<template>
  <div>
    <!-- Multi-select Dropdown -->
    <div class="relative mb-3">
      <div
        data-icon-trigger
        @click="toggleDropdown"
        class="cursor-pointer rounded-control border border-gray-300 bg-white px-3 py-2 dark:border-dark-500 dark:bg-dark-700"
      >
        <TransitionGroup
          name="motion-list"
          tag="div"
          class="relative grid grid-cols-2 gap-1.5"
          @before-leave="prepareListLeave"
          @before-enter="restoreEnteringElement"
        >
          <span
            v-for="model in modelValue"
            :key="model"
            class="inline-flex items-center justify-between gap-1 rounded-compact bg-gray-100 px-2 py-1 text-xs text-gray-700 dark:bg-dark-600 dark:text-gray-300"
          >
            <span class="flex items-center gap-1 truncate">
              <ModelIcon :model="model" size="14px" />
              <span class="truncate">{{ model }}</span>
            </span>
            <button
              type="button"
              @click.stop="removeModel(model)"
              class="shrink-0 rounded-full hover:bg-gray-200 dark:hover:bg-dark-700"
            >
              <Icon name="x" size="xs" class="h-3.5 w-3.5" :stroke-width="2" />
            </button>
          </span>
        </TransitionGroup>
        <div
          class="mt-2 flex items-center justify-between border-t border-gray-200 pt-2 dark:border-dark-600"
        >
          <span class="text-xs text-gray-400">{{
            t('admin.providers.modelCount', { count: modelValue.length })
          }}</span>
          <Icon
            name="chevronDown"
            size="md"
            :animate-on-hover="false"
            class="h-5 w-5 text-gray-400"
          />
        </div>
      </div>
      <!-- Dropdown List -->
      <MotionTransition name="dropdown-fade">
        <div
          v-if="showDropdown"
          :inert="!showDropdown || undefined"
          class="dropdown left-0 right-0 top-full z-50 mt-1 py-0"
        >
          <div
            class="sticky top-0 border-b border-gray-200 bg-white p-2 dark:border-dark-600 dark:bg-dark-900"
          >
            <input
              v-model="searchQuery"
              type="text"
              class="input w-full text-sm"
              :placeholder="t('admin.providers.searchModels')"
              @click.stop
            />
          </div>
          <div class="max-h-52 overflow-auto">
            <div
              v-for="model in filteredModels"
              :key="model.value"
              data-testid="model-option"
              class="group flex items-center hover:bg-gray-100 dark:hover:bg-dark-800"
            >
              <button
                type="button"
                data-testid="select-model"
                class="flex min-w-0 flex-1 items-center gap-2 px-3 py-2 text-left text-sm"
                @click="toggleModel(model.value)"
              >
                <span
                  :class="[
                    'flex h-4 w-4 shrink-0 items-center justify-center rounded-compact border',
                    modelValue.includes(model.value)
                      ? 'border-primary-700 bg-primary-700 text-white dark:border-primary-600 dark:bg-primary-600'
                      : 'border-gray-300 dark:border-dark-500',
                  ]"
                >
                  <Icon
                    name="check"
                    size="xs"
                    :animate-on-hover="false"
                    v-if="modelValue.includes(model.value)"
                    class="h-3 w-3"
                  />
                </span>
                <ModelIcon :model="model.value" size="18px" />
                <span class="truncate text-gray-900 dark:text-white">{{ model.value }}</span>
              </button>
              <button
                type="button"
                data-testid="copy-model-id"
                class="mr-2 shrink-0 rounded-compact p-1.5 text-gray-400 opacity-70 transition-colors hover:bg-gray-200 hover:text-primary-600 focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 group-hover:opacity-100 dark:text-gray-500 dark:hover:bg-dark-700 dark:hover:text-primary-400 dark:focus-visible:ring-primary-500"
                :title="`${t('common.copy')} ${model.value}`"
                :aria-label="`${t('common.copy')} ${model.value}`"
                @click="copyModelId(model.value)"
              >
                <Icon name="copy" size="sm" />
              </button>
            </div>
            <div v-if="loadingCatalog" class="px-3 py-2 text-sm text-gray-500">
              {{ t('common.loading') }}
            </div>
            <button
              v-if="catalogFailed"
              type="button"
              class="btn btn-secondary m-2"
              @click.stop="loadCatalog()"
            >
              {{ t('common.retry') }}
            </button>
            <button
              v-if="
                !loadingCatalog &&
                !catalogFailed &&
                props.models === undefined &&
                catalogModels.length < catalogTotal
              "
              type="button"
              class="btn btn-secondary m-2"
              @click.stop="loadCatalog(true)"
            >
              {{ t('admin.providers.loadMoreModels') }}
            </button>
            <div
              v-if="!loadingCatalog && !catalogFailed && filteredModels.length === 0"
              class="px-3 py-4 text-center text-sm text-gray-500"
            >
              {{ t('admin.providers.noMatchingModels') }}
            </div>
          </div>
        </div>
      </MotionTransition>
    </div>

    <!-- Quick Actions -->
    <div class="mb-4 flex flex-wrap gap-2">
      <button
        v-if="canSyncUpstream"
        type="button"
        @click="syncUpstreamModels"
        :disabled="isSyncingUpstream"
        class="rounded-control border border-emerald-200 px-3 py-1.5 text-sm text-emerald-600 hover:bg-emerald-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-emerald-800 dark:text-emerald-400 dark:hover:bg-emerald-900/30"
      >
        {{
          isSyncingUpstream
            ? t('admin.providers.syncUpstreamModelsLoading')
            : t('admin.providers.syncUpstreamModels')
        }}
      </button>
      <button
        type="button"
        @click="clearAll"
        class="rounded-control border border-red-200 px-3 py-1.5 text-sm text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-900/30"
      >
        {{ t('admin.providers.clearAllModels') }}
      </button>
    </div>

    <!-- Custom Model Input -->
    <p
      v-if="unobservedQoderSelections.length"
      class="mb-3 text-xs text-amber-600 dark:text-amber-400"
    >
      {{
        t('admin.providers.qoder.modelsNotObserved', {
          models: unobservedQoderSelections.join(', '),
        })
      }}
    </p>
    <div class="mb-3">
      <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">{{
        t('admin.providers.customModelName')
      }}</label>
      <div class="flex gap-2">
        <input
          v-model="customModel"
          type="text"
          class="input flex-1"
          :placeholder="t('admin.providers.enterCustomModelName')"
          @keydown.enter.prevent="handleEnter"
          @compositionstart="isComposing = true"
          @compositionend="isComposing = false"
        />
        <button
          type="button"
          @click="addCustom"
          class="rounded-control bg-primary-50 px-4 py-2 text-sm font-medium text-primary-600 hover:bg-primary-100 dark:bg-primary-900/30 dark:text-primary-400 dark:hover:bg-primary-900/50"
        >
          {{ t('admin.providers.addModel') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { prepareListLeave, restoreEnteringElement } from '@/utils/leavingElement'

import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { providersAPI } from '@/api/admin/providers'
import type { SyncUpstreamModelsResult, SyncUpstreamPreviewParams } from '@/api/admin/providers'
import { useClipboard } from '@/composables/useClipboard'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { modelAttributesAPI } from '@/api/admin/modelAttributes'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'

// ModelWhitelistSelector.vue edits selected model IDs alongside catalog or
// caller-supplied suggestions. An omitted models prop enables paginated catalog lookup;
// an explicit empty array supplies no suggestions. Selected IDs remain owned by the form
// and can include values absent from either source. Catalog lookup never removes those
// selections.
//
// Search changes and dropdown visibility advance a generation before scheduling the next
// query. Only the current generation may publish its results or loading state, and
// unmount invalidates pending catalog work. Upstream synchronization follows a separate
// request path: a changed provider identity or preview credential cancels it, and a
// current completion reports the full result through the synced event. All platforms
// merge supported IDs into the current selection. Qoder also uses that account’s
// latest candidates for local suggestions and missing-name feedback.

// t resolves interface labels from the active component locale while reactive consumers
// update their displayed text.
const { t } = useI18n()

// props exposes caller-owned inputs used by the template and derived state in this
// component.
const props = defineProps<{
  // Selected model IDs remain parent-owned and are independent from explicit or catalog
  // candidate suggestions.
  modelValue: string[]
  platform?: string // Provider platform identifier.
  // A nonempty list takes precedence over platform when determining upstream
  // synchronization availability for this selector.
  platforms?: string[]
  // Explicit candidates override catalog lookup, including an empty array that
  // intentionally supplies no suggestions.
  models?: string[]
  // Previously saved raw IDs can identify current routes without being offered as suggestions.
  knownRouteKeys?: string[]
  // Saved provider identifier; when present, upstream synchronization uses it before any
  // preview credentials.
  providerId?: number
  // Unsaved credentials used for preview synchronization when a saved provider identifier
  // is unavailable.
  syncCredentials?: SyncUpstreamPreviewParams
}>()

// emit publishes typed draft changes and actions to the parent that owns this component.
const emit = defineEmits<{
  'update:modelValue': [value: string[]] // Replacement model value draft value.
  // synced reports each accepted upstream response, including optional per-model
  // protocol suggestions, before its IDs are merged into the selection.
  synced: [result: SyncUpstreamModelsResult]
}>()

// appStore displays operation feedback through the shared notification area used by the
// provider administration screens.
const appStore = useAppStore()
// copyToClipboard owns browser clipboard access and reports its outcome through the
// shared notification behavior.
const { copyToClipboard } = useClipboard()

// showDropdown controls candidate visibility and participates in invalidating pending
// catalog queries when the menu closes.
const showDropdown = ref(false)
// searchQuery filters explicit candidates locally or supplies the trimmed query for a
// debounced catalog request.
const searchQuery = ref('')
// customModel stores the administrator-entered ID until a successful addition clears the
// draft field for another entry.
const customModel = ref('')
// isComposing tracks IME composition so Enter can finish text entry before a custom model
// is added.
const isComposing = ref(false)
// isSyncingUpstream prevents overlapping sync actions while the saved-provider or preview
// request is waiting for upstream model IDs.
const isSyncingUpstream = ref(false)
// syncRequest identifies the synchronization allowed to publish. Replacing or clearing
// it invalidates older responses even when the inputs later return to their old values.
let syncRequest: AbortController | undefined
// syncedQoderModels keeps account suggestions separate from the editable whitelist.
// Undefined means the current form has not completed a manual sync for this identity.
const syncedQoderModels = ref<string[]>()
// unobservedQoderSelections identifies saved names missing from the latest account
// response; explicit custom routes remain editable and are not silently removed.
const unobservedQoderSelections = computed(() => {
  if (!props.providerId || !normalizedPlatforms.value.includes('qoder')) return []
  const models = syncedQoderModels.value ?? props.models
  if (!models) return []
  const known = new Set(
    [...models, ...(props.knownRouteKeys ?? [])].map((model) => model.toLowerCase()),
  )
  return props.modelValue.filter((model) => !model.includes('*') && !known.has(model.toLowerCase()))
})
// normalizedPlatforms trims and deduplicates the preferred platform list for determining
// whether the upstream synchronization action is available.
const normalizedPlatforms = computed(() => {
  const rawPlatforms =
    props.platforms && props.platforms.length > 0
      ? props.platforms
      : props.platform
        ? [props.platform]
        : []

  return Array.from(
    new Set(
      rawPlatforms
        .map((platform) => platform?.trim())
        .filter((platform): platform is string => Boolean(platform)),
    ),
  )
})

// upstreamSyncPlatforms lists the platforms accepted by this UI for upstream
// synchronization; the endpoint still validates the request.
const upstreamSyncPlatforms = new Set([
  'anthropic',
  'openai',
  'gemini',
  'antigravity',
  'grok',
  'kimi',
  'zhipu',
  'deepseek',
  'jev',
  'opencode',
  'qoder',
])
// canSyncUpstream selects saved-provider or preview-credential eligibility from the
// normalized platforms before the template shows its synchronization action.
const canSyncUpstream = computed(() => {
  if (props.providerId) {
    if (normalizedPlatforms.value.length === 0) return true
    return normalizedPlatforms.value.some((platform) =>
      upstreamSyncPlatforms.has(platform.toLowerCase()),
    )
  }
  if (props.syncCredentials) {
    if (props.syncCredentials.platform === 'qoder') return false
    return upstreamSyncPlatforms.has(props.syncCredentials.platform.toLowerCase())
  }
  return false
})

// catalogModels stores fetched suggestions independently from the selected IDs owned by
// the parent provider form.
const catalogModels = ref<string[]>([])
// catalogPage records the most recently accepted catalog page so load-more requests
// advance from the published result.
const catalogPage = ref(0)
// catalogTotal retains the server result count used to decide whether additional catalog
// pages can be requested.
const catalogTotal = ref(0)
// loadingCatalog belongs to the current query generation and cannot be cleared by an
// older request completion.
const loadingCatalog = ref(false)
// catalogFailed records failure for the current query generation so the dropdown can
// display its retry action.
const catalogFailed = ref(false)
// queryVersion invalidates older catalog responses on searches, visibility changes, and
// unmount before they can publish candidate state.
let queryVersion = 0
// searchTimer owns the pending debounce callback and is cleared when query inputs change
// or the component unmounts.
let searchTimer: ReturnType<typeof setTimeout> | undefined

// loadCatalog loads one page of catalog suggestions for the current search. Supplying
// models disables catalog fetching, including when that array is empty. Each request
// advances queryVersion and captures its page before awaiting the API.
//
// Only the current generation may replace or append IDs, record an error, or clear
// loading. Successful pages deduplicate IDs and leave the selected model array untouched;
// failures retain any already displayed suggestions for retry.
const loadCatalog = async (append = false) => {
  if (props.models !== undefined) return
  const version = ++queryVersion
  const page = append ? catalogPage.value + 1 : 1
  loadingCatalog.value = true
  catalogFailed.value = false
  try {
    // The catalog page size is fifty suggestions per request, limiting dropdown payloads
    // while load-more retains access to later matches.
    const result = await modelAttributesAPI.defaults({
      search: searchQuery.value.trim(),
      page,
      page_size: 50,
    })
    // A newer search or lifecycle change advances the generation, so this response cannot
    // replace suggestions belonging to the current query.
    if (version !== queryVersion) return
    catalogModels.value = [
      ...new Set([
        ...(append ? catalogModels.value : []),
        ...result.items.map((item) => item.model),
      ]),
    ]
    catalogPage.value = page
    catalogTotal.value = result.total
  } catch {
    // Only the active query may show a failure; an older rejected request belongs to
    // suggestions that have already been invalidated.
    if (version === queryVersion) catalogFailed.value = true
  } finally {
    if (version === queryVersion) loadingCatalog.value = false
  }
}

// Invalidate outstanding responses before clearing suggestions and scheduling another
// debounce, so earlier completion cannot refill a closed or changed dropdown.
watch([showDropdown, searchQuery, () => props.models], () => {
  queryVersion++
  clearTimeout(searchTimer)
  loadingCatalog.value = false
  catalogFailed.value = false
  catalogModels.value = []
  catalogTotal.value = 0
  if (showDropdown.value && props.models === undefined) {
    searchTimer = setTimeout(() => {
      void loadCatalog()
    }, SEARCH_DEBOUNCE_MS)
  }
})
// cancelUpstreamSync aborts the active synchronization request and releases its busy
// state so another target can be synchronized immediately.
const cancelUpstreamSync = () => {
  syncedQoderModels.value = undefined
  syncRequest?.abort()
  syncRequest = undefined
  isSyncingUpstream.value = false
}

// A new provider identity or preview credential object means the pending response
// describes another upstream target. Cancel synchronously, before a later change can
// restore the original values and make the stale response look current.
watch([() => props.providerId, () => props.syncCredentials], cancelUpstreamSync, {
  flush: 'sync',
})

// Unmount invalidates pending responses and clears the scheduled callback, preventing
// either source from publishing another catalog update into this component. It also
// cancels synchronization so a closed form cannot receive upstream results.
onBeforeUnmount(() => {
  queryVersion++
  clearTimeout(searchTimer)
  cancelUpstreamSync()
})

// filteredModels filters explicit candidates locally or displays fetched candidates, then
// deduplicates IDs without rewriting the selected model array.
const filteredModels = computed(() => {
  const query = searchQuery.value.toLowerCase().trim()
  const ids =
    syncedQoderModels.value !== undefined
      ? syncedQoderModels.value.filter((id) => id.toLowerCase().includes(query))
      : props.models === undefined
        ? catalogModels.value
        : props.models.filter((id) => id.toLowerCase().includes(query))
  return [...new Set(ids)].map((id) => ({ value: id, label: id }))
})

// toggleDropdown toggles suggestion visibility and clears the query when closing the
// menu. The visibility watcher invalidates pending catalog work and schedules a fresh
// lookup when the menu opens again.
const toggleDropdown = () => {
  showDropdown.value = !showDropdown.value
  if (!showDropdown.value) searchQuery.value = ''
}

// removeModel emits a selection with every occurrence of the requested model removed. The
// parent receives a new array and decides when that selection enters its provider
// payload.
const removeModel = (model: string) => {
  emit(
    'update:modelValue',
    props.modelValue.filter((m) => m !== model),
  )
}

// toggleModel adds an unselected model or removes a selected model from the draft. It
// emits a replacement array, leaving the parent responsible for applying and persisting
// the update.
const toggleModel = (model: string) => {
  if (props.modelValue.includes(model)) {
    removeModel(model)
  } else {
    emit('update:modelValue', [...props.modelValue, model])
  }
}

// copyModelId copies the model identifier through the shared clipboard notification
// helper. Its copy button is a sibling of the selection button, so activating
// the copy control does not invoke the selection button's toggle handler.
const copyModelId = async (model: string) => {
  await copyToClipboard(model)
}

// addCustom adds a trimmed custom identifier to the current selection. Blank input is
// ignored, duplicates show informational feedback, and a successful addition clears the
// custom text field.
const addCustom = () => {
  const model = customModel.value.trim()
  if (!model) return
  if (props.modelValue.includes(model)) {
    appStore.showInfo(t('admin.providers.modelExists'))
    return
  }
  emit('update:modelValue', [...props.modelValue, model])
  customModel.value = ''
}

// handleEnter adds the custom identifier when the input is outside an IME composition.
// Composition events own the guard so Enter used to finish text entry does not submit it
// early.
const handleEnter = () => {
  if (!isComposing.value) addCustom()
}

// syncUpstreamModels merges fetched upstream identifiers into the selected model draft. A
// saved provider ID takes precedence over preview credentials, and an in-progress
// synchronization suppresses another invocation. Every accepted response is emitted as
// synced; empty upstream results then produce informational feedback and leave the
// selection untouched.
//
// Each invocation owns an AbortController recorded in syncRequest. A provider or
// credential change, unmount, or newer invocation replaces that owner, so a late
// response or failure from the earlier target publishes nothing. The merge reads the
// current selected array after the request resolves, trims fetched IDs, and appends only
// missing values.
const syncUpstreamModels = async () => {
  if (isSyncingUpstream.value) return
  const providerId = props.providerId
  const credentials = props.syncCredentials
  if (!providerId && !credentials) return

  const controller = new AbortController()
  syncRequest = controller
  isSyncingUpstream.value = true
  try {
    // A saved provider ID selects server-held credentials before preview input, so edit
    // forms synchronize using the persisted provider identity.
    const result = providerId
      ? await providersAPI.syncUpstreamModels(providerId, controller.signal)
      : await providersAPI.syncUpstreamModelsPreview(credentials!, controller.signal)
    // The target changed or the form closed while waiting; the response belongs to a
    // different provider configuration and must not reach the current draft.
    if (syncRequest !== controller) return
    emit('synced', result)

    const upstreamModels = result.models.map((model) => model.trim()).filter(Boolean)
    let newModels = [...props.modelValue]
    if (normalizedPlatforms.value.includes('qoder')) {
      // Older drafts can contain both representations of the same route. Normalize
      // the current account's enabled keys on manual sync and preserve custom entries.
      syncedQoderModels.value = upstreamModels
      const displayIds = new Map<string, string>()
      for (const model of result.sync_status?.model_details ?? []) {
        if (model.enabled && model.alias) {
          displayIds.set(model.key.toLowerCase(), model.alias)
          displayIds.set(model.alias.toLowerCase(), model.alias)
        }
      }
      newModels = [...new Set(newModels.map((id) => displayIds.get(id.trim().toLowerCase()) ?? id))]
    }
    if (upstreamModels.length === 0) {
      appStore.showInfo(t('admin.providers.syncUpstreamModelsEmpty'))
      return
    }

    let addedCount = 0
    for (const model of upstreamModels) {
      if (!newModels.includes(model)) {
        newModels.push(model)
        addedCount += 1
      }
    }

    emit('update:modelValue', newModels)
    if (addedCount > 0) {
      appStore.showSuccess(
        t('admin.providers.syncUpstreamModelsSuccess', {
          count: addedCount,
          total: upstreamModels.length,
        }),
      )
    } else {
      appStore.showInfo(
        t('admin.providers.syncUpstreamModelsNoChanges', { count: upstreamModels.length }),
      )
    }
  } catch (error) {
    // Canceled or superseded requests fail after their owner was replaced; only the
    // current request reports an error.
    if (syncRequest !== controller) return
    const message =
      error instanceof Error ? error.message : t('admin.providers.syncUpstreamModelsFailed')
    appStore.showError(t('admin.providers.syncUpstreamModelsError', { message }))
  } finally {
    // A canceled invocation can settle after another starts, so only the current owner
    // releases the busy state.
    if (syncRequest === controller) {
      syncRequest = undefined
      isSyncingUpstream.value = false
    }
  }
}

// clearAll emits an empty selected-model array to the owning provider form.
const clearAll = () => {
  emit('update:modelValue', [])
}
</script>
