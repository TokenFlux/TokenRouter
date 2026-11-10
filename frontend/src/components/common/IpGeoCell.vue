<template>
  <div v-if="entry.status === 'idle'" class="mt-0.5 text-xs">
    <button
      type="button"
      class="text-primary-600 underline decoration-dashed underline-offset-2 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
      @click="handleFetch"
    >
      {{ t('usage.ipGeo.fetch') }}
    </button>
  </div>

  <div
    v-else-if="entry.status === 'loading'"
    class="mt-1"
    role="status"
    :aria-label="t('common.loading')"
    aria-busy="true"
    data-loading-skeleton
  >
    <Skeleton :width="96" :height="12" />
  </div>

  <div v-else-if="entry.status === 'success'" class="mt-0.5 flex items-center gap-1 text-xs">
    <button type="button" class="truncate text-link" :title="tooltipText" @click="handleOpenDetail">
      {{ entry.label }}
    </button>
    <button
      type="button"
      class="text-gray-400 hover:text-primary-600 dark:hover:text-primary-400"
      :title="t('usage.ipGeo.refreshTitle')"
      @click="handleRefresh"
    >
      <Icon name="refresh" size="xs" />
    </button>
  </div>

  <div v-else-if="entry.status === 'error'" class="mt-0.5 text-xs">
    <button
      type="button"
      class="text-red-600 underline decoration-dashed underline-offset-2 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300"
      @click="handleFetch"
    >
      {{ t('usage.ipGeo.failed') }}
    </button>
  </div>

  <div v-else class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
    {{ t('usage.ipGeo.private') }}
  </div>
</template>

<script setup lang="ts">
import Skeleton from '@/components/common/Skeleton.vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { fetchOne, getEntry } from '@/utils/ipGeoLookup'

/**
 * IpGeoCell.vue displays one IP lookup cache entry and exposes fetch, retry, refresh,
 * and external detail actions. The lookup utility owns request state, so the cell
 * reads the shared entry reactively and renders private addresses as text.
 */

/**
 * props supplies the address used for the shared lookup entry, refresh requests, and
 * external detail URL.
 */
const props = defineProps<{ ip: string /* lookup IP address */ }>()
/**
 * The i18n translator resolves this component's message keys using the current
 * application locale and its configured translation catalog.
 */
const { t } = useI18n()

/**
 * entry reads the shared lookup record for the current IP so all cells observe cache
 * state changes.
 */
const entry = computed(() => getEntry(props.ip))

/**
 * tooltipText lists available organization, timezone, accuracy, and coordinate details
 * for the successful lookup label.
 */
const tooltipText = computed(() => {
  const detail = entry.value.detail
  if (!detail) return ''
  const lines = [
    detail.organization ? `${t('usage.ipGeo.detailOrg')}: ${detail.organization}` : '',
    detail.timezone ? `${t('usage.ipGeo.detailTimezone')}: ${detail.timezone}` : '',
    detail.accuracy != null ? `${t('usage.ipGeo.detailAccuracy')}: ${detail.accuracy}km` : '',
    detail.latitude && detail.longitude
      ? `${t('usage.ipGeo.detailCoordinates')}: ${detail.latitude}, ${detail.longitude}`
      : '',
  ].filter(Boolean)
  return lines.join('\n')
})

/**
 * handleFetch requests the current IP through the shared lookup utility.
 */
const handleFetch = () => {
  void fetchOne(props.ip)
}

/**
 * handleRefresh requests the current IP with the force flag enabled.
 */
const handleRefresh = () => {
  void fetchOne(props.ip, true)
}

/**
 * handleOpenDetail opens the encoded IP in an external lookup page. The new browsing
 * context receives noopener and noreferrer features so the request detail action is
 * isolated from this application window.
 */
const handleOpenDetail = () => {
  window.open(
    `https://www.iplocation.net/ip-lookup?query=${encodeURIComponent(props.ip)}`,
    '_blank',
    'noopener,noreferrer',
  )
}
</script>
