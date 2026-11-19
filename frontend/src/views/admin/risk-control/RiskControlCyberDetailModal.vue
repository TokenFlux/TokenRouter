<template>
  <BaseDialog
    :show="row !== null"
    :title="t('admin.riskControl.cyberDetailTitle')"
    width="wide"
    @close="emit('close')"
  >
    <div v-if="row" class="space-y-5">
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div
          class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
        >
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('admin.riskControl.table.time')
          }}</p>
          <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white">{{
            formatRiskDateTime(row.created_at)
          }}</p>
        </div>
        <div
          class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
        >
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('admin.riskControl.table.user')
          }}</p>
          <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white">{{
            row.user_email || '-'
          }}</p>
          <p
            v-if="row.team_id"
            class="mt-1 text-xs text-gray-500 dark:text-gray-400"
            data-test="cyber-detail-team-attribution"
          >
            {{
              t('admin.riskControl.teamAttribution', {
                teamId: row.team_id,
                billingUserId: row.billing_user_id ?? '-',
              })
            }}
          </p>
        </div>
        <div
          class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
        >
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('admin.riskControl.cyberProvider')
          }}</p>
          <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white">{{
            row.provider_name || '-'
          }}</p>
        </div>
        <div
          class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
        >
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('admin.riskControl.cyberStatus')
          }}</p>
          <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white"
            >HTTP {{ row.upstream_status || '-' }}</p
          >
        </div>
      </div>

      <ContentSkeleton v-if="loading" variant="article" :rows="2" class="py-4" />

      <div v-else class="space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p class="text-sm font-semibold text-gray-900 dark:text-white">{{
              t('admin.riskControl.cyberPrompt')
            }}</p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ row.endpoint || '-' }} · openai / {{ row.model || '-' }}
            </p>
            <RequestIdLink v-if="row.request_id" :value="row.request_id" class="mt-1.5" />
          </div>
          <span
            v-if="row.group_name"
            class="inline-flex rounded-compact bg-sky-50 px-2.5 py-1 text-xs font-medium text-sky-700 dark:bg-sky-900/20 dark:text-sky-300"
          >
            {{ row.group_name }}
          </span>
        </div>
        <div
          v-if="!row.content_complete"
          class="border-l-4 border-amber-400 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200"
        >
          {{ t('admin.riskControl.historicalContentTruncated') }}
        </div>
        <div
          v-if="row.input_items?.length"
          class="divide-y divide-gray-200 border-y border-gray-200 dark:divide-dark-700 dark:border-dark-700"
        >
          <section v-for="item in row.input_items" :key="item.index" class="py-4">
            <div
              class="mb-2 flex items-center gap-2 text-xs font-semibold uppercase text-gray-500 dark:text-gray-400"
            >
              <span>{{ detailSourceLabel(item.source, t) }}</span>
              <span>·</span>
              <span>{{ detailTypeLabel(item.type, t) }}</span>
            </div>
            <pre
              class="max-h-panel overflow-auto whitespace-pre-wrap break-words bg-gray-950 p-4 text-sm leading-6 text-gray-100"
              >{{ detailItemContent(item) }}</pre>
          </section>
        </div>
        <pre
          v-else
          class="max-h-menu overflow-auto whitespace-pre-wrap break-words bg-gray-950 p-4 text-sm leading-6 text-gray-100"
          >{{ cyberPromptText }}</pre>

        <div v-if="row.media?.length" class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <figure
            v-for="media in row.media"
            :key="media.id"
            class="overflow-hidden border border-gray-200 dark:border-dark-700"
          >
            <img
              v-if="mediaObjectURLs[media.id]"
              :src="mediaObjectURLs[media.id]"
              :alt="t('admin.riskControl.reviewImage')"
              class="aspect-video w-full bg-gray-100 object-contain dark:bg-dark-900"
            />
            <div
              v-else
              class="flex aspect-video items-center justify-center bg-gray-100 px-4 text-center text-xs text-gray-500 dark:bg-dark-900 dark:text-gray-400"
            >
              {{ media.snapshot_error || t('admin.riskControl.mediaUnavailable') }}
            </div>
            <figcaption
              class="border-t border-gray-200 px-3 py-2 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400"
            >
              {{ detailSourceLabel(media.source, t) }} · {{ formatByteSize(media.byte_size) }}
            </figcaption>
          </figure>
        </div>
      </div>

      <div v-if="!loading" class="border-t border-gray-200 pt-4 dark:border-dark-700">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p class="text-sm font-semibold text-gray-900 dark:text-white">{{
              t('admin.riskControl.cyberWarning')
            }}</p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ row.endpoint || '-' }} · openai / {{ row.model || '-' }}
            </p>
          </div>
          <span
            v-if="row.group_name"
            class="inline-flex rounded-compact bg-sky-50 px-2.5 py-1 text-xs font-medium text-sky-700 dark:bg-sky-900/20 dark:text-sky-300"
          >
            {{ row.group_name }}
          </span>
        </div>
        <pre
          class="mt-4 max-h-panel overflow-auto whitespace-pre-wrap break-words rounded-control bg-gray-950 p-4 text-sm leading-6 text-gray-100 shadow-inner dark:bg-black/50"
          >{{ cyberDetailText }}</pre>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{
          t('common.close')
        }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import RequestIdLink from '@/components/common/RequestIdLink.vue'
import type { ContentModerationCyberWarning } from '@/api/admin/riskControl'
import {
  detailItemContent,
  detailSourceLabel,
  detailTypeLabel,
  formatByteSize,
  formatRiskDateTime,
} from './riskControlPresentation'

/**
 * RiskControlCyberDetailModal.vue displays submitted prompt items, media
 * snapshots, and upstream cyber warning text for a selected cyber alert row.
 */

const props = defineProps<{
  row: ContentModerationCyberWarning | null // Selected cyber warning record or null when closed.
  loading: boolean // Whether full detail and media hydration is in flight.
  mediaObjectURLs: Record<number, string> // Authenticated media preview object URLs by media ID.
}>()

const emit = defineEmits<{
  close: [] // Dismisses the cyber warning detail dialog.
}>()

const { t } = useI18n()

/**
 * cyberPromptText returns the stored prompt excerpt or a dash placeholder.
 */
const cyberPromptText = computed(() => {
  if (!props.row) return '-'
  return props.row.prompt_excerpt || '-'
})

/**
 * cyberDetailText returns the upstream cyber refusal message or a dash placeholder.
 */
const cyberDetailText = computed(() => {
  if (!props.row) return '-'
  return props.row.warning_text || '-'
})
</script>
