<template>
  <button
    v-if="appStore.cachedPublicSettings?.show_user_id === true && userId"
    type="button"
    class="btn btn-ghost btn-sm shrink-0 gap-1.5 font-mono"
    :aria-label="t('profile.copyUserId', { id: userId })"
    :title="t('profile.copyUserId', { id: userId })"
    @click="copyToClipboard(`#${userId}`)"
  >
    <span>#{{ userId }}</span>
    <Icon name="copy" size="sm" />
  </button>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore } from '@/stores/app'

// UserIdBadge.vue displays an account identifier and copies its hash-prefixed text.
// The public display switch controls visibility in the account menu and profile,
// while the shared clipboard composable supplies browser support and copy feedback.

// userId identifies the displayed account; an absent identifier hides the control.
defineProps<{
  userId?: number // Displayed account identifier.
}>()

// Shared public settings gate the control, and translations label the copy action.
const appStore = useAppStore()
const { t } = useI18n()
const { copyToClipboard } = useClipboard()
</script>
