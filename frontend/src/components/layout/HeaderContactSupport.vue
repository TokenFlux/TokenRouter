<template>
  <div v-if="contactEntries.length" class="relative" ref="dropdownRef">
    <!-- From md upward the trigger reads "Contact Us"; narrower headers show the chat icon. -->
    <button
      type="button"
      data-testid="contact-support-toggle"
      class="header-nav-link"
      :aria-label="t('common.contactSupport')"
      :aria-expanded="isOpen"
      @click="toggleDropdown"
    >
      <Icon name="chat" size="md" class="md:hidden" />
      <span class="hidden md:inline">{{ t('common.contactSupport') }}</span>
    </button>

    <!-- Each contact occupies one row; links open a new tab and text values can be copied. -->
    <MotionTransition name="dropdown-fade">
      <div v-if="isOpen" class="dropdown right-0 z-50 mt-2 w-72 origin-top-right py-0">
        <div class="menu-section">
          <div class="menu-heading">{{ t('common.contactSupport') }}</div>
          <template v-for="(entry, idx) in contactEntries" :key="idx">
            <a
              v-if="entry.url"
              :href="entry.url"
              target="_blank"
              rel="noopener noreferrer"
              class="menu-item"
              :title="entry.value"
              @click="closeDropdown"
            >
              <span
                v-if="entry.label"
                class="shrink-0 font-normal text-primary-900/60 dark:text-dark-400"
                >{{ entry.label }}</span
              >
              <span class="ml-auto min-w-0 truncate">{{ formatContactUrl(entry.url) }}</span>
              <Icon name="externalLink" size="sm" class="shrink-0" />
            </a>
            <button
              v-else
              type="button"
              class="menu-item"
              :title="t('common.copy')"
              @click="copyContact(entry.value)"
            >
              <span
                v-if="entry.label"
                class="shrink-0 font-normal text-primary-900/60 dark:text-dark-400"
                >{{ entry.label }}</span
              >
              <span class="ml-auto min-w-0 truncate tabular-nums">{{ entry.value }}</span>
              <Icon name="copy" size="sm" class="shrink-0" />
            </button>
          </template>
        </div>
      </div>
    </MotionTransition>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import MotionTransition from '@/components/common/MotionTransition.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore } from '@/stores'
import { formatContactUrl, parseContactInfo } from '@/utils/contactInfo'

// HeaderContactSupport.vue shows configured contact entries in a header menu of links and
// copy actions. Its trigger is a labeled header navigation entry from md upward and a
// chat icon below md. The shared contactInfo parser splits the setting and decides which
// entries are links, so this menu and the dashboard contact card show the same rows.
// Menu visibility is local, and document click observation lasts until the component
// unmounts.

// Translation, public contact settings, and clipboard feedback supply the menu labels
// and actions for this mounted support control.
const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

// isOpen owns menu visibility, and dropdownRef identifies the root used to keep internal
// clicks from triggering the document outside-click close handler.
const isOpen = ref(false)
const dropdownRef = ref<HTMLElement | null>(null)

// contactEntries holds the parsed menu rows; an empty list hides the header control.
const contactEntries = computed(() => parseContactInfo(appStore.contactInfo))

// toggleDropdown toggles the support menu when its header button is clicked.
function toggleDropdown() {
  isOpen.value = !isOpen.value
}

// closeDropdown closes the support menu after a link or copy action.
function closeDropdown() {
  isOpen.value = false
}

// copyContact waits for clipboard handling before closing the support menu.
//
// Clipboard feedback is owned by useClipboard. A resolved false still closes the menu; a
// thrown clipboard-command error propagates and leaves the menu open.
async function copyContact(value: string) {
  await copyToClipboard(value)
  closeDropdown()
}

// handleClickOutside closes support when a document click is outside its root.
//
// If the template root is absent, the handler leaves local visibility unchanged until a
// later toggle or close action.
function handleClickOutside(event: MouseEvent) {
  if (dropdownRef.value && !dropdownRef.value.contains(event.target as Node)) {
    closeDropdown()
  }
}

// Observe document clicks during the mounted support control lifetime and remove the
// same callback before unmount so detached menus stop reacting to clicks.
onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>
