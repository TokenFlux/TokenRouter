<template>
  <div ref="dropdownRef" class="relative">
    <button
      type="button"
      class="btn btn-secondary md:w-auto md:min-h-0 md:px-3 md:py-1.5 btn-icon"
      :title="label"
      :aria-label="`${label}: ${currentLabel}`"
      :aria-expanded="open"
      aria-haspopup="menu"
      data-test="scope-dropdown-trigger"
      @click="open = !open"
    >
      <Icon :name="currentOption?.icon ?? 'key'" size="sm" class="md:mr-1.5" />
      <span class="hidden md:inline">{{ currentLabel }}</span>
      <Icon name="chevronDown" size="xs" class="ml-1 hidden md:inline" :animate-on-hover="false" />
    </button>

    <MotionTransition name="dropdown-fade">
      <div
        v-if="open"
        :inert="!open || undefined"
        class="dropdown top-full z-50 mt-2 p-2"
        :style="panelStyle"
        role="menu"
        data-test="scope-dropdown-menu"
      >
        <button
          v-for="option in options"
          :key="option.value"
          type="button"
          role="menuitemradio"
          :aria-checked="scope === option.value"
          class="dropdown-item-sm rounded-control"
          :data-test="`scope-option-${option.value}`"
          @click="select(option.value)"
        >
          <Icon :name="option.icon" size="sm" class="text-gray-400 dark:text-gray-500" />
          <span class="flex-1">{{ option.label }}</span>
          <Icon
            v-if="scope === option.value"
            name="check"
            size="sm"
            class="text-primary-500"
            :stroke-width="2"
            :animate-on-hover="false"
          />
        </button>
      </div>
    </MotionTransition>
  </div>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { computed, onMounted, onUnmounted, ref } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/registry'
import { useAnchoredPanel } from '@/composables/useAnchoredPanel'

// ScopeSelector.vue renders an icon dropdown for list scopes in the toolbar.
// Desktop triggers show the current label, while narrow screens retain the icon
// and accessible name. The parent owns the selected scope and any URL changes;
// this component owns menu presentation and captured outside-click dismissal.

// ScopeOption describes a selectable list scope and its presentation identity.
export interface ScopeOption {
  value: string // Parent-owned selection identifier.
  label: string // Localized visible option label.
  icon: IconName // Existing scope artwork identifier.
}

// Props keep navigation and list loading with the owning page or domain wrapper.
const props = defineProps<{
  modelValue: string // Currently selected list scope.
  options: ScopeOption[] // Available scope presentation choices.
  label: string // Accessible scope selection label.
}>()
const emit = defineEmits<{
  (event: 'update:modelValue', value: string): void // Newly selected list scope.
}>()

// Menu visibility, the root element, and clamped horizontal placement belong to this
// mounted selector.
const open = ref(false)
const dropdownRef = ref<HTMLElement | null>(null)
const { panelStyle } = useAnchoredPanel(dropdownRef, open, { widthRem: 12 })
const scope = computed(() => props.modelValue)
const currentOption = computed(() => props.options.find((option) => option.value === scope.value))
const currentLabel = computed(() => currentOption.value?.label ?? '')

// select dismisses the menu and publishes a changed parent-owned scope.
// Selecting the current option only closes the menu; the parent handles any
// data loading or navigation that follows a different selection.
function select(value: string) {
  open.value = false
  if (value !== scope.value) emit('update:modelValue', value)
}

// closeOnOutsideClick dismisses the menu when a captured click leaves its root.
// The mounted component owns the capture listener and releases it on disposal,
// allowing the selector to work inside dialogs whose contents stop click bubbling.
function closeOnOutsideClick(event: MouseEvent) {
  if (!dropdownRef.value?.contains(event.target as Node)) open.value = false
}

// Capture runs before nested click handlers so stopped bubbling cannot strand the
// menu; teardown removes this instance's exact listener.
onMounted(() => document.addEventListener('click', closeOnOutsideClick, true))
onUnmounted(() => document.removeEventListener('click', closeOnOutsideClick, true))
</script>
