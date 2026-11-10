<template>
  <div v-if="variant === 'status' || hasActiveSubscriptions" ref="containerRef" class="relative">
    <button
      :class="triggerClass"
      :title="hasActiveSubscriptions ? t('subscriptionProgress.viewDetails') : undefined"
      :disabled="!hasActiveSubscriptions"
      @click="toggleTooltip"
    >
      <template v-if="variant === 'status'">
        <slot />

        <span
          v-if="statusDotClass"
          data-testid="subscription-status-dot"
          class="ml-1 h-2 w-2 shrink-0 rounded-full"
          :class="statusDotClass"
        />
      </template>
      <div v-else class="flex items-center gap-2">
        <Icon name="creditCard" size="sm" class="text-primary-600 dark:text-primary-400" />
        <div class="flex items-center gap-0.5">
          <div
            v-for="(dotClass, index) in displayDots"
            :key="index"
            class="rounded-full"
            :class="['h-2 w-2', dotClass]"
          />
        </div>
        <span class="text-xs font-medium text-primary-700 dark:text-primary-300">
          {{ statusCount }}
        </span>
      </div>
    </button>

    <MotionTransition name="dropdown-fade">
      <div
        v-if="tooltipOpen"
        class="dropdown subscription-progress-popover right-0 z-50 mt-2 w-[340px] overflow-hidden py-0"
      >
        <div class="border-b border-gray-100 p-3 dark:border-dark-700">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('subscriptionProgress.title') }}
          </h3>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
            {{ t('subscriptionProgress.activeCount', { count: activeSubscriptions.length }) }}
          </p>
        </div>

        <SubscriptionUsageList
          class="max-h-64 overflow-y-auto"
          :subscriptions="activeSubscriptions"
          item-class="border-b border-gray-50 p-3 last:border-b-0 dark:border-dark-700/50"
        />

        <div class="border-t border-gray-100 p-2 dark:border-dark-700">
          <router-link
            to="/subscriptions"
            class="block w-full py-1 text-center text-xs text-link"
            @click="closeTooltip"
          >
            {{ t('subscriptionProgress.viewAll') }}
          </router-link>
        </div>
      </div>
    </MotionTransition>
  </div>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import SubscriptionUsageList from '@/components/common/SubscriptionUsageList.vue'
import { useSubscriptionUsage } from '@/composables/useSubscriptionUsage'
import { useSubscriptionStore } from '@/stores'

/**
 * SubscriptionProgressMini.vue shows active subscription usage in a compact header
 * control and detail popover. The ordinary variant disappears with no active
 * subscriptions, while the status variant retains its slot and disables expansion;
 * usage sorting supplies the most constrained subscription dots.
 */

/**
 * props chooses the ordinary subscription indicator or the persistent header status
 * slot with an optional usage dot.
 */
const props = withDefaults(
  defineProps<{
    variant?: 'default' | 'status' /* visual presentation variant */
  }>(),
  {
    variant: 'default',
  },
)

/**
 * The i18n translator resolves this component's message keys using the current
 * application locale and its configured translation catalog.
 */
const { t } = useI18n()
/**
 * subscriptionStore owns the shared active subscription list fetched when this header
 * control mounts and reused by its computed display.
 */
const subscriptionStore = useSubscriptionStore()
/**
 * The subscription usage composable supplies the shared ordering and color rules for
 * the header indicators.
 */
const { sortByUsage, getProgressDotClass } = useSubscriptionUsage()

/**
 * containerRef identifies the control and popover root used to decide whether document
 * clicks should dismiss the details.
 */
const containerRef = ref<HTMLElement | null>(null)
/**
 * tooltipOpen owns popover visibility and changes through the header trigger, outside
 * clicks, or the full subscription page link.
 */
const tooltipOpen = ref(false)

/**
 * activeSubscriptions exposes the store list used by both the compact status and
 * detailed subscription rows.
 */
const activeSubscriptions = computed(() => subscriptionStore.activeSubscriptions)
/**
 * hasActiveSubscriptions follows the store availability flag that controls whether the
 * trigger can open the detail popover.
 */
const hasActiveSubscriptions = computed(() => subscriptionStore.hasActiveSubscriptions)
/**
 * variant exposes the caller presentation choice to the template and computed trigger
 * styles for the header control.
 */
const variant = computed(() => props.variant)
/**
 * statusCount reports the number of active subscriptions beside the compact group of
 * usage indicator dots.
 */
const statusCount = computed(() => activeSubscriptions.value.length)
/**
 * triggerClass selects an ordinary usage pill or the header status control whose
 * disabled appearance retains the supplied slot.
 */
const triggerClass = computed(() => {
  if (variant.value === 'status') {
    return 'flex h-9 items-center gap-1 rounded-control px-3 text-sm transition-colors hover:bg-primary-100 disabled:cursor-default disabled:hover:bg-transparent dark:hover:bg-dark-700 dark:disabled:hover:bg-transparent'
  }
  return 'flex cursor-pointer items-center gap-2 rounded-control bg-primary-50 px-3 py-1.5 transition-colors hover:bg-primary-100 dark:bg-primary-900/20 dark:hover:bg-primary-900/30'
})

/**
 * displayDots sorts subscriptions by usage and keeps the three leading indicator
 * colors for the compact trigger.
 */
const displayDots = computed(() =>
  sortByUsage(activeSubscriptions.value)
    /**
     * Cap the dots at three to keep the header trigger compact.
     */
    .slice(0, 3)
    .map((subscription) => getProgressDotClass(subscription)),
)
/**
 * statusDotClass selects the leading usage color for the status variant, leaving the
 * dot hidden when no indicators exist.
 */
const statusDotClass = computed(() => displayDots.value[0] ?? '')

/**
 * toggleTooltip toggles the popover when active subscriptions exist. An empty
 * subscription set leaves the current open state unchanged, matching the disabled
 * trigger rendered for that state.
 */
function toggleTooltip() {
  if (!hasActiveSubscriptions.value) return
  tooltipOpen.value = !tooltipOpen.value
}

/**
 * closeTooltip dismisses the subscription detail popover for this component instance.
 */
function closeTooltip() {
  tooltipOpen.value = false
}

/**
 * handleClickOutside dismisses details when a document click falls outside the
 * component root. An unavailable root leaves state unchanged, while clicks on the
 * trigger or popover stay within the control.
 */
function handleClickOutside(event: MouseEvent) {
  if (containerRef.value && !containerRef.value.contains(event.target as Node)) {
    closeTooltip()
  }
}

/**
 * Mounting observes outside clicks and requests active subscriptions for the shared
 * store, allowing the header indicators and popover to update together.
 */
onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  /**
   * A failed initial refresh is logged while the store keeps ownership of available
   * subscriptions, allowing the header control to remain mounted.
   */
  subscriptionStore.fetchActiveSubscriptions().catch((error) => {
    console.error('Failed to load subscriptions in SubscriptionProgressMini:', error)
  })
})

/**
 * Unmounting removes the document click observer so later page clicks do not access
 * this detached subscription popover instance.
 */
onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style scoped>
/*
 * Below the small breakpoint, viewport based positioning keeps the details
 * popover within side margins instead of anchoring its fixed width to the
 * header.
 */
@media (max-width: 639px) {
  .subscription-progress-popover {
    position: fixed;
    top: 4.5rem;
    right: 0.75rem;
    left: 0.75rem;
    width: auto;
    margin-top: 0;
  }
}
</style>
