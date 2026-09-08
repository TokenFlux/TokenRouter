<template>
  <!-- 模型能力标识：输入模态图标 -> 输出模态图标，样式对齐 OpenRouter（Lucide 图标 + 类型色）。 -->
  <span
    class="inline-flex min-w-0 max-w-full flex-wrap items-center justify-end gap-1"
    data-testid="model-capability-tags"
    :title="summary"
  >
    <span
      v-for="modality in capabilities.input"
      :key="`input-${modality}`"
      :data-modality="`input-${modality}`"
      class="inline-flex h-5 w-5 items-center justify-center rounded-md"
      :class="modalityTagClass(modality)"
    >
      <Icon :name="modalityIconName(modality)" size="xs" :stroke-width="2.5" />
    </span>
    <Icon name="moveRight" size="xs" :stroke-width="2" class="text-gray-400 dark:text-dark-500" />
    <span
      v-for="modality in capabilities.output"
      :key="`output-${modality}`"
      :data-modality="`output-${modality}`"
      class="inline-flex h-5 w-5 items-center justify-center rounded-md"
      :class="modalityTagClass(modality)"
    >
      <Icon :name="modalityIconName(modality)" size="xs" :stroke-width="2.5" />
    </span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { resolveModelCapabilities, type ModelModality } from '@/utils/modelCapabilities'
import type { MarketplaceModel } from '@/types'

const props = defineProps<{
  model: Pick<MarketplaceModel, 'id' | 'pricing' | 'input_modalities' | 'output_modalities'>
}>()

const { t } = useI18n()

// 能力数据优先用后端从定价元数据下发的字段，缺失时由解析器回退到模型 ID 规则。
const capabilities = computed(() =>
  resolveModelCapabilities(props.model.id, props.model.pricing, {
    input: props.model.input_modalities,
    output: props.model.output_modalities,
  })
)

function modalityLabel(modality: ModelModality): string {
  switch (modality) {
    case 'text':
      return t('marketplace.modalityText')
    case 'image':
      return t('marketplace.modalityImage')
    case 'audio':
      return t('marketplace.modalityAudio')
    case 'video':
      return t('marketplace.modalityVideo')
  }
}

function modalityIconName(modality: ModelModality): 'modalityText' | 'modalityImage' | 'modalityAudio' | 'modalityVideo' {
  switch (modality) {
    case 'text':
      return 'modalityText'
    case 'image':
      return 'modalityImage'
    case 'audio':
      return 'modalityAudio'
    case 'video':
      return 'modalityVideo'
  }
}

// 每种模态使用独立颜色（10% 底色 + 同色图标），配色参考 OpenRouter：文字=蓝、图片=绿、音频=琥珀、视频=玫红。
function modalityTagClass(modality: ModelModality): string {
  switch (modality) {
    case 'text':
      return 'bg-blue-500/10 text-blue-600 dark:bg-blue-400/10 dark:text-blue-300'
    case 'image':
      return 'bg-green-500/10 text-green-600 dark:bg-green-400/10 dark:text-green-300'
    case 'audio':
      return 'bg-amber-500/10 text-amber-600 dark:bg-amber-400/10 dark:text-amber-300'
    case 'video':
      return 'bg-rose-500/10 text-rose-600 dark:bg-rose-400/10 dark:text-rose-300'
  }
}

// 悬停提示完整能力描述，如「文字·图片 -> 文字」。
const summary = computed(() =>
  capabilities.value.input.map(modalityLabel).join('·') + ' -> ' + capabilities.value.output.map(modalityLabel).join('·')
)
</script>
