<template>
  <Teleport to="body">
    <MotionTransition name="dropdown-fade">
      <div
        v-if="open && position"
        :inert="!(open && position) || undefined"
        ref="panelRef"
        class="dropdown animate-in fade-in slide-in-from-top-2 fixed z-teleport-dropdown w-max max-w-[calc(100vw-16px)] overflow-hidden py-0 duration-normal sm:min-w-[380px]"
        style="pointer-events: auto !important"
        :style="{
          top: position.top !== undefined ? position.top + 'px' : undefined,
          bottom: position.bottom !== undefined ? position.bottom + 'px' : undefined,
          left: position.left + 'px',
        }"
      >
        <div class="border-b border-gray-100 p-2 dark:border-dark-700">
          <SearchInput v-model="searchQuery" :placeholder="t('keys.searchGroup')" @click.stop />
        </div>
        <div class="max-h-80 overflow-y-auto p-1.5">
          <button
            v-for="option in filteredOptions"
            :key="option.value ?? 'null'"
            type="button"
            @click="selectedKey && emit('select', selectedKey, option.value)"
            :class="[
              'flex w-full items-center justify-between rounded-control px-3 py-2.5 text-sm transition-colors',
              'border-b border-gray-100 last:border-0 dark:border-dark-700',
              isSelectedGroup(option.value)
                ? 'bg-primary-50 dark:bg-primary-500/8 dark:text-primary-500'
                : 'hover:bg-gray-100 dark:hover:bg-dark-700',
            ]"
            :title="option.description || undefined"
          >
            <GroupOptionItem
              :name="option.label"
              :display-brand="option.displayBrand"
              :rate-multiplier="option.rate"
              :user-rate-multiplier="option.userRate"
              :description="option.description"
              :selected="isSelectedGroup(option.value)"
            />
          </button>
          <div
            v-if="filteredOptions.length === 0"
            class="py-4 text-center text-sm text-gray-400 dark:text-gray-500"
          >
            {{ t('keys.noGroupFound') }}
          </div>
        </div>
      </div>
    </MotionTransition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import type { ApiKey } from '@/api/keys.types'
import type { KeyGroupSelectOption } from './keysPresentation'

/**
 * KeyGroupSelectorDropdown.vue renders the teleported inline group-reassignment
 * menu for single-group API keys, encapsulating local group search and exposing
 * containsTarget for document outside-click dismissal.
 */

const props = defineProps<{
  open: boolean // Whether a key row currently owns the open selector.
  selectedKey: ApiKey | null // Target API key row being reassigned.
  options: KeyGroupSelectOption[] // Available routing group choices.
  position: {
    top?: number // Optional downward anchor offset in viewport pixels.
    bottom?: number // Optional upward anchor offset in viewport pixels.
    left: number // Horizontal anchor offset in viewport pixels.
  } | null // Computed viewport coordinates for the floating panel.
}>()

const emit = defineEmits<{
  select: [key: ApiKey, groupId: number | null] // Persists a newly chosen group for selectedKey.
}>()

const { t } = useI18n()
const panelRef = ref<HTMLElement | null>(null)
const searchQuery = ref('')

// Clear the search filter whenever the selector opens for a different key row.
watch(
  () => [props.open, props.selectedKey?.id] as const,
  ([isOpen]) => {
    if (isOpen) {
      searchQuery.value = ''
    }
  },
)

const filteredOptions = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  if (!query) return props.options
  return props.options.filter((opt) => {
    return (
      opt.search_terms?.some((text) => text.toLowerCase().includes(query)) ||
      opt.label.toLowerCase().includes(query) ||
      (opt.displayBrand && opt.displayBrand.toLowerCase().includes(query)) ||
      (opt.description && opt.description.toLowerCase().includes(query))
    )
  })
})

/**
 * isSelectedGroup reports whether optionValue matches selectedKey's bound group.
 */
const isSelectedGroup = (optionValue: number | null): boolean =>
  props.selectedKey?.group_id === optionValue ||
  (!props.selectedKey?.group_id && optionValue === null)

/**
 * containsTarget reports whether target lies inside the teleported dropdown DOM
 * node so document click listeners keep internal clicks from closing the panel.
 */
const containsTarget = (target: Node | null): boolean =>
  Boolean(target && panelRef.value?.contains(target))

defineExpose({
  containsTarget,
})
</script>
