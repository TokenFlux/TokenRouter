<template>
  <div :class="flat ? 'p-4' : 'card p-6'">
    <div class="space-y-4">
      <div class="flex items-center justify-between gap-2">
        <!-- Unselected predicates may be undefined; passing null matches the option lists' unfiltered entry while retaining the query object values. -->
        <FilterDropdown :active-count="activeFilterCount" keep-mounted @reset="resetPanelFilters">
          <!-- Request-ID filtering removes date bounds from queries; the user ranking uses its own supported filter dimensions. -->
          <RequestIdFilterField
            v-if="mode !== 'ranking'"
            v-model="filters.request_id"
            @search="emitChange"
          />
          <FilterField v-if="mode === 'usage'" :label="t('admin.usage.teamFilter')" icon="users">
            <FilterOptions
              :model-value="filters.team_id ?? null"
              :options="teamOptions"
              searchable
              @update:model-value="filters.team_id = $event"
              @change="emitChange"
            />
          </FilterField>

          <FilterField v-if="mode === 'usage'" :label="t('admin.usage.requestSource')" icon="send">
            <FilterOptions
              :model-value="filters.request_source ?? null"
              :options="requestSourceOptions"
              @update:model-value="filters.request_source = $event"
              @change="emitChange"
            />
          </FilterField>

          <FilterField :label="t('admin.usage.userFilter')" icon="user">
            <FilterOptions
              remote
              :model-value="filters.user_id ?? null"
              :options="userOptions"
              :selected-label="userLabel"
              :search-placeholder="t('admin.usage.searchUserPlaceholder')"
              @search="debounceUserSearch"
              @change="onUserChange"
            >
              <template #icon="{ value }">
                <UserAvatar :user-id="Number(value)" size-class="h-4 w-4" />
              </template>
              <template #option="{ option, label }">
                <span class="min-w-0 flex-1 truncate"
                  >{{ label
                  }}<span v-if="option?.deleted" class="usage-filter-meta ml-1"
                    >（{{ t('admin.usage.userDeletedBadge') }}）</span
                  ></span
                >
                <span v-if="option" class="usage-filter-meta">#{{ option.value }}</span>
              </template>
            </FilterOptions>
          </FilterField>

          <FilterField :label="t('usage.apiKeyFilter')" icon="key">
            <FilterOptions
              remote
              :model-value="filters.api_key_id ?? null"
              :options="apiKeyOptions"
              :selected-label="apiKeyLabel"
              :search-placeholder="t('admin.usage.searchApiKeyPlaceholder')"
              @search="debounceApiKeySearch"
              @change="onApiKeyChange"
            >
              <template #option="{ option, label }">
                <span class="min-w-0 flex-1 truncate">{{ label }}</span>
                <span v-if="option" class="usage-filter-meta">#{{ option.value }}</span>
              </template>
            </FilterOptions>
          </FilterField>

          <FilterField :label="t('usage.model')" icon="cube">
            <FilterOptions
              :model-value="filters.model ?? null"
              :options="modelOptions"
              searchable
              @update:model-value="filters.model = $event"
              @change="emitChange"
            >
              <template #icon="{ value }">
                <ModelIcon :model="String(value)" size="16px" hide-fallback />
              </template>
            </FilterOptions>
          </FilterField>

          <FilterField :label="t('admin.usage.provider')" icon="server">
            <FilterOptions
              remote
              :model-value="filters.provider_id ?? null"
              :options="providerOptions"
              :selected-label="providerLabel"
              :search-placeholder="t('admin.usage.searchProviderPlaceholder')"
              @search="debounceProviderSearch"
              @change="onProviderChange"
            >
              <template #option="{ option, label }">
                <span class="min-w-0 flex-1 truncate">{{ label }}</span>
                <span v-if="option" class="usage-filter-meta">#{{ option.value }}</span>
              </template>
            </FilterOptions>
          </FilterField>

          <!-- Request mode for usage and ranking filters. -->
          <FilterField v-if="mode !== 'errors'" :label="t('usage.type')" icon="bolt">
            <FilterOptions
              :model-value="filters.request_type ?? null"
              :options="requestTypeOptions"
              @update:model-value="filters.request_type = $event"
              @change="emitChange"
            />
          </FilterField>

          <!-- Billing source for usage and ranking filters. -->
          <FilterField
            v-if="mode !== 'errors'"
            :label="t('admin.usage.billingType')"
            icon="creditCard"
          >
            <FilterOptions
              :model-value="filters.billing_type ?? null"
              :options="billingTypeOptions"
              @update:model-value="filters.billing_type = $event"
              @change="emitChange"
            />
          </FilterField>

          <!-- Billing-mode selection belongs to usage records; ranking accepts the dimensions supported by its own API. -->
          <FilterField
            v-if="mode === 'usage'"
            :label="t('admin.usage.billingMode')"
            icon="calculator"
          >
            <FilterOptions
              :model-value="filters.billing_mode ?? null"
              :options="billingModeOptions"
              @update:model-value="filters.billing_mode = $event"
              @change="emitChange"
            />
          </FilterField>

          <!-- Native compaction selection applies to usage records and the ranking query. -->
          <FilterField
            v-if="mode !== 'errors'"
            :label="t('usage.compactionFilter')"
            icon="minimize"
          >
            <FilterOptions
              :model-value="filters.native_compaction_v2 ?? null"
              :options="compactionOptions"
              @update:model-value="filters.native_compaction_v2 = $event"
              @change="emitChange"
            />
          </FilterField>

          <!-- Error phase selector. -->
          <FilterField v-if="mode === 'errors'" :label="t('admin.ops.errorLog.type')" icon="layers">
            <FilterOptions
              :model-value="filters.error_phase ?? null"
              :options="errorPhaseOptions"
              @update:model-value="filters.error_phase = $event"
              @change="emitChange"
            />
          </FilterField>

          <!-- Error category selector. -->
          <FilterField v-if="mode === 'errors'" :label="t('usage.errors.category')" icon="tag">
            <FilterOptions
              :model-value="filters.error_category ?? null"
              :options="errorCategoryOptions"
              @update:model-value="filters.error_category = $event"
              @change="emitChange"
            />
          </FilterField>

          <!-- Error HTTP status selector. -->
          <FilterField
            v-if="mode === 'errors'"
            :label="t('admin.ops.errorLog.status')"
            icon="exclamationCircle"
          >
            <FilterOptions
              :model-value="filters.status_code ?? null"
              :options="statusCodeOptions"
              @update:model-value="filters.status_code = $event"
              @change="emitChange"
            />
          </FilterField>

          <!-- Group filter -->
          <FilterField :label="t('admin.usage.group')" icon="folder">
            <FilterOptions
              :model-value="filters.group_id ?? null"
              :options="groupOptions"
              searchable
              @update:model-value="filters.group_id = $event"
              @change="emitChange"
            >
              <template #icon="{ option }">
                <ProviderIcon
                  v-if="option?.brand"
                  :brand="String(option.brand)"
                  size="16px"
                  hide-fallback
                />
              </template>
            </FilterOptions>
          </FilterField>
        </FilterDropdown>

        <div v-if="showActions" class="flex flex-wrap items-center justify-end gap-2">
          <button
            type="button"
            @click="$emit('refresh')"
            class="btn btn-secondary btn-icon"
            :title="t('common.refresh')"
          >
            <Icon name="refresh" size="sm" />
          </button>
          <slot name="after-reset" />
          <template v-if="mode === 'usage'">
            <button
              type="button"
              @click="$emit('cleanup')"
              class="btn btn-danger whitespace-nowrap px-3 sm:px-4"
            >
              {{ t('admin.usage.cleanup.button') }}
            </button>
            <button
              type="button"
              @click="$emit('export')"
              :disabled="exporting"
              class="btn btn-primary whitespace-nowrap px-3 sm:px-4"
            >
              {{ t('usage.exportExcel') }}
            </button>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import RequestIdFilterField from '@/components/common/RequestIdFilterField.vue'
import { ref, onMounted, onUnmounted, toRef, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { SelectOption } from '@/components/common/Select.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import {
  BILLING_MODE_ICONS,
  BILLING_TYPE_ICONS,
  COMPACTION_ICONS,
  ERROR_CATEGORY_ICONS,
  ERROR_PHASE_ICONS,
  REQUEST_SOURCE_ICONS,
  REQUEST_TYPE_ICONS,
  withOptionIcons,
} from '@/components/common/filterOptionIcons'
import ModelIcon from '@/components/common/ModelIcon.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { COMMON_ERROR_STATUS_CODES } from '@/utils/errorBadges'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'
import type { SimpleApiKey, SimpleUser } from '@/api/admin/usage'

// UsageFilters.vue edits shared administrator usage filters and provides user, API-key, and
// provider searches. The modelValue object remains parent-owned and is mutated through its
// ref; change events ask the page to reload. Usage, error, and ranking modes expose
// different filter subsets.
//
// The three searches use remote FilterOptions lists: this component turns typed text into
// debounced requests and keeps the returned candidates, while each list keeps its own
// search text. A selected identity can fall outside the latest candidates, so a separate
// label per search names it for the pinned row and the collapsed summary.
//
// Two revisions guard against late responses. The user search revision advances with each
// typed query so an older response cannot replace newer candidates. The user label revision
// advances whenever the label changes, and the page compares it before applying a label
// looked up from the route, so a later selection keeps its own label. API-key and provider
// search use their own debounce timers without a response guard. Date changes update the
// shared object, while panel reset clears only non-date fields.

// ModelValue holds mutable query predicates shared with the parent usage page.
type ModelValue = Record<string, any>

// Props controls filter visibility, shared query state, and caller-provided model choices.
interface Props {
  // modelValue is the parent-owned query object mutated through filters; callers handle
  // change events to reload its current values.
  modelValue: ModelValue
  exporting: boolean // export operation busy flag
  startDate: string // beginning calendar date
  endDate: string // ending calendar date
  showActions?: boolean // action button visibility flag
  modelOptions?: string[] // parent-provided model name choices
  // mode selects usage filters by default; errors substitutes error predicates and ranking
  // hides billing mode and destructive/export actions.
  mode?: 'usage' | 'errors' | 'ranking'
  // flat removes the component card surface when the parent already supplies the shared
  // filter section container.
  flat?: boolean
}

// props supplies usage-mode defaults and parent-owned values used to render and edit the
// filter panel.
const props = withDefaults(defineProps<Props>(), {
  showActions: true,
  mode: 'usage',
  flat: false,
})
// emit publishes model changes and reload, reset, export, or cleanup actions for the owning
// page.
const emit = defineEmits(['update:modelValue', 'change', 'refresh', 'reset', 'export', 'cleanup'])

// Translation supplies option labels while filters keeps a ref to the parent-owned mutable
// query object.
const { t } = useI18n()
const filters = toRef(props, 'modelValue')

// User search state tracks returned candidates, debounce ownership, and the current
// response revision; userLabel names the selected user and userLabelRevision counts its
// changes for route-label hydration.
const userResults = ref<SimpleUser[]>([])
let userSearchTimeout: ReturnType<typeof setTimeout> | null = null
let userSearchSequence = 0
const userLabel = ref('')
let userLabelRevision = 0

// API-key search state tracks candidates scoped to the selected user, the selected key's
// label, and the last typed text, which a user change reuses to refresh that scope.
const apiKeyResults = ref<SimpleApiKey[]>([])
const apiKeyLabel = ref('')
const apiKeyQuery = ref('')
let apiKeySearchTimeout: ReturnType<typeof setTimeout> | null = null

// SimpleProvider contains provider identity and name for remote provider selection.
interface SimpleProvider {
  id: number // provider record identifier
  name: string // provider display name
}
// Provider search state tracks the returned candidates and the selected provider's label.
const providerResults = ref<SimpleProvider[]>([])
const providerLabel = ref('')
let providerSearchTimeout: ReturnType<typeof setTimeout> | null = null

// userOptions, apiKeyOptions, and providerOptions convert search candidates into option
// rows; the deleted flag lets the user row show its badge.
const userOptions = computed<SelectOption[]>(() =>
  userResults.value.map((u) => ({ value: u.id, label: u.email, deleted: u.deleted })),
)
const apiKeyOptions = computed<SelectOption[]>(() =>
  apiKeyResults.value.map((k) => ({ value: k.id, label: k.name || `#${k.id}` })),
)
const providerOptions = computed<SelectOption[]>(() =>
  providerResults.value.map((p) => ({ value: p.id, label: p.name })),
)

// modelOptions prepends the unfiltered choice to the model names supplied by the parent
// usage page.
const modelOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allModels') },
  ...(props.modelOptions ?? []).map((m) => ({ value: m, label: m })),
])
// Group and team choices begin with an unfiltered option and receive independent additions
// after mounting; group choices also carry the display brand drawn as their row icon.
const groupOptions = ref<SelectOption[]>([{ value: null, label: t('admin.usage.allGroups') }])
const teamOptions = ref<SelectOption[]>([{ value: null, label: t('admin.usage.allTeams') }])
// activeFilterCount counts nonempty predicates outside dates, excluding request ID when the
// ranking mode hides that field.
const activeFilterCount = computed(
  () =>
    Object.entries(filters.value).filter(
      ([key, value]) =>
        !['start_date', 'end_date'].includes(key) &&
        !(key === 'request_id' && props.mode === 'ranking') &&
        !(key === 'request_source' && props.mode !== 'usage') &&
        value !== null &&
        value !== undefined &&
        String(value) !== '',
    ).length,
)

// requestSourceOptions separates customer usage from the internal model work recorded
// by probes and tests. The empty option retains the administrator's complete view.
const requestSourceOptions = computed(() =>
  withOptionIcons(
    [
      { value: null, label: t('admin.usage.sourcesAll') },
      ...['user', 'system', 'group_probe', 'provider_test', 'scheduled_test', 'quota_probe'].map(
        (value) => ({
          value,
          label: t(`admin.usage.requestSources.${value}`),
        }),
      ),
    ],
    REQUEST_SOURCE_ICONS,
  ),
)

// requestTypeOptions supplies the canonical request modes accepted by usage filtering and
// their translated display labels.
const requestTypeOptions = ref<SelectOption[]>(
  withOptionIcons(
    [
      { value: null, label: t('admin.usage.allTypes') },
      { value: 'ws_v2', label: t('usage.ws') },
      { value: 'live', label: t('usage.live') },
      { value: 'stream', label: t('usage.stream') },
      { value: 'sync', label: t('usage.sync') },
      { value: 'cyber', label: t('usage.cyber') },
    ],
    REQUEST_TYPE_ICONS,
  ),
)

// billingTypeOptions distinguishes balance and subscription billing while null clears the
// billing-source predicate for requests.
const billingTypeOptions = ref<SelectOption[]>(
  withOptionIcons(
    [
      { value: null, label: t('admin.usage.allBillingTypes') },
      { value: 0, label: t('admin.usage.billingTypeBalance') },
      { value: 1, label: t('admin.usage.billingTypeSubscription') },
    ],
    BILLING_TYPE_ICONS,
  ),
)

// errorPhaseOptions maps backend phase identifiers to the same translated categories used
// by error type badges.
const errorPhaseOptions = computed<SelectOption[]>(() =>
  withOptionIcons(
    [
      { value: null, label: t('admin.usage.allTypes') },
      { value: 'upstream', label: t('admin.ops.errorLog.typeUpstream') },
      { value: 'provider_auth', label: t('admin.ops.errorLog.typeProviderAuth') },
      { value: 'request', label: t('admin.ops.errorLog.typeRequest') },
      { value: 'auth', label: t('admin.ops.errorLog.typeAuth') },
      { value: 'routing', label: t('admin.ops.errorLog.typeRouting') },
      { value: 'internal', label: t('admin.ops.errorLog.typeInternal') },
    ],
    ERROR_PHASE_ICONS,
  ),
)

// errorCategoryCodes lists searchable backend categories; the display-only Other bucket
// cannot be translated into a query predicate.
const errorCategoryCodes = [
  'auth',
  'rate_limit',
  'quota',
  'invalid_request',
  'service_unavailable',
  'upstream',
  'internal',
  'cyber',
]

// errorCategoryOptions translates searchable category codes and adds the null choice that
// clears error category filtering.
const errorCategoryOptions = computed<SelectOption[]>(() =>
  withOptionIcons(
    [
      { value: null, label: t('usage.errors.allCategories') },
      ...errorCategoryCodes.map((c) => ({ value: c, label: t('usage.errors.categories.' + c) })),
    ],
    ERROR_CATEGORY_ICONS,
  ),
)

// statusCodeOptions uses common status codes independently of current rows so later-page
// errors remain selectable.
const statusCodeOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.errors.allStatuses') },
  ...COMMON_ERROR_STATUS_CODES.map((c) => ({ value: c, label: String(c) })),
])

// billingModeOptions maps token, request, image, and video billing modes to labels for
// usage filtering.
const billingModeOptions = ref<SelectOption[]>(
  withOptionIcons(
    [
      { value: null, label: t('admin.usage.allBillingModes') },
      { value: 'token', label: t('admin.usage.billingModeToken') },
      { value: 'per_request', label: t('admin.usage.billingModePerRequest') },
      { value: 'image', label: t('admin.usage.billingModeImage') },
      { value: 'video', label: t('admin.usage.billingModeVideo') },
    ],
    BILLING_MODE_ICONS,
  ),
)

// compactionOptions distinguishes native and legacy compaction while null removes that
// predicate from usage selection.
const compactionOptions = computed<SelectOption[]>(() =>
  withOptionIcons(
    [
      { value: null, label: t('usage.allCompactions') },
      { value: true, label: t('usage.nativeCompactionV2') },
      { value: false, label: t('usage.legacyCompaction') },
    ],
    COMPACTION_ICONS,
  ),
)

// emitChange notifies the parent that shared filter values need reloading.
const emitChange = () => emit('change')

// clearPendingUserSearch cancels the pending user debounce and advances its response
// revision. Already running requests continue, but their captured revisions become stale
// and prevent their results from being displayed.
const clearPendingUserSearch = () => {
  if (userSearchTimeout) {
    // Cancel scheduled work before advancing the revision so both pending and already
    // running user lookups lose publication authority together.
    clearTimeout(userSearchTimeout)
    userSearchTimeout = null
  }
  userSearchSequence += 1
}

// debounceUserSearch schedules a trimmed nonempty query after canceling the preceding user
// search. Empty text clears candidates immediately and still invalidates older responses
// through clearPendingUserSearch.
//
// The callback captures the new revision before waiting for the API. Success and failure
// both compare that revision before publishing candidates, so later typing or selection
// wins regardless of response order. Current successes sort active users before deleted
// users; current failures clear the candidate list.
const debounceUserSearch = (text: string) => {
  clearPendingUserSearch()
  const query = text.trim()
  if (!query) {
    userResults.value = []
    return
  }

  const sequence = userSearchSequence
  userSearchTimeout = setTimeout(async () => {
    userSearchTimeout = null
    try {
      const results = await adminAPI.usage.searchUsers(query)
      // Publish only for the captured user-search revision; later typing, clearing, or
      // selection invalidates that response even when it succeeds.
      if (sequence === userSearchSequence) {
        userResults.value = results.sort((a, b) => Number(a.deleted) - Number(b.deleted))
      }
    } catch {
      // Clear candidates only for the current search revision so an older rejected
      // request cannot erase results belonging to later input.
      if (sequence === userSearchSequence) {
        userResults.value = []
      }
    }
  }, SEARCH_DEBOUNCE_MS)
}

// debounceApiKeySearch remembers the text and replaces the pending timer before searching
// keys of the selected user. Failures clear candidates; already running searches are not
// canceled or checked for staleness.
const debounceApiKeySearch = (text: string) => {
  apiKeyQuery.value = text
  // Replacing the debounce timer coalesces rapid input; an API-key request that has
  // already started still completes independently.
  if (apiKeySearchTimeout) clearTimeout(apiKeySearchTimeout)
  apiKeySearchTimeout = setTimeout(async () => {
    try {
      apiKeyResults.value = await adminAPI.usage.searchApiKeys(filters.value.user_id, text)
    } catch {
      // Treat key lookup failure as no candidates so the selected user predicate remains
      // usable even when key discovery fails.
      apiKeyResults.value = []
    }
  }, SEARCH_DEBOUNCE_MS)
}

// onUserChange applies a user row choice from the remote list, where a null value means the
// checked row was chosen again to clear the selection.
const onUserChange = (value: SelectOption['value']) => {
  if (value === null) {
    clearUser()
    return
  }
  const user = userResults.value.find((u) => u.id === value)
  if (user) selectUser(user)
}

// setUserLabelValue replaces the selected user's label and advances the label revision.
const setUserLabelValue = (label: string) => {
  userLabelRevision += 1
  userLabel.value = label
}

// selectUser records the selected identity and email and clears the key selection. It then
// loads that user's keys for the current key search text before emitting the shared filter
// change.
const selectUser = async (u: SimpleUser) => {
  setUserLabelValue(u.email)
  filters.value.user_id = u.id
  clearApiKey()

  try {
    apiKeyResults.value = await adminAPI.usage.searchApiKeys(u.id, apiKeyQuery.value)
  } catch {
    // Keep the newly selected user and publish its filter change even when loading that
    // user's key candidates fails.
    apiKeyResults.value = []
  }

  emitChange()
}

// clearUser removes the selected user and dependent key. Key candidates belonged to the
// removed user, so they are dropped and reload when the key search next gains focus; the
// user candidates stay because they still match the list's search text.
const clearUser = () => {
  setUserLabelValue('')
  filters.value.user_id = undefined
  clearApiKey()
  apiKeyResults.value = []
  emitChange()
}

// onApiKeyChange applies a key row choice; a null value clears the selected key.
const onApiKeyChange = (value: SelectOption['value']) => {
  if (value === null) {
    clearApiKey()
    emitChange()
    return
  }
  const key = apiKeyResults.value.find((k) => k.id === value)
  if (!key) return
  apiKeyLabel.value = key.name || `#${key.id}`
  filters.value.api_key_id = key.id
  emitChange()
}

// clearApiKey clears the selected key and its label. It does not emit change itself
// because callers can combine key clearing with a user selection update.
const clearApiKey = () => {
  apiKeyLabel.value = ''
  filters.value.api_key_id = undefined
}

// resetPanelFilters clears every shared predicate except the beginning and ending dates and
// emits one change after the shared object has been updated.
const resetPanelFilters = () => {
  for (const key of Object.keys(filters.value)) {
    if (key !== 'start_date' && key !== 'end_date') filters.value[key] = undefined
  }
  emitChange()
}

// debounceProviderSearch replaces the pending provider timer and retrieves up to twenty
// matching providers. Empty text or retrieval failure clears candidates; completed requests
// do not compare a response revision.
const debounceProviderSearch = (text: string) => {
  // Replacing the debounce timer coalesces provider input; already running provider
  // requests retain their existing completion behavior.
  if (providerSearchTimeout) clearTimeout(providerSearchTimeout)
  providerSearchTimeout = setTimeout(async () => {
    if (!text) {
      providerResults.value = []
      return
    }
    try {
      const res = await adminAPI.providers.list(1, 20, { search: text })
      providerResults.value = res.items.map((a) => ({ id: a.id, name: a.name }))
    } catch {
      // Treat provider lookup failure as an empty candidate list while keeping the typed
      // provider text available for another search.
      providerResults.value = []
    }
  }, SEARCH_DEBOUNCE_MS)
}

// onProviderChange records a chosen provider identity and label before notifying the
// parent; a null value clears the provider predicate.
const onProviderChange = (value: SelectOption['value']) => {
  if (value === null) {
    providerLabel.value = ''
    filters.value.provider_id = undefined
    emitChange()
    return
  }
  const provider = providerResults.value.find((p) => p.id === value)
  if (!provider) return
  providerLabel.value = provider.name
  filters.value.provider_id = provider.id
  emitChange()
}

watch(
  () => props.startDate,
  (value) => {
    filters.value.start_date = value
  },
  { immediate: true },
)

watch(
  () => props.endDate,
  (value) => {
    filters.value.end_date = value
  },
  { immediate: true },
)

// Clear each selection label when a parent update or panel reset removes its predicate, so
// the next selection is not named by the previous one. Candidates stay because they still
// match the search text shown in their lists.
watch(
  () => filters.value.user_id,
  (userId) => {
    if (!userId && userLabel.value) setUserLabelValue('')
  },
)

watch(
  () => filters.value.api_key_id,
  (apiKeyId) => {
    if (!apiKeyId) apiKeyLabel.value = ''
  },
)

watch(
  () => filters.value.provider_id,
  (providerId) => {
    if (!providerId) providerLabel.value = ''
  },
)

onMounted(async () => {
  // Team and group choices load independently so a failure in either source leaves the
  // other filters usable.
  await Promise.allSettled([
    adminAPI.groups.list(1, 1000).then((gs) => {
      groupOptions.value.push(
        ...gs.items.map((g) => ({ value: g.id, label: g.name, brand: g.display_brand })),
      )
    }),
    adminAPI.teams.list().then((teams) => {
      teamOptions.value.push(...teams.map((team) => ({ value: team.id, label: team.name })))
    }),
  ])
})

// Unmount invalidates user search results; API-key and provider timer lifetimes remain
// independent here.
onUnmounted(() => {
  clearPendingUserSearch()
})

// setUserLabel names a user selected by the page, such as a route or ranking selection. It
// advances the label revision, so a route lookup started earlier cannot replace this newer
// label.
const setUserLabel = (email: string) => setUserLabelValue(email)

// getUserLabelRevision returns the current user label revision for route label
// validation.
const getUserLabelRevision = () => userLabelRevision

defineExpose({ getUserLabelRevision, setUserLabel })
</script>

<style scoped>
/* Candidate identifiers and badges stay secondary to the user, key, or provider name. */
.usage-filter-meta {
  @apply shrink-0 text-xs text-gray-400 dark:text-dark-400;
}
</style>
