<template>
  <div
    v-if="uniqueIps.length > 0"
    class="flex flex-shrink-0 items-center justify-end gap-2"
    :class="inline
      ? ''
      : 'border-b border-gray-200 px-4 py-2 dark:border-dark-700'"
  >
    <button
      type="button"
      :class="inline
        ? compact
          ? 'btn btn-secondary btn-sm h-[34px] w-[34px] p-0'
          : 'btn btn-secondary h-9 w-9 p-0'
        : 'inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-primary-600 transition-colors hover:bg-primary-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-primary-400 dark:hover:bg-primary-900/30'"
      :disabled="loading || pendingCount === 0"
      :aria-label="loading ? t('usage.ipGeo.batchFetching') : t('usage.ipGeo.batchFetch')"
      :title="loading ? t('usage.ipGeo.batchFetching') : t('usage.ipGeo.batchFetch')"
      @click="run"
    >
      <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
      <span v-if="!inline">{{ loading ? t('usage.ipGeo.batchFetching') : t('usage.ipGeo.batchFetch') }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { fetchBatch, getEntry } from '@/utils/ipGeoLookup'

// 当前页 IP 批量地理查询工具条:传入原始 IP 列表(可含空值),内部去重;
// 无 IP 时自身不渲染。批量失败 emit failed,由使用方弹提示。
const props = withDefaults(defineProps<{
  ips: Array<string | null | undefined>
  /** 嵌入页面操作栏时使用按钮样式，不显示表格工具条边框。 */
  inline?: boolean
  /** 紧凑筛选区使用小尺寸按钮，避免撑高或挤压网格布局。 */
  compact?: boolean
}>(), {
  inline: false,
  compact: false,
})

const emit = defineEmits<{
  (e: 'failed'): void
}>()

const { t } = useI18n()
const inline = computed(() => props.inline)
const compact = computed(() => props.compact)

const uniqueIps = computed(() =>
  Array.from(new Set(props.ips.filter((ip): ip is string => Boolean(ip))))
)

const pendingCount = computed(() =>
  uniqueIps.value.filter((ip) => {
    const status = getEntry(ip).status
    return status === 'idle' || status === 'error'
  }).length
)

const loading = ref(false)

const run = async () => {
  loading.value = true
  try {
    const ok = await fetchBatch(uniqueIps.value)
    if (!ok) emit('failed')
  } finally {
    loading.value = false
  }
}
</script>
