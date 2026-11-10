<template>
  <!-- Keep input and output modalities on opposite sides of the arrow, with each icon naming its modality. -->
  <span
    class="inline-flex min-w-0 max-w-full flex-wrap items-center justify-end gap-1"
    data-testid="model-capability-tags"
  >
    <ModalityIcon
      v-for="modality in capabilities.input"
      :key="`input-${modality}`"
      :data-modality="`input-${modality}`"
      :modality="modality"
    />
    <span v-if="!capabilities.input.length" class="text-xs text-gray-500">{{
      t(
        props.model.attributes?.input_modalities === undefined
          ? 'admin.modelAttributes.unknown'
          : 'admin.modelAttributes.none',
      )
    }}</span>
    <Icon name="moveRight" size="xs" :stroke-width="2" class="text-gray-950 dark:text-white" />
    <ModalityIcon
      v-for="modality in capabilities.output"
      :key="`output-${modality}`"
      :data-modality="`output-${modality}`"
      :modality="modality"
    />
    <span v-if="!capabilities.output.length" class="text-xs text-gray-500">{{
      t(
        props.model.attributes?.output_modalities === undefined
          ? 'admin.modelAttributes.unknown'
          : 'admin.modelAttributes.none',
      )
    }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import ModalityIcon from '@/components/common/ModalityIcon.vue'
import { resolveModelCapabilities, type ModelModality } from '@/utils/modelCapabilities'
import type { MarketplaceModel } from '@/api/marketplace.types'

// ModelCapabilityTags.vue presents the input and output modalities for one
// marketplace model. Explicit attributes supply the current modality lists,
// including empty lists; older records use the shared capability resolver and
// retain the existing unknown-versus-none labels.

// The model input supplies both explicit attributes and the legacy identifiers
// needed by fallback inference when the server omits the attributes object.
const props = defineProps<{
  // Model metadata used for explicit modality display or legacy capability inference.
  model: Pick<
    MarketplaceModel,
    'id' | 'pricing' | 'input_modalities' | 'output_modalities' | 'attributes'
  >
}>()

const { t } = useI18n()

// capabilities gives an existing attributes object precedence over legacy hints.
// Missing modality fields inside that object become empty arrays, while the
// template separately distinguishes omitted fields from explicitly empty ones.
const capabilities = computed(() =>
  props.model.attributes
    ? {
        input: (props.model.attributes.input_modalities ?? []) as ModelModality[],
        output: (props.model.attributes.output_modalities ?? []) as ModelModality[],
      }
    : resolveModelCapabilities(props.model.id, props.model.pricing, {
        input: props.model.input_modalities,
        output: props.model.output_modalities,
      }),
)
</script>
