<template>
  <span :class="badgeClass">
    <ProviderIcon :brand="platform" size="14px" />
    {{ label }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import { resolveProviderBrand } from '@/utils/providerBrand'

// 平台品牌胶囊：与模型广场分组徽章同款，未知平台回退到中性色与原始名称。
const props = defineProps<{ platform: string }>()

const { t } = useI18n()

const label = computed(() => t(`admin.groups.platforms.${props.platform}`, props.platform))
const badgeClass = computed(
  () => `inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-semibold ring-1 ring-inset ${resolveProviderBrand(props.platform).badgeClass}`
)
</script>
