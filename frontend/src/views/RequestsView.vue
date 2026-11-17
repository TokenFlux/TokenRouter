<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import RequestIdSearch from '@/components/common/RequestIdSearch.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import RequestDetailCard from '@/components/requests/RequestDetailCard.vue'
import OpsErrorDetailModal from '@/views/admin/ops/components/OpsErrorDetailModal.vue'
import Icon from '@/components/icons/Icon.vue'
import { findRequests, type RequestDetail } from '@/api/requests'

// RequestsView.vue looks up administrator request details from the request_id route query.
// Search submission writes the trimmed identifier into the route, and submitting the same
// identifier reloads it directly. Each response can contain several requests sharing an
// external alias.
//
// A new lookup aborts the previous controller before clearing results. Response and error
// handlers check their own signal, while finalization checks controller identity, so a
// superseded lookup cannot publish records or stop the current loading indicator. Unmount
// aborts the active request. The selected error record opens a separate administrator
// detail modal.

// Route and router bindings keep the search input synchronized with the administrator
// request lookup URL.
const route = useRoute()
const router = useRouter()
// The translation binding supplies empty-state, failure, and search labels for
// administrator request lookup results.
const { t } = useI18n()

// Search state separates input, retrieval progress, completed-search status, failure text,
// and matched request records for the current route.
const requestId = ref('')
const searchInput = ref<InstanceType<typeof RequestIdSearch> | null>(null)
const loading = ref(false)
const searched = ref(false)
const error = ref('')
const items = ref<RequestDetail[]>([])
const hasMore = ref(false)
// Error detail state retains the clicked error identifier while the nested administrator
// modal is open.
const selectedError = ref<number | null>(null)
const showError = ref(false)
// controller owns the active lookup cancellation signal and identifies which request may
// finish the loading state.
let controller: AbortController | undefined

// load cancels the previous lookup and searches the trimmed input identifier. An empty
// identifier clears results and returns to the unsearched state without sending an API
// request.
//
// Each request captures its own controller and checks its signal before publishing results
// or failure text. The finally block clears loading only when that controller still owns
// the active lookup. Aborted older requests therefore cannot replace a newer result or hide
// its loading state, even when their promises settle late.
async function load() {
  // Abort the previous list owner before installing the new controller so late completion
  // can identify that it no longer owns publication.
  controller?.abort()
  const current = new AbortController()
  controller = current
  const id = requestId.value.trim()
  items.value = []
  hasMore.value = false
  error.value = ''
  if (!id) {
    searched.value = false
    loading.value = false
    return
  }
  loading.value = true
  try {
    const response = await findRequests(id, current.signal)
    if (current.signal.aborted) return
    items.value = response.items || []
    hasMore.value = response.has_more
    searched.value = true
  } catch (cause: unknown) {
    // Ignore failures from an aborted lookup before selecting its message, preventing
    // superseded requests from replacing the current search feedback.
    if (current.signal.aborted) return
    const message = (
      cause as {
        message?: unknown // optional request failure message
      }
    )?.message
    error.value = typeof message === 'string' && message ? message : t('requests.loadFailed')
  } finally {
    if (controller === current) loading.value = false
  }
}

// search trims the input and writes it to the request_id query parameter. When the route
// already contains that identifier, it reloads directly because the route watcher would see
// no change.
async function search() {
  const id = requestId.value.trim()
  if (id === (route.query.request_id ?? '')) {
    await load()
    return
  }
  await router.replace({ query: { request_id: id || undefined } })
}

// openError records the selected error identifier and opens its administrator detail modal.
function openError(id: number) {
  selectedError.value = id
  showError.value = true
}

watch(
  () => [route.path, route.query.request_id],
  () => {
    requestId.value = typeof route.query.request_id === 'string' ? route.query.request_id : ''
    void load()
  },
  { immediate: true },
)
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <AppLayout>
    <div class="space-y-4">
      <!-- Search controls -->
      <div class="flex flex-wrap items-center gap-2">
        <RequestIdSearch
          ref="searchInput"
          v-model="requestId"
          class="flex-1 sm:w-64 sm:flex-none"
          @search="search"
        />
        <button
          type="button"
          class="btn btn-primary"
          :disabled="loading"
          @click="searchInput?.apply()"
        >
          {{ t('common.search') }}
        </button>
      </div>

      <div
        v-if="loading"
        class="card overflow-hidden"
        role="status"
        aria-busy="true"
        :aria-label="t('common.loading')"
        data-loading-skeleton
      >
        <div
          class="flex flex-wrap items-start justify-between gap-x-4 gap-y-2 border-b border-gray-100 px-6 py-4 dark:border-dark-700"
          aria-hidden="true"
        >
          <div class="flex min-w-0 items-center gap-2">
            <Skeleton :width="66" :height="20" class="shrink-0 rounded-full" />
            <Skeleton :width="36" :height="18" class="shrink-0" />
            <Skeleton :width="116" :height="14" class="min-w-0" />
          </div>
          <Skeleton :width="168" :height="14" class="max-w-full" />
        </div>
        <div class="grid grid-cols-2 gap-4 px-6 py-4 md:grid-cols-4" aria-hidden="true">
          <div
            v-for="field in [
              t('requests.startedAt'),
              t('requests.duration'),
              t('requests.statusCode'),
              t('requests.model'),
            ]"
            :key="field"
            class="min-w-0"
          >
            <p class="text-xs text-gray-500 dark:text-dark-400">{{ field }}</p>
            <Skeleton :width="92" :height="14" class="my-1.5 max-w-full" />
          </div>
        </div>
      </div>

      <!-- Lookup failure -->
      <div v-else-if="error" class="card" role="alert">
        <EmptyState :title="t('requests.loadFailed')" :description="error">
          <template #icon>
            <Icon name="exclamationTriangle" class="empty-state-icon h-10 w-10" />
          </template>
          <template #action>
            <button type="button" class="btn btn-secondary" @click="load">
              {{ t('common.retry') }}
            </button>
          </template>
        </EmptyState>
      </div>

      <!-- No identifier selected -->
      <div v-else-if="!searched" class="card">
        <EmptyState :title="t('requests.emptyTitle')" :description="t('requests.emptyDescription')">
          <template #icon>
            <Icon name="search" class="empty-state-icon h-10 w-10" />
          </template>
        </EmptyState>
      </div>

      <!-- No matching request -->
      <div v-else-if="!items.length" class="card">
        <EmptyState :title="t('requests.notFoundTitle')" :description="t('requests.notFound')" />
      </div>

      <template v-else>
        <p v-if="items.length > 1" class="text-sm text-gray-500 dark:text-dark-300">
          {{ t('requests.matchCount', { count: items.length }) }}
        </p>
        <SettingsNotice v-if="hasMore" tone="warning">{{ t('requests.more') }}</SettingsNotice>
        <RequestDetailCard
          v-for="item in items"
          :key="`${item.request_id}:${item.usage?.[0]?.id ?? ''}:${item.errors?.[0]?.id ?? ''}:${item.audit_ids?.[0] ?? ''}`"
          :item="item"
          @open-error="openError"
        />
      </template>
    </div>

    <OpsErrorDetailModal v-model:show="showError" :error-id="selectedError" error-type="request" />
  </AppLayout>
</template>
