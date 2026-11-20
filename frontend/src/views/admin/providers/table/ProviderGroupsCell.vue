<template>
  <div v-if="groups && groups.length > 0" class="relative max-w-56">
    <!-- Group badges wrap within the bounded two-row display area. -->
    <div class="flex flex-wrap gap-1 max-h-14 overflow-hidden">
      <GroupBadge
        v-for="group in displayGroups"
        :key="group.id"
        :name="group.name"
        :display-brand="group.display_brand"
        :rate-multiplier="group.rate_multiplier"
        :show-rate="false"
        class="max-w-24"
      />
      <!-- Overflow count trigger. -->
      <button
        v-if="hiddenCount > 0"
        ref="moreButtonRef"
        @click.stop="togglePopover"
        class="inline-flex items-center gap-0.5 rounded-compact px-1.5 py-0.5 text-xs font-medium bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600 transition-colors cursor-pointer whitespace-nowrap"
      >
        <span>+{{ hiddenCount }}</span>
      </button>
    </div>

    <!-- The popover displays the complete group list. -->
    <Teleport to="body">
      <MotionTransition name="dropdown-fade">
        <div
          v-if="showPopover"
          class="fixed z-50 min-w-48 rounded-surface border border-gray-200 bg-white p-3 shadow-lg dark:border-dark-600 dark:bg-dark-900"
          :style="popoverStyle"
        >
          <div class="mb-2 flex items-center justify-between">
            <span class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.providers.groupCountTotal', { count: groups.length }) }}
            </span>
            <button
              @click="showPopover = false"
              class="rounded-compact p-0.5 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700 dark:hover:text-gray-300"
            >
              <Icon name="x" size="xs" class="h-3.5 w-3.5" />
            </button>
          </div>
          <div class="flex flex-wrap gap-1.5 max-h-64 overflow-y-auto">
            <GroupBadge
              v-for="group in groups"
              :key="group.id"
              :name="group.name"
              :display-brand="group.display_brand"
              :rate-multiplier="group.rate_multiplier"
              :show-rate="false"
            />
          </div>
        </div>
      </MotionTransition>
    </Teleport>

    <!-- The backdrop closes the group popover. -->
    <div v-if="showPopover" class="fixed inset-0 z-40" @click="showPopover = false" />
  </div>
  <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
</template>

<script setup lang="ts">
import { useFloatingMotion } from '@/composables/useFloatingMotion'
import MotionTransition from '@/components/common/MotionTransition.vue'
import Icon from '@/components/icons/Icon.vue'
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import GroupBadge from '@/components/common/GroupBadge.vue'
import type { Group } from '@/api/groups.types'
import { getFloatingPanelPosition, type FloatingPanelPosition } from '@/utils/floatingPanel'

/**
 * ProviderGroupsCell.vue displays provider group badges and an overflow popover when the
 * configured display count is exceeded. The overflow button consumes one visible slot, and the
 * popover exposes the complete group list. Escape and floating-motion dismissal close the
 * popover, with the keyboard listener released on unmount.
 */

/**
 * Props supplies assigned groups and the visible-slot budget for their badges.
 */
interface Props {
  groups: Group[] | null | undefined // assigned groups or no data
  /**
   * Visible slot budget defaults to four; an overflowing list reserves one slot for the more
   * button.
   */
  maxDisplay?: number
}

/**
 * props defaults the badge slot budget to four and allows absent group arrays to render an
 * empty group list.
 */
const props = withDefaults(defineProps<Props>(), {
  maxDisplay: 4,
})

/**
 * Translation and popover state provide the overflow label, anchor element, and current
 * visibility for the complete group list.
 */
const { t } = useI18n()

const moreButtonRef = ref<HTMLElement | null>(null)
const showPopover = ref(false)
const popoverPosition = ref<FloatingPanelPosition | null>(null)

/**
 * Visible group selection and hidden count share the same slot rule, reserving one slot for
 * overflow when the list exceeds maxDisplay.
 */
const displayGroups = computed(() => {
  if (!props.groups) return []
  if (props.groups.length <= props.maxDisplay) {
    return props.groups
  }
  // Reserve one visible slot for overflow access.
  return props.groups.slice(0, props.maxDisplay - 1)
})

const hiddenCount = computed(() => {
  if (!props.groups) return 0
  if (props.groups.length <= props.maxDisplay) return 0
  return props.groups.length - (props.maxDisplay - 1)
})

/**
 * updatePopoverPosition measures the overflow trigger and delegates viewport clamping and
 * upward flipping to getFloatingPanelPosition so narrow viewports cap panel width inside
 * safe margins.
 */
const updatePopoverPosition = () => {
  if (!moreButtonRef.value) return
  popoverPosition.value = getFloatingPanelPosition(
    moreButtonRef.value.getBoundingClientRect(),
    window.innerWidth,
    window.innerHeight,
    {
      maxWidth: 384,
      align: 'left',
      viewportPadding: 8,
      gap: 8,
      minComfortableHeight: 280,
    },
  )
}

/**
 * togglePopover flips overflow popover visibility and measures placement before rendering
 * when opening so the first frame uses clamped coordinates.
 */
const togglePopover = () => {
  const next = !showPopover.value
  if (next) updatePopoverPosition()
  showPopover.value = next
}

/**
 * popoverStyle maps the floating placement result to fixed CSS offsets and a viewport-bounded
 * maximum width.
 */
const popoverStyle = computed(() => {
  const position = popoverPosition.value
  if (!position) return {}
  return {
    top: position.top === null ? 'auto' : `${position.top}px`,
    bottom: position.bottom === null ? 'auto' : `${position.bottom}px`,
    left: `${position.left}px`,
    maxWidth: `${position.width}px`,
  }
})

/**
 * handleKeydown closes the group overflow popover when Escape is pressed.
 */
const handleKeydown = (e: KeyboardEvent) => {
  if (e.key === 'Escape') {
    showPopover.value = false
  }
}

useFloatingMotion(
  moreButtonRef,
  () => showPopover.value,
  () => {
    showPopover.value = false
  },
  updatePopoverPosition,
)

onMounted(() => {
  // The mounted cell owns this Escape listener so an open group popover can be dismissed from
  // the keyboard.
  window.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  // Release the cell-owned listener when its row unmounts so later keyboard events cannot
  // target the removed popover state.
  window.removeEventListener('keydown', handleKeydown)
})
</script>
