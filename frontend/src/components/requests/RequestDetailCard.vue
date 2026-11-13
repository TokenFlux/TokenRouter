<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import RequestTimeline from '@/components/requests/RequestTimeline.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import PlatformBadge from '@/components/common/PlatformBadge.vue'
import RequestIdLink from '@/components/common/RequestIdLink.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatDateTime, formatTokens } from '@/utils/format'
import { formatTimingMs } from '@/utils/requestTiming'
import type { IconName } from '@/components/icons/registry'
import type { UsageLogTiming } from '@/api/usage.types'
import type { RequestAlias, RequestDetail } from '@/api/requests'

// RequestDetailCard.vue presents one request with its failure timings, upstream attempts,
// usage records, errors, and identifiers. Failure diagnostics and each usage performance
// record retain separate timelines on the request scale because their observations belong
// to different work.
//
// Identifier placement follows provider and attempt ownership. Classified response and
// upstream-request aliases appear beneath their matching attempt, while unmatched aliases
// retain their source and ownership in the related list. Historical unclassified upstream
// aliases use a narrower value comparison when their attempt is absent. Equal identifier
// strings from different providers or attempts remain distinct list identities.
// Provider names label both attempts and related identifiers by their recorded provider ID.

// props supplies the request detail record whose nested collections drive the card sections
// and timing views.
const props = defineProps<{
  item: RequestDetail // request detail response record
}>()
// emit asks the page to open the administrator error detail matching a clicked error
// reference.
const emit = defineEmits<{
  // openError identifies the clicked error record for the page-owned administrator detail
  // dialog.
  (event: 'openError', id: number): void
}>()

// Translation and balance formatting supply request labels and six-decimal user charge
// amounts throughout this detail card.
const { t } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()

// STATE_STYLES shares status badge and icon choices between overall request state and
// individual attempt outcomes.
const STATE_STYLES: Record<
  string,
  {
    badge: string // state badge CSS classes
    icon: IconName // state icon registry name
  }
> = {
  completed: { badge: 'badge-success', icon: 'checkCircle' },
  usage_unknown: { badge: 'badge-warning', icon: 'questionCircle' },
  usage_recorded: { badge: 'badge-primary', icon: 'checkCircle' },
  failed: { badge: 'badge-danger', icon: 'xCircle' },
  canceled: { badge: 'badge-gray', icon: 'ban' },
  running: { badge: 'badge-warning', icon: 'clock' },
}

// stateStyle selects the recorded state's badge and icon pair. Missing or unknown states
// use a neutral question badge so incomplete historical records still have a visible status
// indicator.
const stateStyle = (state?: string) =>
  STATE_STYLES[state || ''] ?? { badge: 'badge-gray', icon: 'questionCircle' as IconName }
// stateLabel translates a recorded state and uses its raw value when translation is
// unavailable. An absent state renders a dash for requests that lack this observation.
const stateLabel = (state?: string) => (state ? t(`requests.states.${state}`, state) : '-')

// statusClass colors HTTP failures by server and client status families. Missing or zero
// codes use gray, codes below 400 use green, and 400 or higher use warning or error colors.
function statusClass(code?: number) {
  if (!code) return 'text-gray-500 dark:text-dark-300'
  if (code >= 500) return 'text-red-600 dark:text-red-400'
  if (code >= 400) return 'text-amber-600 dark:text-amber-400'
  return 'text-emerald-600 dark:text-emerald-400'
}

// errorText translates the request error code, retains unknown codes verbatim, and returns
// empty text when no code exists.
const errorText = computed(() => {
  const code = props.item.error_code
  return code ? t(`requests.errorCodes.${code}`, code) : ''
})

// phaseLabel translates the failure phase for request error details. When the locale
// has no matching entry, callers receive the recorded phase unchanged so the
// detail label still identifies the observed failure stage.
const phaseLabel = (phase: string) => t(`requests.errorPhases.${phase}`, phase)
// aliasLabel translates an identifier category for the request alias list. When the
// locale has no matching entry, callers receive the recorded kind unchanged so
// the display still identifies that alias category.
const aliasLabel = (kind: string) => t(`requests.aliasKinds.${kind}`, kind)
// formatCost formats a request usage charge to six decimal places in balance units.
const formatCost = (value: number) => formatBalanceAmount(value, { fractionDigits: 6 })

// Optional request collections become empty display arrays so each section can consistently
// render or hide its own rows.
const usageRows = computed(() => props.item.usage ?? [])
const errorRows = computed(() => props.item.errors ?? [])
const aliasRows = computed(() => props.item.aliases ?? [])
const attemptRows = computed(() => props.item.attempts ?? [])

// Timeline pairs one timing observation record with its stable identity and caption.
interface Timeline {
  key: string // stable timing row identity
  label: string // failure or usage caption
  timing: UsageLogTiming // ingress-relative timing observation map
}

// timelines collects failure observations and each usage record into separately identified
// timing rows. Missing failure maps and usage records without timings contribute no row,
// while usage captions retain model and attempt attribution.
//
// Usage timing values are copied with their stream mode so the shared timeline can apply
// the correct event visibility rules. Failure and performance observations remain distinct
// even when they belong to one request, and section captions depend on how many resulting
// timelines are present.
const timelines = computed<Timeline[]>(() => {
  const list: Timeline[] = []
  const failure = props.item.failure_timings
  if (failure && Object.keys(failure).length) {
    list.push({ key: 'failure', label: t('requests.failureTimings'), timing: failure })
  }
  for (const usage of usageRows.value) {
    if (!usage.timings) continue
    const attempt = usage.timings.values.attempt_number
    list.push({
      key: `usage:${usage.id}`,
      label: attempt
        ? `${usage.model} · ${t('requests.attemptNumber', { number: attempt })}`
        : usage.model,
      timing: { ...usage.timings.values, upstream_stream: usage.timings.upstream_stream },
    })
  }
  return list
})
// timingTitle uses the failure label for a lone failure timeline and the generic timing
// heading for every other collection.
const timingTitle = computed(() => {
  const [only] = timelines.value
  return timelines.value.length === 1 && only.key === 'failure' ? only.label : t('requests.timings')
})

// aliasKey serializes kind, value, source, attempt, and provider into a list key. Equal
// identifier text can therefore remain separately rendered when its ownership or provenance
// differs.
const aliasKey = (alias: RequestAlias) =>
  JSON.stringify([alias.kind, alias.value, alias.source, alias.attempt_number, alias.provider_id])

// Attempt represents one optional upstream attempt entry in the request detail.
type Attempt = NonNullable<RequestDetail['attempts']>[number]

// attemptResponses selects response identifiers whose provider and attempt both match the
// supplied attempt. This joint check keeps equal attempt numbers from assigning another
// provider's response identifier to the row.
const attemptResponses = (attempt: Attempt) =>
  aliasRows.value.filter(
    (alias) =>
      alias.kind === 'response' &&
      alias.attempt_number === attempt.number &&
      alias.provider_id === attempt.provider_id,
  )

// shownByAttempt reports whether an alias already appears inside an upstream attempt.
// Classified response and upstream-request aliases require matching provider and attempt
// identities, and upstream-request aliases also match the displayed value.
//
// Historical upstream aliases have no classified ownership, so an alias with no attempt
// number can match an attempt by its upstream request value. Other aliases remain in the
// related list. This division keeps unmatched diagnostic identifiers visible while removing
// rows already represented in an attempt section.
function shownByAttempt(alias: RequestAlias) {
  return attemptRows.value.some((attempt) => {
    if (alias.kind === 'upstream')
      return !alias.attempt_number && attempt.upstream_request_id === alias.value
    if (alias.attempt_number !== attempt.number || alias.provider_id !== attempt.provider_id)
      return false
    return (
      alias.kind === 'response' ||
      (alias.kind === 'upstream_request' && alias.value === attempt.upstream_request_id)
    )
  })
}
// listedAliases retains aliases that no attempt section already represents, keeping
// unmatched provenance visible in the related list.
const listedAliases = computed(() => aliasRows.value.filter((alias) => !shownByAttempt(alias)))

// SOURCE_KINDS identifies alias categories whose unknown origin receives an explicit
// source-unrecorded label in the related list.
const SOURCE_KINDS = new Set(['caller', 'upstream', 'upstream_request', 'response'])

// providerLabel displays the configured provider name for a recorded ID. The numeric label
// remains available for historical records whose provider row could not be resolved.
function providerLabel(id: number) {
  // Keep the recorded ID visible when the lookup response has no name for this provider.
  return props.item.provider_names?.[id] || `${t('requests.provider')} #${id}`
}

// aliasMeta joins recorded source, provider, and attempt annotations for an identifier.
// Source-sensitive kinds with no source receive an unknown-origin label, while missing
// ownership fields contribute no text.
function aliasMeta(alias: RequestAlias) {
  const parts: string[] = []
  if (alias.source) parts.push(t('requests.via', { source: alias.source }))
  else if (SOURCE_KINDS.has(alias.kind)) parts.push(t('requests.sourceUnrecorded'))
  if (alias.provider_id) parts.push(providerLabel(alias.provider_id))
  if (alias.attempt_number)
    parts.push(t('requests.attemptNumber', { number: alias.attempt_number }))
  return parts.join(' · ')
}

// attemptHasDetails reports whether upstream identity, reason, stage, or matching response
// aliases require an expanded attempt detail list.
const attemptHasDetails = (attempt: Attempt) =>
  Boolean(
    attempt.upstream_request_id ||
    attempt.reason ||
    attempt.stage ||
    attemptResponses(attempt).length,
  )
</script>

<template>
  <article class="card overflow-hidden">
    <!-- Request state, endpoint, and identifier -->
    <header class="space-y-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <div class="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <span class="badge" :class="stateStyle(item.state).badge">
            <Icon :name="stateStyle(item.state).icon" size="xs" :animate-on-hover="false" />
            {{ stateLabel(item.state) }}
          </span>
          <span
            v-if="item.method"
            class="rounded-compact bg-gray-100 px-1.5 py-0.5 font-mono text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-dark-200"
            >{{ item.method }}</span
          >
          <span class="min-w-0 break-all font-mono text-sm text-gray-900 dark:text-dark-50">{{
            item.path || '-'
          }}</span>
        </div>
        <RequestIdLink :value="item.request_id" :link="false" full class="shrink-0" />
      </div>
      <p v-if="errorText" class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400">
        <Icon name="exclamationCircle" size="sm" class="shrink-0" :animate-on-hover="false" />
        {{ errorText }}
      </p>
      <div v-if="item.pending || item.legacy" class="space-y-2">
        <SettingsNotice v-if="item.pending">{{ t('requests.pending') }}</SettingsNotice>
        <SettingsNotice v-if="item.legacy" tone="warning">{{
          t('requests.legacy')
        }}</SettingsNotice>
      </div>
    </header>

    <!-- Start time, duration, HTTP status, and model -->
    <dl class="grid grid-cols-2 gap-4 px-6 py-4 md:grid-cols-4">
      <div>
        <dt class="text-xs text-gray-500 dark:text-dark-400">{{
          t(item.legacy ? 'requests.usageCreatedAt' : 'requests.startedAt')
        }}</dt>
        <dd class="mt-1 text-sm font-medium tabular-nums text-gray-900 dark:text-dark-50">{{
          formatDateTime(item.started_at)
        }}</dd>
      </div>
      <div>
        <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('requests.duration') }}</dt>
        <dd class="mt-1 text-sm font-medium tabular-nums text-gray-900 dark:text-dark-50">{{
          formatTimingMs(item.duration_ms)
        }}</dd>
      </div>
      <div>
        <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('requests.statusCode') }}</dt>
        <dd class="mt-1 font-mono text-sm font-medium" :class="statusClass(item.status_code)">{{
          item.status_code ?? '-'
        }}</dd>
      </div>
      <div class="min-w-0">
        <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('requests.model') }}</dt>
        <dd
          class="mt-1 flex min-w-0 items-center gap-1.5 text-sm font-medium text-gray-900 dark:text-dark-50"
        >
          <template v-if="item.model">
            <ModelIcon :model="item.model" size="16px" class="shrink-0" />
            <span class="truncate" :title="item.model">{{ item.model }}</span>
          </template>
          <span v-else class="text-gray-400 dark:text-dark-500">-</span>
        </dd>
      </div>
    </dl>

    <div
      class="divide-y divide-gray-100 border-t border-gray-100 dark:divide-dark-700 dark:border-dark-700"
    >
      <!-- Originating request and platform -->
      <div
        v-if="item.parent_request_id || item.platform"
        class="flex flex-wrap items-center gap-x-8 gap-y-2 px-6 py-3 text-sm"
      >
        <div v-if="item.parent_request_id" class="flex min-w-0 items-center gap-2">
          <span class="shrink-0 text-xs text-gray-500 dark:text-dark-400">{{
            t('requests.parent')
          }}</span>
          <RequestIdLink :value="item.parent_request_id" full />
        </div>
        <div v-if="item.platform" class="flex items-center gap-2">
          <span class="text-xs text-gray-500 dark:text-dark-400">{{ t('requests.platform') }}</span>
          <PlatformBadge :platform="item.platform" />
        </div>
      </div>

      <!-- Timing: one timeline per failure record or usage row, all scaled to the request duration. -->
      <section v-if="timelines.length" class="px-6 py-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          timingTitle
        }}</h3>
        <div class="space-y-5">
          <div v-for="timeline in timelines" :key="timeline.key">
            <p
              v-if="timelines.length > 1"
              class="mb-2 text-xs font-medium text-gray-700 dark:text-dark-200"
              >{{ timeline.label }}</p
            >
            <RequestTimeline :timing="timeline.timing" :duration="item.duration_ms" />
          </div>
        </div>
      </section>

      <!-- Attempts: a summary line, then details that align with the outcome badge from sm upward. -->
      <section v-if="attemptRows.length" class="px-6 py-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">
          {{ t('requests.attempts') }}
          <span class="ml-1 font-normal text-gray-400 dark:text-dark-400">{{
            attemptRows.length
          }}</span>
        </h3>
        <ol
          class="divide-y divide-gray-100 rounded-control border border-gray-200 dark:divide-dark-700 dark:border-dark-600"
        >
          <li
            v-for="(attempt, attemptIndex) in attemptRows"
            :key="`${attempt.number}:${attempt.provider_id}:${attemptIndex}`"
            class="px-4 py-3"
          >
            <div class="flex flex-wrap items-center gap-x-3 gap-y-1 whitespace-nowrap text-sm">
              <span class="w-6 shrink-0 font-mono text-xs text-gray-400 dark:text-dark-400">
                <template v-if="attempt.number > 0">#{{ attempt.number }}</template>
                <template v-else>
                  <span aria-hidden="true" :title="t('requests.unassignedAttempt')">-</span>
                  <span class="sr-only">{{ t('requests.unassignedAttempt') }}</span>
                </template>
              </span>
              <span class="badge" :class="stateStyle(attempt.outcome).badge">{{
                stateLabel(attempt.outcome)
              }}</span>
              <span
                v-if="attempt.status_code"
                class="font-mono text-xs"
                :class="statusClass(attempt.status_code)"
                >HTTP {{ attempt.status_code }}</span
              >
              <span
                v-if="attempt.provider_id"
                class="min-w-0 whitespace-normal break-words text-xs text-gray-500 dark:text-dark-300"
                >{{ providerLabel(attempt.provider_id) }}</span
              >
              <span
                v-if="attempt.duration_ms != null"
                class="ml-auto font-mono text-xs tabular-nums text-gray-600 dark:text-dark-200"
                >{{ formatTimingMs(attempt.duration_ms) }}</span
              >
            </div>
            <dl
              v-if="attemptHasDetails(attempt)"
              class="mt-2 grid grid-cols-1 items-baseline gap-x-3 gap-y-1 text-xs sm:ml-9 sm:grid-cols-[9rem_minmax(0,1fr)] sm:gap-y-1.5"
            >
              <template v-if="attempt.upstream_request_id">
                <dt class="mt-1 text-gray-500 first:mt-0 dark:text-dark-400 sm:mt-0">{{
                  aliasLabel(attempt.upstream_request_id_header ? 'upstream_request' : 'upstream')
                }}</dt>
                <dd class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                  <RequestIdLink :value="attempt.upstream_request_id" :link="false" full />
                  <span class="text-gray-400 dark:text-dark-500">{{
                    attempt.upstream_request_id_header
                      ? t('requests.via', { source: attempt.upstream_request_id_header })
                      : t('requests.sourceUnrecorded')
                  }}</span>
                </dd>
              </template>
              <template v-for="alias in attemptResponses(attempt)" :key="aliasKey(alias)">
                <dt class="mt-1 text-gray-500 first:mt-0 dark:text-dark-400 sm:mt-0">{{
                  aliasLabel('response')
                }}</dt>
                <dd class="min-w-0"><RequestIdLink :value="alias.value" :link="false" full /></dd>
              </template>
              <template v-if="attempt.reason || attempt.stage">
                <dt class="mt-1 text-gray-500 first:mt-0 dark:text-dark-400 sm:mt-0">{{
                  t('requests.reason')
                }}</dt>
                <dd class="min-w-0 break-words text-gray-700 dark:text-dark-100">
                  <span v-if="attempt.stage" class="font-medium">{{
                    phaseLabel(attempt.stage)
                  }}</span>
                  <span
                    v-if="attempt.stage && attempt.reason"
                    class="text-gray-400 dark:text-dark-500"
                  >
                    ·
                  </span>
                  <span v-if="attempt.reason">{{ attempt.reason }}</span>
                </dd>
              </template>
            </dl>
            <RequestTimeline
              v-if="attempt.failure_timings"
              class="mt-3 sm:ml-9"
              :timing="attempt.failure_timings"
            />
          </li>
        </ol>
      </section>

      <section
        v-if="
          item.usage_write_error ||
          (item.usage_write_state &&
            item.usage_write_state !== 'persisted' &&
            (item.usage_write_state !== 'pending' || !usageRows.length))
        "
        class="px-6 py-5"
      >
        <SettingsNotice tone="warning">
          {{ t(`requests.usageWriteStates.${item.usage_write_state}`) }}
          <span v-if="item.usage_write_error" class="ml-1">{{ item.usage_write_error }}</span>
        </SettingsNotice>
      </section>

      <!-- Usage: model, tokens and cost, then the performance of the producing attempt. -->
      <section v-if="usageRows.length" class="px-6 py-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          t('requests.usage')
        }}</h3>
        <ul
          class="divide-y divide-gray-100 rounded-control border border-gray-200 dark:divide-dark-700 dark:border-dark-600"
        >
          <li v-for="usage in usageRows" :key="usage.id" class="px-4 py-3">
            <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
              <span
                class="flex min-w-0 items-center gap-1.5 font-medium text-gray-900 dark:text-dark-50"
              >
                <ModelIcon :model="usage.model" size="16px" class="shrink-0" />
                <span class="truncate">{{ usage.model }}</span>
              </span>
              <span class="text-xs tabular-nums text-gray-500 dark:text-dark-300">
                {{
                  t('requests.tokens', {
                    input: formatTokens(usage.input_tokens),
                    output: formatTokens(usage.output_tokens),
                  })
                }}
              </span>
              <span
                class="ml-auto text-sm font-medium tabular-nums text-emerald-600 dark:text-emerald-400"
                >{{ formatCost(usage.actual_cost) }}</span
              >
            </div>
            <dl
              v-if="
                usage.timings?.values.attempt_number ||
                usage.duration_ms != null ||
                usage.ttft_ms != null ||
                usage.tps != null
              "
              class="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-xs"
            >
              <div v-if="usage.timings?.values.attempt_number" class="flex gap-1.5">
                <dt class="text-gray-500 dark:text-dark-400">{{ t('requests.producedBy') }}</dt>
                <dd class="font-mono text-gray-900 dark:text-dark-100"
                  >#{{ usage.timings.values.attempt_number }}</dd
                >
              </div>
              <div v-if="usage.duration_ms != null" class="flex gap-1.5">
                <dt class="text-gray-500 dark:text-dark-400">{{
                  t('requests.upstreamDuration')
                }}</dt>
                <dd class="font-mono tabular-nums text-gray-900 dark:text-dark-100">{{
                  formatTimingMs(usage.duration_ms)
                }}</dd>
              </div>
              <div v-if="usage.ttft_ms != null" class="flex gap-1.5">
                <dt class="text-gray-500 dark:text-dark-400">{{
                  usage.timings?.ttft_mode === 'first_event'
                    ? t('usage.latencyFirstEvent')
                    : t('usage.latencyFirstToken')
                }}</dt>
                <dd class="font-mono tabular-nums text-gray-900 dark:text-dark-100">{{
                  formatTimingMs(usage.ttft_ms)
                }}</dd>
              </div>
              <div v-if="usage.tps != null" class="flex gap-1.5">
                <dt class="text-gray-500 dark:text-dark-400">TPS</dt>
                <dd class="font-mono tabular-nums text-gray-900 dark:text-dark-100">{{
                  usage.tps.toFixed(1)
                }}</dd>
              </div>
            </dl>
          </li>
        </ul>
      </section>

      <!-- Error references open the administrator detail view. -->
      <section v-if="errorRows.length" class="px-6 py-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          t('requests.errors')
        }}</h3>
        <ul
          class="divide-y divide-gray-100 overflow-hidden rounded-control border border-gray-200 dark:divide-dark-700 dark:border-dark-600"
        >
          <li v-for="failure in errorRows" :key="failure.id">
            <button
              type="button"
              class="flex w-full items-center gap-4 px-4 py-3 text-left text-sm transition-colors hover:bg-gray-50 dark:hover:bg-dark-800"
              @click="emit('openError', failure.id)"
            >
              <span class="font-mono text-xs font-medium" :class="statusClass(failure.status_code)"
                >HTTP {{ failure.status_code }}</span
              >
              <span class="text-gray-700 dark:text-dark-100">{{ phaseLabel(failure.phase) }}</span>
              <span class="font-mono text-xs text-gray-400 dark:text-dark-400"
                >#{{ failure.id }}</span
              >
              <Icon
                name="chevronRight"
                size="sm"
                class="ml-auto text-gray-400 dark:text-dark-400"
              />
            </button>
          </li>
        </ul>
      </section>

      <!-- Related identifiers left after attempts show their own IDs; administrators only. -->
      <section v-if="listedAliases.length" class="px-6 py-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          t('requests.aliases')
        }}</h3>
        <dl
          class="grid grid-cols-1 items-baseline gap-x-3 gap-y-1 text-xs sm:grid-cols-[9rem_minmax(0,1fr)] sm:gap-y-2"
        >
          <template v-for="alias in listedAliases" :key="aliasKey(alias)">
            <dt class="mt-1 text-gray-500 first:mt-0 dark:text-dark-400 sm:mt-0">{{
              aliasLabel(alias.kind)
            }}</dt>
            <dd class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
              <RequestIdLink :value="alias.value" :link="false" full />
              <span
                v-if="aliasMeta(alias)"
                class="min-w-0 break-words text-gray-400 dark:text-dark-500"
                >{{ aliasMeta(alias) }}</span
              >
            </dd>
          </template>
        </dl>
      </section>
    </div>
  </article>
</template>
