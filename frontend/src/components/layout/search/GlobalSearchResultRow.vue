<template>
  <div
    :id="optionId"
    role="option"
    :aria-selected="active"
    class="search-row"
    :class="{ 'search-row-active': active }"
    :data-result-key="item.key"
    @click="emit('select')"
    @mousemove="emit('pointer')"
  >
    <!-- Models show their brand mark; pages keep their sidebar icon or uploaded SVG. -->
    <ModelIcon
      v-if="item.kind === 'model'"
      :model="item.model.id"
      size="20px"
      class="shrink-0 select-none"
    />
    <span
      v-else-if="item.kind === 'page' && item.iconSvg"
      class="search-row-svg shrink-0"
      v-html="sanitizeSvg(item.iconSvg)"
    ></span>
    <Icon v-else :name="item.icon ?? 'home'" size="md" class="shrink-0" />

    <span class="min-w-0 flex-1">
      <span class="block truncate text-sm font-medium">
        <span
          v-for="(segment, index) in titleSegments"
          :key="index"
          :class="{ 'search-row-match': segment.matched }"
          >{{ segment.text }}</span
        >
      </span>
      <span v-if="idSegments" class="search-row-secondary block truncate font-mono">
        <span
          v-for="(segment, index) in idSegments"
          :key="index"
          :class="{ 'search-row-match': segment.matched }"
          >{{ segment.text }}</span
        >
      </span>
    </span>

    <!-- Model rows end with the cheapest quote and, on the current row, a copy button. -->
    <template v-if="item.kind === 'model'">
      <span v-if="price" class="flex shrink-0 flex-col items-end text-right">
        <span class="text-sm font-medium tabular-nums">
          <span v-if="price.from" class="search-row-secondary mr-1 font-normal">{{
            t('globalSearch.priceFrom')
          }}</span
          >{{ price.value }}
        </span>
        <span v-if="price.unit" class="search-row-secondary">{{ price.unit }}</span>
      </span>
      <button
        v-if="active"
        type="button"
        tabindex="-1"
        class="btn-icon-sm shrink-0 text-primary-900/50 hover:bg-white hover:text-primary-700 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-primary-500"
        data-testid="global-search-copy"
        :aria-label="t('globalSearch.copyModelId')"
        :title="t('globalSearch.copyModelId')"
        @click.stop="emit('copy')"
      >
        <Icon name="copy" size="sm" />
      </button>
    </template>

    <template v-else-if="item.kind === 'page'">
      <span v-if="item.area" class="search-row-secondary shrink-0">{{ item.area }}</span>
      <Icon
        v-if="item.external"
        name="externalLink"
        size="sm"
        class="search-row-secondary shrink-0"
        :animate-on-hover="false"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import type { RankedItem } from '@/components/layout/search/searchCatalog'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { highlightSegments, type TextSegment } from '@/utils/fuzzyMatch'
import type { ModelPriceSummary } from '@/utils/marketplaceCatalog'
import { formatPriceNumber } from '@/utils/marketplacePricing'
import { sanitizeSvg } from '@/utils/sanitize'

// GlobalSearchResultRow.vue renders one result in the global search palette: a model with
// its ID and cheapest quote, a page with its section label, or an action. Matched
// characters are highlighted only in the field that produced the match, so a model found
// by its ID highlights the ID line and leaves the display name plain. The row is a
// listbox option; keyboard focus stays in the palette input, which points at the current
// row through aria-activedescendant.

// props identify the ranked item and whether it is the palette's current row.
const props = defineProps<{
  ranked: RankedItem // Item and its best match.
  active: boolean // Current keyboard and pointer row.
  optionId: string // DOM id for aria-activedescendant.
}>()

// emit reports selection, the model copy button, and pointer movement; the palette owns
// the resulting navigation, clipboard write, and current-row change.
const emit = defineEmits<{
  (e: 'select'): void // Row activated by click.
  (e: 'copy'): void // Copy button clicked on a model row.
  (e: 'pointer'): void // Pointer moved over the row.
}>()

const { t } = useI18n()
const { balanceUnitName } = useBalanceDisplay()

// PriceDisplay is the formatted quote: the amount line, its unit line, and whether the
// amount is the lowest of several different group quotes.
interface PriceDisplay {
  value: string // Amounts shown on the first line.
  unit: string // Balance unit and billing unit.
  from: boolean // Prefix the amount with "from".
}

// TOKENS_PER_MILLION converts per-token quotes into the per-million-token unit the
// marketplace uses for display.
const TOKENS_PER_MILLION = 1_000_000

// item is the row's search item, read by the template and the derived display values.
const item = computed(() => props.ranked.item)

// titleSegments highlights the title when field 0 matched; idSegments shows the model ID
// as a second line unless it repeats the display name, highlighting it when field 1
// matched.
const titleSegments = computed<TextSegment[]>(() => {
  const match = props.ranked.match
  return highlightSegments(item.value.title, match?.field === 0 ? match.ranges : [])
})
const idSegments = computed<TextSegment[] | null>(() => {
  if (item.value.kind !== 'model' || item.value.model.id === item.value.title) return null
  const match = props.ranked.match
  return highlightSegments(item.value.model.id, match?.field === 1 ? match.ranges : [])
})

// formatTokenSide formats one per-token amount per million tokens, or a dash when the
// group quotes no price for that side.
function formatTokenSide(value: number | null): string {
  return value === null ? '—' : formatPriceNumber(value * TOKENS_PER_MILLION)
}

// formatPrice converts a price summary into display text. Token quotes show input and
// output per million tokens; image and video quotes show their single cheapest unit;
// unpriced models show the marketplace's unpriced label without a unit line.
function formatPrice(summary: ModelPriceSummary): PriceDisplay {
  if (summary.kind === 'token') {
    return {
      value: `${formatTokenSide(summary.input)} / ${formatTokenSide(summary.output)}`,
      unit: `${balanceUnitName.value} ${t('usage.perMillionTokens')}`,
      from: summary.varies,
    }
  }
  if (summary.kind === 'image' || summary.kind === 'video') {
    const perUnit =
      summary.kind === 'image' ? t('marketplace.perImage') : t('marketplace.perSecond')
    return {
      value: formatPriceNumber(summary.amount),
      unit: `${balanceUnitName.value} ${perUnit}`,
      from: summary.varies,
    }
  }
  return { value: t('marketplace.unpriced'), unit: '', from: false }
}

// price formats the quote of a model row and is null for pages and actions.
const price = computed<PriceDisplay | null>(() =>
  item.value.kind === 'model' ? formatPrice(item.value.model.price) : null,
)
</script>

<style scoped>
/*
 * Rows share the inset menu geometry and colors: the current row uses the .menu-item
 * hover fill and brand text, so keyboard and pointer selection look the same.
 */
.search-row {
  @apply flex min-h-11 w-full cursor-pointer select-none items-center gap-3 rounded-control px-2.5 py-1.5 text-left;
  @apply text-primary-900 transition-colors duration-fast;
}

.dark .search-row {
  @apply text-dark-100;
}

.search-row-active {
  @apply bg-primary-100 text-primary-700;
}

.dark .search-row-active {
  @apply bg-dark-800 text-primary-500;
}

/* Secondary text (IDs, units, section labels) stays muted on the current row too. */
.search-row-secondary {
  @apply text-xs text-primary-900/50;
}

.dark .search-row-secondary {
  @apply text-dark-400;
}

/* Matched characters use the link hover brand color and a heavier weight. */
.search-row-match {
  @apply font-semibold text-primary-600;
}

.dark .search-row-match {
  @apply text-primary-500;
}

/* Uploaded page icons use the same 18px box as registered navigation icons. */
.search-row-svg {
  width: 1.125rem;
  height: 1.125rem;
}

.search-row-svg :deep(svg) {
  display: block;
  width: 100%;
  height: 100%;
}
</style>
