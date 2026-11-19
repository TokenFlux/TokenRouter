<template>
  <BaseDialog
    :show="show"
    :title="title"
    width="extra-wide"
    :close-on-click-outside="true"
    @close="close"
  >
    <ContentSkeleton v-if="loading" variant="detail" :rows="6" />

    <div v-else-if="detailFailed" class="space-y-4 py-10 text-center" role="alert">
      <p class="text-sm text-gray-600 dark:text-dark-300">
        {{ t('admin.ops.failedToLoadErrorDetail') }}
      </p>
      <button type="button" class="btn btn-secondary" @click="loadDetail">
        {{ t('common.retry') }}
      </button>
    </div>
    <p v-else-if="!detail" class="py-10 text-center text-sm text-gray-500 dark:text-dark-300">
      {{ t('admin.ops.errorDetail.noErrorSelected') }}
    </p>

    <div v-else class="error-detail min-w-0 space-y-6">
      <section :aria-labelledby="`${panelId}-summary`" class="min-w-0" data-testid="error-summary">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h4
            :id="`${panelId}-summary`"
            class="text-sm font-semibold text-gray-900 dark:text-dark-100"
          >
            {{ t('admin.ops.errorDetail.summary') }}
          </h4>
          <span v-if="summary.source" class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.ops.errorDetail.summarySource', { source: payloadLabel(summary.source) }) }}
          </span>
        </div>
        <p
          class="mt-3 line-clamp-4 break-words text-base font-semibold leading-6 text-gray-900 dark:text-dark-100"
        >
          {{ summaryText(summary) }}
        </p>
        <div class="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs">
          <div class="flex items-center gap-2">
            <span class="text-gray-500 dark:text-dark-400">{{
              t('admin.ops.errorDetail.status')
            }}</span>
            <span
              class="rounded-compact px-2 py-1 font-mono font-semibold"
              :class="statusBadgeClass(detail.status_code)"
              >{{ detail.status_code }}</span
            >
          </div>
          <div class="flex items-center gap-2">
            <span class="text-gray-500 dark:text-dark-400">{{
              t('admin.ops.errorDetail.upstreamStatus')
            }}</span>
            <span
              class="rounded-compact px-2 py-1 font-mono font-semibold"
              :class="statusBadgeClass(detail.upstream_status_code)"
              >{{ detail.upstream_status_code ?? '—' }}</span
            >
          </div>
        </div>
        <dl
          class="mt-4 grid min-w-0 grid-cols-1 gap-4 rounded-surface bg-gray-50 p-4 dark:bg-dark-950 sm:grid-cols-2"
        >
          <div class="min-w-0">
            <dt class="detail-label">{{ t('admin.ops.errorDetail.provider') }}</dt>
            <dd class="detail-value">{{ detail.provider_name || detail.provider_id || '—' }}</dd>
          </div>
          <div class="min-w-0">
            <dt class="detail-label">{{ t('admin.ops.errorDetail.model') }}</dt>
            <dd class="detail-value font-mono">
              <template v-if="hasModelMapping">
                <span class="sr-only">{{ t('admin.ops.errorDetail.requestedModel') }}: </span>
                <span>{{ detail.requested_model }}</span>
                <span class="mx-1 text-gray-400" aria-hidden="true">→</span>
                <span class="sr-only">{{ t('admin.ops.errorDetail.upstreamModel') }}: </span>
                <span>{{ detail.upstream_model }}</span>
              </template>
              <template v-else>{{
                detail.upstream_model || detail.requested_model || detail.model || '—'
              }}</template>
            </dd>
          </div>
        </dl>
      </section>

      <section class="detail-section" :aria-labelledby="`${panelId}-request`">
        <h4 :id="`${panelId}-request`" class="detail-heading">
          {{ t('admin.ops.errorDetail.requestInfo') }}
        </h4>
        <dl class="mt-4 grid min-w-0 grid-cols-1 gap-x-8 gap-y-3 sm:grid-cols-2">
          <div class="detail-metadata sm:col-span-2">
            <dt class="detail-label">{{ t('admin.ops.errorDetail.requestId') }}</dt>
            <dd class="min-w-0 flex-1"><RequestIdLink :value="requestId" full /></dd>
          </div>
          <div v-for="field in requestFields" :key="field.key" class="detail-metadata">
            <dt class="detail-label">{{ t(`admin.ops.errorDetail.${field.key}`) }}</dt>
            <dd
              class="min-w-0 flex-1 break-all text-sm leading-6 text-gray-900 dark:text-dark-100"
              :class="{ 'font-mono': field.mono }"
            >
              {{ field.value }}
            </dd>
          </div>
        </dl>
      </section>

      <section
        v-if="errorType === 'request'"
        class="detail-section"
        :aria-labelledby="`${panelId}-attempts`"
        data-testid="upstream-attempts"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h4 :id="`${panelId}-attempts`" class="detail-heading">
            {{ t('admin.ops.errorDetail.upstreamAttempts') }}
          </h4>
          <span
            v-if="upstreamErrors.length"
            class="text-xs tabular-nums text-gray-500 dark:text-dark-400"
          >
            {{
              t('admin.ops.errorDetail.attemptCount', {
                count: upstreamErrors.length,
                total: upstreamTotal,
              })
            }}
          </span>
        </div>
        <ContentSkeleton
          v-if="upstreamLoading && !upstreamErrors.length"
          variant="list"
          :rows="2"
          class="mt-4"
        />
        <p
          v-else-if="!upstreamFailed && !upstreamErrors.length && upstreamTotal === 0"
          class="mt-4 text-sm text-gray-500 dark:text-dark-300"
        >
          {{ t('admin.ops.errorDetail.noUpstreamAttempts') }}
        </p>
        <ol v-if="upstreamErrors.length" class="mt-4 divide-y divide-gray-200 dark:divide-dark-700">
          <li
            v-for="(attempt, index) in upstreamAttempts"
            :key="attempt.detail.id"
            class="min-w-0 py-4 first:pt-0 last:pb-0"
          >
            <div class="flex min-w-0 flex-wrap items-center gap-2">
              <span class="text-xs tabular-nums text-gray-500 dark:text-dark-400"
                >#{{ index + 1 }}</span
              >
              <span
                class="min-w-0 max-w-prose break-all text-sm font-medium text-gray-900 dark:text-dark-100"
                >{{ attempt.detail.provider_name || attempt.detail.provider_id || '—' }}</span
              >
              <span
                class="rounded-compact px-2 py-1 font-mono text-xs font-semibold"
                :class="
                  statusBadgeClass(
                    attempt.detail.upstream_status_code ?? attempt.detail.status_code,
                  )
                "
                >{{
                  attempt.detail.upstream_status_code ?? attempt.detail.status_code ?? '—'
                }}</span
              >
              <time class="text-xs text-gray-500 dark:text-dark-400 sm:ml-auto">{{
                formatDateTime(attempt.detail.created_at)
              }}</time>
            </div>
            <p
              class="mt-2 line-clamp-2 break-words text-sm leading-6 text-gray-700 dark:text-dark-200"
            >
              {{ summaryText(attempt.summary) }}
            </p>
            <div class="mt-2 flex min-w-0 flex-wrap items-center justify-between gap-2">
              <RequestIdLink
                :value="attempt.detail.request_id || attempt.detail.client_request_id"
                full
              />
              <div v-if="attempt.payload" class="ml-auto flex shrink-0 items-center gap-1">
                <button
                  type="button"
                  class="detail-action"
                  :aria-expanded="expandedAttempts.has(attempt.detail.id)"
                  :aria-controls="`${panelId}-attempt-${attempt.detail.id}`"
                  @click="toggleAttempt(attempt.detail.id)"
                >
                  <Icon
                    name="chevronRight"
                    size="sm"
                    class="transition-transform duration-normal"
                    :class="{ 'rotate-90': expandedAttempts.has(attempt.detail.id) }"
                  />
                  {{
                    expandedAttempts.has(attempt.detail.id)
                      ? t('admin.ops.errorDetail.hideResponse')
                      : t('admin.ops.errorDetail.viewResponse')
                  }}
                </button>
                <button
                  v-if="expandedAttempts.has(attempt.detail.id)"
                  type="button"
                  class="detail-action"
                  @click="copyPayload(`attempt-${attempt.detail.id}`, attempt.payload)"
                >
                  <Icon
                    :name="
                      copied && copiedKey === `attempt-${attempt.detail.id}` ? 'check' : 'copy'
                    "
                    size="sm"
                  />
                  {{
                    copied && copiedKey === `attempt-${attempt.detail.id}`
                      ? t('common.copied')
                      : t('common.copy')
                  }}
                </button>
              </div>
            </div>
            <Collapse :open="expandedAttempts.has(attempt.detail.id)" unmount-on-hide>
              <div
                :id="`${panelId}-attempt-${attempt.detail.id}`"
                class="mt-2 min-w-0 rounded-control border border-gray-200 dark:border-dark-600"
              >
                <pre
                  class="detail-payload rounded-control"
                  tabindex="0"
                  :aria-label="t('admin.ops.errorDetail.viewResponse')"
                ><code>{{ formatErrorPayload(attempt.payload) }}</code></pre>
              </div>
            </Collapse>
          </li>
        </ol>
        <div
          v-if="upstreamFailed"
          class="mt-4 flex flex-wrap items-center justify-between gap-2 rounded-control bg-gray-50 p-3 dark:bg-dark-950"
          role="alert"
        >
          <p class="text-sm text-gray-600 dark:text-dark-300">
            {{ t('admin.ops.errorDetail.attemptsLoadFailed') }}
          </p>
          <button type="button" class="detail-action" @click="loadUpstreamErrors">
            {{ t('common.retry') }}
          </button>
        </div>
        <button
          v-else-if="upstreamPage < upstreamPages"
          type="button"
          class="detail-action mt-4"
          :disabled="upstreamLoading"
          @click="loadUpstreamErrors"
        >
          {{ upstreamLoading ? t('common.loading') : t('admin.ops.errorDetail.loadMoreAttempts') }}
        </button>
        <p
          v-if="
            !upstreamLoading &&
            !upstreamFailed &&
            upstreamPage >= upstreamPages &&
            upstreamErrors.length < upstreamTotal
          "
          class="mt-3 text-sm text-gray-500 dark:text-dark-300"
        >
          {{ t('admin.ops.errorDetail.incompleteAttempts') }}
        </p>
      </section>

      <section
        class="detail-section"
        :aria-labelledby="`${panelId}-payloads`"
        data-testid="diagnostic-payloads"
      >
        <h4 :id="`${panelId}-payloads`" class="detail-heading">
          {{ t('admin.ops.errorDetail.diagnosticPayloads') }}
        </h4>
        <p v-if="!payloads.length" class="mt-4 text-sm text-gray-500 dark:text-dark-300">
          {{ t('admin.ops.errorDetail.noPayloads') }}
        </p>
        <div
          v-else
          class="mt-4 divide-y divide-gray-200 border-y border-gray-200 dark:divide-dark-700 dark:border-dark-600"
        >
          <div v-for="payload in payloads" :key="payload.key" class="min-w-0">
            <div class="flex min-w-0 items-center gap-2">
              <button
                type="button"
                class="detail-action min-w-0 flex-1 justify-start py-3 text-left"
                :aria-expanded="expandedPayloads.has(payload.key)"
                :aria-controls="`${panelId}-payload-${payload.key}`"
                @click="togglePayload(payload.key)"
              >
                <Icon
                  name="chevronRight"
                  size="sm"
                  class="shrink-0 transition-transform duration-normal"
                  :class="{ 'rotate-90': expandedPayloads.has(payload.key) }"
                />
                <span class="min-w-0 break-words">{{
                  payload.sources.map(payloadLabel).join(' · ')
                }}</span>
              </button>
              <button
                type="button"
                class="detail-action shrink-0"
                :aria-label="
                  t('admin.ops.errorDetail.copyPayload', {
                    source: payload.sources.map(payloadLabel).join(' · '),
                  })
                "
                @click="copyPayload(payload.key, payload.value)"
              >
                <Icon :name="copied && copiedKey === payload.key ? 'check' : 'copy'" size="sm" />
                <span>{{
                  copied && copiedKey === payload.key ? t('common.copied') : t('common.copy')
                }}</span>
              </button>
            </div>
            <Collapse :open="expandedPayloads.has(payload.key)" unmount-on-hide>
              <pre
                :id="`${panelId}-payload-${payload.key}`"
                class="detail-payload mb-3 rounded-control"
                tabindex="0"
                :aria-label="payload.sources.map(payloadLabel).join(' · ')"
              ><code>{{ formatErrorPayload(payload.value) }}</code></pre>
            </Collapse>
          </div>
        </div>
      </section>
    </div>
    <template v-if="backToList" #footer>
      <button
        type="button"
        class="btn btn-secondary"
        data-testid="error-detail-back-to-list"
        @click="goBack"
      >
        {{ t('admin.ops.errorDetail.backToList') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import Collapse from '@/components/common/Collapse.vue'
import RequestIdLink from '@/components/common/RequestIdLink.vue'
import Icon from '@/components/icons/Icon.vue'
import { opsAPI, type OpsErrorDetail } from '@/api/admin/ops'
import { useClipboard } from '@/composables/useClipboard'
import { formatDateTime } from '@/utils/format'
import {
  formatErrorPayload,
  getAttemptPayload,
  getErrorPayloads,
  getErrorSummary,
  type ErrorSummary,
  type PayloadSource,
} from './errorDetailPresentation'

// OpsErrorDetailModal.vue presents an error summary, request context, upstream
// attempts, and collapsible response evidence in investigation order. Stored
// messages remain available in the response inspector while summary formatting
// keeps long HTML and JSON bodies out of the initial reading path. Response
// copies use the stored string, including its whitespace, rather than the
// formatted preview displayed by the inspector.
//
// Detail and attempt queries run independently for one selected error. A
// generation counter invalidates both query streams when the selection changes,
// closes, or unmounts. Each area retains its own loading and retry state so an
// attempt-query failure cannot erase a successfully loaded request detail.

/** Props identifies the selected error and its dialog navigation context. */
interface Props {
  show: boolean // owner-controlled dialog visibility
  // errorId selects the persisted error; null or a nonpositive value leaves the
  // detail loaders idle after selection state has been reset.
  errorId: number | null
  // errorType defaults to request and selects the detail endpoint; changing it
  // invalidates the current reads even when errorId remains the same.
  errorType?: 'request' | 'upstream'
  backToList?: boolean // list-return action availability
}

/** Emits returns visibility and list navigation events to the owning page. */
interface Emits {
  // update:show asks the parent to change this dialog visibility state.
  (e: 'update:show', value: boolean): void
  // back requests the originating list after dialog visibility has been cleared.
  (e: 'back'): void
}

// Component bindings share translation, clipboard feedback, and stable
// disclosure identifiers across the independently loaded sections of one dialog
// instance.
const props = withDefaults(defineProps<Props>(), { errorType: 'request', backToList: false })
const emit = defineEmits<Emits>()
const { t } = useI18n()
const { copied, copyToClipboard } = useClipboard()
const panelId = useId()

// Query and disclosure state belongs to the current selection and is reset
// before another error can publish its detail or attempt records.
const detail = ref<OpsErrorDetail | null>(null)
const loading = ref(false)
const detailFailed = ref(false)
const upstreamErrors = ref<OpsErrorDetail[]>([])
const upstreamLoading = ref(false)
const upstreamFailed = ref(false)
// upstreamTotal records the server count, while upstreamPage advances only after
// successful reads and upstreamPages determines whether another page is
// available.
const upstreamTotal = ref(0)
const upstreamPage = ref(0)
const upstreamPages = ref(0)
// Disclosure sets use persisted attempt IDs and payload source keys, so
// expansion stays attached to the corresponding evidence within this selection.
const expandedAttempts = ref(new Set<number>())
const expandedPayloads = ref(new Set<PayloadSource>())
const copiedKey = ref('')
// loadGeneration invalidates detail, attempt, and per-payload copy completions
// when visibility, selected identity, category, or component lifetime changes.
let loadGeneration = 0

// Derived display values preserve the selected record's stored model mapping and
// identity while placing optional metadata below the primary error message.
const title = computed(() =>
  props.errorId
    ? t('admin.ops.errorDetail.titleWithId', { id: String(props.errorId) })
    : t('admin.ops.errorDetail.title'),
)
// Prefer the internal request identifier for navigation, using the recorded
// client alias when the internal identity is missing.
const requestId = computed(() => detail.value?.request_id || detail.value?.client_request_id || '')
const summary = computed(() => getErrorSummary(detail.value))
const payloads = computed(() => getErrorPayloads(detail.value))
const hasModelMapping = computed(() =>
  Boolean(
    detail.value?.requested_model &&
    detail.value?.upstream_model &&
    detail.value.requested_model !== detail.value.upstream_model,
  ),
)
const requestFields = computed(() => {
  const current = detail.value
  if (!current) return []
  const fields = [
    { key: 'time', value: formatDateTime(current.created_at) },
    { key: 'requestType', value: formatRequestType(current.request_type) },
    { key: 'platform', value: current.platform || '—' },
    { key: 'group', value: current.group_name || current.group_id || '—' },
    { key: 'inboundEndpoint', value: current.inbound_endpoint || '—', mono: true },
    { key: 'upstreamEndpoint', value: current.upstream_endpoint || '—', mono: true },
    { key: 'user', value: current.user_email || current.user_id || '—' },
  ]
  if (current.api_key_prefix)
    fields.push({ key: 'apiKeyPrefix', value: current.api_key_prefix, mono: true })
  return fields
})
const upstreamAttempts = computed(() =>
  upstreamErrors.value.map((current) => ({
    detail: current,
    summary: getErrorSummary(current),
    payload: getAttemptPayload(current),
  })),
)

/** payloadLabel names the stored field behind a diagnostic section. */
function payloadLabel(key: PayloadSource): string {
  return t(`admin.ops.errorDetail.payloads.${key}`)
}

/** summaryText translates format-only summaries and retains extracted message text.
 * Empty evidence receives an explicit missing-message label. The format labels
 * describe recorded responses and do not infer a failure cause.
 */
function summaryText(value: ErrorSummary): string {
  if (value.kind === 'text') return value.text
  return t(`admin.ops.errorDetail.summaryFormats.${value.kind}`)
}

/** formatRequestType translates recorded request modes and labels unknown codes.
 * Missing and unrecognized values use the same unknown label because the stored
 * record supplies no supported request-mode classification for either case.
 */
function formatRequestType(value: number | null | undefined): string {
  switch (value) {
    case 1:
      return t('admin.ops.errorDetail.requestTypeSync')
    case 2:
      return t('admin.ops.errorDetail.requestTypeStream')
    case 3:
      return t('admin.ops.errorDetail.requestTypeWs')
    case 4:
      return t('admin.ops.errorDetail.requestTypeCyber')
    default:
      return t('admin.ops.errorDetail.requestTypeUnknown')
  }
}

/** statusBadgeClass applies status color while leaving missing codes neutral.
 * Callers retain the numeric status next to the color. A missing upstream status
 * uses neutral styling and is displayed separately as a placeholder.
 */
function statusBadgeClass(code: number | null | undefined): string {
  if (code && code >= 500) return 'bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-300'
  if (code === 429) return 'bg-purple-50 text-purple-700 dark:bg-purple-500/10 dark:text-purple-300'
  if (code && code >= 400)
    return 'bg-amber-50 text-amber-800 dark:bg-amber-500/10 dark:text-amber-200'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-950 dark:text-dark-300'
}

/** toggleAttempt expands or collapses one attempt's retained response preview. */
function toggleAttempt(id: number): void {
  if (expandedAttempts.value.has(id)) expandedAttempts.value.delete(id)
  else expandedAttempts.value.add(id)
}

/** togglePayload expands one source section without changing other disclosure states. */
function togglePayload(key: PayloadSource): void {
  if (expandedPayloads.value.has(key)) expandedPayloads.value.delete(key)
  else expandedPayloads.value.add(key)
}

// copyPayload copies the supplied stored evidence and associates success
// feedback with its disclosure key. It captures the current selection generation
// before waiting for the shared clipboard helper.
//
// If the dialog selection changes while copying, the completed operation cannot
// replace the new selection feedback key. The clipboard helper still owns its
// general copied state and failure feedback, while this function guards the
// per-payload identity. Failed copies leave the local key unchanged for the
// current dialog.
async function copyPayload(key: string, value: string): Promise<void> {
  // Capture selection ownership before waiting for the clipboard so its eventual
  // feedback cannot attach to a different error payload.
  const generation = loadGeneration
  const succeeded = await copyToClipboard(value)
  if (succeeded && generation === loadGeneration) copiedKey.value = key
}

/** loadDetail retrieves the selected error and owns the main retry state.
 * Only one detail request runs for the current selection at a time. Each
 * completion compares its captured generation before publishing data, failure,
 * or loading state because the user can select another error while it waits.
 * Closing and unmounting also invalidate completions, keeping stale failures
 * from replacing the visible record or showing an unrelated error message.
 */
async function loadDetail(): Promise<void> {
  const id = props.errorId
  if (!props.show || !id || loading.value) return
  const generation = loadGeneration
  const kind = props.errorType
  loading.value = true
  detailFailed.value = false
  try {
    const result =
      kind === 'upstream'
        ? await opsAPI.getUpstreamErrorDetail(id)
        : await opsAPI.getRequestErrorDetail(id)
    if (generation === loadGeneration) detail.value = result
  } catch {
    // Keep retrieval failures in the dialog so retry preserves the selected
    // record and remains distinct from an empty or missing error selection.
    if (generation === loadGeneration) detailFailed.value = true
  } finally {
    if (generation === loadGeneration) loading.value = false
  }
}

/** loadUpstreamErrors appends the next chronological page for the selected request.
 * The successful page cursor advances after data arrives, so a failed page can
 * be retried while earlier attempts remain readable. Selection generations
 * prevent a previous request's records or failure state from entering the new
 * dialog. The loading guard serializes page requests within a selection and
 * the total count exposes any additional pages that remain available to inspect.
 */
async function loadUpstreamErrors(): Promise<void> {
  const id = props.errorId
  if (!props.show || !id || props.errorType !== 'request' || upstreamLoading.value) return
  const generation = loadGeneration
  const page = upstreamPage.value + 1
  upstreamLoading.value = true
  upstreamFailed.value = false
  try {
    const result = await opsAPI.listRequestErrorUpstreamErrors(
      id,
      { page, page_size: 100, view: 'all', sort_by: 'created_at', sort_order: 'asc' },
      { include_detail: true },
    )
    // A page can arrive after selection changes; reject it before appending attempts
    // or advancing the cursor for the newly selected error.
    if (generation !== loadGeneration) return
    upstreamErrors.value = [...upstreamErrors.value, ...(result.items || [])]
    upstreamTotal.value = result.total ?? upstreamErrors.value.length
    // Detail expansion can omit unavailable records, so pagination follows the
    // server page count instead of waiting for visible rows to reach total.
    upstreamPages.value = result.pages ?? Math.ceil(upstreamTotal.value / (result.page_size || 100))
    upstreamPage.value = page
  } catch {
    // Preserve loaded attempts when a later page fails, and expose a local retry
    // action instead of treating a transport failure as an empty history.
    if (generation === loadGeneration) upstreamFailed.value = true
  } finally {
    if (generation === loadGeneration) upstreamLoading.value = false
  }
}

/** close asks the owning page to hide the error dialog. */
function close(): void {
  emit('update:show', false)
}

/** goBack closes the dialog before returning to its originating list.
 * The owning page receives the visibility event first, then the navigation event
 * that restores its previously displayed list context.
 */
function goBack(): void {
  close()
  emit('back')
}

watch(
  () => [props.show, props.errorId, props.errorType] as const,
  ([show, id]) => {
    // Invalidate in-flight reads before resetting local state so a later
    // completion cannot publish data belonging to a previously selected error.
    loadGeneration += 1
    detail.value = null
    detailFailed.value = false
    loading.value = false
    upstreamErrors.value = []
    upstreamFailed.value = false
    upstreamLoading.value = false
    upstreamTotal.value = 0
    upstreamPage.value = 0
    upstreamPages.value = 0
    expandedAttempts.value = new Set()
    expandedPayloads.value = new Set()
    copiedKey.value = ''
    if (show && typeof id === 'number' && id > 0) {
      void loadDetail()
      void loadUpstreamErrors()
    }
  },
  { immediate: true },
)

// Invalidate both query streams when the owner removes the dialog, even if
// visibility was never changed before the component's lifecycle ended.
onBeforeUnmount(() => {
  loadGeneration += 1
})
</script>

<style scoped>
.detail-section {
  @apply min-w-0 border-t border-gray-200 pt-6 dark:border-dark-600;
}
.detail-heading {
  @apply text-sm font-semibold text-gray-900 dark:text-dark-100;
}
.detail-label {
  @apply text-xs text-gray-500 dark:text-dark-400;
}
.detail-value {
  @apply mt-1 break-all text-sm leading-6 text-gray-900 dark:text-dark-100;
}
.detail-metadata {
  @apply flex min-w-0 items-baseline gap-4;
}
/* Fixed metadata label width keeps adjacent values aligned while long endpoint and identity strings wrap within their available column. */
.detail-metadata > dt {
  @apply w-20 shrink-0;
}
.detail-action {
  @apply inline-flex min-h-11 items-center gap-2 rounded-control px-2 text-sm font-medium text-primary-600 transition-colors hover:bg-gray-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 disabled:opacity-50 dark:text-primary-400 dark:hover:bg-dark-800 sm:min-h-9;
}
/* Payloads keep whitespace for diagnostic inspection and scroll within the panel limit so long stored bodies cannot expand the whole dialog. */
.detail-payload {
  @apply max-h-panel min-w-0 overflow-auto whitespace-pre-wrap break-all bg-gray-50 p-4 font-mono text-sm leading-6 text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:bg-dark-950 dark:text-dark-200;
}
</style>
