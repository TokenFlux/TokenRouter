<template>
  <div
    class="grid min-w-0 grid-cols-1 gap-2 rounded-control border p-3 sm:grid-cols-2"
    :class="
      isEmpty
        ? 'border-red-400 bg-red-50 dark:border-red-500 dark:bg-red-950/20'
        : 'border-gray-200 bg-white dark:border-dark-500 dark:bg-dark-700'
    "
  >
    <!-- Token bounds and unit prices. -->
    <template v-if="mode === 'token'">
      <div class="pricing-interval-grid grid min-w-0 gap-2 sm:col-span-2">
        <div>
          <label class="text-xs text-gray-400">Min</label>
          <input
            :value="interval.min_tokens"
            @input="emitField('min_tokens', toInt(($event.target as HTMLInputElement).value))"
            type="number"
            min="0"
            class="input mt-1 text-xs"
          />
        </div>
        <div>
          <label class="text-xs text-gray-400">Max <span class="text-gray-300">(含)</span></label>
          <input
            :value="interval.max_tokens ?? ''"
            @input="emitField('max_tokens', toIntOrNull(($event.target as HTMLInputElement).value))"
            type="number"
            min="0"
            class="input mt-1 text-xs"
            :placeholder="'∞'"
          />
        </div>
        <div>
          <label class="text-xs text-gray-400"
            >{{ t('admin.pricing.form.inputPrice', '输入') }}
            <span v-if="isEmpty" class="text-red-500">*</span>{{ ' '
            }}<span class="text-gray-300">$/M</span></label
          >
          <input
            :value="interval.input_price"
            @input="emitField('input_price', ($event.target as HTMLInputElement).value)"
            type="number"
            step="any"
            min="0"
            class="input mt-1 text-xs"
          />
        </div>
        <div>
          <label class="text-xs text-gray-400"
            >{{ t('admin.pricing.form.outputPrice', '输出') }}
            <span v-if="isEmpty" class="text-red-500">*</span>{{ ' '
            }}<span class="text-gray-300">$/M</span></label
          >
          <input
            :value="interval.output_price"
            @input="emitField('output_price', ($event.target as HTMLInputElement).value)"
            type="number"
            step="any"
            min="0"
            class="input mt-1 text-xs"
          />
        </div>
        <div v-if="effectiveCacheWriteMode === 'uniform'">
          <label class="text-xs text-gray-400"
            >{{ t('admin.pricing.form.cacheWritePriceShort', '缓存写') }}
            <span class="text-gray-300">$/M</span></label
          >
          <input
            :value="interval.cache_write_price"
            @input="emitField('cache_write_price', ($event.target as HTMLInputElement).value)"
            type="number"
            step="any"
            min="0"
            class="input mt-1 text-xs"
          />
        </div>
        <template v-else-if="effectiveCacheWriteMode === '5m_1h'">
          <div>
            <label class="text-xs text-gray-400"
              >{{ t('admin.pricing.form.cacheWrite5mPriceShort', '缓存写 5m') }}
              <span class="text-gray-300">$/M</span></label
            >
            <input
              :value="interval.cache_write_5m_price ?? interval.cache_write_price"
              @input="emitCacheWrite5mPrice(($event.target as HTMLInputElement).value)"
              type="number"
              step="any"
              min="0"
              class="input mt-1 text-xs"
            />
          </div>
          <div>
            <label class="text-xs text-gray-400"
              >{{ t('admin.pricing.form.cacheWrite1hPriceShort', '缓存写 1h') }}
              <span class="text-gray-300">$/M</span></label
            >
            <input
              :value="interval.cache_write_1h_price"
              @input="emitField('cache_write_1h_price', ($event.target as HTMLInputElement).value)"
              type="number"
              step="any"
              min="0"
              class="input mt-1 text-xs"
            />
          </div>
        </template>
        <template v-else>
          <div v-for="ttl in ['30m', '12h'] as const" :key="ttl">
            <label class="text-xs text-gray-400">
              {{ t('admin.pricing.form.cacheWriteLifetime', { ttl }) }}
              <span class="text-gray-300">$/M</span>
            </label>
            <input
              :value="interval[`cache_write_${ttl}_price`]"
              @input="
                emitField(`cache_write_${ttl}_price`, ($event.target as HTMLInputElement).value)
              "
              type="number"
              step="any"
              min="0"
              class="input mt-1 text-xs"
            />
          </div>
        </template>
        <div>
          <label class="text-xs text-gray-400"
            >{{ t('admin.pricing.form.cacheReadPrice', '缓存R') }}
            <span class="text-gray-300">$/M</span></label
          >
          <input
            :value="interval.cache_read_price"
            @input="emitField('cache_read_price', ($event.target as HTMLInputElement).value)"
            type="number"
            step="any"
            min="0"
            class="input mt-1 text-xs"
          />
        </div>
      </div>
    </template>

    <!-- Video tiers select per-second prices by resolution. The completed output
         duration determines the total charge for that selected video price. -->
    <template v-else>
      <div class="min-w-0">
        <label class="text-xs text-gray-400">
          {{
            mode === 'image' || mode === 'video'
              ? t('admin.pricing.form.resolution', '分辨率')
              : t('admin.pricing.form.tierLabel', '层级')
          }}
        </label>
        <input
          :value="interval.tier_label"
          @input="emitField('tier_label', ($event.target as HTMLInputElement).value)"
          type="text"
          class="input mt-1 text-xs"
          :maxlength="mode === 'video' ? 50 : undefined"
          :placeholder="
            mode === 'video'
              ? '480p / 720p / 768p / 1080p / 2K'
              : mode === 'image'
                ? '1K / 2K / 4K'
                : ''
          "
        />
      </div>
      <div v-if="mode !== 'video'" class="min-w-0">
        <label class="text-xs text-gray-400">Min</label>
        <input
          :value="interval.min_tokens"
          @input="emitField('min_tokens', toInt(($event.target as HTMLInputElement).value))"
          type="number"
          min="0"
          class="input mt-1 text-xs"
        />
      </div>
      <div v-if="mode !== 'video'" class="min-w-0">
        <label class="text-xs text-gray-400">Max <span class="text-gray-300">(含)</span></label>
        <input
          :value="interval.max_tokens ?? ''"
          @input="emitField('max_tokens', toIntOrNull(($event.target as HTMLInputElement).value))"
          type="number"
          min="0"
          class="input mt-1 text-xs"
          :placeholder="'∞'"
        />
      </div>
      <div class="min-w-0">
        <label class="text-xs text-gray-400"
          >{{
            mode === 'video'
              ? t('admin.pricing.form.videoUnitPrice')
              : mode === 'image'
                ? t('admin.pricing.form.imageUnitPrice')
                : t('admin.pricing.form.perRequestPrice', '单次价格')
          }}
          <span v-if="isEmpty" class="text-red-500">*</span>{{ ' '
          }}<span class="text-gray-300">{{ mode === 'video' ? '$/s' : '$' }}</span></label
        >
        <input
          :value="interval.per_request_price"
          @input="emitField('per_request_price', ($event.target as HTMLInputElement).value)"
          type="number"
          step="any"
          min="0"
          class="input mt-1 text-xs"
        />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  resolveCacheWritePricingMode,
  type CacheWritePricingMode,
  type IntervalFormEntry,
} from './types'
import type { BillingMode } from '@/api/admin/pricing'

// IntervalRow.vue edits one labeled or context-based pricing interval. Video rows expose
// resolution labels and per-second prices, while token rows expose context bounds and token
// amounts. Updates copy the interval and represent cleared price fields as null for the parent
// validator.

// t resolves the current locale messages used by labels, validation, and action notifications.
const { t } = useI18n()

// props supplies parent-owned editor values and display controls consumed by the fields below.
const props = defineProps<{
  interval: IntervalFormEntry // editable price interval
  mode: BillingMode // selected editor mode
  cacheWriteMode?: CacheWritePricingMode // parent card cache-write input group
}>()

// emit forwards typed edits and actions to the parent that owns the displayed draft.
const emit = defineEmits<{
  update: [
    interval: IntervalFormEntry, // replacement interval draft
  ] // replacement edited row value
}>()

// effectiveCacheWriteMode follows the parent card's selected mode or infers it from interval
// fields when rendered standalone.
const effectiveCacheWriteMode = computed(
  () => props.cacheWriteMode ?? resolveCacheWritePricingMode(props.interval),
)

// isEmpty checks all interval amount and multiplier inputs so the row can display its
// unconfigured pricing hint.
const isEmpty = computed(() => {
  const iv = props.interval
  return (
    (iv.input_price == null || iv.input_price === '') &&
    (iv.output_price == null || iv.output_price === '') &&
    (iv.cache_write_price == null || iv.cache_write_price === '') &&
    (iv.cache_write_5m_price == null || iv.cache_write_5m_price === '') &&
    (iv.cache_write_1h_price == null || iv.cache_write_1h_price === '') &&
    (iv.cache_write_30m_price == null || iv.cache_write_30m_price === '') &&
    (iv.cache_write_12h_price == null || iv.cache_write_12h_price === '') &&
    (iv.cache_read_price == null || iv.cache_read_price === '') &&
    (iv.input_multiplier == null || iv.input_multiplier === '') &&
    (iv.output_multiplier == null || iv.output_multiplier === '') &&
    (iv.cache_write_multiplier == null || iv.cache_write_multiplier === '') &&
    (iv.cache_read_multiplier == null || iv.cache_read_multiplier === '') &&
    (iv.per_request_price == null || iv.per_request_price === '')
  )
})

// emitField publishes an interval copy with one changed input field. Empty strings become null,
// retaining the distinction between unconfigured amounts and numeric zero prices for validation
// and serialization.
function emitField(field: keyof IntervalFormEntry, value: string | number | null) {
  emit('update', { ...props.interval, [field]: value === '' ? null : value })
}

// emitCacheWrite5mPrice updates cache_write_5m_price and clears any legacy uniform
// cache_write_price on the edited interval row.
function emitCacheWrite5mPrice(value: string) {
  emit('update', {
    ...props.interval,
    cache_write_price: null,
    cache_write_5m_price: value === '' ? null : value,
  })
}

// toInt parses a decimal integer for a required token bound. Invalid input returns zero, which
// supplies the lower-bound input representation subsequently checked by the parent interval
// validator.
function toInt(val: string): number {
  const n = parseInt(val, 10)
  return isNaN(n) ? 0 : n
}

// toIntOrNull parses an optional decimal token bound for the editor. Empty and invalid inputs
// return null so the upper bound can represent an interval with no configured limit.
function toIntOrNull(val: string): number | null {
  if (val === '') return null
  const n = parseInt(val, 10)
  return isNaN(n) ? null : n
}
</script>

<style scoped>
.pricing-interval-grid {
  grid-template-columns: repeat(auto-fit, minmax(7.5rem, 1fr));
}
</style>
