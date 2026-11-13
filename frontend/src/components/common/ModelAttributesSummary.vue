<template>
  <div :class="isTooltip ? 'space-y-3' : 'space-y-4 text-sm'">
    <dl :class="isTooltip ? 'space-y-1.5' : 'space-y-2'">
      <div
        v-for="field in textFields"
        :key="field"
        class="flex items-baseline justify-between gap-4"
      >
        <dt class="shrink-0" :class="labelClass">
          {{ t(`admin.modelAttributes.fields.${field}`) }}
        </dt>
        <dd class="min-w-0 break-words text-right tabular-nums" :class="valueClass">
          {{ formatValue(attributes[field]) }}
        </dd>
      </div>
    </dl>

    <div
      class="flex items-center justify-between gap-4 border-t pt-3"
      :class="dividerClass"
      data-testid="model-attributes-modalities"
    >
      <span class="shrink-0" :class="labelClass">{{
        t('admin.modelAttributes.fields.modalities')
      }}</span>
      <span class="flex min-w-0 flex-wrap items-center justify-end gap-1">
        <template v-if="inputModalities.length">
          <ModalityIcon
            v-for="modality in inputModalities"
            :key="`input-${modality}`"
            :data-modality="`input-${modality}`"
            :modality="modality"
            :tone="modalityTone"
            :state="modalityState(modality, 'input_modalities')"
          />
        </template>
        <span v-else :class="mutedClass">{{ emptyModalityText(attributes.input_modalities) }}</span>
        <Icon name="moveRight" size="xs" :class="mutedClass" />
        <template v-if="outputModalities.length">
          <ModalityIcon
            v-for="modality in outputModalities"
            :key="`output-${modality}`"
            :data-modality="`output-${modality}`"
            :modality="modality"
            :tone="modalityTone"
            :state="modalityState(modality, 'output_modalities')"
          />
        </template>
        <span v-else :class="mutedClass">{{
          emptyModalityText(attributes.output_modalities)
        }}</span>
      </span>
    </div>

    <p v-if="hasUnknownModalities" :class="mutedClass">{{ t('admin.modelAttributes.unknown') }}</p>

    <ul
      class="flex flex-wrap gap-1.5 border-t pt-3"
      :class="dividerClass"
      data-testid="model-attributes-capabilities"
    >
      <li
        v-for="field in attributeCapabilities"
        :key="field"
        class="inline-flex items-center gap-1 rounded-compact px-1.5 py-0.5"
        :class="chipClass"
        :data-capability="field"
        :data-state="capabilityState(attributes[field], field)"
        :title="`${t(`admin.modelAttributes.fields.${field}`)} · ${capabilityText(attributes[field], field)}`"
      >
        <Icon
          :name="MODEL_CAPABILITY_ICONS[field]"
          size="xs"
          class="h-3.5 w-3.5"
          :class="attributes[field] ? labelClass : mutedClass"
        />
        <span class="whitespace-nowrap" :class="attributes[field] ? valueClass : mutedClass">{{
          t(`admin.modelAttributes.fields.${field}`)
        }}</span>
        <Icon
          :name="STATE_ICONS[capabilityState(attributes[field], field)]"
          size="xs"
          :stroke-width="2.5"
          :animate-on-hover="false"
          class="h-3.5 w-3.5"
          :class="stateClass(capabilityState(attributes[field], field))"
          role="img"
          :aria-label="capabilityText(attributes[field], field)"
        />
      </li>
    </ul>

    <dl
      v-if="detailRows.length"
      class="border-t pt-3"
      :class="[dividerClass, isTooltip ? 'space-y-1.5' : 'space-y-2']"
      data-testid="model-attributes-details"
    >
      <div
        v-for="row in detailRows"
        :key="row.key"
        class="flex items-baseline justify-between gap-4"
      >
        <dt class="shrink-0" :class="labelClass">{{ row.label }}</dt>
        <dd class="min-w-0 break-words text-right" :class="row.muted ? mutedClass : valueClass">
          {{ row.value }}
        </dd>
      </div>
    </dl>
    <div
      v-if="detailVariants.length"
      class="space-y-3 border-t pt-3"
      :class="dividerClass"
      data-testid="model-attributes-variants"
    >
      <section v-for="(rows, index) in detailVariants" :key="index" class="space-y-1.5">
        <p :class="labelClass">{{ t('marketplace.attributeVariant', { index: index + 1 }) }}</p>
        <dl class="space-y-1.5">
          <div v-for="row in rows" :key="row.key" class="flex items-baseline justify-between gap-4">
            <dt class="shrink-0" :class="labelClass">{{ row.label }}</dt>
            <dd class="min-w-0 break-words text-right" :class="row.muted ? mutedClass : valueClass">
              {{ row.value }}
            </dd>
          </div>
        </dl>
      </section>
    </div>
    <slot />
    <p
      v-if="attributes.route_differences"
      class="flex items-start gap-1.5"
      :class="isTooltip ? 'text-amber-300' : 'text-amber-700 dark:text-amber-300'"
    >
      <Icon name="exclamationTriangle" size="xs" class="mt-0.5 h-3.5 w-3.5" />
      <span>{{ t(routeDifferencesKey) }}</span>
    </p>
  </div>
</template>

<script setup lang="ts">
import { getLocale } from '@/i18n'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import ModalityIcon from '@/components/common/ModalityIcon.vue'
import { MODEL_CAPABILITY_ICONS } from '@/components/common/modelCapabilityIcons'
import type { IconName } from '@/components/icons/registry'
import type { ModelModality } from '@/utils/modelCapabilities'
import {
  attributeCapabilities,
  attributeLimits,
  attributeModalities,
  conditionalCapabilities,
  mediaLimitFields,
  mediaListFields,
  type ConditionalCapability,
  type ModelAttributes,
  type ModelAttributePresentation,
  type ParameterCondition,
  type ReasoningOption,
} from '@/api/modelAttributes.types'

/**
 * ModelAttributesSummary.vue renders model limits, modality evidence, and linked
 * capability details for ordinary panels and dark tooltips. A supplied modality
 * baseline determines icon positions, while the selected attributes determine their
 * support state. Missing evidence remains distinct from an empty supported set or an
 * explicit false capability.
 *
 * Reasoning options and parameter conditions can describe incompatible alternatives.
 * The component deduplicates those linked controls, then shows multiple combinations
 * in separate groups instead of filling gaps between budgets or merging opposite
 * thinking requirements. Aggregate media fields remain in the shared details area.
 * Callers supply the route difference explanation appropriate to their own aggregation
 * rules.
 */

/**
 * props supplies active model evidence, an optional overall modality baseline, and
 * presentation choices for panel or tooltip consumers.
 */
const props = withDefaults(
  defineProps<{
    attributes: ModelAttributePresentation /* presented model attributes */

    /**
     * modalityBaseline supplies the overall icon list when provided; attributes still
     * determines each icon support state for the active presentation.
     */
    modalityBaseline?: ModelAttributes

    routeDifferencesKey?: string /* route explanation translation key */

    /**
     * variant selects a dark compact tooltip presentation and omits display_name there
     * because the surrounding tooltip already names the model.
     */
    variant?: 'default' | 'tooltip'
  }>(),
  {
    variant: 'default',
    routeDifferencesKey: 'admin.modelAttributes.routeDifferences',
  },
)
/**
 * The i18n translator resolves this component's message keys using the current
 * application locale and its configured translation catalog.
 */
const { t } = useI18n()

/**
 * CapabilityState represents capability evidence: supported is positive, unsupported
 * is negative, unknown lacks evidence, and conditional requires particular request
 * parameters.
 */
type CapabilityState = 'supported' | 'unsupported' | 'unknown' | 'conditional'

/**
 * STATE_ICONS distinguishes supported, unsupported, unknown, and conditional evidence
 * in each capability status indicator.
 */
const STATE_ICONS: Record<CapabilityState, IconName> = {
  supported: 'check',
  unsupported: 'x',
  unknown: 'minus',
  conditional: 'exclamationCircle',
}

/**
 * isTooltip selects compact dark surface presentation and removes the display name
 * already present beside the tooltip trigger.
 */
const isTooltip = computed(() => props.variant === 'tooltip')
/**
 * textFields includes the display name outside tooltips and then appends the model
 * limit fields in catalog order.
 */
const textFields = computed(
  () => [...(isTooltip.value ? [] : (['display_name'] as const)), ...attributeLimits] as const,
)

/**
 * labelClass selects secondary label text colors suitable for the current summary
 * surface and application theme.
 */
const labelClass = computed(() =>
  isTooltip.value ? 'text-gray-400 dark:text-dark-400' : 'text-gray-500 dark:text-dark-400',
)
/**
 * valueClass selects the primary value text color for ordinary panels or permanently
 * dark tooltip backgrounds.
 */
const valueClass = computed(() =>
  isTooltip.value ? 'text-white dark:text-dark-100' : 'text-gray-900 dark:text-dark-50',
)
/**
 * mutedClass supplies subdued text for absent support evidence and details marked as
 * muted by their row formatter.
 */
const mutedClass = computed(() =>
  isTooltip.value ? 'text-gray-400 dark:text-dark-400' : 'text-gray-400 dark:text-dark-500',
)
/**
 * dividerClass selects separators that remain visible between summary sections on
 * ordinary or dark tooltip surfaces.
 */
const dividerClass = computed(() =>
  isTooltip.value ? 'border-white/10 dark:border-dark-600' : 'border-gray-100 dark:border-dark-700',
)
/**
 * chipClass supplies a subtle background for capability chips according to the summary
 * surface variant.
 */
const chipClass = computed(() =>
  isTooltip.value ? 'bg-white/5 dark:bg-dark-800' : 'bg-gray-50 dark:bg-dark-800',
)
/**
 * checkClass selects the green supported state color for the current summary surface
 * and theme.
 */
const checkClass = computed(() =>
  isTooltip.value ? 'text-emerald-400' : 'text-emerald-600 dark:text-emerald-400',
)
/**
 * conditionalClass selects the amber request conditional state color for the current
 * summary surface and theme.
 */
const conditionalClass = computed(() =>
  isTooltip.value ? 'text-amber-300' : 'text-amber-600 dark:text-amber-300',
)
/**
 * modalityTone passes the tooltip surface choice to modality icons so their colors
 * follow the containing panel.
 */
const modalityTone = computed(() => (isTooltip.value ? 'inverse' : 'auto'))

/**
 * inputModalities retains recognized baseline input icons when a baseline exists,
 * otherwise using the active attributes input list.
 */
const inputModalities = computed(() =>
  knownModalities((props.modalityBaseline ?? props.attributes).input_modalities),
)
/**
 * outputModalities retains recognized baseline output icons when a baseline exists,
 * otherwise using the active attributes output list.
 */
const outputModalities = computed(() =>
  knownModalities((props.modalityBaseline ?? props.attributes).output_modalities),
)

/**
 * knownModalities filters supplied values to the supported modality vocabulary.
 * Missing lists produce no icons, while recognized values retain their input order for
 * consistent display beside the direction arrow.
 */
function knownModalities(values: string[] | undefined): ModelModality[] {
  /**
   * Discard vocabulary entries that the modality icon registry cannot render, while
   * recognized entries keep their supplied display order.
   */
  return (values ?? []).filter((value): value is ModelModality =>
    (attributeModalities as readonly string[]).includes(value),
  )
}

/**
 * formatValue renders numeric limits with the active locale or returns text unchanged.
 * Missing and null values use the unknown label, keeping absent limits distinguishable
 * from a numeric zero.
 */
function formatValue(value: string | number | undefined) {
  if (value === undefined || value === null) return t('admin.modelAttributes.unknown')
  return typeof value === 'number' ? value.toLocaleString(getLocale()) : value
}

/**
 * emptyModalityText distinguishes a missing modality list from a supplied list with no
 * rendered icons. Undefined uses the unknown label; any supplied list uses the
 * translated none label at this call site.
 */
function emptyModalityText(value: string[] | undefined) {
  return t(value === undefined ? 'admin.modelAttributes.unknown' : 'admin.modelAttributes.none')
}

/**
 * CapabilityField names the boolean capability columns rendered in summary chips.
 */
type CapabilityField = (typeof attributeCapabilities)[number]

/**
 * isConditional reports whether a capability supports parameter dependent condition
 * metadata.
 */
function isConditional(field: CapabilityField): field is ConditionalCapability {
  return (conditionalCapabilities as readonly string[]).includes(field)
}

/**
 * capabilityState combines a capability value with its retained condition evidence.
 * Missing values are unknown, and false values are unsupported. A true conditional
 * capability is marked conditional when the aggregate conditions or any enabled
 * variant supplies a restriction, so a union display continues to warn that request
 * parameters can affect availability. Other true capabilities use the ordinary
 * supported state in the chip.
 */
function capabilityState(value: boolean | undefined, field: CapabilityField): CapabilityState {
  if (value === undefined || value === null) return 'unknown'
  if (
    value &&
    isConditional(field) &&
    (props.attributes.conditions?.[field] ||
      props.attributes.variants?.some((route) => route[field] && route.conditions?.[field]))
  )
    return 'conditional'
  return value ? 'supported' : 'unsupported'
}

/**
 * capabilityText translates the evidence state derived for a capability field.
 */
function capabilityText(value: boolean | undefined, field: CapabilityField) {
  return t(`admin.modelAttributes.${capabilityState(value, field)}`)
}

/**
 * stateClass gives supported and conditional states their dedicated colors.
 * Unsupported and unknown states share muted text, while their different icons and
 * translated labels keep the evidence distinction visible to readers.
 */
function stateClass(state: CapabilityState) {
  if (state === 'supported') return checkClass.value
  return state === 'conditional' ? conditionalClass.value : mutedClass.value
}

/**
 * DetailRow pairs a stable row key with display labels and values.
 */
interface DetailRow {
  key: string /* stable detail identifier */
  label: string /* translated detail label */
  value: string /* formatted detail text */
  muted?: boolean /* subdued row text */
}

/**
 * linkedVariants deduplicates variant records by their linked reasoning and condition
 * controls. The serialized identity includes reasoning options, interleaved
 * representation, conditions, and conditional capability booleans. Differences
 * confined to media limits or modality lists therefore share one detail combination,
 * while differing budgets or thinking requirements keep their own records. The first
 * occurrence supplies the retained record and its original position in the
 * alternatives list.
 */
const linkedVariants = computed(() => {
  /**
   * Deduplicate by linked control values so repeated media variants do not multiply
   * identical reasoning and condition detail sections.
   */
  const seen = new Set<string>()
  return (props.attributes.variants ?? []).filter((value) => {
    const key = JSON.stringify([
      value.reasoning_options,
      value.interleaved,
      value.conditions,
      ...conditionalCapabilities.map((field) => value[field]),
    ])
    /**
     * Retain the first matching combination so detail ordering follows the supplied
     * alternatives while duplicate linked controls share one section.
     */
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
})

/**
 * detailRows reserves reasoning and condition rows for separate alternatives when
 * multiple linked combinations remain, while aggregate media details stay shared.
 */
const detailRows = computed<DetailRow[]>(() => [
  ...(linkedVariants.value.length > 1
    ? []
    : [
        ...reasoningRows(props.attributes.reasoning_options),
        ...interleavedRows(props.attributes.interleaved),
        ...conditionalCapabilities.flatMap((field) =>
          conditionRows(field, props.attributes.conditions?.[field]),
        ),
      ]),
  ...mediaRows(props.attributes.media),
])

/**
 * detailVariants formats each retained linked combination separately when several
 * remain, including capability evidence beside its own conditions and reasoning
 * controls.
 */
const detailVariants = computed(() =>
  linkedVariants.value.length > 1
    ? linkedVariants.value.map((value) => [
        ...reasoningRows(value.reasoning_options),
        ...interleavedRows(value.interleaved),
        ...conditionalCapabilities.flatMap((field) => [
          {
            key: `capability.${field}`,
            label: t(`admin.modelAttributes.fields.${field}`),
            value: t(
              `admin.modelAttributes.${value[field] === undefined ? 'unknown' : value[field] ? 'supported' : 'unsupported'}`,
            ),
          },
          ...conditionRows(field, value.conditions?.[field]),
        ]),
      ])
    : [],
)

/**
 * modalityState resolves one displayed modality against the active attributes and
 * retained variants. Aggregate inclusion returns supported immediately. When the
 * aggregate omits the modality, a missing list in any retained variant makes the
 * result unknown; otherwise the result is unsupported. With no variants supplied, the
 * active attributes themselves provide that evidence, keeping explicit empty lists
 * distinct from absent lists in summary icons.
 */
function modalityState(
  modality: ModelModality,
  field: 'input_modalities' | 'output_modalities',
): 'supported' | 'unsupported' | 'unknown' {
  const values = props.attributes[field]
  if (values?.includes(modality)) return 'supported'
  const variants = props.attributes.variants ?? [props.attributes]
  return variants.some((value) => value[field] === undefined) ? 'unknown' : 'unsupported'
}

/**
 * hasUnknownModalities adds a visible unknown label when either displayed modality
 * list contains an icon with missing support evidence.
 */
const hasUnknownModalities = computed(
  () =>
    inputModalities.value.some((value) => modalityState(value, 'input_modalities') === 'unknown') ||
    outputModalities.value.some((value) => modalityState(value, 'output_modalities') === 'unknown'),
)

/**
 * reasoningRows formats supplied reasoning controls as detail rows. Missing controls
 * yield no rows, an empty list reports nonadjustability, and separate budget records
 * retain their own endpoints rather than being combined.
 */
function reasoningRows(options: ReasoningOption[] | undefined): DetailRow[] {
  if (options === undefined) return []
  if (!options.length)
    return [
      {
        key: 'reasoning_options',
        label: t('admin.modelAttributes.fields.reasoning_options'),
        value: t('admin.modelAttributes.notAdjustable'),
        muted: true,
      },
    ]
  return options.map((option, index) => {
    const key = `reasoning.${option.type}.${index}`
    if (option.type === 'toggle')
      return {
        key,
        label: t('admin.modelAttributes.thinkingToggle'),
        value: t('admin.modelAttributes.supported'),
      }
    if (option.type === 'effort') {
      const values = option.values
        ?.map((value) => value ?? t('admin.modelAttributes.vendorDefault'))
        .join(', ')
      return {
        key,
        label: t('admin.modelAttributes.efforts'),
        /**
         * An effort control with no displayed choices still gets a row, so its
         * declared presence remains distinct from missing reasoning metadata.
         */
        value: values || t('admin.modelAttributes.none'),
      }
    }
    return {
      key,
      label: t('admin.modelAttributes.tokenBudget'),
      value: `${formatValue(option.min)} ~ ${formatValue(option.max)}`,
    }
  })
}

/**
 * interleavedRows formats declared interleaved reasoning support as one detail row.
 * Missing metadata yields no row; true produces the supported label, while an object
 * exposes its declared response field name.
 */
function interleavedRows(value: ModelAttributes['interleaved']): DetailRow[] {
  if (value === undefined) return []
  return [
    {
      key: 'interleaved',
      label: t('admin.modelAttributes.fields.interleaved'),
      value: value === true ? t('admin.modelAttributes.supported') : value.field,
    },
  ]
}

/**
 * conditionRows formats effort and thinking restrictions for one conditional
 * capability. Empty effort lists and absent thinking flags contribute no text, so a
 * condition with neither displayed restriction produces no row.
 */
function conditionRows(
  field: ConditionalCapability,
  condition: ParameterCondition | undefined,
): DetailRow[] {
  const parts = [
    condition?.efforts?.length
      ? `${t('admin.modelAttributes.efforts')} ${condition.efforts.join(', ')}`
      : '',
    condition?.thinking === undefined
      ? ''
      : t(`admin.modelAttributes.${condition.thinking ? 'thinkingOn' : 'thinkingOff'}`),
  ].filter(Boolean)
  if (!parts.length) return []
  return [
    {
      key: `conditions.${field}`,
      label: `${t(`admin.modelAttributes.fields.${field}`)} · ${t('admin.modelAttributes.fields.conditions')}`,
      value: parts.join(' · '),
    },
  ]
}

/**
 * mediaRows formats known media lists, numeric ceilings, and input fidelity into
 * detail rows. Empty lists are omitted, zero ceilings remain visible, and a supplied
 * false fidelity value retains its unsupported label.
 */
function mediaRows(media: ModelAttributes['media']): DetailRow[] {
  if (!media) return []
  const rows: DetailRow[] = []
  for (const field of mediaListFields) {
    const values = media[field]
    if (values?.length)
      rows.push({
        key: `media.${field}`,
        label: t(`admin.modelAttributes.mediaFields.${field}`),
        value: values.join(', '),
      })
  }
  for (const field of mediaLimitFields) {
    const value = media[field]
    if (value !== undefined)
      rows.push({
        key: `media.${field}`,
        label: t(`admin.modelAttributes.mediaFields.${field}`),
        value: formatValue(value),
      })
  }
  if (media.input_fidelity !== undefined) {
    rows.push({
      key: 'media.input_fidelity',
      label: t('admin.modelAttributes.mediaFields.input_fidelity'),
      value: t(`admin.modelAttributes.${media.input_fidelity ? 'supported' : 'unsupported'}`),
    })
  }
  return rows
}
</script>
