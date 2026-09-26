<template>
  <!-- 跨页校验定位价格字段时，先展开条目以便聚焦并显示错误。 -->
  <div class="rounded-surface border border-gray-200 bg-gray-50 p-3 dark:border-dark-600 dark:bg-dark-800" @form-field-reveal="collapsed = false">
    <!-- Collapsed summary header (clickable) -->
    <div
      class="flex cursor-pointer select-none items-center gap-2"
      @click="collapsed = !collapsed"
    >
      <Icon
        :name="collapsed ? 'chevronRight' : 'chevronDown'"
        size="sm"
        :stroke-width="2"
        class="flex-shrink-0 text-gray-400 transition-transform duration-200"
      />

      <!-- Summary: model tags + billing badge -->
      <div v-if="collapsed" class="flex min-w-0 flex-1 items-center gap-2 overflow-hidden">
        <!-- Compact model tags (show first 3) -->
        <div class="flex min-w-0 flex-1 flex-wrap items-center gap-1">
          <span
            v-for="(m, i) in entry.models.slice(0, 3)"
            :key="i"
            class="inline-flex shrink-0 rounded-compact px-1.5 py-0.5 text-xs"
            :class="getPlatformTagClass(props.platform || '')"
          >
            {{ m }}
          </span>
          <span
            v-if="entry.models.length > 3"
            class="whitespace-nowrap text-xs text-gray-400"
          >
            +{{ entry.models.length - 3 }}
          </span>
          <span
            v-if="entry.models.length === 0"
            class="text-xs italic text-gray-400"
          >
            {{ t('admin.pricing.form.noModels', '未添加模型') }}
          </span>
        </div>

        <!-- Billing mode badge -->
        <span
          class="flex-shrink-0 rounded-full bg-primary-100 px-2 py-0.5 text-xs font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
        >
          {{ billingModeLabel }}
        </span>
        <span
          v-if="entry.price_multiplier !== null && entry.price_multiplier !== undefined && entry.price_multiplier !== ''"
          class="flex-shrink-0 rounded-compact bg-gray-200 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-dark-600 dark:text-gray-300"
        >
          {{ entry.price_multiplier }}x
        </span>
        <span
          v-if="props.showFastModeMultiplier && entry.billing_mode === 'token' && entry.fast_mode_multiplier !== null && entry.fast_mode_multiplier !== undefined && entry.fast_mode_multiplier !== ''"
          class="flex-shrink-0 rounded-compact bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
        >
          Fast {{ entry.fast_mode_multiplier }}x
        </span>
        <span
          v-if="props.enableTierMultipliers && entry.billing_mode === 'token' && effectiveFastMultiplier !== null && effectiveFastMultiplier !== undefined && effectiveFastMultiplier !== ''"
          class="flex-shrink-0 rounded-compact bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
        >
          Fast {{ effectiveFastMultiplier }}x
        </span>
        <span
          v-if="props.enableTierMultipliers && entry.billing_mode === 'token' && entry.flex_multiplier !== null && entry.flex_multiplier !== undefined && entry.flex_multiplier !== ''"
          class="flex-shrink-0 rounded-compact bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-300"
        >
          Flex {{ entry.flex_multiplier }}x
        </span>
        <span
          v-if="props.enableTierMultipliers && entry.billing_mode === 'token' && entry.max_reasoning_effort_multiplier !== null && entry.max_reasoning_effort_multiplier !== undefined && entry.max_reasoning_effort_multiplier !== ''"
          class="flex-shrink-0 rounded-compact bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
        >
          Max {{ entry.max_reasoning_effort_multiplier }}x
        </span>
      </div>

      <!-- Expanded: show the label "Pricing Entry" or similar -->
      <div v-else class="flex-1 text-xs font-medium text-gray-500 dark:text-gray-400">
        {{ t('admin.pricing.form.pricingEntry', '定价配置') }}
      </div>

      <!-- Remove button (always visible, stop propagation) -->
      <button
        type="button"
        @click.stop="emit('remove')"
        class="flex-shrink-0 rounded-compact p-1 text-gray-400 hover:text-red-500"
      >
        <Icon name="trash" size="sm" />
      </button>
    </div>

    <!-- Expandable content with transition -->
    <div
      class="collapsible-content"
      :class="{ 'collapsible-content--collapsed': collapsed }"
    >
      <div class="collapsible-inner">
        <!-- Header: Models + Billing Mode -->
        <div
          class="mt-3 grid grid-cols-1 items-start gap-2"
          :class="props.showFastModeMultiplier && entry.billing_mode === 'token'
            ? 'sm:grid-cols-[minmax(0,1fr)_10rem_8rem_8rem]'
            : 'sm:grid-cols-[minmax(0,1fr)_10rem_8rem]'"
        >
          <div>
            <label class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.pricing.form.models', '模型列表') }} <span class="text-red-500">*</span>
            </label>
            <ModelTagInput
              :models="entry.models"
              :platform="props.platform"
              @update:models="onModelsUpdate($event)"
              :placeholder="t('admin.pricing.form.modelsPlaceholder', '输入模型名后按回车添加，支持通配符 *')"
              class="mt-1"
            />
          </div>
          <div>
            <label class="text-xs font-medium text-gray-500 dark:text-gray-400">
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
            <label class="text-xs font-medium text-gray-500 dark:text-gray-400">
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
            <label class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.pricing.form.fastModeMultiplier', 'Fast 模式倍率') }}
            </label>
            <input
              :value="entry.fast_mode_multiplier"
              @input="emitField('fast_mode_multiplier', ($event.target as HTMLInputElement).value)"
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
          <label class="mt-3 block text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('admin.pricing.form.defaultPrices', '默认价格（未命中区间时使用）') }}
            <span class="ml-1 font-normal text-gray-400">$/MTok</span>
          </label>
          <div class="mt-1 grid grid-cols-2 gap-2 sm:grid-cols-4 xl:grid-cols-7">
            <div>
              <label class="text-xs text-gray-400">{{ t('admin.pricing.form.inputPrice', '输入') }}</label>
              <input :value="entry.input_price" @input="emitField('input_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" class="input mt-0.5 text-sm" :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')" />
            </div>
            <div>
              <label class="text-xs text-gray-400">{{ t('admin.pricing.form.outputPrice', '输出') }}</label>
              <input :value="entry.output_price" @input="emitField('output_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" class="input mt-0.5 text-sm" :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')" />
            </div>
            <div>
              <label class="text-xs text-gray-400">{{ t('admin.pricing.form.cacheWrite5mPrice', '缓存写入（5m）') }}</label>
              <input :value="entry.cache_write_price" @input="emitField('cache_write_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" class="input mt-0.5 text-sm" :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')" />
            </div>
            <div>
              <label class="text-xs text-gray-400">{{ t('admin.pricing.form.cacheWrite1hPrice', '缓存写入（1h）') }}</label>
              <input :value="entry.cache_write_1h_price" @input="emitField('cache_write_1h_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" class="input mt-0.5 text-sm" :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')" />
            </div>
            <div>
              <label class="text-xs text-gray-400">{{ t('admin.pricing.form.cacheReadPrice', '缓存读取') }}</label>
              <input :value="entry.cache_read_price" @input="emitField('cache_read_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" class="input mt-0.5 text-sm" :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')" />
            </div>
            <div>
              <label class="text-xs text-gray-400">{{ t('admin.pricing.form.imageInputPrice', '图片输入') }}</label>
              <input :value="entry.image_input_price" @input="emitField('image_input_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" class="input mt-0.5 text-sm" :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')" />
            </div>
            <div>
              <label class="text-xs text-gray-400">{{ t('admin.pricing.form.imageTokenPrice', '图片输出') }}</label>
              <input :value="entry.image_output_price" @input="emitField('image_output_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" class="input mt-0.5 text-sm" :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')" />
            </div>
          </div>

          <div v-if="props.enableTierMultipliers" class="mt-3 grid max-w-2xl grid-cols-1 gap-2 sm:grid-cols-3">
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
                @input="emitField('max_reasoning_effort_multiplier', ($event.target as HTMLInputElement).value)"
                type="number"
                step="any"
                min="0.000001"
                class="input mt-0.5 text-sm"
                data-testid="max-reasoning-effort-multiplier"
                :placeholder="maxReasoningEffortMultiplierPlaceholder"
              />
            </div>
          </div>

          <!-- 分组和共享价格配置使用相同的上下文区间编辑器。 -->
          <div v-if="!hideTokenIntervals" class="mt-3">
            <div class="flex items-center justify-between">
              <label class="text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.pricing.form.intervals', '上下文区间定价（可选）') }}
                <span class="ml-1 font-normal text-gray-400">(min, max]</span>
              </label>
              <button type="button" @click="addInterval" class="text-xs text-primary-600 hover:text-primary-700">
                + {{ t('admin.pricing.form.addInterval', '添加区间') }}
              </button>
            </div>
            <div v-if="entry.intervals && entry.intervals.length > 0" class="mt-2 space-y-2">
              <IntervalRow
                v-for="(iv, idx) in entry.intervals"
                :key="idx"
                :interval="iv"
                :mode="entry.billing_mode"
                :enable-multipliers="props.enableTierMultipliers"
                @update="updateInterval(idx, $event)"
                @remove="removeInterval(idx)"
              />
            </div>
          </div>

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
            {{ t('admin.pricing.form.defaultPerRequestPrice', '默认单次价格（未命中层级时使用）') }}
            <span class="ml-1 font-normal text-gray-400">$</span>
          </label>
          <div class="mt-1 w-48">
            <input :value="entry.per_request_price" @input="emitField('per_request_price', ($event.target as HTMLInputElement).value)"
              type="number" step="any" min="0" class="input text-sm" :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')" />
          </div>

          <!-- Tiers -->
          <div class="mt-3 flex items-center justify-between">
            <label class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.pricing.form.requestTiers', '按次计费层级') }}
            </label>
            <button type="button" @click="addInterval" class="text-xs text-primary-600 hover:text-primary-700">
              + {{ t('admin.pricing.form.addTier', '添加层级') }}
            </button>
          </div>
          <div v-if="entry.intervals && entry.intervals.length > 0" class="mt-2 space-y-2">
            <IntervalRow
              v-for="(iv, idx) in entry.intervals"
              :key="idx"
              :interval="iv"
              :mode="entry.billing_mode"
              @update="updateInterval(idx, $event)"
              @remove="removeInterval(idx)"
            />
          </div>
          <div v-else class="mt-2 rounded-compact border border-dashed border-gray-300 p-3 text-center text-xs text-gray-400 dark:border-dark-500">
            {{ t('admin.pricing.form.noTiersYet', '暂无层级，点击添加配置按次计费价格') }}
          </div>
        </div>

        <!-- 图片或视频计费模式。 -->
        <div v-else-if="entry.billing_mode === 'image' || entry.billing_mode === 'video'">
          <!-- Default image price (per-request, same as per_request mode) -->
          <label class="mt-3 block text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ entry.billing_mode === 'video'
              ? t('admin.pricing.form.defaultVideoPrice', '默认视频价格（未命中层级时使用）')
              : t('admin.pricing.form.defaultImagePrice', '默认图片价格（未命中层级时使用）') }}
            <span class="ml-1 font-normal text-gray-400">{{ entry.billing_mode === 'video' ? '$/s' : '$/image' }}</span>
          </label>
          <div class="mt-1 w-48">
            <input :value="entry.per_request_price" @input="emitField('per_request_price', ($event.target as HTMLInputElement).value)"
              type="number" step="any" min="0" class="input text-sm" :placeholder="t('admin.pricing.form.pricePlaceholder', '默认')" />
          </div>

          <!-- Image tiers -->
          <div class="mt-3 flex items-center justify-between">
            <label class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ entry.billing_mode === 'video'
                ? t('admin.pricing.form.videoTiers', '视频计费层级')
                : t('admin.pricing.form.imageTiers', '图片计费层级（按次）') }}
            </label>
            <button type="button" @click="addMediaTier" class="text-xs text-primary-600 hover:text-primary-700">
              + {{ t('admin.pricing.form.addTier', '添加层级') }}
            </button>
          </div>
          <div v-if="entry.intervals && entry.intervals.length > 0" class="mt-2 space-y-2">
            <IntervalRow
              v-for="(iv, idx) in entry.intervals"
              :key="idx"
              :interval="iv"
              :mode="entry.billing_mode"
              @update="updateInterval(idx, $event)"
              @remove="removeInterval(idx)"
            />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import IntervalRow from './IntervalRow.vue'
import ModelTagInput from './ModelTagInput.vue'
import TimePricingSection from './TimePricingSection.vue'
import type { PricingFormEntry, IntervalFormEntry } from './types'
import { perTokenToMTok, getPlatformTagClass } from './types'
import type { BillingMode } from '@/api/admin/pricing'
import pricingAPI from '@/api/admin/pricing'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  entry: PricingFormEntry
  platform?: string
  showFastModeMultiplier?: boolean
  hideTokenIntervals?: boolean
  enableTimePricing?: boolean
  enableTierMultipliers?: boolean
}>(), {
  showFastModeMultiplier: false,
  hideTokenIntervals: false,
  enableTimePricing: false,
  enableTierMultipliers: false,
})

const emit = defineEmits<{
  update: [entry: PricingFormEntry]
  remove: []
}>()

// Collapse state: entries with existing models default to collapsed
const collapsed = ref(props.entry.models.length > 0)

const billingModeOptions = computed(() => [
  { value: 'token', label: 'Token' },
  { value: 'per_request', label: t('admin.pricing.billingMode.perRequest', '按次') },
  { value: 'image', label: t('admin.pricing.billingMode.image', '图片（按次）') },
  { value: 'video', label: t('admin.pricing.billingMode.video', '视频') }
])

const billingModeLabel = computed(() => {
  const opt = billingModeOptions.value.find(o => o.value === props.entry.billing_mode)
  return opt ? opt.label : props.entry.billing_mode
})

const maxReasoningEffortMultiplierPlaceholder = computed(() =>
  props.entry.models.some(model => /fable(?:-5-1|-5\.1|5\.1|51)(?!\d)/i.test(model))
    ? t('admin.pricing.form.fable51DefaultMaxReasoningMultiplier', '默认 3')
    : t('admin.pricing.form.multiplierPlaceholder', '沿用默认'),
)

// 旧 Fast 倍率允许零值，未编辑时保留旧字段；用户修改后统一写新版正数倍率。
const effectiveFastMultiplier = computed(() => props.entry.fast_multiplier ?? props.entry.fast_mode_multiplier)
const preservingLegacyFreeFast = computed(() => props.entry.fast_multiplier == null && props.entry.fast_mode_multiplier === 0)

function emitField(field: keyof PricingFormEntry, value: string) {
  emit('update', { ...props.entry, ...(field === 'fast_multiplier' ? { fast_mode_multiplier: null } : {}), [field]: value === '' ? null : value })
}

// 服务层级倍率只适用于 token 计费，切换模式时清除隐藏字段，避免提交无效配置。
function onBillingModeUpdate(billingMode: BillingMode) {
  emit('update', {
    ...props.entry,
    billing_mode: billingMode,
    fast_mode_multiplier: billingMode === 'token' ? props.entry.fast_mode_multiplier : null,
    fast_multiplier: billingMode === 'token' ? props.entry.fast_multiplier : null,
    flex_multiplier: billingMode === 'token' ? props.entry.flex_multiplier : null,
    max_reasoning_effort_multiplier: billingMode === 'token' ? props.entry.max_reasoning_effort_multiplier : null,
    intervals: [],
    time_pricing: billingMode === 'token'
      ? props.entry.time_pricing
      : { ...props.entry.time_pricing, periods: [] },
  })
}

function addInterval() {
  const intervals = [...(props.entry.intervals || [])]
  intervals.push({
    min_tokens: 0, max_tokens: null, tier_label: '',
    input_price: null, output_price: null, cache_write_price: null, cache_write_1h_price: null,
    cache_read_price: null, per_request_price: null,
    input_multiplier: null, output_multiplier: null,
    cache_write_multiplier: null, cache_read_multiplier: null,
    sort_order: intervals.length
  })
  emit('update', { ...props.entry, intervals })
}

function addMediaTier() {
  const intervals = [...(props.entry.intervals || [])]
  const labels = props.entry.billing_mode === 'video'
    ? ['480p', '720p', '1080p']
    : ['1K', '2K', '4K', 'HD']
  intervals.push({
    min_tokens: 0, max_tokens: null, tier_label: labels[intervals.length] || '',
    input_price: null, output_price: null, cache_write_price: null, cache_write_1h_price: null,
    cache_read_price: null, per_request_price: null,
    input_multiplier: null, output_multiplier: null,
    cache_write_multiplier: null, cache_read_multiplier: null,
    sort_order: intervals.length
  })
  emit('update', { ...props.entry, intervals })
}

function updateInterval(idx: number, updated: IntervalFormEntry) {
  const intervals = [...(props.entry.intervals || [])]
  intervals[idx] = updated
  emit('update', { ...props.entry, intervals })
}

function removeInterval(idx: number) {
  const intervals = [...(props.entry.intervals || [])]
  intervals.splice(idx, 1)
  emit('update', { ...props.entry, intervals })
}

async function onModelsUpdate(newModels: string[]) {
  const oldModels = props.entry.models
  emit('update', { ...props.entry, models: newModels })

  // 只在新增模型且当前无价格时自动填充
  const addedModels = newModels.filter(m => !oldModels.includes(m))
  if (addedModels.length === 0) return

  // 检查是否所有价格字段都为空
  const e = props.entry
  const hasPrice = e.input_price != null || e.output_price != null ||
                   e.cache_write_price != null || e.cache_write_1h_price != null || e.cache_read_price != null
  if (hasPrice) return

  // 查询第一个新增模型的默认价格
  try {
    const result = await pricingAPI.getModelDefaultPricing(addedModels[0], props.platform)
    if (result.found) {
      emit('update', {
        ...props.entry,
        models: newModels,
        input_price: perTokenToMTok(result.input_price ?? null),
        output_price: perTokenToMTok(result.output_price ?? null),
        cache_write_price: perTokenToMTok(result.cache_write_price ?? null),
        cache_write_1h_price: perTokenToMTok(result.cache_write_1h_price ?? null),
        cache_read_price: perTokenToMTok(result.cache_read_price ?? null),
        image_output_price: perTokenToMTok(result.image_output_price ?? null),
      })
    }
  } catch {
    // 查询失败不影响用户操作
  }
}
</script>

<style scoped>
.pricing-default-grid {
  grid-template-columns: repeat(auto-fit, minmax(8rem, 1fr));
}

.collapsible-content {
  display: grid;
  grid-template-rows: 1fr;
  transition: grid-template-rows 0.25s ease;
}

.collapsible-content--collapsed {
  grid-template-rows: 0fr;
}

.collapsible-inner {
  overflow: hidden;
}
</style>
