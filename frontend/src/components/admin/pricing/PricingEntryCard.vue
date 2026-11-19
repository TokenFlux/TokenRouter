<template>
  <!-- Reveal the card before field validation requests focus so the targeted price input and its error can be displayed. -->
  <div
    class="rounded-surface border border-gray-200 bg-gray-50 p-3 dark:border-dark-600 dark:bg-dark-800"
    @form-field-reveal="collapsed = false"
  >
    <!-- Collapsed summary header (clickable) -->
    <div
      data-icon-trigger
      class="flex cursor-pointer select-none items-center gap-2"
      @click="collapsed = !collapsed"
    >
      <Icon
        name="chevronRight"
        :class="{ 'rotate-90': !collapsed }"
        size="sm"
        :stroke-width="2"
        class="flex-shrink-0 text-gray-400 transition-transform duration-normal"
        :animate-on-hover="false"
      />

      <!-- Summary: model tags + billing badge -->
      <div v-if="collapsed" class="flex min-w-0 flex-1 items-center gap-2 overflow-hidden">
        <!-- Show three model tags and summarize remaining models by count to keep the collapsed card header compact. -->
        <div class="flex min-w-0 flex-1 flex-wrap items-center gap-1">
          <span
            v-for="(m, i) in entry.models.slice(0, 3)"
            :key="i"
            class="inline-flex shrink-0 rounded-compact px-1.5 py-0.5 text-xs"
            :class="getPlatformTagClass(props.platform || '')"
          >
            {{ m }}
          </span>
          <span v-if="entry.models.length > 3" class="whitespace-nowrap text-xs text-gray-400">
            +{{ entry.models.length - 3 }}
          </span>
          <span v-if="entry.models.length === 0" class="text-xs italic text-gray-400">
            {{ t('admin.pricing.form.noModels', '未添加模型') }}
          </span>
        </div>

        <span
          v-if="entry.reference_mode"
          class="min-w-0 break-all text-sm text-gray-600 dark:text-gray-300"
          >→ {{ entry.reference_model }}</span
        >
        <template v-else>
          <!-- Billing mode badge -->
          <span
            class="flex-shrink-0 rounded-full bg-primary-100 px-2 py-0.5 text-xs font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
          >
            {{ billingModeLabel }}
          </span>
          <span
            v-if="
              entry.price_multiplier !== null &&
              entry.price_multiplier !== undefined &&
              entry.price_multiplier !== ''
            "
            class="flex-shrink-0 rounded-compact bg-gray-200 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-dark-600 dark:text-gray-300"
          >
            {{ entry.price_multiplier }}x
          </span>
          <span
            v-if="
              props.showFastModeMultiplier &&
              entry.billing_mode === 'token' &&
              entry.fast_mode_multiplier !== null &&
              entry.fast_mode_multiplier !== undefined &&
              entry.fast_mode_multiplier !== ''
            "
            class="flex-shrink-0 rounded-compact bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
          >
            Fast {{ entry.fast_mode_multiplier }}x
          </span>
          <span
            v-if="
              props.enableTierMultipliers &&
              entry.billing_mode === 'token' &&
              effectiveFastMultiplier !== null &&
              effectiveFastMultiplier !== undefined &&
              effectiveFastMultiplier !== ''
            "
            class="flex-shrink-0 rounded-compact bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
          >
            Fast {{ effectiveFastMultiplier }}x
          </span>
          <span
            v-if="
              props.enableTierMultipliers &&
              entry.billing_mode === 'token' &&
              entry.flex_multiplier !== null &&
              entry.flex_multiplier !== undefined &&
              entry.flex_multiplier !== ''
            "
            class="flex-shrink-0 rounded-compact bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-300"
          >
            Flex {{ entry.flex_multiplier }}x
          </span>
          <span
            v-if="
              props.enableTierMultipliers &&
              entry.billing_mode === 'token' &&
              entry.max_reasoning_effort_multiplier !== null &&
              entry.max_reasoning_effort_multiplier !== undefined &&
              entry.max_reasoning_effort_multiplier !== ''
            "
            class="flex-shrink-0 rounded-compact bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
          >
            Max {{ entry.max_reasoning_effort_multiplier }}x
          </span>
        </template>
      </div>

      <!-- The expanded header displays the translated price-entry label. -->
      <div v-else class="flex-1 text-xs font-medium text-gray-500 dark:text-gray-400">
        {{ t('admin.pricing.form.pricingEntry', '定价配置') }}
      </div>

      <!-- The removable card stops click propagation on deletion so the action does not also toggle its collapsed state. -->
      <button
        v-if="removable"
        type="button"
        :aria-label="t('common.delete')"
        :title="t('common.delete')"
        @click.stop="emit('remove')"
        class="flex-shrink-0 rounded-compact p-1 text-gray-400 hover:text-red-500"
      >
        <Icon name="trash" size="sm" />
      </button>
    </div>

    <!-- Collapsing retains mounted inputs, and form-field-reveal expands the card before validation attempts to focus an invalid field. -->
    <Collapse :open="!collapsed">
      <div>
        <div v-if="allowReference" class="mt-4">
          <label class="input-label">{{ t('admin.pricing.ruleMode') }}</label>
          <Select
            :model-value="entry.reference_mode ? 'reference' : 'custom'"
            :aria-label="t('admin.pricing.ruleMode')"
            :options="[
              { value: 'custom', label: t('admin.pricing.customMode') },
              { value: 'reference', label: t('admin.pricing.referenceMode') },
            ]"
            @update:model-value="
              emit('update', { ...entry, reference_mode: $event === 'reference' })
            "
          />
        </div>
        <div v-if="allowReference && entry.reference_mode" class="mt-4 space-y-4">
          <div
            ><label class="input-label">{{ t('admin.pricing.form.models') }}</label>
            <ModelTagInput
              :models="entry.models"
              :show-hint="false"
              @update:models="emit('update', { ...entry, models: $event })"
            />
          </div>
          <ModelReferenceInput
            :model-value="entry.reference_model || ''"
            @update:model-value="emit('update', { ...entry, reference_model: $event })"
          />
        </div>
        <template v-else>
          <!-- Header: Models + Billing Mode -->
          <div
            class="mt-3 grid grid-cols-1 items-start gap-2"
            :class="
              props.showFastModeMultiplier && entry.billing_mode === 'token'
                ? 'sm:grid-cols-[minmax(0,1fr)_10rem_8rem_8rem]'
                : 'sm:grid-cols-[minmax(0,1fr)_10rem_8rem]'
            "
          >
            <div>
              <label class="block text-xs font-medium leading-4 text-gray-500 dark:text-gray-400">
                {{ t('admin.pricing.form.models', '模型列表') }} <span class="text-red-500">*</span>
              </label>
              <ModelTagInput
                :models="entry.models"
                :show-hint="false"
                :platform="props.platform"
                @update:models="onModelsUpdate($event)"
                :placeholder="
                  t('admin.pricing.form.modelsPlaceholder', '输入模型名后按回车添加，支持通配符 *')
                "
                class="mt-1"
              />
            </div>
            <div>
              <label class="block text-xs font-medium leading-4 text-gray-500 dark:text-gray-400">
                {{ t('admin.pricing.form.billingMode', '计费模式') }}
              </label>
              <Select
                :modelValue="entry.billing_mode"
                @update:modelValue="onBillingModeUpdate($event as BillingMode)"
                :options="billingModeOptions"
                class="mt-1"
              />
            </div>
            <div>
              <label class="block text-xs font-medium leading-4 text-gray-500 dark:text-gray-400">
                {{ t('admin.pricing.form.priceMultiplier', '定价倍率') }}
              </label>
              <input
                :value="entry.price_multiplier"
                @input="emitField('price_multiplier', ($event.target as HTMLInputElement).value)"
                type="number"
                step="any"
                min="0"
                class="input mt-1 text-sm"
                :placeholder="t('admin.pricing.form.priceMultiplierPlaceholder', '不调整')"
              />
            </div>
            <div v-if="props.showFastModeMultiplier && entry.billing_mode === 'token'">
              <label class="block text-xs font-medium leading-4 text-gray-500 dark:text-gray-400">
                {{ t('admin.pricing.form.fastModeMultiplier', 'Fast 模式倍率') }}
              </label>
              <input
                :value="entry.fast_mode_multiplier"
                @input="
                  emitField('fast_mode_multiplier', ($event.target as HTMLInputElement).value)
                "
                type="number"
                step="any"
                min="0"
                class="input mt-1 text-sm"
                data-testid="fast-mode-multiplier"
                :placeholder="t('admin.pricing.form.fastModeMultiplierPlaceholder', '例如 2')"
              />
            </div>
          </div>

          <!-- Token mode -->
          <div v-if="entry.billing_mode === 'token'">
            <!-- Default prices (fallback when no interval matches) -->
            <div class="mt-3 flex flex-wrap items-center justify-between gap-2">
              <label class="block text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.pricing.form.defaultPrices', '默认价格（未命中区间时使用）') }}
                <span class="ml-1 font-normal text-gray-400">$/MTok</span>
              </label>
              <div class="flex items-center gap-1.5">
                <span class="text-xs text-gray-400">{{
                  t('admin.pricing.form.cacheWriteModeLabel', '缓存写入档位')
                }}</span>
                <div v-segmented class="segmented" data-testid="cache-write-mode-switch">
                  <button
                    v-for="opt in cacheWriteModeOptions"
                    :key="opt.value"
                    type="button"
                    :data-cache-write-mode="opt.value"
                    class="segmented-item px-2 py-0.5 text-xs font-medium"
                    :class="{ 'segmented-item-active': cacheWriteMode === opt.value }"
                    @click="onCacheWriteModeChange(opt.value)"
                  >
                    {{ opt.label }}
                  </button>
                </div>
              </div>
            </div>
            <div class="mt-1 grid grid-cols-2 gap-2 sm:grid-cols-4 xl:grid-cols-7">
              <div>
                <label class="text-xs text-gray-400">{{
                  t('admin.pricing.form.inputPrice', '输入')
                }}</label>
                <input
                  :value="entry.input_price"
                  @input="emitField('input_price', ($event.target as HTMLInputElement).value)"
                  type="number"
                  step="any"
                  min="0"
                  class="input mt-0.5 text-sm"
                  :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')"
                />
              </div>
              <div>
                <label class="text-xs text-gray-400">{{
                  t('admin.pricing.form.outputPrice', '输出')
                }}</label>
                <input
                  :value="entry.output_price"
                  @input="emitField('output_price', ($event.target as HTMLInputElement).value)"
                  type="number"
                  step="any"
                  min="0"
                  class="input mt-0.5 text-sm"
                  :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')"
                />
              </div>
              <div v-if="cacheWriteMode === 'uniform'">
                <label class="text-xs text-gray-400">{{
                  t('admin.pricing.form.cacheWritePrice', '缓存写入')
                }}</label>
                <input
                  :value="entry.cache_write_price"
                  @input="emitField('cache_write_price', ($event.target as HTMLInputElement).value)"
                  type="number"
                  step="any"
                  min="0"
                  data-testid="cache-write-uniform-input"
                  class="input mt-0.5 text-sm"
                  :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')"
                />
              </div>
              <template v-else-if="cacheWriteMode === '5m_1h'">
                <div>
                  <label class="text-xs text-gray-400">{{
                    t('admin.pricing.form.cacheWrite5mPrice', '缓存写入（5m）')
                  }}</label>
                  <input
                    :value="effectiveCacheWrite5mPrice"
                    @input="emitCacheWrite5mPrice(($event.target as HTMLInputElement).value)"
                    type="number"
                    step="any"
                    min="0"
                    data-testid="cache-write-5m-input"
                    class="input mt-0.5 text-sm"
                    :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')"
                  />
                </div>
                <div>
                  <label class="text-xs text-gray-400">{{
                    t('admin.pricing.form.cacheWrite1hPrice', '缓存写入（1h）')
                  }}</label>
                  <input
                    :value="entry.cache_write_1h_price"
                    @input="
                      emitField('cache_write_1h_price', ($event.target as HTMLInputElement).value)
                    "
                    type="number"
                    step="any"
                    min="0"
                    data-testid="cache-write-1h-input"
                    class="input mt-0.5 text-sm"
                    :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')"
                  />
                </div>
              </template>
              <template v-else>
                <div v-for="ttl in ['30m', '12h'] as const" :key="ttl">
                  <label class="text-xs text-gray-400">{{
                    t('admin.pricing.form.cacheWriteLifetime', { ttl })
                  }}</label>
                  <input
                    :value="entry[`cache_write_${ttl}_price`]"
                    @input="
                      emitField(
                        `cache_write_${ttl}_price`,
                        ($event.target as HTMLInputElement).value,
                      )
                    "
                    type="number"
                    step="any"
                    min="0"
                    :data-testid="`cache-write-${ttl}-input`"
                    class="input mt-0.5 text-sm"
                    :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')"
                  />
                </div>
              </template>
              <div>
                <label class="text-xs text-gray-400">{{
                  t('admin.pricing.form.cacheReadPrice', '缓存读取')
                }}</label>
                <input
                  :value="entry.cache_read_price"
                  @input="emitField('cache_read_price', ($event.target as HTMLInputElement).value)"
                  type="number"
                  step="any"
                  min="0"
                  class="input mt-0.5 text-sm"
                  :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')"
                />
              </div>
              <div>
                <label class="text-xs text-gray-400">{{
                  t('admin.pricing.form.imageInputPrice', '图片输入')
                }}</label>
                <input
                  :value="entry.image_input_price"
                  @input="emitField('image_input_price', ($event.target as HTMLInputElement).value)"
                  type="number"
                  step="any"
                  min="0"
                  class="input mt-0.5 text-sm"
                  :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')"
                />
              </div>
              <div>
                <label class="text-xs text-gray-400">{{
                  t('admin.pricing.form.imageTokenPrice', '图片输出')
                }}</label>
                <input
                  :value="entry.image_output_price"
                  @input="
                    emitField('image_output_price', ($event.target as HTMLInputElement).value)
                  "
                  type="number"
                  step="any"
                  min="0"
                  class="input mt-0.5 text-sm"
                  :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')"
                />
              </div>
            </div>

            <div
              v-if="props.enableTierMultipliers"
              class="mt-3 grid max-w-2xl grid-cols-1 gap-2 sm:grid-cols-3"
            >
              <div>
                <label class="text-xs text-gray-400">
                  {{ t('admin.pricing.form.fastMultiplier', 'Fast / Priority 倍率') }}
                </label>
                <input
                  :value="effectiveFastMultiplier"
                  @input="emitField('fast_multiplier', ($event.target as HTMLInputElement).value)"
                  type="number"
                  step="any"
                  :min="preservingLegacyFreeFast ? 0 : 0.000001"
                  class="input mt-0.5 text-sm"
                  data-testid="fast-multiplier"
                  :placeholder="t('admin.pricing.form.multiplierPlaceholder', '沿用默认')"
                />
              </div>
              <div>
                <label class="text-xs text-gray-400">
                  {{ t('admin.pricing.form.flexMultiplier', 'Flex 倍率') }}
                </label>
                <input
                  :value="entry.flex_multiplier"
                  @input="emitField('flex_multiplier', ($event.target as HTMLInputElement).value)"
                  type="number"
                  step="any"
                  min="0.000001"
                  class="input mt-0.5 text-sm"
                  data-testid="flex-multiplier"
                  :placeholder="t('admin.pricing.form.multiplierPlaceholder', '沿用默认')"
                />
              </div>
              <div>
                <label class="text-xs text-gray-400">
                  {{ t('admin.pricing.form.maxReasoningEffortMultiplier', 'Max 推理倍率') }}
                </label>
                <input
                  :value="entry.max_reasoning_effort_multiplier"
                  @input="
                    emitField(
                      'max_reasoning_effort_multiplier',
                      ($event.target as HTMLInputElement).value,
                    )
                  "
                  type="number"
                  step="any"
                  min="0.000001"
                  class="input mt-0.5 text-sm"
                  data-testid="max-reasoning-effort-multiplier"
                  :placeholder="maxReasoningEffortMultiplierPlaceholder"
                />
              </div>
            </div>

            <!-- Group and shared price configurations use the same context interval editor. -->
            <RuleListEditor
              class="mt-3"
              :items="entry.intervals || []"
              @remove="removeInterval"
              test-id="pricing-token-intervals"
              v-if="!hideTokenIntervals"
              :title="t('admin.pricing.form.intervals')"
              hint="(min, max]"
              :add-label="t('admin.pricing.form.addInterval')"
              @add="addInterval"
            >
              <template #row="{ item: iv, index: idx }">
                <IntervalRow
                  :interval="iv"
                  :mode="entry.billing_mode"
                  :cache-write-mode="cacheWriteMode"
                  @update="updateInterval(idx, $event)"
                />
              </template>
            </RuleListEditor>

            <TimePricingSection
              v-if="enableTimePricing"
              :model-value="entry.time_pricing"
              @update:model-value="emit('update', { ...entry, time_pricing: $event })"
            />
          </div>

          <!-- Per-request mode -->
          <div v-else-if="entry.billing_mode === 'per_request'">
            <!-- Default per-request price -->
            <label class="mt-3 block text-xs font-medium text-gray-500 dark:text-gray-400">
              {{
                t('admin.pricing.form.defaultPerRequestPrice', '默认单次价格（未命中层级时使用）')
              }}
              <span class="ml-1 font-normal text-gray-400">$</span>
            </label>
            <div class="mt-1 w-48">
              <input
                :value="entry.per_request_price"
                @input="emitField('per_request_price', ($event.target as HTMLInputElement).value)"
                type="number"
                step="any"
                min="0"
                class="input text-sm"
                :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')"
              />
            </div>

            <!-- Tiers -->
            <RuleListEditor
              class="mt-3"
              :items="entry.intervals || []"
              @remove="removeInterval"
              test-id="pricing-request-intervals"
              :title="t('admin.pricing.form.requestTiers')"
              :add-label="t('admin.pricing.form.addTier')"
              :empty-text="t('admin.pricing.form.noTiersYet')"
              @add="addInterval"
            >
              <template #row="{ item: iv, index: idx }">
                <IntervalRow
                  :interval="iv"
                  :mode="entry.billing_mode"
                  :cache-write-mode="cacheWriteMode"
                  @update="updateInterval(idx, $event)"
                />
              </template>
            </RuleListEditor>
          </div>

          <!-- Image and video pricing fields. -->
          <div v-else-if="entry.billing_mode === 'image' || entry.billing_mode === 'video'">
            <!-- The default media amount uses USD per second for video and USD per image for image billing. -->
            <label class="mt-3 block text-xs font-medium text-gray-500 dark:text-gray-400">
              {{
                entry.billing_mode === 'video'
                  ? t('admin.pricing.form.defaultVideoPrice', '默认视频价格（未命中层级时使用）')
                  : t('admin.pricing.form.defaultImagePrice', '默认图片价格（未命中层级时使用）')
              }}
              <span class="ml-1 font-normal text-gray-400">{{
                entry.billing_mode === 'video' ? '$/s' : '$/image'
              }}</span>
            </label>
            <div class="mt-1 w-48">
              <input
                :value="entry.per_request_price"
                @input="emitField('per_request_price', ($event.target as HTMLInputElement).value)"
                type="number"
                step="any"
                min="0"
                class="input text-sm"
                :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')"
              />
            </div>

            <!-- Resolution-based image and video tiers. -->
            <RuleListEditor
              class="mt-3"
              :items="entry.intervals || []"
              @remove="removeInterval"
              test-id="pricing-media-intervals"
              :title="
                entry.billing_mode === 'video'
                  ? t('admin.pricing.form.videoTiers')
                  : t('admin.pricing.form.imageTiers')
              "
              :add-label="t('admin.pricing.form.addTier')"
              @add="addMediaTier"
            >
              <template #row="{ item: iv, index: idx }">
                <IntervalRow
                  :interval="iv"
                  :mode="entry.billing_mode"
                  :cache-write-mode="cacheWriteMode"
                  @update="updateInterval(idx, $event)"
                />
              </template>
            </RuleListEditor>
          </div>
        </template>
      </div>
    </Collapse>
  </div>
</template>

<script setup lang="ts">
import ModelReferenceInput from '@/components/admin/model-attributes/ModelReferenceInput.vue'
import Collapse from '@/components/common/Collapse.vue'
import { vSegmented } from '@/directives/segmented'
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import IntervalRow from './IntervalRow.vue'
import ModelTagInput from './ModelTagInput.vue'
import TimePricingSection from './TimePricingSection.vue'
import type { PricingFormEntry, IntervalFormEntry, CacheWritePricingMode } from './types'
import { perTokenToMTok, getPlatformTagClass, resolveCacheWritePricingMode } from './types'
import type { BillingMode } from '@/api/admin/pricing'
import pricingAPI from '@/api/admin/pricing'

// PricingEntryCard.vue edits one model pricing entry and its optional intervals and schedules.
// Parent-owned entries are published through update events, with card collapse state retained
// locally. Clearing fields keeps null distinct from a configured free price, and service-tier
// controls can display a legacy free Fast setting.
//
// Changing billing mode resets intervals and clears tier multipliers and scheduled periods
// when leaving token billing. Interval edits retain row objects so input focus survives parent
// updates. Adding models can fetch a default quote when the inspected token-price fields are
// empty; the result fills converted prices and query failures leave manual editing available.

// t resolves the current locale messages used by labels, validation, and action notifications.
const { t } = useI18n()

// props supplies parent-owned editor values and display controls consumed by the fields below.
const props = withDefaults(
  defineProps<{
    allowReference?: boolean // Enables user-price references.
    removable?: boolean // card removal enabled
    // entry is the parent-owned card draft. Scalar updates emit a copied entry, while interval
    // edits retain and mutate the existing row object.
    entry: PricingFormEntry
    platform?: string // provider platform identifier
    // showFastModeMultiplier exposes the legacy Fast field for token billing. The default is
    // false when the parent omits this control option.
    showFastModeMultiplier?: boolean
    // hideTokenIntervals suppresses the token interval editor when true. Existing interval data
    // remains in the draft until another operation changes it.
    hideTokenIntervals?: boolean
    enableTimePricing?: boolean // schedule editor enabled
    enableTierMultipliers?: boolean // tier multiplier controls enabled
  }>(),
  {
    allowReference: false,
    removable: true,
    showFastModeMultiplier: false,
    hideTokenIntervals: false,
    enableTimePricing: false,
    enableTierMultipliers: false,
  },
)

// emit forwards typed edits and actions to the parent that owns the displayed draft.
const emit = defineEmits<{
  update: [
    entry: PricingFormEntry, // replacement price draft
  ] // replacement edited row value
  remove: [] // remove selected row
}>()

// collapsed starts closed for entries with model tags and remains local so parent field updates
// retain the card state.
const collapsed = ref(props.entry.models.length > 0)

// billingModeOptions and billingModeLabel describe the supported billing modes shown in the
// selector and collapsed price-card header.
const billingModeOptions = computed(() => [
  { value: 'token', label: 'Token' },
  { value: 'per_request', label: t('admin.pricing.billingMode.perRequest', '按次') },
  { value: 'image', label: t('admin.pricing.billingMode.image', '图片（按次）') },
  { value: 'video', label: t('admin.pricing.billingMode.video', '视频') },
])

const billingModeLabel = computed(() => {
  const opt = billingModeOptions.value.find((o) => o.value === props.entry.billing_mode)
  return opt ? opt.label : props.entry.billing_mode
})

// maxReasoningEffortMultiplierPlaceholder selects the Fable 5.1 default hint when any model
// pattern matches that configured family expression.
const maxReasoningEffortMultiplierPlaceholder = computed(() =>
  props.entry.models.some((model) => /fable(?:-5-1|-5\.1|5\.1|51)(?!\d)/i.test(model))
    ? t('admin.pricing.form.fable51DefaultMaxReasoningMultiplier', '默认 3')
    : t('admin.pricing.form.multiplierPlaceholder', '沿用默认'),
)

// effectiveFastMultiplier prefers the current Fast field, while preservingLegacyFreeFast marks
// a legacy zero that remains visible until edited.
const effectiveFastMultiplier = computed(
  () => props.entry.fast_multiplier ?? props.entry.fast_mode_multiplier,
)
const preservingLegacyFreeFast = computed(
  () => props.entry.fast_multiplier == null && props.entry.fast_mode_multiplier === 0,
)

// cacheWriteMode and cacheWriteModeOptions control whether the card edits a single uniform
// cache-write amount or a two-tier 5m/1h or 30m/12h pair, while manualCacheWriteMode keeps an
// administrator-selected tab active while its fields are still empty.
const cacheWriteMode = ref<CacheWritePricingMode>(
  resolveCacheWritePricingMode(props.entry, props.platform),
)
const manualCacheWriteMode = ref(false)
const cacheWriteModeOptions = computed((): { value: CacheWritePricingMode; label: string }[] => [
  { value: 'uniform', label: t('admin.pricing.form.cacheWriteModeUniform', '统一') },
  { value: '5m_1h', label: '5m / 1h' },
  { value: '30m_12h', label: '30m / 12h' },
])

// effectiveCacheWrite5mPrice falls back to legacy cache_write_price when an existing Claude
// card configured cache_write_1h_price alongside cache_write_price before migration 316.
const effectiveCacheWrite5mPrice = computed(
  () => props.entry.cache_write_5m_price ?? props.entry.cache_write_price,
)

watch(
  () => [
    props.entry.cache_write_price,
    props.entry.cache_write_5m_price,
    props.entry.cache_write_1h_price,
    props.entry.cache_write_30m_price,
    props.entry.cache_write_12h_price,
    props.entry.models,
    props.platform,
  ],
  () => {
    const explicitMode: CacheWritePricingMode | null =
      props.entry.cache_write_30m_price != null || props.entry.cache_write_12h_price != null
        ? '30m_12h'
        : props.entry.cache_write_5m_price != null || props.entry.cache_write_1h_price != null
          ? '5m_1h'
          : props.entry.cache_write_price != null
            ? 'uniform'
            : null
    if (explicitMode !== null) {
      if (explicitMode !== cacheWriteMode.value) {
        manualCacheWriteMode.value = false
        cacheWriteMode.value = explicitMode
      }
      return
    }
    if (!manualCacheWriteMode.value) {
      cacheWriteMode.value = resolveCacheWritePricingMode(props.entry, props.platform)
    }
  },
)

// emitField updates one price input in a copied entry. Empty input becomes null, and editing
// the current Fast multiplier clears the legacy Fast field so the new field takes precedence.
function emitField(field: keyof PricingFormEntry, value: string) {
  emit('update', {
    ...props.entry,
    ...(field === 'fast_multiplier' ? { fast_mode_multiplier: null } : {}),
    [field]: value === '' ? null : value,
  })
}

// emitCacheWrite5mPrice updates cache_write_5m_price and clears any legacy uniform
// cache_write_price so editing 5m never overwrites inherited 1h catalog pricing.
function emitCacheWrite5mPrice(value: string) {
  emit('update', {
    ...props.entry,
    cache_write_price: null,
    cache_write_5m_price: value === '' ? null : value,
  })
}

// clearInactiveIntervalCacheWrite clears cache-write fields outside nextMode on an interval
// draft while preserving existing row identity through Object.assign in update callers.
function clearInactiveIntervalCacheWrite(
  iv: IntervalFormEntry,
  nextMode: CacheWritePricingMode,
): IntervalFormEntry {
  if (nextMode === 'uniform') {
    return {
      ...iv,
      cache_write_5m_price: null,
      cache_write_1h_price: null,
      cache_write_30m_price: null,
      cache_write_12h_price: null,
    }
  }
  if (nextMode === '5m_1h') {
    return {
      ...iv,
      cache_write_5m_price: iv.cache_write_5m_price ?? iv.cache_write_price ?? null,
      cache_write_price: null,
      cache_write_30m_price: null,
      cache_write_12h_price: null,
    }
  }
  return {
    ...iv,
    cache_write_price: null,
    cache_write_5m_price: null,
    cache_write_1h_price: null,
  }
}

// onCacheWriteModeChange switches the active cache-write input group and clears inactive
// cache-write fields on both the default card and its token intervals.
function onCacheWriteModeChange(nextMode: CacheWritePricingMode) {
  if (cacheWriteMode.value === nextMode) return
  manualCacheWriteMode.value = true
  cacheWriteMode.value = nextMode
  const intervals = (props.entry.intervals || []).map((iv) => {
    Object.assign(iv, clearInactiveIntervalCacheWrite(iv, nextMode))
    return iv
  })
  if (nextMode === 'uniform') {
    emit('update', {
      ...props.entry,
      cache_write_5m_price: null,
      cache_write_1h_price: null,
      cache_write_30m_price: null,
      cache_write_12h_price: null,
      intervals,
    })
    return
  }
  if (nextMode === '5m_1h') {
    emit('update', {
      ...props.entry,
      cache_write_5m_price:
        props.entry.cache_write_5m_price ?? props.entry.cache_write_price ?? null,
      cache_write_price: null,
      cache_write_30m_price: null,
      cache_write_12h_price: null,
      intervals,
    })
    return
  }
  emit('update', {
    ...props.entry,
    cache_write_price: null,
    cache_write_5m_price: null,
    cache_write_1h_price: null,
    intervals,
  })
}

// onBillingModeUpdate publishes the selected billing mode and resets its interval rows. Leaving
// token mode also clears service-tier multipliers and schedule periods, because those hidden
// fields apply to token billing.
function onBillingModeUpdate(billingMode: BillingMode) {
  emit('update', {
    ...props.entry,
    billing_mode: billingMode,
    fast_mode_multiplier: billingMode === 'token' ? props.entry.fast_mode_multiplier : null,
    fast_multiplier: billingMode === 'token' ? props.entry.fast_multiplier : null,
    flex_multiplier: billingMode === 'token' ? props.entry.flex_multiplier : null,
    max_reasoning_effort_multiplier:
      billingMode === 'token' ? props.entry.max_reasoning_effort_multiplier : null,
    intervals: [],
    time_pricing:
      billingMode === 'token'
        ? props.entry.time_pricing
        : { ...props.entry.time_pricing, periods: [] },
  })
}

// addInterval appends an empty token or per-request row with its next sort position.
function addInterval() {
  const intervals = [...(props.entry.intervals || [])]
  intervals.push({
    min_tokens: 0,
    max_tokens: null,
    tier_label: '',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_write_5m_price: null,
    cache_write_1h_price: null,
    cache_write_30m_price: null,
    cache_write_12h_price: null,
    cache_read_price: null,
    per_request_price: null,
    input_multiplier: null,
    output_multiplier: null,
    cache_write_multiplier: null,
    cache_read_multiplier: null,
    sort_order: intervals.length,
  })
  emit('update', { ...props.entry, intervals })
}

// addMediaTier appends a media interval with a suggested resolution label. Video and image
// modes use separate label sequences; rows beyond the suggestions begin with an empty label for
// manual entry.
function addMediaTier() {
  const intervals = [...(props.entry.intervals || [])]
  const labels =
    props.entry.billing_mode === 'video' ? ['480p', '720p', '1080p'] : ['1K', '2K', '4K', 'HD']
  intervals.push({
    min_tokens: 0,
    max_tokens: null,
    tier_label: labels[intervals.length] || '',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_write_5m_price: null,
    cache_write_1h_price: null,
    cache_write_30m_price: null,
    cache_write_12h_price: null,
    cache_read_price: null,
    per_request_price: null,
    input_multiplier: null,
    output_multiplier: null,
    cache_write_multiplier: null,
    cache_read_multiplier: null,
    sort_order: intervals.length,
  })
  emit('update', { ...props.entry, intervals })
}

// updateInterval applies edited fields to the parent-owned interval row and emits a copied
// entry with a copied interval array. Callers supply an existing interval index and return the
// emitted entry through props. Object.assign retains the edited child object, and the copied
// array also retains the other interval objects. RuleListEditor uses these identities as display
// keys, so the parent round trip keeps interval inputs mounted and preserves the active input
// while its field values change.
function updateInterval(idx: number, updated: IntervalFormEntry) {
  const intervals = [...(props.entry.intervals || [])]
  // Keep the existing interval row object when publishing its copied list so the mounted input
  // continues editing the same row.
  Object.assign(intervals[idx], updated)
  emit('update', { ...props.entry, intervals })
}

// removeInterval emits a copied interval list with the indexed row removed.
function removeInterval(idx: number) {
  const intervals = [...(props.entry.intervals || [])]
  intervals.splice(idx, 1)
  emit('update', { ...props.entry, intervals })
}

// onModelsUpdate publishes model tags and optionally fills their first added model's default
// token prices. Existing values in the eight inspected token-price fields suppress lookup, and a
// missing quote leaves the current entry available for manual editing. Quote amounts are
// converted to the form's million-token units.
//
// The request is not canceled or checked against a later model edit. Its successful response
// merges with the current entry and restores the model array passed to this invocation; lookup
// failures are consumed.
async function onModelsUpdate(newModels: string[]) {
  const oldModels = props.entry.models
  emit('update', { ...props.entry, models: newModels })

  // A newly added model can supply initial prices when the card has no configured values in the
  // inspected token fields.
  const addedModels = newModels.filter((m) => !oldModels.includes(m))
  if (addedModels.length === 0) return

  // Inspect the eight token amount fields that guard automatic catalog price filling for this
  // card.
  const e = props.entry
  const hasPrice =
    e.input_price != null ||
    e.output_price != null ||
    e.cache_write_price != null ||
    e.cache_write_5m_price != null ||
    e.cache_write_1h_price != null ||
    e.cache_write_30m_price != null ||
    e.cache_write_12h_price != null ||
    e.cache_read_price != null
  if (hasPrice) return

  // Use the first newly added model as the catalog reference for the initial token price
  // values.
  try {
    const result = await pricingAPI.getModelDefaultPricing(addedModels[0])
    if (result.found && !props.entry.reference_mode) {
      const has30m12h = result.cache_write_30m_price != null || result.cache_write_12h_price != null
      const has5m1h =
        !has30m12h && (result.cache_write_5m_price != null || result.cache_write_1h_price != null)
      manualCacheWriteMode.value = false
      cacheWriteMode.value = has30m12h ? '30m_12h' : has5m1h ? '5m_1h' : 'uniform'
      emit('update', {
        ...props.entry,
        models: newModels,
        input_price: perTokenToMTok(result.input_price ?? null),
        output_price: perTokenToMTok(result.output_price ?? null),
        cache_write_price:
          !has30m12h && !has5m1h ? perTokenToMTok(result.cache_write_price ?? null) : null,
        cache_write_5m_price: has5m1h
          ? perTokenToMTok(result.cache_write_5m_price ?? result.cache_write_price ?? null)
          : null,
        cache_write_1h_price: has5m1h ? perTokenToMTok(result.cache_write_1h_price ?? null) : null,
        cache_write_30m_price: has30m12h
          ? perTokenToMTok(result.cache_write_30m_price ?? null)
          : null,
        cache_write_12h_price: has30m12h
          ? perTokenToMTok(result.cache_write_12h_price ?? null)
          : null,
        cache_read_price: perTokenToMTok(result.cache_read_price ?? null),
        image_output_price: perTokenToMTok(result.image_output_price ?? null),
      })
    }
  } catch {
    // Catalog lookup is optional; keeping the draft after failure lets administrators continue
    // entering the model and prices manually.
  }
}
</script>
