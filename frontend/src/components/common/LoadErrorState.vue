<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import EmptyState from './EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'

// LoadErrorState.vue presents a failed initial read in the space reserved for
// page or table content. The surrounding view owns the request and repeats it
// when the user selects the translated retry action.

// The message describes the resource the surrounding view could not read.
defineProps<{ message: string /* Resource-specific failure message. */ }>()

// The retry event asks the owning view to repeat its current request.
const emit = defineEmits<{ retry: [] }>()

// Translations supply the retry action shared by page and table failure states.
const { t } = useI18n()
</script>

<template>
  <EmptyState
    role="alert"
    :title="message"
    :action-text="t('common.retry')"
    @action="emit('retry')"
  >
    <template #icon>
      <Icon name="exclamationTriangle" class="empty-state-icon h-10 w-10" />
    </template>
  </EmptyState>
</template>
