<template>
  <div class="relative" ref="dropdownRef">
    <button
      @click="toggleDropdown"
      :disabled="switching"
      :class="triggerClass"
      :title="currentLocale?.name"
      :aria-label="currentLocale?.name"
      :aria-expanded="isOpen"
    >
      <!--
        The header text trigger shows the current language name from md upward and the
        language icon below md, matching the other header navigation entries.
      -->
      <template v-if="variant === 'text'">
        <Icon name="languages" size="md" class="md:hidden" />
        <span class="hidden md:inline">{{ currentLocale?.name }}</span>
      </template>
      <span v-else class="text-base leading-none">{{ currentLocale?.flag }}</span>
      <span v-if="variant !== 'text'" class="hidden sm:inline">{{ currentLocale?.name }}</span>
      <Icon
        v-if="variant !== 'text'"
        name="chevronDown"
        size="xs"
        class="text-gray-400 transition-transform duration-normal"
        :class="{ 'rotate-180': isOpen }"
        :animate-on-hover="false"
      />
    </button>

    <MotionTransition name="dropdown-fade">
      <div v-if="isOpen" class="dropdown right-0 z-50 mt-2 w-44 origin-top-right py-0">
        <div class="menu-section">
          <button
            v-for="locale in availableLocales"
            :key="locale.code"
            type="button"
            :disabled="switching"
            @click="selectLocale(locale.code)"
            class="menu-item whitespace-nowrap"
            :class="{ 'text-primary-700 dark:text-primary-500': locale.code === currentLocaleCode }"
          >
            <span class="text-base leading-none">{{ locale.flag }}</span>
            <span class="min-w-0 truncate">{{ locale.name }}</span>
            <Icon
              v-if="locale.code === currentLocaleCode"
              name="check"
              size="sm"
              class="ml-auto shrink-0"
              :animate-on-hover="false"
            />
          </button>
        </div>
      </div>
    </MotionTransition>
  </div>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { setLocale, availableLocales } from '@/i18n'

/**
 * LocaleSwitcher.vue switches the application locale from a dropdown opened by a flag
 * button or by the header's language-name entry. The component locks selection while
 * setLocale runs, closes after successful selection, and owns the document click
 * listener that dismisses the menu outside its root.
 */

/**
 * props chooses the ordinary language and flag button or the header navigation entry
 * that shows the language name from md upward and an icon below md.
 */
const props = withDefaults(
  defineProps<{
    variant?: 'default' | 'text' /* visual presentation variant */
  }>(),
  {
    variant: 'default',
  },
)

/**
 * The active i18n locale is the source for the trigger label and selected language
 * indicator.
 */
const { locale } = useI18n()

/**
 * isOpen owns the menu visibility for this switcher instance and changes on selection
 * or outside clicks.
 */
const isOpen = ref(false)
/**
 * dropdownRef identifies the root used to distinguish document clicks inside the
 * switcher from clicks that dismiss it.
 */
const dropdownRef = ref<HTMLElement | null>(null)
/**
 * switching blocks another locale selection until the current setLocale promise
 * settles and restores the controls.
 */
const switching = ref(false)

/**
 * currentLocaleCode exposes the active i18n locale used for selection equality and the
 * menu checkmark.
 */
const currentLocaleCode = computed(() => locale.value)
/**
 * currentLocale resolves the catalog entry whose display name and flag identify the
 * selected language in the trigger and menu.
 */
const currentLocale = computed(() => availableLocales.find((l) => l.code === locale.value))
/**
 * variant exposes the caller selected trigger presentation for both the template and
 * the computed button classes.
 */
const variant = computed(() => props.variant)
/**
 * triggerClass selects the shared header navigation styling or the ordinary flag and
 * language-name button styling.
 */
const triggerClass = computed(() => {
  if (variant.value === 'text') {
    return 'header-nav-link'
  }
  return 'flex items-center gap-1.5 rounded-control px-2 py-1.5 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-60 dark:text-gray-300 dark:hover:bg-dark-700'
})

/**
 * toggleDropdown switches the local language menu between open and closed.
 */
function toggleDropdown() {
  isOpen.value = !isOpen.value
}

/**
 * selectLocale applies a different locale while serializing selections through
 * switching. An already active locale or concurrent selection closes the menu
 * immediately; successful application closes it, and finally restores the controls
 * after failures.
 */
async function selectLocale(code: string) {
  if (switching.value || code === currentLocaleCode.value) {
    isOpen.value = false
    return
  }
  switching.value = true
  try {
    await setLocale(code)
    isOpen.value = false
  } finally {
    switching.value = false
  }
}

/**
 * handleClickOutside closes the menu when a click falls outside its root. It leaves
 * the current visibility unchanged while the root reference is unavailable during
 * component setup or teardown.
 */
function handleClickOutside(event: MouseEvent) {
  if (dropdownRef.value && !dropdownRef.value.contains(event.target as Node)) {
    isOpen.value = false
  }
}

/**
 * Mounting installs the outside click observer on document, allowing clicks beyond the
 * dropdown root to dismiss the language menu.
 */
onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

/**
 * Unmounting removes the same document click observer so a detached switcher no longer
 * receives events or retains its root state.
 */
onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style scoped></style>
